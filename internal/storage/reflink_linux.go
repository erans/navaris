//go:build linux

package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// ReflinkBackend clones via FICLONE (btrfs/XFS-with-reflink/bcachefs).
type ReflinkBackend struct{}

func (ReflinkBackend) Name() string { return "reflink" }
func (ReflinkBackend) Capabilities() Capabilities {
	return Capabilities{InstantClone: true, SharesBlocks: true, RequiresSameFS: true}
}

func (ReflinkBackend) CloneFile(ctx context.Context, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("storage/reflink open src: %w", err)
	}
	defer in.Close()

	tmp := dst + ".tmp"
	// Mirror CopyBackend's stale-tmp policy: pre-remove + O_EXCL so a stale
	// dst.tmp from a crashed prior run becomes a real error rather than a
	// silent clobber.
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("storage/reflink clean stale dst.tmp: %w", err)
	}
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("storage/reflink create dst.tmp: %w", err)
	}

	cloneErr := unix.IoctlFileClone(int(out.Fd()), int(in.Fd()))
	if cloneErr != nil {
		out.Close()
		os.Remove(tmp)
		if isReflinkUnsupported(cloneErr) {
			return errors.Join(ErrUnsupported, fmt.Errorf("storage/reflink ficlone: %w", cloneErr))
		}
		return fmt.Errorf("storage/reflink ficlone: %w", cloneErr)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("storage/reflink close: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("storage/reflink rename: %w", err)
	}
	return nil
}

// isReflinkUnsupported reports whether the error means "this filesystem or
// kernel does not support reflinks here" — distinct from a real I/O error.
func isReflinkUnsupported(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case syscall.EOPNOTSUPP, syscall.EXDEV, syscall.ENOTTY, syscall.EINVAL:
		return true
	}
	return false
}
