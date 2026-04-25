//go:build firecracker

package firecracker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/storage"
)

// CreateForkPoint materializes a fork-point from the given parent VM. If the
// parent is running, the VM is paused, vmstate.bin is written, the rootfs
// is reflinked, then the VM is resumed. If the parent is stopped, an
// existing on-disk rootfs is reflinked (no memory state is captured —
// children spawned from a stopped fork-point boot fresh from the rootfs).
//
// Returns the fork-point ID. The caller is responsible for spawning
// children via SpawnFromForkPoint and for the fork-point's lifecycle.
func (p *Provider) CreateForkPoint(ctx context.Context, parentVMID string) (string, error) {
	fpID := "fp-" + uuid.NewString()[:12]
	fpDir := p.forkPointDir(fpID)
	if err := os.MkdirAll(fpDir, 0o755); err != nil {
		return "", fmt.Errorf("forkpoint mkdir: %w", err)
	}

	parentInfo, err := p.getVMInfo(parentVMID)
	if err != nil {
		os.RemoveAll(fpDir)
		return "", fmt.Errorf("forkpoint: read parent vminfo: %w", err)
	}

	mode := "stopped"
	var diskBackend storage.Backend
	if parentInfo.PID > 0 && processAlive(parentInfo.PID) {
		mode = "live"
		b, err := p.createLiveSnapshot(ctx, parentVMID, p.vmDir(parentVMID), fpDir)
		if err != nil {
			os.RemoveAll(fpDir)
			return "", err
		}
		diskBackend = b
	} else {
		b, err := p.createStoppedSnapshot(ctx, p.vmDir(parentVMID), fpDir)
		if err != nil {
			os.RemoveAll(fpDir)
			return "", err
		}
		diskBackend = b
	}

	info := &fpInfo{
		ForkPointID: fpID,
		ParentVMID:  parentVMID,
		Mode:        mode,
		CreatedAt:   time.Now().UTC(),
	}
	if diskBackend != nil {
		info.StorageBackend = diskBackend.Name()
	}
	if err := writeFPInfo(p.fpInfoPath(fpID), info); err != nil {
		os.RemoveAll(fpDir)
		return "", err
	}
	slog.Info("firecracker forkpoint created", "fp_id", fpID, "parent", parentVMID, "mode", mode)
	return fpID, nil
}

// SpawnFromForkPoint creates a new VM that restores from the given fork-point.
// The rootfs is reflinked from the fork-point; vmstate.bin and snapshot.meta
// are reflinked into the VM dir (and into the jailer chroot's run dir when
// the jailer is enabled, mirroring RestoreSnapshot's layout). The returned
// BackendRef is in the same "ready to start" state as a sandbox produced by
// CreateSandboxFromSnapshot — StartSandbox handles the actual launch and
// configures Firecracker to MAP_PRIVATE the memory file (kernel-level CoW).
//
// On any error, the partially-created VM dir is removed AND the descendant
// is NOT added to the fork-point.
func (p *Provider) SpawnFromForkPoint(ctx context.Context, fpID string, req domain.CreateSandboxRequest) (domain.BackendRef, error) {
	fpDir := p.forkPointDir(fpID)
	if _, err := readFPInfo(p.fpInfoPath(fpID)); err != nil {
		return domain.BackendRef{}, fmt.Errorf("forkpoint not found: %w", err)
	}

	vmID := vmName()
	vmDir := p.vmDir(vmID)
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		return domain.BackendRef{}, fmt.Errorf("forkpoint spawn mkdir %s: %w", vmID, err)
	}

	// Reflink rootfs from fork-point.
	rootSrc := filepath.Join(fpDir, "rootfs.ext4")
	rootDst := filepath.Join(vmDir, "rootfs.ext4")
	if _, err := p.storage.CloneFile(ctx, rootSrc, rootDst); err != nil {
		os.RemoveAll(vmDir)
		return domain.BackendRef{}, fmt.Errorf("forkpoint spawn clone rootfs %s: %w", vmID, err)
	}

	// Reflink vmstate.bin and snapshot.meta into the VM dir. When the
	// jailer is enabled, RestoreSnapshot writes restore files into
	// <vmDir>/root/, which is the jail chroot — match that layout so
	// StartSandbox finds the files in the same place.
	var restoreDir string
	if p.config.EnableJailer {
		restoreDir = filepath.Join(vmDir, "root")
	} else {
		restoreDir = vmDir
	}
	if err := os.MkdirAll(restoreDir, 0o755); err != nil {
		os.RemoveAll(vmDir)
		return domain.BackendRef{}, fmt.Errorf("forkpoint spawn mkdir restoreDir %s: %w", vmID, err)
	}
	for _, name := range []string{"vmstate.bin", "snapshot.meta"} {
		src := filepath.Join(fpDir, name)
		dst := filepath.Join(restoreDir, name)
		if _, err := p.storage.CloneFile(ctx, src, dst); err != nil {
			os.RemoveAll(vmDir)
			return domain.BackendRef{}, fmt.Errorf("forkpoint spawn clone %s: %w", name, err)
		}
	}

	cid := p.allocateCID()
	uid := p.uids.Allocate()

	// Chown rootfs and restore files for the jailer UID, mirroring the
	// existing CreateSandboxFromSnapshot/RestoreSnapshot patterns.
	if p.config.EnableJailer {
		if err := os.Chown(rootDst, uid, uid); err != nil {
			os.RemoveAll(vmDir)
			return domain.BackendRef{}, fmt.Errorf("forkpoint chown rootfs %s: %w", vmID, err)
		}
		for _, name := range []string{"vmstate.bin", "snapshot.meta"} {
			if err := os.Chown(filepath.Join(restoreDir, name), uid, uid); err != nil {
				os.RemoveAll(vmDir)
				return domain.BackendRef{}, fmt.Errorf("forkpoint chown %s for %s: %w", name, vmID, err)
			}
		}
	}

	info := &VMInfo{
		ID:                  vmID,
		CID:                 cid,
		UID:                 uid,
		NetworkMode:         string(req.NetworkMode),
		RestoreFromSnapshot: true,
		ForkPointID:         fpID,
	}
	if err := info.Write(p.vmInfoPath(vmID)); err != nil {
		os.RemoveAll(vmDir)
		return domain.BackendRef{}, fmt.Errorf("forkpoint spawn write vminfo %s: %w", vmID, err)
	}

	// Bump the fork-point's descendant set. SpawnPending is decremented
	// here too; the controller (T14 ForkSandbox) increments it up-front
	// per requested child, so the math nets out correctly even when an
	// individual spawn fails (the failed-spawn rollback decrements the
	// counter without adding to descendants).
	if err := updateFPInfo(p.fpInfoPath(fpID), func(i *fpInfo) {
		i.Descendants = append(i.Descendants, vmID)
		if i.SpawnPending > 0 {
			i.SpawnPending--
		}
	}); err != nil {
		// VM is already on disk and registered; log but don't roll back.
		slog.Warn("forkpoint update descendants", "fp_id", fpID, "vm_id", vmID, "error", err)
	}

	slog.Debug("firecracker forkpoint child spawned", "fp_id", fpID, "vm_id", vmID)
	return domain.BackendRef{Backend: backendName, Ref: vmID}, nil
}

// ReleaseForkPointDescendant removes vmID from the fork-point's descendant
// set. When the set is empty AND no spawns are pending, the fork-point's
// on-disk directory is removed (its memory backing file is no longer needed
// by any running child). Idempotent — calling on a fork-point that's
// already been GC'd returns no error.
func (p *Provider) ReleaseForkPointDescendant(fpID, vmID string) error {
	path := p.fpInfoPath(fpID)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // already GC'd
	}
	var nowEmpty bool
	if err := updateFPInfo(path, func(i *fpInfo) {
		filtered := i.Descendants[:0]
		for _, d := range i.Descendants {
			if d != vmID {
				filtered = append(filtered, d)
			}
		}
		i.Descendants = filtered
		nowEmpty = len(filtered) == 0 && i.SpawnPending == 0
	}); err != nil {
		return err
	}
	if nowEmpty {
		slog.Info("firecracker forkpoint gc", "fp_id", fpID)
		return os.RemoveAll(p.forkPointDir(fpID))
	}
	return nil
}
