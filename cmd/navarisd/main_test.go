package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/internal/api"
	"github.com/navaris/navaris/internal/eventbus"
	internalmcp "github.com/navaris/navaris/internal/mcp"
	"github.com/navaris/navaris/internal/provider"
	"github.com/navaris/navaris/internal/service"
	"github.com/navaris/navaris/internal/store/sqlite"
	"github.com/navaris/navaris/internal/worker"
)

func TestDaemonStartsAndServesHealth(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	bus := eventbus.New(64)
	disp := worker.NewDispatcher(store.OperationStore(), bus, 4)
	disp.Start()
	defer disp.Stop()

	prov := provider.NewMock()

	projSvc := service.NewProjectService(store.ProjectStore())
	sbxSvc := service.NewSandboxService(
		store.SandboxStore(), store.SnapshotStore(), store.OperationStore(), store.PortBindingStore(),
		store.SessionStore(), prov, bus, disp, "mock",
	)
	snapSvc := service.NewSnapshotService(
		store.SnapshotStore(), store.SandboxStore(), store.OperationStore(),
		prov, bus, disp,
	)
	imgSvc := service.NewImageService(
		store.ImageStore(), store.SnapshotStore(), store.OperationStore(),
		prov, bus, disp,
	)
	sessSvc := service.NewSessionService(
		store.SessionStore(), store.SandboxStore(), prov, bus,
	)
	opsSvc := service.NewOperationService(store.OperationStore(), disp)

	srv := api.NewServer(api.ServerConfig{
		Projects:   projSvc,
		Sandboxes:  sbxSvc,
		Snapshots:  snapSvc,
		Images:     imgSvc,
		Sessions:   sessSvc,
		Operations: opsSvc,
		Provider:   prov,
		Events:     bus,
		Ports:      store.PortBindingStore(),
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	httpSrv := &http.Server{Handler: srv.Handler()}
	go httpSrv.Serve(ln)
	defer httpSrv.Close()

	addr := fmt.Sprintf("http://%s", ln.Addr().String())

	// Wait for server to be ready
	client := &http.Client{Timeout: 2 * time.Second}
	var resp *http.Response
	for i := 0; i < 10; i++ {
		resp, err = client.Get(addr + "/v1/health")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["Healthy"] != true {
		t.Errorf("expected Healthy=true, got %v", body["Healthy"])
	}
}

func TestParseFlagsUIDefaults(t *testing.T) {
	// parseFlags uses the default CommandLine; swap it out per-test.
	origArgs := os.Args
	origFS := flag.CommandLine
	t.Cleanup(func() {
		os.Args = origArgs
		flag.CommandLine = origFS
	})
	flag.CommandLine = flag.NewFlagSet("navarisd", flag.ContinueOnError)
	os.Args = []string{"navarisd"}

	cfg := parseFlags()
	if cfg.uiPassword != "" {
		t.Errorf("uiPassword default = %q, want empty", cfg.uiPassword)
	}
	if cfg.uiSessionKey != "" {
		t.Errorf("uiSessionKey default = %q, want empty", cfg.uiSessionKey)
	}
	if cfg.uiSessionTTL != 24*time.Hour {
		t.Errorf("uiSessionTTL default = %v, want 24h", cfg.uiSessionTTL)
	}
}

func TestParseFlagsUIExplicit(t *testing.T) {
	origArgs := os.Args
	origFS := flag.CommandLine
	t.Cleanup(func() {
		os.Args = origArgs
		flag.CommandLine = origFS
	})
	flag.CommandLine = flag.NewFlagSet("navarisd", flag.ContinueOnError)
	os.Args = []string{
		"navarisd",
		"--ui-password=s3cret",
		"--ui-session-key=deadbeef",
		"--ui-session-ttl=8h",
	}

	cfg := parseFlags()
	if cfg.uiPassword != "s3cret" {
		t.Errorf("uiPassword = %q, want s3cret", cfg.uiPassword)
	}
	if cfg.uiSessionKey != "deadbeef" {
		t.Errorf("uiSessionKey = %q, want deadbeef", cfg.uiSessionKey)
	}
	if cfg.uiSessionTTL != 8*time.Hour {
		t.Errorf("uiSessionTTL = %v, want 8h", cfg.uiSessionTTL)
	}
}

func TestNormalizeListen(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "bare port", in: ":8080", want: "127.0.0.1:8080"},
		{name: "wildcard ipv4", in: "0.0.0.0:8080", want: "127.0.0.1:8080"},
		{name: "wildcard ipv6", in: "[::]:8080", want: "127.0.0.1:8080"},
		{name: "loopback ipv4", in: "127.0.0.1:8080", want: "127.0.0.1:8080"},
		{name: "localhost", in: "localhost:8080", want: "localhost:8080"},
		{name: "explicit host", in: "some.host:9000", want: "some.host:9000"},
		{name: "loopback ipv6", in: "[::1]:8080", want: "[::1]:8080"},
		{name: "explicit ipv6", in: "[2001:db8::1]:8080", want: "[2001:db8::1]:8080"},
		{name: "invalid addr passthrough", in: "not-a-valid-addr", want: "not-a-valid-addr"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeListen(tc.in)
			if got != tc.want {
				t.Errorf("normalizeListen(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// bearerTransport is an http.RoundTripper that injects a Bearer token.
type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// TestWildcardListenMCPEndToEnd confirms the wildcard→loopback path:
//  1. A listener is bound on 0.0.0.0:0 (wildcard, ephemeral port).
//  2. normalizeListen derives the 127.0.0.1:<port> URL used as LocalAPIURL.
//  3. The MCP HTTP handler is served on the wildcard listener.
//  4. An MCP SDK client connects via the wildcard-derived loopback address and
//     calls tools/list, confirming at least one tool is reachable end-to-end.
func TestWildcardListenMCPEndToEnd(t *testing.T) {
	// -- backing navaris API on 127.0.0.1:0 --
	store, err := sqlite.Open(fmt.Sprintf("file:wildcard_test_%d?mode=memory&cache=shared", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	bus := eventbus.New(64)
	disp := worker.NewDispatcher(store.OperationStore(), bus, 4)
	disp.Start()
	t.Cleanup(func() { disp.Stop() })

	mock := provider.NewMock()
	projSvc := service.NewProjectService(store.ProjectStore())
	sbxSvc := service.NewSandboxService(
		store.SandboxStore(), store.SnapshotStore(), store.OperationStore(),
		store.PortBindingStore(), store.SessionStore(), mock, bus, disp, "mock",
	)
	snapSvc := service.NewSnapshotService(
		store.SnapshotStore(), store.SandboxStore(), store.OperationStore(),
		mock, bus, disp,
	)
	imgSvc := service.NewImageService(
		store.ImageStore(), store.SnapshotStore(), store.OperationStore(),
		mock, bus, disp,
	)
	sessSvc := service.NewSessionService(store.SessionStore(), store.SandboxStore(), mock, bus)
	opsSvc := service.NewOperationService(store.OperationStore(), disp)

	const token = "wildcard-test-token"

	apiLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	apiURL := "http://" + apiLn.Addr().String()

	apiSrv := &http.Server{Handler: api.NewServer(api.ServerConfig{
		Projects:   projSvc,
		Sandboxes:  sbxSvc,
		Snapshots:  snapSvc,
		Images:     imgSvc,
		Sessions:   sessSvc,
		Operations: opsSvc,
		Provider:   mock,
		Events:     bus,
		Ports:      store.PortBindingStore(),
		AuthToken:  token,
	}).Handler()}
	go apiSrv.Serve(apiLn) //nolint:errcheck
	t.Cleanup(func() { apiSrv.Close() })

	// -- wildcard MCP listener: 0.0.0.0:0 --
	// This is the key part of the test: the listener is wildcard, not loopback.
	mcpLn, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}

	// Derive the loopback URL the same way main.go does.
	listenAddr := mcpLn.Addr().String() // "0.0.0.0:<port>"
	loopbackURL := "http://" + normalizeListen(listenAddr)

	mcpHandler := internalmcp.NewHTTPHandler(internalmcp.HTTPOptions{
		LocalAPIURL: apiURL,
		AuthToken:   token,
		ReadOnly:    false,
		MaxTimeout:  5 * time.Minute,
	})

	mcpSrv := &http.Server{Handler: mcpHandler}
	go mcpSrv.Serve(mcpLn) //nolint:errcheck
	t.Cleanup(func() { mcpSrv.Close() })

	// Connect via the loopback URL derived from the wildcard listener address.
	mc := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "wildcard-test"}, nil)
	transport := &mcpsdk.StreamableClientTransport{
		Endpoint: loopbackURL,
		HTTPClient: &http.Client{
			Transport: bearerTransport{token: token},
			Timeout:   10 * time.Second,
		},
	}

	sess, err := mc.Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatalf("connect via wildcard→loopback URL %q: %v", loopbackURL, err)
	}
	t.Cleanup(func() { sess.Close() })

	tools, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("expected at least one tool from wildcard-bound MCP server, got none")
	}
}
