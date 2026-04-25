package storage

import (
	"context"
	"errors"
	"fmt"
)

// ZfsBackend will clone via "zfs clone" of a snapshot. Stub only: the v1
// Firecracker provider is file-based (reflink-friendly) and ZFS clones hold
// their parent snapshot immutable for the clone's lifetime, which would
// require a lifecycle dependency graph navaris does not currently model.
type ZfsBackend struct{}

func (ZfsBackend) Name() string               { return "zfs" }
func (ZfsBackend) Capabilities() Capabilities { return Capabilities{} }

func (ZfsBackend) CloneFile(ctx context.Context, src, dst string) error {
	return errors.Join(ErrUnsupported,
		fmt.Errorf("zfs backend not wired in v1; use reflink for file-based rootfs"))
}
