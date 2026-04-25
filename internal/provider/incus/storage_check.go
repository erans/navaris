//go:build incus

package incus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// ErrIncusPoolNotCoW is returned (advisory) when the configured Incus storage
// pool driver does not provide copy-on-write clones. Non-fatal by default;
// strict mode (Config.StrictPoolCoW) elevates it to a startup error.
var ErrIncusPoolNotCoW = errors.New("incus storage pool driver does not support copy-on-write")

// isCowDriver reports whether an Incus pool driver provides CoW clones.
// Unknown drivers default to true (assume capable) — false negatives that
// refuse to start on an exotic-but-capable driver are worse than the
// occasional missing warning on an unfamiliar driver.
func isCowDriver(driver string) bool {
	switch driver {
	case "dir", "lvm", "lvmcluster", "":
		return false
	case "btrfs", "zfs", "lvm-thin", "ceph", "cephfs":
		return true
	default:
		return true
	}
}

// classifyPool returns nil if the driver is CoW-capable; otherwise an error
// wrapping ErrIncusPoolNotCoW with a human-readable message.
func classifyPool(driver string) error {
	if isCowDriver(driver) {
		return nil
	}
	return fmt.Errorf("driver=%q: %w (configure a btrfs/zfs/lvm-thin pool for storage-efficient sandbox cloning)", driver, ErrIncusPoolNotCoW)
}

// CheckPool fetches the configured pool's driver and reports any advisory.
// fetchDriver lets callers inject an Incus client probe; returning a fetch
// error does NOT gate startup — that lets the daemon stay usable when the
// Incus daemon is reachable for sandboxes but the storage probe is flaky.
// strict=true converts a CoW advisory (not a fetch error) into a fatal
// startup error.
func CheckPool(ctx context.Context, fetchDriver func(ctx context.Context) (string, error), strict bool) error {
	driver, err := fetchDriver(ctx)
	if err != nil {
		slog.Warn("incus storage pool probe failed", "error", err)
		return nil
	}
	advisory := classifyPool(driver)
	if advisory == nil {
		slog.Info("incus storage pool is CoW-capable", "driver", driver)
		return nil
	}
	if strict {
		return advisory
	}
	slog.Warn("incus storage pool advisory", "driver", driver, "error", advisory)
	return nil
}
