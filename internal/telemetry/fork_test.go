package telemetry

import (
	"context"
	"testing"
	"time"
)

// These tests primarily verify the helpers don't panic and that the
// instruments construct cleanly. Verifying actual metric emission would
// require setting up a MeterProvider with a manual reader as the existing
// telemetry tests do; for the helpers alone, smoke-testing is enough.

func TestRecordStorageCloneDuration_Smoke(t *testing.T) {
	RecordStorageCloneDuration(context.Background(), "copy", "/tmp", 1*time.Millisecond)
	RecordStorageCloneDuration(context.Background(), "reflink", "/srv", 100*time.Microsecond)
}

func TestRecordForkPauseDuration_Smoke(t *testing.T) {
	RecordForkPauseDuration(context.Background(), true, 50*time.Millisecond)
	RecordForkPauseDuration(context.Background(), false, 1*time.Second)
}

func TestRecordForkChildSpawnDuration_Smoke(t *testing.T) {
	RecordForkChildSpawnDuration(context.Background(), 200*time.Millisecond)
}
