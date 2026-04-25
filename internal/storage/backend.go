// Package storage provides copy-on-write backends for cloning sandbox files.
package storage

import (
	"context"
	"errors"
)

// ErrUnsupported is returned by backends that cannot perform a clone in the
// current environment (wrong filesystem, missing kernel feature, etc.).
// Callers may wrap this with errors.Join to add context while preserving Is.
var ErrUnsupported = errors.New("storage: backend not supported here")

// Capabilities describes what a backend's clone op offers. It is informational
// only; correctness must not depend on it (clones may still fail at op time).
type Capabilities struct {
	InstantClone   bool // O(1) metadata op, not O(size) data copy
	SharesBlocks   bool // clones share physical blocks until written
	RequiresSameFS bool // src and dst must share a filesystem
}

// Backend clones a single regular file from src to dst.
//
// Contract:
//   - On success, dst is a complete, writable, independent file.
//   - On any error, dst either does not exist or has been removed by the
//     backend (no partial files visible to readers). Implementations
//     achieve this via dst.tmp + rename(2).
//   - src is not modified.
type Backend interface {
	Name() string
	CloneFile(ctx context.Context, src, dst string) error
	Capabilities() Capabilities
}
