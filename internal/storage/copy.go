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
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
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
