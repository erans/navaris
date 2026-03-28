package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/navaris/navaris/internal/domain"
)

type GCConfig struct {
	Interval         time.Duration
	StaleOpThreshold time.Duration
}

type GC struct {
	sandboxes domain.SandboxStore
	snapshots domain.SnapshotStore
	ops       domain.OperationStore
	provider  domain.Provider
	config    GCConfig
	done      chan struct{}
}

func NewGC(sandboxes domain.SandboxStore, snapshots domain.SnapshotStore, ops domain.OperationStore, provider domain.Provider, cfg GCConfig) *GC {
	if cfg.Interval == 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.StaleOpThreshold == 0 {
		cfg.StaleOpThreshold = 24 * time.Hour
	}
	return &GC{
		sandboxes: sandboxes,
		snapshots: snapshots,
		ops:       ops,
		provider:  provider,
		config:    cfg,
		done:      make(chan struct{}),
	}
}

func (gc *GC) Start() {
	go gc.loop()
}

func (gc *GC) Stop() {
	close(gc.done)
}

func (gc *GC) Sweep(ctx context.Context) {
	gc.sweepExpiredSandboxes(ctx)
	gc.sweepOrphanedSnapshots(ctx)
	gc.sweepStaleOperations(ctx)
}

func (gc *GC) loop() {
	ticker := time.NewTicker(gc.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-gc.done:
			return
		case <-ticker.C:
			gc.Sweep(context.Background())
		}
	}
}

func (gc *GC) sweepExpiredSandboxes(ctx context.Context) {
	expired, err := gc.sandboxes.ListExpired(ctx, time.Now().UTC())
	if err != nil {
		slog.Error("gc: list expired sandboxes", "error", err)
		return
	}
	for _, sbx := range expired {
		ref := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}
		if err := gc.provider.DestroySandbox(ctx, ref); err != nil {
			slog.Error("gc: destroy expired sandbox", "sandbox_id", sbx.SandboxID, "error", err)
			continue
		}
		sbx.State = domain.SandboxDestroyed
		sbx.UpdatedAt = time.Now().UTC()
		if err := gc.sandboxes.Update(ctx, sbx); err != nil {
			slog.Error("gc: update expired sandbox", "sandbox_id", sbx.SandboxID, "error", err)
		}
	}
}

func (gc *GC) sweepOrphanedSnapshots(ctx context.Context) {
	orphaned, err := gc.snapshots.ListOrphaned(ctx)
	if err != nil {
		slog.Error("gc: list orphaned snapshots", "error", err)
		return
	}
	for _, snap := range orphaned {
		ref := domain.BackendRef{Backend: snap.Backend, Ref: snap.BackendRef}
		if err := gc.provider.DeleteSnapshot(ctx, ref); err != nil {
			slog.Error("gc: delete orphaned snapshot", "snapshot_id", snap.SnapshotID, "error", err)
		}
		if err := gc.snapshots.Delete(ctx, snap.SnapshotID); err != nil {
			slog.Error("gc: delete orphaned snapshot record", "snapshot_id", snap.SnapshotID, "error", err)
		}
	}
}

func (gc *GC) sweepStaleOperations(ctx context.Context) {
	threshold := time.Now().UTC().Add(-gc.config.StaleOpThreshold)
	stale, err := gc.ops.ListStale(ctx, threshold)
	if err != nil {
		slog.Error("gc: list stale operations", "error", err)
		return
	}
	for _, op := range stale {
		// For stale operations, we just clean up the metadata but keep the record
		// (could also delete, but keeping for audit)
		slog.Info("gc: found stale operation", "operation_id", op.OperationID, "state", op.State)
	}
}
