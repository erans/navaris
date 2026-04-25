//go:build !linux

package storage

import (
	"fmt"
	"os"
)

// Detect on non-Linux always returns CopyBackend (or an error if root is
// missing/not-a-directory). Reflink and other CoW primitives are Linux-only
// in v1; navaris targets Linux for VMM workloads.
func Detect(root string) (Backend, error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("storage/detect stat %s: %w", root, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("storage/detect %s: not a directory", root)
	}
	return CopyBackend{}, nil
}

// probeReflink on non-Linux always reports unsupported.
func probeReflink(root string) error { return ErrUnsupported }
