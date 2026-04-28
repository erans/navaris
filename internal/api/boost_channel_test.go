package api

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/eventbus"
	"github.com/navaris/navaris/internal/provider"
	"github.com/navaris/navaris/internal/service"
	"github.com/navaris/navaris/internal/store/sqlite"
	"github.com/navaris/navaris/internal/worker"
)

// boostChannelEnv builds the same service stack the other tests use, then
// constructs a BoostHTTPHandler.
type boostChannelEnv struct {
	store      *sqlite.Store
	mock       *provider.MockProvider
	events     *eventbus.MemoryBus
	dispatcher *worker.Dispatcher
	sandboxes  *service.SandboxService
	boost      *service.BoostService
	handler    *BoostHTTPHandler
}

func newBoostChannelEnv(t *testing.T) *boostChannelEnv {
	t.Helper()
	dbPath := t.TempDir() + "/test.db"
	s, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	mock := provider.NewMock()
	bus := eventbus.New(64)
	disp := worker.NewDispatcher(s.OperationStore(), bus, 4)
	disp.Start()
	t.Cleanup(func() { disp.Stop() })

	sbxSvc := service.NewSandboxService(
		s.SandboxStore(), s.SnapshotStore(), s.OperationStore(),
		s.PortBindingStore(), s.SessionStore(), mock, bus, disp, "mock", true,
	)
	boostSvc := service.NewBoostService(
		s.BoostStore(), s.SandboxStore(), sbxSvc, bus, service.RealClock{}, time.Hour,
	)
	sbxSvc.SetBoostService(boostSvc)

	h := NewBoostHTTPHandler(boostSvc, s.SandboxStore(),
		NewRateLimiter(RateLimiterConfig{Burst: 10, RefillPerSec: 1.0, IdleTTL: time.Hour}, nil))

	return &boostChannelEnv{
		store: s, mock: mock, events: bus, dispatcher: disp,
		sandboxes: sbxSvc, boost: boostSvc, handler: h,
	}
}

func (e *boostChannelEnv) seedRunningSandbox(t *testing.T, name string) *domain.Sandbox {
	t.Helper()
	now := time.Now().UTC()
	// Create a parent project row first — the sandbox store has a FK.
	proj := &domain.Project{
		ProjectID: "proj-" + name,
		Name:      "proj-" + name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := e.store.ProjectStore().Create(t.Context(), proj); err != nil {
		t.Fatal(err)
	}
	cpu, mem := 1, 256
	sbx := &domain.Sandbox{
		SandboxID:          "sbx-" + name,
		ProjectID:          proj.ProjectID,
		Name:               name,
		State:              domain.SandboxRunning,
		Backend:            "mock",
		BackendRef:         "ref-" + name,
		CPULimit:           &cpu,
		MemoryLimitMB:      &mem,
		NetworkMode:        domain.NetworkIsolated,
		EnableBoostChannel: true,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := e.store.SandboxStore().Create(t.Context(), sbx); err != nil {
		t.Fatal(err)
	}
	return sbx
}

// pipeConn pairs a client and server conn over a synchronous in-memory pipe.
func pipeConn() (client, server net.Conn) { return net.Pipe() }

func TestBoostChannel_PostBoost_OK(t *testing.T) {
	env := newBoostChannelEnv(t)
	sbx := env.seedRunningSandbox(t, "ok")

	cli, srv := pipeConn()
	go env.handler.Serve(context.Background(), srv, sbx.SandboxID)

	req := "POST /boost HTTP/1.1\r\nHost: _\r\nContent-Type: application/json\r\nContent-Length: 37\r\nConnection: close\r\n\r\n" +
		`{"cpu_limit":4,"duration_seconds":60}`
	if _, err := cli.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}

	cli.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4096)
	n, _ := cli.Read(buf)
	resp := string(buf[:n])
	cli.Close()

	if !strings.HasPrefix(resp, "HTTP/1.1 200") {
		t.Fatalf("status line: %s", strings.SplitN(resp, "\r\n", 2)[0])
	}
	if !strings.Contains(resp, `"boost_id"`) {
		t.Errorf("body missing boost_id: %s", resp)
	}
}

func TestBoostChannel_PostBoost_BothFieldsOmitted_400(t *testing.T) {
	env := newBoostChannelEnv(t)
	sbx := env.seedRunningSandbox(t, "empty")

	cli, srv := pipeConn()
	go env.handler.Serve(context.Background(), srv, sbx.SandboxID)

	req := "POST /boost HTTP/1.1\r\nHost: _\r\nContent-Type: application/json\r\nContent-Length: 23\r\nConnection: close\r\n\r\n" +
		`{"duration_seconds":60}`
	cli.Write([]byte(req))
	cli.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4096)
	n, _ := cli.Read(buf)
	cli.Close()
	resp := string(buf[:n])
	if !strings.HasPrefix(resp, "HTTP/1.1 400") {
		t.Fatalf("status: %s", strings.SplitN(resp, "\r\n", 2)[0])
	}
}

func TestBoostChannel_GetSandbox_OK(t *testing.T) {
	env := newBoostChannelEnv(t)
	sbx := env.seedRunningSandbox(t, "info")

	cli, srv := pipeConn()
	go env.handler.Serve(context.Background(), srv, sbx.SandboxID)

	req := "GET /sandbox HTTP/1.1\r\nHost: _\r\nConnection: close\r\n\r\n"
	cli.Write([]byte(req))
	cli.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4096)
	n, _ := cli.Read(buf)
	cli.Close()
	resp := string(buf[:n])
	if !strings.HasPrefix(resp, "HTTP/1.1 200") {
		t.Fatalf("status: %s", strings.SplitN(resp, "\r\n", 2)[0])
	}
	if !strings.Contains(resp, sbx.SandboxID) {
		t.Errorf("body missing sandbox_id: %s", resp)
	}
}

func TestBoostChannel_PostBoost_429_AfterBurst(t *testing.T) {
	env := newBoostChannelEnv(t)
	// Drop limiter to burst=1 so we hit 429 quickly.
	env.handler.limiter = NewRateLimiter(RateLimiterConfig{Burst: 1, RefillPerSec: 1.0, IdleTTL: time.Hour}, nil)

	sbx := env.seedRunningSandbox(t, "rl")

	doRaw := func() string {
		cli, srv := pipeConn()
		go env.handler.Serve(context.Background(), srv, sbx.SandboxID)
		req := "POST /boost HTTP/1.1\r\nHost: _\r\nContent-Type: application/json\r\nContent-Length: 37\r\nConnection: close\r\n\r\n" +
			`{"cpu_limit":4,"duration_seconds":60}`
		// Write in a goroutine: with net.Pipe the write blocks until the server
		// reads. When the server rate-limits, it skips reading the request, so
		// the write must be non-blocking from the test's perspective.
		go cli.Write([]byte(req))
		cli.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 4096)
		n, _ := cli.Read(buf)
		cli.Close()
		return string(buf[:n])
	}

	if !strings.HasPrefix(doRaw(), "HTTP/1.1 200") {
		t.Fatal("first request should succeed")
	}
	resp := doRaw()
	if !strings.HasPrefix(resp, "HTTP/1.1 429") {
		t.Fatalf("second request should 429: %s", strings.SplitN(resp, "\r\n", 2)[0])
	}
	if !strings.Contains(resp, "Retry-After") {
		t.Errorf("429 missing Retry-After: %s", resp)
	}
}

// TestWriteServiceError_ResizeError_5xxScrubsDetail unit-tests the in-sandbox
// boost channel's error path for ProviderResizeError responses. The 5xx
// branch must redact backend-supplied Detail (which can include host paths
// or raw I/O strings) while preserving the stable Reason for guest code to
// branch on. 4xx must keep the full message including Detail (it's part of
// the actionable feedback).
func TestWriteServiceError_ResizeError_5xxScrubsDetail(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus string // first line prefix
		wantInBody string
		notInBody  string
	}{
		{
			name: "503 cgroup_unavailable scrubs detail",
			err: &domain.ProviderResizeError{
				Reason: domain.ResizeReasonCgroupUnavailable,
				Detail: "secret-host-path/sys/fs/cgroup/foo",
			},
			wantStatus: "HTTP/1.1 503",
			wantInBody: domain.ResizeReasonCgroupUnavailable,
			notInBody:  "secret-host-path",
		},
		{
			name: "500 cgroup_write_failed scrubs detail",
			err: &domain.ProviderResizeError{
				Reason: domain.ResizeReasonCgroupWriteFailed,
				Detail: "open /sys/fs/cgroup/x/cpu.max: input/output error",
			},
			wantStatus: "HTTP/1.1 500",
			wantInBody: domain.ResizeReasonCgroupWriteFailed,
			notInBody:  "input/output error",
		},
		{
			name: "409 exceeds_ceiling keeps detail",
			err: &domain.ProviderResizeError{
				Reason: domain.ResizeReasonExceedsCeiling,
				Detail: "cpu_limit 8 over ceiling 4",
			},
			wantStatus: "HTTP/1.1 409",
			wantInBody: "cpu_limit 8 over ceiling 4", // 4xx keeps the actionable detail
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cli, srv := pipeConn()
			done := make(chan struct{})
			go func() {
				writeServiceError(srv, tc.err)
				srv.Close()
				close(done)
			}()

			cli.SetReadDeadline(time.Now().Add(time.Second))
			buf := make([]byte, 4096)
			n, _ := cli.Read(buf)
			cli.Close()
			<-done

			resp := string(buf[:n])
			if !strings.HasPrefix(resp, tc.wantStatus) {
				t.Fatalf("status line: %q", strings.SplitN(resp, "\r\n", 2)[0])
			}
			if !strings.Contains(resp, tc.wantInBody) {
				t.Errorf("body missing %q: %s", tc.wantInBody, resp)
			}
			if tc.notInBody != "" && strings.Contains(resp, tc.notInBody) {
				t.Errorf("body must not contain %q (Detail leak): %s", tc.notInBody, resp)
			}
		})
	}
}
