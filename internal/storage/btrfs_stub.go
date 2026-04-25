package storage

import (
	"context"
	"errors"
	"fmt"
)

// BtrfsSubvolBackend will clone via "btrfs subvolume snapshot". Stub only:
// the v1 Firecracker provider stores rootfs as a single .ext4 file, for which
// reflink is the natural CoW path. This stub exists so the interface stays
// honest and so future non-Firecracker providers (or Firecracker variants
// using a directory-based rootfs) can plug in without changing the Backend
// contract.
type BtrfsSubvolBackend struct{}

func (BtrfsSubvolBackend) Name() string               { return "btrfs-subvol" }
func (BtrfsSubvolBackend) Capabilities() Capabilities { return Capabilities{} }

func (BtrfsSubvolBackend) CloneFile(ctx context.Context, src, dst string) error {
	return errors.Join(ErrUnsupported,
		fmt.Errorf("btrfs-subvol backend not wired in v1; use reflink for file-based rootfs"))
}
