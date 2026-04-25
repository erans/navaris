package storage

import (
	"context"
	"fmt"
	"io"
	"os"
)

// CopyBackend clones via io.Copy. Always available, never CoW.
type CopyBackend struct{}

func (CopyBackend) Name() string               { return "copy" }
func (CopyBackend) Capabilities() Capabilities { return Capabilities{} }

func (CopyBackend) CloneFile(ctx context.Context, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("storage/copy open src: %w", err)
	}
	defer in.Close()

	tmp := dst + ".tmp"
	// Best-effort remove a stale dst.tmp from a prior crashed run before we
	// open with O_EXCL — that turns "tmp already exists" into a real error
	// rather than silently clobbering whatever it pointed at.
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("storage/copy clean stale dst.tmp: %w", err)
	}
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("storage/copy create dst.tmp: %w", err)
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("storage/copy write: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("storage/copy close: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("storage/copy rename: %w", err)
	}
	return nil
}
