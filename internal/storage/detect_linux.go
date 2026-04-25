//go:build linux

package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Detect probes root and returns the best Backend known to work there.
// Falls back to CopyBackend on probe failure or unsupported FS.
// Returns nil + error if root does not exist or is not a directory.
func Detect(root string) (Backend, error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("storage/detect stat %s: %w", root, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("storage/detect %s: not a directory", root)
	}
	if probeReflink(root) == nil {
		return ReflinkBackend{}, nil
	}
	return CopyBackend{}, nil
}

// probeReflink writes a 1-byte file in root and attempts to FICLONE it into
// a sibling. Returns nil if reflink works; an error otherwise. Always cleans
// up its temp files. The probe is best-effort — a transient failure (ENOSPC,
// permission, races with concurrent probes) is treated the same as
// "filesystem does not support reflinks" and produces a fallback to copy.
func probeReflink(root string) error {
	src, err := os.CreateTemp(root, "navaris-storage-*.probe")
	if err != nil {
		return fmt.Errorf("probe create src: %w", err)
	}
	srcPath := src.Name()
	defer os.Remove(srcPath)

	if _, err := src.Write([]byte{0}); err != nil {
		src.Close()
		return fmt.Errorf("probe write: %w", err)
	}
	if err := src.Close(); err != nil {
		return fmt.Errorf("probe close src: %w", err)
	}

	dstPath := filepath.Join(root, filepath.Base(srcPath)+".probe-clone")
	defer os.Remove(dstPath)

	if err := (ReflinkBackend{}).CloneFile(context.Background(), srcPath, dstPath); err != nil {
		if errors.Is(err, ErrUnsupported) {
			return ErrUnsupported
		}
		return err
	}
	return nil
}
