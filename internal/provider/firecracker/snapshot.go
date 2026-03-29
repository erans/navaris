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
	"github.com/navaris/navaris/internal/provider/firecracker/jailer"
)

type snapInfo struct {
	ID        string                `json:"id"`
	SourceVM  string                `json:"source_vm"`
	Label     string                `json:"label"`
	Mode      domain.ConsistencyMode `json:"mode"`
	CreatedAt time.Time             `json:"created_at"`
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

func (p *Provider) CreateSnapshot(ctx context.Context, ref domain.BackendRef, label string, mode domain.ConsistencyMode) (domain.BackendRef, error) {
	vmID := ref.Ref
	vmDir := jailer.ChrootPath(p.config.ChrootBase, vmID)

	snapID := "snap-" + uuid.NewString()[:8]
	snapDir := p.snapshotDir(snapID)

	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		return domain.BackendRef{}, fmt.Errorf("firecracker create snapshot dir: %w", err)
	}

	switch mode {
	case domain.ConsistencyStopped:
		if err := p.createStoppedSnapshot(vmDir, snapDir); err != nil {
			os.RemoveAll(snapDir)
			return domain.BackendRef{}, err
		}

	case domain.ConsistencyLive:
		if err := p.createLiveSnapshot(ctx, vmID, vmDir, snapDir); err != nil {
			os.RemoveAll(snapDir)
			return domain.BackendRef{}, err
		}

	default:
		os.RemoveAll(snapDir)
		return domain.BackendRef{}, fmt.Errorf("firecracker: unsupported consistency mode %q", mode)
	}

	si := &snapInfo{
		ID:        snapID,
		SourceVM:  vmID,
		Label:     label,
		Mode:      mode,
		CreatedAt: time.Now().UTC(),
	}
	if err := writeSnapInfo(p.snapInfoPath(snapID), si); err != nil {
		os.RemoveAll(snapDir)
		return domain.BackendRef{}, fmt.Errorf("firecracker write snapinfo: %w", err)
	}

	return domain.BackendRef{Backend: backendName, Ref: snapID}, nil
}

func (p *Provider) createStoppedSnapshot(vmDir, snapDir string) error {
	src := filepath.Join(vmDir, "rootfs.ext4")
	dst := filepath.Join(snapDir, "rootfs.ext4")
	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("firecracker snapshot copy rootfs: %w", err)
	}
	return nil
}

func (p *Provider) createLiveSnapshot(ctx context.Context, vmID, vmDir, snapDir string) error {
	// Connect to running VM via the post-jailer socket path.
	sockPath := filepath.Join(vmDir, "root", "run", "firecracker.socket")
	machine, err := fcsdk.NewMachine(ctx, fcsdk.Config{SocketPath: sockPath})
	if err != nil {
		return fmt.Errorf("firecracker live snapshot connect %s: %w", vmID, err)
	}

	// Pause -> snapshot -> copy -> resume.
	if err := machine.PauseVM(ctx); err != nil {
		return fmt.Errorf("firecracker pause %s: %w", vmID, err)
	}

	// Ensure we resume even if something fails.
	var snapErr error
	defer func() {
		if snapErr != nil {
			if rerr := machine.ResumeVM(ctx); rerr != nil {
				slog.Error("firecracker: failed to resume after snapshot error", "vm", vmID, "error", rerr)
			}
		}
	}()

	// Create Firecracker memory snapshot. Paths are relative to the jailer chroot root.
	memFile := "/vmstate.bin"
	snapMeta := "/snapshot.meta"
	if snapErr = machine.CreateSnapshot(ctx, memFile, snapMeta); snapErr != nil {
		return fmt.Errorf("firecracker create snapshot %s: %w", vmID, snapErr)
	}

	// Copy rootfs while VM is paused (disk consistent).
	rootfsSrc := filepath.Join(vmDir, "rootfs.ext4")
	rootfsDst := filepath.Join(snapDir, "rootfs.ext4")
	if snapErr = copyFile(rootfsSrc, rootfsDst); snapErr != nil {
		return fmt.Errorf("firecracker snapshot copy rootfs: %w", snapErr)
	}

	// Copy snapshot files from chroot to snapshot dir.
	chrootRoot := filepath.Join(vmDir, "root")
	for _, name := range []string{"vmstate.bin", "snapshot.meta"} {
		src := filepath.Join(chrootRoot, name)
		dst := filepath.Join(snapDir, name)
		if snapErr = copyFile(src, dst); snapErr != nil {
			return fmt.Errorf("firecracker snapshot copy %s: %w", name, snapErr)
		}
	}

	// Resume the VM.
	if err := machine.ResumeVM(ctx); err != nil {
		return fmt.Errorf("firecracker resume %s: %w", vmID, err)
	}
	snapErr = nil // Clear so defer doesn't try to resume again.

	// Clean up snapshot files from chroot dir.
	for _, name := range []string{"vmstate.bin", "snapshot.meta"} {
		os.Remove(filepath.Join(chrootRoot, name))
	}

	return nil
}

func (p *Provider) RestoreSnapshot(ctx context.Context, sandboxRef domain.BackendRef, snapshotRef domain.BackendRef) error {
	vmID := sandboxRef.Ref
	snapID := snapshotRef.Ref

	vmDir := jailer.ChrootPath(p.config.ChrootBase, vmID)
	snapDir := p.snapshotDir(snapID)

	si, err := readSnapInfo(p.snapInfoPath(snapID))
	if err != nil {
		return fmt.Errorf("firecracker read snapinfo %s: %w", snapID, err)
	}

	// Copy rootfs from snapshot to VM.
	if err := copyFile(filepath.Join(snapDir, "rootfs.ext4"), filepath.Join(vmDir, "rootfs.ext4")); err != nil {
		return fmt.Errorf("firecracker restore copy rootfs: %w", err)
	}

	if si.Mode == domain.ConsistencyLive {
		// Copy snapshot files to VM's chroot root for live restore.
		chrootRoot := filepath.Join(vmDir, "root")
		os.MkdirAll(chrootRoot, 0o755)
		for _, name := range []string{"vmstate.bin", "snapshot.meta"} {
			if err := copyFile(filepath.Join(snapDir, name), filepath.Join(chrootRoot, name)); err != nil {
				return fmt.Errorf("firecracker restore copy %s: %w", name, err)
			}
		}

		// Set restore flag in vminfo.
		infoPath := jailer.VMInfoPath(p.config.ChrootBase, vmID)
		info, err := ReadVMInfo(infoPath)
		if err != nil {
			return fmt.Errorf("firecracker restore read vminfo: %w", err)
		}
		info.RestoreFromSnapshot = true
		if err := info.Write(infoPath); err != nil {
			return fmt.Errorf("firecracker restore write vminfo: %w", err)
		}
	}

	return nil
}

func (p *Provider) DeleteSnapshot(ctx context.Context, snapshotRef domain.BackendRef) error {
	snapDir := p.snapshotDir(snapshotRef.Ref)
	if err := os.RemoveAll(snapDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("firecracker delete snapshot %s: %w", snapshotRef.Ref, err)
	}
	return nil
}
