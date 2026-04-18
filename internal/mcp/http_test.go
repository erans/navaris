package mcp_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	internalmcp "github.com/navaris/navaris/internal/mcp"
	"github.com/navaris/navaris/internal/testutil/apiserver"
)

// bearerInjector is a RoundTripper that adds an Authorization: Bearer header
// to every outbound request, letting the MCP SDK client authenticate against
// the bearer-only handler.
type bearerInjector struct{ token string }

func (b bearerInjector) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// TestHTTPHandler_RejectsCookieAuth confirms that a navaris_ui_session cookie
// without a Bearer header is rejected with 401. The handler never inspects
// cookies — it only checks Authorization — so any non-bearer request fails here
// before ever reaching the backing API server. Passing a dummy apiURL is fine
// because auth short-circuits before the handler talks to navarisd.
func TestHTTPHandler_RejectsCookieAuth(t *testing.T) {
	handler := internalmcp.NewHTTPHandler(internalmcp.HTTPOptions{
		LocalAPIURL: "http://127.0.0.1:0", // irrelevant — auth short-circuits first
		AuthToken:   "secret",
	})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: "navaris_ui_session", Value: "some-session-value"})
	// No Authorization: Bearer header — cookie auth must not grant MCP access.

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate header on 401 response")
	}
}

// TestHTTPHandler_AcceptsBearerToken verifies that a valid bearer token reaches
// the MCP server and that the tools list is non-empty — confirming the full
// auth→handler→NewServer path works end-to-end over HTTP.
func TestHTTPHandler_AcceptsBearerToken(t *testing.T) {
	apiURL, _, _ := apiserver.New(t)
	const token = "test-token"

	handler := internalmcp.NewHTTPHandler(internalmcp.HTTPOptions{
		LocalAPIURL: apiURL,
		AuthToken:   token,
	})

	httpSrv := httptest.NewServer(handler)
	t.Cleanup(httpSrv.Close)

	mc := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-bearer"}, nil)
	transport := &mcpsdk.StreamableClientTransport{
		Endpoint: httpSrv.URL,
		HTTPClient: &http.Client{
			Transport: bearerInjector{token: token},
		},
	}

	sess, err := mc.Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })

	tools, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("expected at least one tool, got none")
	}
}

// TestHTTPHandler_MountedEndToEnd_ReadAndMutate exercises the full mounted
// path: navarisd's middleware chain → MCP handler at /v1/mcp → loopback back
// into navarisd for tool execution. It calls both a read tool (sandbox_list)
// and a mutating tool (sandbox_create) to confirm the complete round-trip works.
func TestHTTPHandler_MountedEndToEnd_ReadAndMutate(t *testing.T) {
	const token = "test-token"
	baseURL, disp, _ := apiserver.New(t, apiserver.WithMCP(false))
	mcpURL := baseURL + "/v1/mcp"

	mc := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-mounted"}, nil)
	transport := &mcpsdk.StreamableClientTransport{
		Endpoint: mcpURL,
		HTTPClient: &http.Client{
			Transport: bearerInjector{token: token},
		},
	}

	sess, err := mc.Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })

	// ListTools: both sandbox_list and sandbox_create must be present.
	tools, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	toolNames := make(map[string]struct{}, len(tools.Tools))
	for _, tl := range tools.Tools {
		toolNames[tl.Name] = struct{}{}
	}
	for _, want := range []string{"sandbox_list", "sandbox_create"} {
		if _, ok := toolNames[want]; !ok {
			t.Errorf("expected tool %q in full-mode tool list, not found", want)
		}
	}

	// Get the default project ID via project_list.
	projRes, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "project_list",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("project_list call: %v", err)
	}
	if projRes.IsError {
		t.Fatalf("project_list tool error: %v", projRes.Content)
	}
	projects := decodeJSONResult(t, projRes)
	arr, ok := projects.([]any)
	if !ok || len(arr) == 0 {
		t.Fatalf("expected at least one project from project_list, got %T: %v", projects, projects)
	}
	projObj, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("expected project object, got %T", arr[0])
	}
	pid, _ := projObj["ProjectID"].(string)
	if pid == "" {
		t.Fatalf("expected non-empty ProjectID from project_list result: %v", projObj)
	}

	// sandbox_list with default project: should return an empty list.
	listRes, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "sandbox_list",
		Arguments: map[string]any{"project_id": pid},
	})
	if err != nil {
		t.Fatalf("sandbox_list call: %v", err)
	}
	if listRes.IsError {
		t.Fatalf("sandbox_list tool error: %v", listRes.Content)
	}
	listVal := decodeJSONResult(t, listRes)
	listArr, ok := listVal.([]any)
	if !ok {
		t.Fatalf("expected array from sandbox_list, got %T", listVal)
	}
	if len(listArr) != 0 {
		t.Errorf("expected empty sandbox list before creation, got %d entries", len(listArr))
	}

	// sandbox_create with wait=false: should return an Operation payload.
	createRes, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "sandbox_create",
		Arguments: map[string]any{
			"project_id": pid,
			"image_id":   "mock-image",
			"name":       "e2e-test-sandbox",
			"wait":       false,
		},
	})
	if err != nil {
		t.Fatalf("sandbox_create call: %v", err)
	}
	if createRes.IsError {
		t.Fatalf("sandbox_create tool error: %v", createRes.Content)
	}
	createVal := decodeJSONResult(t, createRes)
	createObj, ok := createVal.(map[string]any)
	if !ok || len(createObj) == 0 {
		t.Fatalf("expected non-empty object from sandbox_create, got %T: %v", createVal, createVal)
	}
	// wait=false returns an operation; verify OperationID is present.
	opID, _ := createObj["OperationID"].(string)
	if opID == "" {
		t.Errorf("expected OperationID in sandbox_create result, got %v", createObj)
	}

	// Wait for the dispatcher to drain so the async worker has fully committed
	// the new sandbox to the store before we read it back.
	disp.WaitIdle()

	// sandbox_list on the same project must now return exactly one entry —
	// proving the mutation side-effect is visible via the read tool.
	listRes2, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "sandbox_list",
		Arguments: map[string]any{"project_id": pid},
	})
	if err != nil {
		t.Fatalf("sandbox_list (post-create) call: %v", err)
	}
	if listRes2.IsError {
		t.Fatalf("sandbox_list (post-create) tool error: %v", listRes2.Content)
	}
	listVal2 := decodeJSONResult(t, listRes2)
	listArr2, ok := listVal2.([]any)
	if !ok {
		t.Fatalf("expected array from sandbox_list (post-create), got %T", listVal2)
	}
	if len(listArr2) == 0 {
		t.Error("expected non-empty sandbox list after sandbox_create, got empty list")
	}
}

// TestHTTPHandler_MountedEndToEnd_RejectsMissingBearer confirms that the
// mounted /v1/mcp endpoint enforces bearer auth when accessed via raw HTTP —
// both with no Authorization header and with a wrong token.
func TestHTTPHandler_MountedEndToEnd_RejectsMissingBearer(t *testing.T) {
	baseURL, _, _ := apiserver.New(t, apiserver.WithMCP(false))
	mcpURL := baseURL + "/v1/mcp"

	cases := []struct {
		name      string
		setHeader func(*http.Request)
	}{
		{name: "no bearer", setHeader: func(*http.Request) {}},
		{name: "wrong token", setHeader: func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer wrong-token")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, mcpURL, bytes.NewBufferString("{}"))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			tc.setHeader(req)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			resp.Body.Close()

			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", resp.StatusCode)
			}
			if resp.Header.Get("WWW-Authenticate") == "" {
				t.Error("expected WWW-Authenticate header on 401 response")
			}
		})
	}
}

// TestHTTPHandler_MountedEndToEnd_ReadOnlyHidesMutations confirms that when the
// MCP handler is mounted in read-only mode, mutating tools are absent from the
// tool list while read-only tools remain present.
func TestHTTPHandler_MountedEndToEnd_ReadOnlyHidesMutations(t *testing.T) {
	const token = "test-token"
	baseURL, _, _ := apiserver.New(t, apiserver.WithMCP(true)) // read-only
	mcpURL := baseURL + "/v1/mcp"

	mc := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-mounted-ro"}, nil)
	transport := &mcpsdk.StreamableClientTransport{
		Endpoint: mcpURL,
		HTTPClient: &http.Client{
			Transport: bearerInjector{token: token},
		},
	}

	sess, err := mc.Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })

	tools, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	toolNames := make(map[string]struct{}, len(tools.Tools))
	for _, tl := range tools.Tools {
		toolNames[tl.Name] = struct{}{}
	}

	// Read tool must be present.
	if _, ok := toolNames["sandbox_list"]; !ok {
		t.Error("expected read-only tool sandbox_list to be present in read-only mode")
	}

	// Mutating tools must be absent.
	for _, mutator := range []string{"sandbox_create", "snapshot_create", "operation_cancel"} {
		if _, ok := toolNames[mutator]; ok {
			t.Errorf("mutating tool %q should be hidden in read-only mode", mutator)
		}
	}
}
