//go:build firecracker

package firecracker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	fcsdk "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/network"
	"github.com/navaris/navaris/internal/telemetry"
)

// validImageRef matches safe image reference names (alphanumeric, dots, dashes, underscores).
var validImageRef = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func vmName() string {
	return "nvrs-fc-" + uuid.NewString()[:8]
}

func (p *Provider) CreateSandbox(ctx context.Context, req domain.CreateSandboxRequest) (_ domain.BackendRef, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "CreateSandbox")
	defer func() { endSpan(retErr) }()

	// Validate ImageRef to prevent path traversal.
	if !validImageRef.MatchString(req.ImageRef) {
		return domain.BackendRef{}, fmt.Errorf("firecracker: invalid image ref %q", req.ImageRef)
	}

	vmID := vmName()
	vmDir := p.vmDir(vmID)

	// Create VM directory.
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		return domain.BackendRef{}, fmt.Errorf("firecracker create dir %s: %w", vmID, err)
	}

	// Copy rootfs image.
	srcImage := filepath.Join(p.config.ImageDir, req.ImageRef+".ext4")
	dstImage := filepath.Join(vmDir, "rootfs.ext4")
	if _, err := p.storage.CloneFile(ctx, srcImage, dstImage); err != nil {
		os.RemoveAll(vmDir)
		return domain.BackendRef{}, fmt.Errorf("firecracker copy rootfs %s: %w", vmID, err)
	}

	// Allocate resources.
	cid := p.allocateCID()
	uid := p.uids.Allocate()

	// Chown rootfs so the jailer UID can access it after privilege drop.
	if p.config.EnableJailer {
		if err := os.Chown(dstImage, uid, uid); err != nil {
			os.RemoveAll(vmDir)
			return domain.BackendRef{}, fmt.Errorf("firecracker chown rootfs %s: %w", vmID, err)
		}
	}

	// Write vminfo.json.
	info := &VMInfo{ID: vmID, CID: cid, UID: uid, NetworkMode: string(req.NetworkMode)}
	if err := info.Write(p.vmInfoPath(vmID)); err != nil {
		os.RemoveAll(vmDir)
		return domain.BackendRef{}, fmt.Errorf("firecracker write vminfo %s: %w", vmID, err)
	}

	return domain.BackendRef{Backend: backendName, Ref: vmID}, nil
}

func (p *Provider) StartSandbox(ctx context.Context, ref domain.BackendRef) (retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "StartSandbox")
	defer func() { endSpan(retErr) }()

	vmID := ref.Ref

	// Read vminfo.
	infoPath := p.vmInfoPath(vmID)
	info, err := ReadVMInfo(infoPath)
	if err != nil {
		return fmt.Errorf("firecracker start %s: %w", vmID, err)
	}

	// Check if already running.
	if info.PID > 0 && processAlive(info.PID) {
		return nil
	}

	vmDir := p.vmDir(vmID)

	// Clean up stale socket files from a previous run so Firecracker
	// can bind fresh ones.
	os.Remove(p.vsockPath(vmID))
	os.Remove(filepath.Join(vmDir, "firecracker.sock"))

	// Check for live snapshot restore.
	if info.RestoreFromSnapshot {
		return p.startFromSnapshot(ctx, vmID, vmDir, info, infoPath)
	}

	// Allocate networking.
	subnetIdx := p.subnets.Allocate()
	tapName := network.TapName(vmID)
	hostIP := p.subnets.HostIP(subnetIdx).String()

	if err := network.CreateTap(tapName, hostIP); err != nil {
		p.subnets.Release(subnetIdx)
		return fmt.Errorf("firecracker create tap %s: %w", vmID, err)
	}

	// Build Firecracker config.
	rootfsPath := filepath.Join(vmDir, "rootfs.ext4")
	bootArgs := "console=ttyS0 reboot=k panic=1 pci=off " + p.subnets.KernelBootArg(subnetIdx)

	var jailerCfg *fcsdk.JailerConfig
	if p.config.EnableJailer {
		jailerCfg = &fcsdk.JailerConfig{
			GID:            fcsdk.Int(info.UID),
			UID:            fcsdk.Int(info.UID),
			ID:             vmID,
			NumaNode:       fcsdk.Int(0),
			ExecFile:       p.config.FirecrackerBin,
			JailerBinary:   p.config.JailerBin,
			ChrootBaseDir:  p.config.ChrootBase,
			ChrootStrategy: fcsdk.NewNaiveChrootStrategy(p.config.KernelPath),
			CgroupVersion:  p.cgroupVersion,
		}
	}

	fcCfg := fcsdk.Config{
		SocketPath:      p.socketPath(vmID),
		KernelImagePath: p.config.KernelPath,
		KernelArgs:      bootArgs,
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  fcsdk.Int64(1),
			MemSizeMib: fcsdk.Int64(256),
		},
		Drives: []models.Drive{
			{
				DriveID:      fcsdk.String("rootfs"),
				PathOnHost:   fcsdk.String(rootfsPath),
				IsRootDevice: fcsdk.Bool(true),
				IsReadOnly:   fcsdk.Bool(false),
			},
		},
		NetworkInterfaces: fcsdk.NetworkInterfaces{
			{
				StaticConfiguration: &fcsdk.StaticNetworkConfiguration{
					MacAddress:  fmt.Sprintf("02:FC:00:00:%02x:%02x", subnetIdx>>8, subnetIdx&0xFF),
					HostDevName: tapName,
				},
			},
		},
		VsockDevices: []fcsdk.VsockDevice{
			{Path: p.vsockUDSPath(vmID), CID: uint32(info.CID)},
		},
		JailerCfg: jailerCfg,
	}

	// Launch VM.
	// Use a detached context so the Firecracker process outlives the
	// operation context that created it.
	machineCtx := context.Background()
	machine, err := fcsdk.NewMachine(machineCtx, fcCfg)
	if err != nil {
		network.DeleteTap(tapName)
		p.subnets.Release(subnetIdx)
		return fmt.Errorf("firecracker new machine %s: %w", vmID, err)
	}

	if err := machine.Start(machineCtx); err != nil {
		network.DeleteTap(tapName)
		p.subnets.Release(subnetIdx)
		return fmt.Errorf("firecracker start machine %s: %w", vmID, err)
	}

	// Update vminfo with runtime state.
	pid, pidErr := machine.PID()
	if pidErr != nil {
		slog.Warn("firecracker: could not get PID", "vm", vmID, "error", pidErr)
	}
	info.PID = pid
	info.TapDevice = tapName
	info.SubnetIdx = subnetIdx
	info.Write(infoPath)

	// Register in memory.
	p.vmMu.Lock()
	p.vms[vmID] = info
	p.vmMu.Unlock()

	// Add masquerade for published mode.
	if info.NetworkMode == string(domain.NetworkPublished) {
		guestIP := p.subnets.GuestIP(subnetIdx).String()
		if err := network.AddMasquerade(guestIP, p.hostIface); err != nil {
			// Non-fatal -- log but continue.
			slog.Warn("firecracker: masquerade failed", "vm", vmID, "error", err)
		}
	}

	// Wait for agent health check.
	if err := p.waitForAgent(ctx, vmID, 30*time.Second); err != nil {
		// Agent didn't respond -- leave VM running, caller can retry or destroy.
		return fmt.Errorf("firecracker agent timeout %s: %w", vmID, err)
	}

	return nil
}

func (p *Provider) startFromSnapshot(ctx context.Context, vmID, vmDir string, info *VMInfo, infoPath string) error {
	// For live restore, reuse the original subnet so the guest's in-memory
	// network config matches the host-side routing.
	var subnetIdx int
	if info.RestoreSubnetIdx > 0 || info.RestoreFromSnapshot {
		subnetIdx = info.RestoreSubnetIdx
		p.subnets.Reserve(subnetIdx)
	} else {
		subnetIdx = p.subnets.Allocate()
	}
	tapName := network.TapName(vmID)
	hostIP := p.subnets.HostIP(subnetIdx).String()

	if err := network.CreateTap(tapName, hostIP); err != nil {
		p.subnets.Release(subnetIdx)
		return fmt.Errorf("firecracker snapshot restore create tap %s: %w", vmID, err)
	}

	rootfsPath := filepath.Join(vmDir, "rootfs.ext4")

	var memPath, snapPath string
	if p.config.EnableJailer {
		chrootRoot := filepath.Join(vmDir, "root")
		memPath = filepath.Join(chrootRoot, "vmstate.bin")
		snapPath = filepath.Join(chrootRoot, "snapshot.meta")
	} else {
		memPath = filepath.Join(vmDir, "vmstate.bin")
		snapPath = filepath.Join(vmDir, "snapshot.meta")
	}

	// Build config for snapshot restore — omit KernelImagePath, KernelArgs, MachineCfg.
	var jailerCfg *fcsdk.JailerConfig
	if p.config.EnableJailer {
		jailerCfg = &fcsdk.JailerConfig{
			GID:            fcsdk.Int(info.UID),
			UID:            fcsdk.Int(info.UID),
			ID:             vmID,
			NumaNode:       fcsdk.Int(0),
			ExecFile:       p.config.FirecrackerBin,
			JailerBinary:   p.config.JailerBin,
			ChrootBaseDir:  p.config.ChrootBase,
			ChrootStrategy: fcsdk.NewNaiveChrootStrategy(p.config.KernelPath),
			CgroupVersion:  p.cgroupVersion,
		}
	}

	fcCfg := fcsdk.Config{
		SocketPath: p.socketPath(vmID),
		Drives: []models.Drive{
			{
				DriveID:      fcsdk.String("rootfs"),
				PathOnHost:   fcsdk.String(rootfsPath),
				IsRootDevice: fcsdk.Bool(true),
				IsReadOnly:   fcsdk.Bool(false),
			},
		},
		NetworkInterfaces: fcsdk.NetworkInterfaces{
			{
				StaticConfiguration: &fcsdk.StaticNetworkConfiguration{
					MacAddress:  fmt.Sprintf("02:FC:00:00:%02x:%02x", subnetIdx>>8, subnetIdx&0xFF),
					HostDevName: tapName,
				},
			},
		},
		VsockDevices: []fcsdk.VsockDevice{
			{Path: p.vsockUDSPath(vmID), CID: uint32(info.CID)},
		},
		JailerCfg: jailerCfg,
	}

	// Use a detached context so the Firecracker process outlives the
	// operation context that created it.
	machineCtx := context.Background()
	machine, err := fcsdk.NewMachine(machineCtx, fcCfg, fcsdk.WithSnapshot(memPath, snapPath, func(cfg *fcsdk.SnapshotConfig) {
		cfg.ResumeVM = true
	}))
	if err != nil {
		network.DeleteTap(tapName)
		p.subnets.Release(subnetIdx)
		return fmt.Errorf("firecracker snapshot restore new machine %s: %w", vmID, err)
	}

	if err := machine.Start(machineCtx); err != nil {
		network.DeleteTap(tapName)
		p.subnets.Release(subnetIdx)
		return fmt.Errorf("firecracker snapshot restore start %s: %w", vmID, err)
	}

	// Update vminfo with runtime state.
	pid, pidErr := machine.PID()
	if pidErr != nil {
		slog.Warn("firecracker: could not get PID", "vm", vmID, "error", pidErr)
	}
	info.PID = pid
	info.TapDevice = tapName
	info.SubnetIdx = subnetIdx
	info.RestoreFromSnapshot = false // Clear the flag.
	info.RestoreSubnetIdx = 0
	if err := info.Write(infoPath); err != nil {
		slog.Warn("firecracker: failed to write vminfo after snapshot restore", "vm", vmID, "error", err)
	}

	// Register in memory.
	p.vmMu.Lock()
	p.vms[vmID] = info
	p.vmMu.Unlock()

	// Add masquerade for published mode.
	if info.NetworkMode == string(domain.NetworkPublished) {
		guestIP := p.subnets.GuestIP(subnetIdx).String()
		if err := network.AddMasquerade(guestIP, p.hostIface); err != nil {
			slog.Warn("firecracker: masquerade failed", "vm", vmID, "error", err)
		}
	}

	// Clean up snapshot files from chroot.
	os.Remove(memPath)
	os.Remove(snapPath)

	// Wait for agent health check.
	if err := p.waitForAgent(ctx, vmID, 30*time.Second); err != nil {
		return fmt.Errorf("firecracker agent timeout %s: %w", vmID, err)
	}

	return nil
}

func (p *Provider) StopSandbox(ctx context.Context, ref domain.BackendRef, force bool) (retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "StopSandbox")
	defer func() { endSpan(retErr) }()

	vmID := ref.Ref
	infoPath := p.vmInfoPath(vmID)
	info, err := ReadVMInfo(infoPath)
	if err != nil {
		return fmt.Errorf("firecracker stop %s: %w", vmID, err)
	}

	if info.PID > 0 && processAlive(info.PID) {
		info.Stopping = true
		info.Write(infoPath)

		if force {
			syscall.Kill(info.PID, syscall.SIGKILL)
		} else {
			// Graceful: send CtrlAltDel via Firecracker API socket.
			vmDir := p.vmDir(vmID)
			var sockPath string
			if p.config.EnableJailer {
				sockPath = filepath.Join(vmDir, "root", "firecracker.sock")
			} else {
				sockPath = filepath.Join(vmDir, "firecracker.sock")
			}
			machine, merr := fcsdk.NewMachine(ctx, fcsdk.Config{SocketPath: sockPath})
			if merr == nil {
				machine.Shutdown(ctx)
			}
			deadline := time.After(30 * time.Second)
			for processAlive(info.PID) {
				select {
				case <-ctx.Done():
					syscall.Kill(info.PID, syscall.SIGKILL)
					goto stopped
				case <-deadline:
					syscall.Kill(info.PID, syscall.SIGKILL)
					goto stopped
				case <-time.After(100 * time.Millisecond):
				}
			}
		}
	}
stopped:

	// Clean up port forwarding rules.
	if len(info.Ports) > 0 && info.TapDevice != "" {
		guestIP := p.subnets.GuestIP(info.SubnetIdx).String()
		for hp, tp := range info.Ports {
			network.RemoveDNAT(hp, guestIP, tp)
			p.portAlloc.Release(hp)
		}
	}

	// Clean up networking.
	if info.TapDevice != "" {
		network.DeleteTap(info.TapDevice)
		if info.NetworkMode == string(domain.NetworkPublished) {
			guestIP := p.subnets.GuestIP(info.SubnetIdx).String()
			network.RemoveMasquerade(guestIP, p.hostIface)
		}
		p.subnets.Release(info.SubnetIdx)
	}

	// Update vminfo.
	info.ClearRuntime()
	info.Write(infoPath)

	p.vmMu.Lock()
	delete(p.vms, vmID)
	p.vmMu.Unlock()

	return nil
}

func (p *Provider) DestroySandbox(ctx context.Context, ref domain.BackendRef) (retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "DestroySandbox")
	defer func() { endSpan(retErr) }()

	// Stop first if running. Ignore "not found" errors (already stopped/cleaned).
	if err := p.StopSandbox(ctx, ref, true); err != nil && !os.IsNotExist(errors.Unwrap(err)) {
		return fmt.Errorf("firecracker destroy stop %s: %w", ref.Ref, err)
	}

	vmID := ref.Ref
	vmDir := p.vmDir(vmID)

	// Capture ForkPointID before we delete the VM dir, so we can release
	// the descendant set after the file is gone. Errors here are tolerated
	// — a missing or malformed vminfo just means we won't release; the
	// fork-point's own GC will sweep eventually.
	var fpID string
	if vmi, err := ReadVMInfo(p.vmInfoPath(vmID)); err == nil && vmi != nil {
		fpID = vmi.ForkPointID
	}

	if err := os.RemoveAll(vmDir); err != nil {
		return fmt.Errorf("firecracker destroy %s: %w", vmID, err)
	}

	p.vmMu.Lock()
	delete(p.vms, vmID)
	p.vmMu.Unlock()

	if fpID != "" {
		if err := p.ReleaseForkPointDescendant(fpID, vmID); err != nil {
			slog.Warn("firecracker forkpoint release", "fp_id", fpID, "vm_id", vmID, "error", err)
		}
	}

	return nil
}

func (p *Provider) GetSandboxState(ctx context.Context, ref domain.BackendRef) (_ domain.SandboxState, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "GetSandboxState")
	defer func() { endSpan(retErr) }()

	vmID := ref.Ref
	vmDir := p.vmDir(vmID)

	// Check if VM directory exists.
	if _, err := os.Stat(vmDir); os.IsNotExist(err) {
		return domain.SandboxDestroyed, nil
	}

	infoPath := p.vmInfoPath(vmID)
	info, err := ReadVMInfo(infoPath)
	if err != nil {
		if os.IsNotExist(errors.Unwrap(err)) {
			return domain.SandboxDestroyed, nil
		}
		return "", fmt.Errorf("firecracker state %s: %w", vmID, err)
	}

	// Check if stopping.
	if info.Stopping {
		return domain.SandboxStopping, nil
	}

	// Check process liveness.
	if info.PID == 0 || !processAlive(info.PID) {
		if info.PID > 0 {
			// Had a PID but it's dead -- unexpected crash.
			return domain.SandboxFailed, nil
		}
		return domain.SandboxStopped, nil
	}

	// Process alive -- check agent health.
	if err := p.pingAgent(ctx, info.ID); err != nil {
		return domain.SandboxStarting, nil
	}

	return domain.SandboxRunning, nil
}

func (p *Provider) CreateSandboxFromSnapshot(ctx context.Context, snapshotRef domain.BackendRef, req domain.CreateSandboxRequest) (_ domain.BackendRef, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "CreateSandboxFromSnapshot")
	defer func() { endSpan(retErr) }()

	snapID := snapshotRef.Ref
	if err := validateRef(snapID); err != nil {
		return domain.BackendRef{}, fmt.Errorf("firecracker create from snapshot: %w", err)
	}
	snapDir := p.snapshotDir(snapID)

	vmID := vmName()
	vmDir := p.vmDir(vmID)

	// Create VM directory.
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		return domain.BackendRef{}, fmt.Errorf("firecracker create dir %s: %w", vmID, err)
	}

	// Copy rootfs from snapshot.
	src := filepath.Join(snapDir, "rootfs.ext4")
	dst := filepath.Join(vmDir, "rootfs.ext4")
	if _, err := p.storage.CloneFile(ctx, src, dst); err != nil {
		os.RemoveAll(vmDir)
		return domain.BackendRef{}, fmt.Errorf("firecracker copy snapshot rootfs %s: %w", vmID, err)
	}

	// Allocate resources.
	cid := p.allocateCID()
	uid := p.uids.Allocate()

	// Chown rootfs so the jailer UID can access it after privilege drop.
	if p.config.EnableJailer {
		if err := os.Chown(dst, uid, uid); err != nil {
			os.RemoveAll(vmDir)
			return domain.BackendRef{}, fmt.Errorf("firecracker chown snapshot rootfs %s: %w", vmID, err)
		}
	}

	// Write vminfo.json.
	info := &VMInfo{ID: vmID, CID: cid, UID: uid, NetworkMode: string(req.NetworkMode)}
	if err := info.Write(p.vmInfoPath(vmID)); err != nil {
		os.RemoveAll(vmDir)
		return domain.BackendRef{}, fmt.Errorf("firecracker write vminfo %s: %w", vmID, err)
	}

	return domain.BackendRef{Backend: backendName, Ref: vmID}, nil
}

// Helper functions.

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func (p *Provider) waitForAgent(ctx context.Context, vmID string, timeout time.Duration) error {
	udsPath := p.vsockPath(vmID)
	deadline := time.After(timeout)

	// Phase 1: wait for the vsock UDS file to appear.
	for {
		if _, err := os.Stat(udsPath); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("agent at %s: vsock UDS not found within %s", vmID, timeout)
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Phase 2: ping the agent until it responds. Systemd-based guests
	// (Debian) boot slower than OpenRC (Alpine), so a blind sleep is
	// unreliable. Retry with backoff until the agent acks a ping.
	for {
		if err := p.pingAgent(ctx, vmID); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("agent at %s: ping not acked within %s", vmID, timeout)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (p *Provider) pingAgent(ctx context.Context, vmID string) error {
	client, err := p.dialAgent(vmID)
	if err != nil {
		return err
	}
	defer client.Close()
	return client.Ping(5 * time.Second)
}
