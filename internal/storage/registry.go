package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Mode is a configured backend selection strategy.
type Mode string

const (
	ModeAuto        Mode = "auto"
	ModeCopy        Mode = "copy"
	ModeReflink     Mode = "reflink"
	ModeBtrfsSubvol Mode = "btrfs-subvol"
	ModeZfs         Mode = "zfs"
)

// Config controls registry construction at daemon startup.
type Config struct {
	Mode Mode // global mode; "auto" probes each root.
}

// Registry maps destination paths to a Backend. The longest matching prefix
// wins. A fallback Backend is used when no prefix matches; if no fallback is
// set, For panics — callers are expected to set one (BuildRegistry always
// does).
type Registry struct {
	mu       sync.RWMutex
	prefixes []string             // sorted longest-first for For lookups
	byPrefix map[string]Backend
	fallback Backend
}

func NewRegistry() *Registry {
	return &Registry{byPrefix: map[string]Backend{}}
}

func (r *Registry) Set(prefix string, b Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	prefix = filepath.Clean(prefix)
	if _, exists := r.byPrefix[prefix]; !exists {
		r.prefixes = append(r.prefixes, prefix)
		sort.Slice(r.prefixes, func(i, j int) bool {
			return len(r.prefixes[i]) > len(r.prefixes[j])
		})
	}
	r.byPrefix[prefix] = b
}

func (r *Registry) SetFallback(b Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fallback = b
}

// For returns the Backend that should be used for a given destination path.
// Lookup is by longest matching prefix. Falls back to the registered
// fallback if no prefix matches; panics if no fallback is set.
func (r *Registry) For(path string) Backend {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clean := filepath.Clean(path)
	for _, p := range r.prefixes {
		if clean == p || strings.HasPrefix(clean, p+string(filepath.Separator)) {
			return r.byPrefix[p]
		}
	}
	if r.fallback == nil {
		panic(fmt.Sprintf("storage.Registry: no backend for %q and no fallback", path))
	}
	return r.fallback
}

// CloneFile resolves the backend for dst and runs the clone. If the
// resolved backend reports ErrUnsupported at op time (e.g. cross-mount
// EXDEV the startup probe didn't catch), CloneFile transparently falls
// back to CopyBackend. The returned Backend is the one that actually ran.
func (r *Registry) CloneFile(ctx context.Context, src, dst string) (Backend, error) {
	b := r.For(dst)
	err := b.CloneFile(ctx, src, dst)
	if err == nil {
		return b, nil
	}
	if errors.Is(err, ErrUnsupported) {
		fallback := CopyBackend{}
		if err2 := fallback.CloneFile(ctx, src, dst); err2 != nil {
			return nil, fmt.Errorf("primary %s failed (%v); fallback copy also failed: %w", b.Name(), err, err2)
		}
		return fallback, nil
	}
	return nil, err
}

// BuildRegistry constructs a Registry from a Config and a list of CoW-relevant
// roots. Per-root overrides win over the global Mode. Returns an error when
// an explicit non-auto mode is incompatible with a probed root (deterministic
// startup).
func BuildRegistry(cfg Config, roots []string, overrides map[string]Mode) (*Registry, error) {
	r := NewRegistry()
	r.SetFallback(CopyBackend{})

	// Normalise override keys via filepath.Clean so callers can pass
	// trailing slashes or unclean paths.
	cleanOverrides := map[string]Mode{}
	for k, v := range overrides {
		cleanOverrides[filepath.Clean(k)] = v
	}

	for _, root := range roots {
		mode := cfg.Mode
		if mode == "" {
			mode = ModeAuto
		}
		if ov, ok := cleanOverrides[filepath.Clean(root)]; ok && ov != "" {
			mode = ov
		}
		b, err := resolveMode(mode, root)
		if err != nil {
			return nil, fmt.Errorf("storage: root %q: %w", root, err)
		}
		r.Set(root, b)
	}
	return r, nil
}

// resolveMode picks a backend for a single root under a single mode, probing
// when mode is auto and verifying when mode is explicit.
func resolveMode(mode Mode, root string) (Backend, error) {
	switch mode {
	case ModeAuto, "":
		b, err := Detect(root)
		if err != nil {
			return nil, err
		}
		return b, nil
	case ModeCopy:
		return CopyBackend{}, nil
	case ModeReflink:
		if err := probeReflink(root); err != nil {
			return nil, fmt.Errorf("reflink not available at %s: %w", root, err)
		}
		return ReflinkBackend{}, nil
	case ModeBtrfsSubvol:
		return nil, errors.Join(ErrUnsupported,
			fmt.Errorf("btrfs-subvol mode not wired in v1"))
	case ModeZfs:
		return nil, errors.Join(ErrUnsupported,
			fmt.Errorf("zfs mode not wired in v1"))
	default:
		return nil, fmt.Errorf("unknown storage mode %q", mode)
	}
}
