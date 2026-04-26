package sqlite_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

func TestBoostStore_UpsertGetDelete(t *testing.T) {
	s := newTestStore(t)
	bs := s.BoostStore()
	ctx := t.Context()

	proj := createTestProject(t, s)
	sbx := createTestSandbox(t, s, proj.ProjectID, "sbx-boost-1")

	cpu, mem, boostedCPU, boostedMem := 1, 256, 4, 1024
	now := time.Now().UTC().Truncate(time.Microsecond)
	b := &domain.Boost{
		BoostID:               "b-" + uuid.NewString()[:8],
		SandboxID:             sbx.SandboxID,
		OriginalCPULimit:      &cpu,
		OriginalMemoryLimitMB: &mem,
		BoostedCPULimit:       &boostedCPU,
		BoostedMemoryLimitMB:  &boostedMem,
		StartedAt:             now,
		ExpiresAt:             now.Add(10 * time.Minute),
		State:                 domain.BoostActive,
	}
	if err := bs.Upsert(ctx, b); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := bs.Get(ctx, sbx.SandboxID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BoostID != b.BoostID || got.State != domain.BoostActive {
		t.Fatalf("got %+v", got)
	}
	if got.BoostedCPULimit == nil || *got.BoostedCPULimit != 4 {
		t.Fatalf("BoostedCPULimit = %+v", got.BoostedCPULimit)
	}
	if got.BoostedMemoryLimitMB == nil || *got.BoostedMemoryLimitMB != 1024 {
		t.Fatalf("BoostedMemoryLimitMB = %+v", got.BoostedMemoryLimitMB)
	}

	if err := bs.Delete(ctx, b.BoostID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := bs.Get(ctx, sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestBoostStore_UpdateState(t *testing.T) {
	s := newTestStore(t)
	bs := s.BoostStore()
	ctx := t.Context()

	proj := createTestProject(t, s)
	sbx := createTestSandbox(t, s, proj.ProjectID, "sbx-boost-2")

	now := time.Now().UTC()
	b := &domain.Boost{
		BoostID:   "b-state",
		SandboxID: sbx.SandboxID,
		StartedAt: now,
		ExpiresAt: now.Add(time.Minute),
		State:     domain.BoostActive,
	}
	if err := bs.Upsert(ctx, b); err != nil {
		t.Fatal(err)
	}

	if err := bs.UpdateState(ctx, b.BoostID, domain.BoostRevertFailed, 5, "boom"); err != nil {
		t.Fatal(err)
	}
	got, err := bs.GetByID(ctx, b.BoostID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.BoostRevertFailed || got.RevertAttempts != 5 || got.LastError != "boom" {
		t.Fatalf("after UpdateState: %+v", got)
	}
}

func TestBoostStore_CascadesOnSandboxDelete(t *testing.T) {
	s := newTestStore(t)
	bs := s.BoostStore()
	ctx := t.Context()

	proj := createTestProject(t, s)
	sbx := createTestSandbox(t, s, proj.ProjectID, "sbx-boost-3")
	now := time.Now().UTC()
	if err := bs.Upsert(ctx, &domain.Boost{
		BoostID: "b-cascade", SandboxID: sbx.SandboxID, StartedAt: now,
		ExpiresAt: now.Add(time.Minute), State: domain.BoostActive,
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.SandboxStore().Delete(ctx, sbx.SandboxID); err != nil {
		t.Fatal(err)
	}
	if _, err := bs.GetByID(ctx, "b-cascade"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after sandbox delete, got %v", err)
	}
}

func TestBoostStore_ListAll(t *testing.T) {
	s := newTestStore(t)
	bs := s.BoostStore()
	ctx := t.Context()

	proj := createTestProject(t, s)
	sbx1 := createTestSandbox(t, s, proj.ProjectID, "sbx-list-1")
	sbx2 := createTestSandbox(t, s, proj.ProjectID, "sbx-list-2")

	now := time.Now().UTC()
	for _, sbx := range []*domain.Sandbox{sbx1, sbx2} {
		if err := bs.Upsert(ctx, &domain.Boost{
			BoostID:   "b-" + sbx.SandboxID,
			SandboxID: sbx.SandboxID,
			StartedAt: now,
			ExpiresAt: now.Add(time.Minute),
			State:     domain.BoostActive,
		}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := bs.ListAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll = %d rows, want 2", len(all))
	}
}
