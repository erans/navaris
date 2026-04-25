//go:build firecracker

package firecracker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	fcsdk "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/storage"
	"github.com/navaris/navaris/internal/telemetry"
)

type snapInfo struct {
	ID             string                 `json:"id"`
	SourceVM       string                 `json:"source_vm"`
	Label          string                 `json:"label"`
	Mode           domain.ConsistencyMode `json:"mode"`
	SubnetIdx      int                    `json:"subnet_idx,omitempty"` // preserved for live restore
	CreatedAt      time.Time              `json:"created_at"`
	StorageBackend string                 `json:"storage_backend,omitempty"`
}

func (p *Provider) snapshotDir(snapID string) string {
	return filepath.Join(p.config.SnapshotDir, snapID)
}

func (p *Provider) snapInfoPath(snapID string) string {
	return filepath.Join(p.snapshotDir(snapID), "snapinfo.json")
}

func readSnapInfo(path string) (*snapInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var si snapInfo
	if err := json.Unmarshal(data, &si); err != nil {
		return nil, err
	}
	return &si, nil
}

func writeSnapInfo(path string, si *snapInfo) error {
	data, err := json.MarshalIndent(si, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (p *Provider) CreateSnapshot(ctx context.Context, ref domain.BackendRef, label string, mode domain.ConsistencyMode) (_ domain.BackendRef, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "CreateSnapshot")
	defer func() { endSpan(retErr) }()

	vmID := ref.Ref
	vmDir := p.vmDir(vmID)

	snapID := "snap-" + uuid.NewString()[:8]
	snapDir := p.snapshotDir(snapID)

	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		return domain.BackendRef{}, fmt.Errorf("firecracker create snapshot dir: %w", err)
	}

	var diskBackend storage.Backend

	switch mode {
	case domain.ConsistencyStopped:
		b, err := p.createStoppedSnapshot(ctx, vmDir, snapDir)
		if err != nil {
			os.RemoveAll(snapDir)
			return domain.BackendRef{}, err
		}
		diskBackend = b

	case domain.ConsistencyLive:
		b, err := p.createLiveSnapshot(ctx, vmID, vmDir, snapDir)
		if err != nil {
			os.RemoveAll(snapDir)
			return domain.BackendRef{}, err
		}
		diskBackend = b

	default:
		os.RemoveAll(snapDir)
		return domain.BackendRef{}, fmt.Errorf("firecracker: unsupported consistency mode %q", mode)
	}

	if diskBackend != nil {
		slog.Debug("firecracker snapshot disk clone", "snap_id", snapID, "vm_id", vmID, "backend", diskBackend.Name())
	}

	si := &snapInfo{
		ID:        snapID,
		SourceVM:  vmID,
		Label:     label,
		Mode:      mode,
		CreatedAt: time.Now().UTC(),
	}
	if diskBackend != nil {
		si.StorageBackend = diskBackend.Name()
	}

	// For live snapshots, preserve SubnetIdx for network-correct restore.
	if mode == domain.ConsistencyLive {
		infoPath := p.vmInfoPath(vmID)
		info, err := ReadVMInfo(infoPath)
		if err != nil {
			os.RemoveAll(snapDir)
			return domain.BackendRef{}, fmt.Errorf("firecracker snapshot read vminfo %s: %w", vmID, err)
		}
		si.SubnetIdx = info.SubnetIdx
	}

	if err := writeSnapInfo(p.snapInfoPath(snapID), si); err != nil {
		os.RemoveAll(snapDir)
		return domain.BackendRef{}, fmt.Errorf("firecracker write snapinfo: %w", err)
	}

	return domain.BackendRef{Backend: backendName, Ref: snapID}, nil
}

func (p *Provider) createStoppedSnapshot(ctx context.Context, vmDir, snapDir string) (storage.Backend, error) {
	src := filepath.Join(vmDir, "rootfs.ext4")
	dst := filepath.Join(snapDir, "rootfs.ext4")
	b, err := p.storage.CloneFile(ctx, src, dst)
	if err != nil {
		return nil, fmt.Errorf("firecracker snapshot copy rootfs: %w", err)
	}
	return b, nil
}

func (p *Provider) createLiveSnapshot(ctx context.Context, vmID, vmDir, snapDir string) (storage.Backend, error) {
	// Connect to running VM via the API socket.
	var sockPath string
	if p.config.EnableJailer {
		sockPath = filepath.Join(vmDir, "root", "run", "firecracker.socket")
	} else {
		sockPath = filepath.Join(vmDir, "firecracker.sock")
	}
	machine, err := fcsdk.NewMachine(ctx, fcsdk.Config{SocketPath: sockPath})
	if err != nil {
		return nil, fmt.Errorf("firecracker live snapshot connect %s: %w", vmID, err)
	}

	// Pause -> snapshot -> copy -> resume.
	if err := machine.PauseVM(ctx); err != nil {
		return nil, fmt.Errorf("firecracker pause %s: %w", vmID, err)
	}

	// Ensure we resume even if something fails.
	// Use a fresh context for cleanup so cancellation of the parent doesn't prevent resume.
	var snapErr error
	defer func() {
		if snapErr != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if rerr := machine.ResumeVM(cleanupCtx); rerr != nil {
				slog.Error("firecracker: failed to resume after snapshot error", "vm", vmID, "error", rerr)
			}
		}
	}()

	// Create Firecracker memory snapshot.
	// With jailer, paths are relative to the chroot root.
	// Without jailer, use paths relative to the VM directory.
	memFile := "/vmstate.bin"
	snapMeta := "/snapshot.meta"
	if !p.config.EnableJailer {
		memFile = filepath.Join(vmDir, "vmstate.bin")
		snapMeta = filepath.Join(vmDir, "snapshot.meta")
	}
	if snapErr = machine.CreateSnapshot(ctx, memFile, snapMeta); snapErr != nil {
		return nil, fmt.Errorf("firecracker create snapshot %s: %w", vmID, snapErr)
	}

	// Copy rootfs while VM is paused (disk consistent).
	rootfsSrc := filepath.Join(vmDir, "rootfs.ext4")
	rootfsDst := filepath.Join(snapDir, "rootfs.ext4")
	var diskBackend storage.Backend
	diskBackend, snapErr = p.storage.CloneFile(ctx, rootfsSrc, rootfsDst)
	if snapErr != nil {
		return nil, fmt.Errorf("firecracker snapshot copy rootfs: %w", snapErr)
	}

	// Copy snapshot files from VM directory to snapshot dir.
	var snapshotFilesDir string
	if p.config.EnableJailer {
		snapshotFilesDir = filepath.Join(vmDir, "root")
	} else {
		snapshotFilesDir = vmDir
	}
	for _, name := range []string{"vmstate.bin", "snapshot.meta"} {
		src := filepath.Join(snapshotFilesDir, name)
		dst := filepath.Join(snapDir, name)
		if _, snapErr = p.storage.CloneFile(ctx, src, dst); snapErr != nil {
			return nil, fmt.Errorf("firecracker snapshot copy %s: %w", name, snapErr)
		}
	}

	// Resume the VM.
	if err := machine.ResumeVM(ctx); err != nil {
		return nil, fmt.Errorf("firecracker resume %s: %w", vmID, err)
	}
	snapErr = nil // Clear so defer doesn't try to resume again.

	// Clean up snapshot files from VM dir.
	for _, name := range []string{"vmstate.bin", "snapshot.meta"} {
		os.Remove(filepath.Join(snapshotFilesDir, name))
	}

	return diskBackend, nil
}

func (p *Provider) RestoreSnapshot(ctx context.Context, sandboxRef domain.BackendRef, snapshotRef domain.BackendRef) (retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "RestoreSnapshot")
	defer func() { endSpan(retErr) }()

	vmID := sandboxRef.Ref
	snapID := snapshotRef.Ref

	vmDir := p.vmDir(vmID)
	snapDir := p.snapshotDir(snapID)

	si, err := readSnapInfo(p.snapInfoPath(snapID))
	if err != nil {
		return fmt.Errorf("firecracker read snapinfo %s: %w", snapID, err)
	}

	// Copy rootfs from snapshot to VM.
	if _, err := p.storage.CloneFile(ctx, filepath.Join(snapDir, "rootfs.ext4"), filepath.Join(vmDir, "rootfs.ext4")); err != nil {
		return fmt.Errorf("firecracker restore copy rootfs: %w", err)
	}

	if si.Mode == domain.ConsistencyLive {
		// Copy snapshot files to VM directory for live restore.
		var restoreDir string
		if p.config.EnableJailer {
			restoreDir = filepath.Join(vmDir, "root")
		} else {
			restoreDir = vmDir
		}
		os.MkdirAll(restoreDir, 0o755)
		for _, name := range []string{"vmstate.bin", "snapshot.meta"} {
			if _, err := p.storage.CloneFile(ctx, filepath.Join(snapDir, name), filepath.Join(restoreDir, name)); err != nil {
				return fmt.Errorf("firecracker restore copy %s: %w", name, err)
			}
		}

		// Set restore flag in vminfo, preserving original subnet for network-correct restore.
		infoPath := p.vmInfoPath(vmID)
		info, err := ReadVMInfo(infoPath)
		if err != nil {
			return fmt.Errorf("firecracker restore read vminfo: %w", err)
		}
		info.RestoreFromSnapshot = true
		info.RestoreSubnetIdx = si.SubnetIdx
		if err := info.Write(infoPath); err != nil {
			return fmt.Errorf("firecracker restore write vminfo: %w", err)
		}
	}

	return nil
}

func (p *Provider) DeleteSnapshot(ctx context.Context, snapshotRef domain.BackendRef) (retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "DeleteSnapshot")
	defer func() { endSpan(retErr) }()

	snapDir := p.snapshotDir(snapshotRef.Ref)
	if err := os.RemoveAll(snapDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("firecracker delete snapshot %s: %w", snapshotRef.Ref, err)
	}
	return nil
}
