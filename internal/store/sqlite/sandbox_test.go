package sqlite_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/store/sqlite"
)

func createTestProject(t *testing.T, s *sqlite.Store) *domain.Project {
	t.Helper()
	p := &domain.Project{
		ProjectID: uuid.NewString(),
		Name:      "proj-" + uuid.NewString()[:8],
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.ProjectStore().Create(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func createTestSandbox(t *testing.T, s *sqlite.Store, projectID, name string) *domain.Sandbox {
	t.Helper()
	sbx := &domain.Sandbox{
		SandboxID:   uuid.NewString(),
		ProjectID:   projectID,
		Name:        name,
		State:       domain.SandboxPending,
		Backend:     "incus",
		NetworkMode: domain.NetworkIsolated,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	if err := s.SandboxStore().Create(t.Context(), sbx); err != nil {
		t.Fatal(err)
	}
	return sbx
}

func createTestSandboxWithExpiry(t *testing.T, s *sqlite.Store, projectID, name string, expiresAt *time.Time) *domain.Sandbox {
	t.Helper()
	sbx := &domain.Sandbox{
		SandboxID:   uuid.NewString(),
		ProjectID:   projectID,
		Name:        name,
		State:       domain.SandboxRunning,
		Backend:     "incus",
		NetworkMode: domain.NetworkIsolated,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
		ExpiresAt:   expiresAt,
	}
	if err := s.SandboxStore().Create(t.Context(), sbx); err != nil {
		t.Fatal(err)
	}
	return sbx
}

func TestSandboxCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	proj := createTestProject(t, s)
	now := time.Now().UTC()
	cpu := 2
	mem := 1024
	sbx := &domain.Sandbox{
		SandboxID:     uuid.NewString(),
		ProjectID:     proj.ProjectID,
		Name:          "test-sbx",
		State:         domain.SandboxPending,
		Backend:       "incus",
		NetworkMode:   domain.NetworkIsolated,
		CPULimit:      &cpu,
		MemoryLimitMB: &mem,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.SandboxStore().Create(t.Context(), sbx); err != nil {
		t.Fatal(err)
	}
	got, err := s.SandboxStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "test-sbx" {
		t.Error("wrong name")
	}
	if *got.CPULimit != 2 {
		t.Error("wrong cpu")
	}
	if *got.MemoryLimitMB != 1024 {
		t.Error("wrong memory")
	}
}

func TestSandboxListByProject(t *testing.T) {
	s := newTestStore(t)
	p1 := createTestProject(t, s)
	p2 := createTestProject(t, s)
	createTestSandbox(t, s, p1.ProjectID, "s1")
	createTestSandbox(t, s, p1.ProjectID, "s2")
	createTestSandbox(t, s, p2.ProjectID, "s3")
	list, _ := s.SandboxStore().List(t.Context(), domain.SandboxFilter{ProjectID: &p1.ProjectID})
	if len(list) != 2 {
		t.Errorf("expected 2 sandboxes for project 1, got %d", len(list))
	}
}

func TestSandboxListExpired(t *testing.T) {
	s := newTestStore(t)
	proj := createTestProject(t, s)
	past := time.Now().UTC().Add(-time.Hour)
	future := time.Now().UTC().Add(time.Hour)
	sbx1 := createTestSandboxWithExpiry(t, s, proj.ProjectID, "expired", &past)
	createTestSandboxWithExpiry(t, s, proj.ProjectID, "notyet", &future)
	expired, _ := s.SandboxStore().ListExpired(t.Context(), time.Now().UTC())
	if len(expired) != 1 || expired[0].SandboxID != sbx1.SandboxID {
		t.Errorf("expected 1 expired sandbox, got %d", len(expired))
	}
}

func TestSandboxUpdate(t *testing.T) {
	s := newTestStore(t)
	proj := createTestProject(t, s)
	sbx := createTestSandbox(t, s, proj.ProjectID, "upd")
	sbx.State = domain.SandboxRunning
	sbx.UpdatedAt = time.Now().UTC()
	if err := s.SandboxStore().Update(t.Context(), sbx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.SandboxStore().Get(t.Context(), sbx.SandboxID)
	if got.State != domain.SandboxRunning {
		t.Error("state not updated")
	}
}

func TestSandboxNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.SandboxStore().Get(t.Context(), "nonexistent")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSandboxDuplicateName(t *testing.T) {
	s := newTestStore(t)
	proj := createTestProject(t, s)
	createTestSandbox(t, s, proj.ProjectID, "dup")
	sbx2 := &domain.Sandbox{
		SandboxID: uuid.NewString(), ProjectID: proj.ProjectID, Name: "dup",
		State: domain.SandboxPending, Backend: "incus", NetworkMode: domain.NetworkIsolated,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	err := s.SandboxStore().Create(t.Context(), sbx2)
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

func newTestSandboxFixture(projectID, name string) *domain.Sandbox {
	return &domain.Sandbox{
		SandboxID:   uuid.NewString(),
		ProjectID:   projectID,
		Name:        name,
		State:       domain.SandboxPending,
		Backend:     "incus",
		NetworkMode: domain.NetworkIsolated,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
}

func TestSandboxStore_EnableBoostChannel_Roundtrip(t *testing.T) {
	s := newTestStore(t)
	ss := s.SandboxStore()
	ctx := t.Context()
	proj := createTestProject(t, s)

	cases := []struct {
		name    string
		enabled bool
	}{
		{"enabled", true},
		{"disabled", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sbx := newTestSandboxFixture(proj.ProjectID, tc.name)
			sbx.EnableBoostChannel = tc.enabled
			if err := ss.Create(ctx, sbx); err != nil {
				t.Fatal(err)
			}
			got, err := ss.Get(ctx, sbx.SandboxID)
			if err != nil {
				t.Fatal(err)
			}
			if got.EnableBoostChannel != tc.enabled {
				t.Errorf("enable_boost_channel = %v; want %v", got.EnableBoostChannel, tc.enabled)
			}
		})
	}
}
