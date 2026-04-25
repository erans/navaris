//go:build firecracker

package firecracker

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// fpInfo is persisted to <forkPointDir>/fpinfo.json. It is the source of
// truth for fork-point lifecycle across daemon restarts.
//
// SpawnPending: count of children whose creation operation has been
// enqueued but has not reached a terminal state. Used solely for orphan
// cleanup at daemon start: a fork-point with SpawnPending > 0 and a
// CreatedAt older than fpOrphanTTL is GC'd (assumed crashed mid-fork).
//
// Descendants: set of sandbox IDs that currently hold a MAP_PRIVATE mapping
// of this fork-point's vmstate.bin. The fork-point directory must remain on
// disk while Descendants is non-empty; a child VM may still be page-faulting
// against the backing file.
//
// These two fields are independent: SpawnPending tracks the spawn phase;
// Descendants tracks the runtime backing-file dependency.
type fpInfo struct {
	ForkPointID    string    `json:"fork_point_id"`
	ParentVMID     string    `json:"parent_vm_id"`
	Mode           string    `json:"mode"` // "live" or "stopped"
	CreatedAt      time.Time `json:"created_at"`
	StorageBackend string    `json:"storage_backend,omitempty"`
	SpawnPending   int       `json:"spawn_pending"`
	Descendants    []string  `json:"descendants,omitempty"`
}

// fpOrphanTTL is the age threshold for orphan-cleanup of fork-points whose
// spawn phase never completed. Used by recoverForkPoints (added in T17).
const fpOrphanTTL = time.Hour

// fpInfoMu serialises read/write/update against fpinfo.json files.
// Per-file locking is unnecessary — fork operations are not hot, and a
// single daemon process owns these files.
var fpInfoMu sync.Mutex

func (p *Provider) forkPointDir(fpID string) string {
	return filepath.Join(p.config.ChrootBase, "forkpoints", fpID)
}

func (p *Provider) fpInfoPath(fpID string) string {
	return filepath.Join(p.forkPointDir(fpID), "fpinfo.json")
}

// writeFPInfo creates the fork-point directory if needed and atomically
// writes the JSON metadata via tmp+rename.
func writeFPInfo(path string, info *fpInfo) error {
	fpInfoMu.Lock()
	defer fpInfoMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("forkpoint mkdir: %w", err)
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("forkpoint marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("forkpoint write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("forkpoint rename: %w", err)
	}
	return nil
}

// readFPInfo reads and unmarshals the fpinfo.json at path.
func readFPInfo(path string) (*fpInfo, error) {
	fpInfoMu.Lock()
	defer fpInfoMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("forkpoint read: %w", err)
	}
	var info fpInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("forkpoint unmarshal: %w", err)
	}
	return &info, nil
}

// updateFPInfo applies fn to the fpInfo loaded from path under the global
// lock and atomically rewrites. Use for descendant-set or spawn-pending
// updates that must be race-free across goroutines.
func updateFPInfo(path string, fn func(*fpInfo)) error {
	fpInfoMu.Lock()
	defer fpInfoMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("forkpoint read: %w", err)
	}
	var info fpInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return fmt.Errorf("forkpoint unmarshal: %w", err)
	}
	fn(&info)
	out, err := json.MarshalIndent(&info, "", "  ")
	if err != nil {
		return fmt.Errorf("forkpoint marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("forkpoint write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("forkpoint rename: %w", err)
	}
	return nil
}

// recoverForkPoints scans the fork-points directory at daemon start.
// Two GC predicates:
//   - Rule A (orphan): SpawnPending > 0 AND CreatedAt older than fpOrphanTTL
//     — assume the daemon crashed mid-fork and this fork-point's spawn
//     phase will never complete.
//   - Unreferenced: Descendants empty AND SpawnPending == 0 — no live
//     child holds a MAP_PRIVATE mapping of vmstate.bin AND no spawn is
//     pending; backing files are no longer needed.
//
// Fork-points whose fpinfo.json is missing or unreadable are left alone
// for human inspection; the daemon logs a warning but does not delete them.
func (p *Provider) recoverForkPoints() error {
	root := filepath.Join(p.config.ChrootBase, "forkpoints")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("forkpoint scan: %w", err)
	}
	now := time.Now().UTC()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fpID := e.Name()
		info, err := readFPInfo(p.fpInfoPath(fpID))
		if err != nil {
			// Don't auto-delete: a half-written fpinfo could indicate a
			// crash mid-write that needs investigation.
			slog.Warn("firecracker forkpoint scan: skip unreadable", "fp_id", fpID, "error", err)
			continue
		}
		isOrphan := info.SpawnPending > 0 && now.Sub(info.CreatedAt) > fpOrphanTTL
		isUnreferenced := len(info.Descendants) == 0 && info.SpawnPending == 0
		if isOrphan || isUnreferenced {
			slog.Info("firecracker forkpoint gc on recover",
				"fp_id", fpID,
				"reason", reasonForGC(isOrphan, isUnreferenced),
				"descendants", len(info.Descendants),
				"spawn_pending", info.SpawnPending,
			)
			if err := os.RemoveAll(p.forkPointDir(fpID)); err != nil {
				return fmt.Errorf("forkpoint GC %s: %w", fpID, err)
			}
		}
	}
	return nil
}

func reasonForGC(orphan, unreferenced bool) string {
	switch {
	case orphan && unreferenced:
		return "orphan+unreferenced"
	case orphan:
		return "orphan"
	case unreferenced:
		return "unreferenced"
	default:
		return ""
	}
}
