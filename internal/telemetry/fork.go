package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// RecordStorageCloneDuration records the time taken by a single
// storage.Backend.CloneFile call. Backend is the resolved backend name
// (copy / reflink / etc.), sourceRoot is the parent directory under which
// the source file lives — typically chroot-base, image-dir, or snapshot-dir.
func RecordStorageCloneDuration(ctx context.Context, backend, sourceRoot string, d time.Duration) {
	h, _ := otel.Meter("navaris.storage").Float64Histogram(
		"storage.clone.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Time per storage.Backend.CloneFile"),
	)
	h.Record(ctx, d.Seconds(),
		metric.WithAttributes(
			attribute.String("backend", backend),
			attribute.String("source_root", sourceRoot),
		),
	)
}

// RecordForkPauseDuration records the time the parent VM was paused while a
// fork-point's memory + disk state was captured. hostCoWCapable is true
// when the disk-clone step used a CoW backend (reflink/btrfs/zfs).
func RecordForkPauseDuration(ctx context.Context, hostCoWCapable bool, d time.Duration) {
	h, _ := otel.Meter("navaris.fork").Float64Histogram(
		"fork.pause.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Time the parent VM was paused during fork-point materialization"),
	)
	h.Record(ctx, d.Seconds(),
		metric.WithAttributes(attribute.Bool("host_cow_capable", hostCoWCapable)),
	)
}

// RecordForkChildSpawnDuration records the time to spawn a single fork child
// from a fork-point (file clones + identity allocation + VMInfo write).
func RecordForkChildSpawnDuration(ctx context.Context, d time.Duration) {
	h, _ := otel.Meter("navaris.fork").Float64Histogram(
		"fork.child.spawn.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Time to spawn a single fork child from a fork-point"),
	)
	h.Record(ctx, d.Seconds())
}
