//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func uiPassword(t *testing.T) string {
	t.Helper()
	pw := os.Getenv("NAVARIS_UI_PASSWORD")
	if pw == "" {
		t.Skip("NAVARIS_UI_PASSWORD not set; skipping UI integration test")
	}
	return pw
}

// httpClientWithJar returns an *http.Client that persists cookies across
// calls for the duration of one test.
func httpClientWithJar(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	return &http.Client{Jar: jar, Timeout: 10 * time.Second}
}

// postJSON POSTs v as JSON to path and returns the response. Honors the
// client's cookie jar.
func postJSON(t *testing.T, c *http.Client, path string, v any) *http.Response {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest("POST", apiURL()+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", apiURL())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

// TestWebUIEndToEnd walks through the seven-step flow from the spec:
//  1. POST /ui/login → 200 + cookie
//  2. GET /v1/projects using only the cookie → 200
//  3. POST /ui/logout → 200, cookie cleared
//  4. GET /v1/projects with the (now-deleted) cookie → 401
//  5. GET /v1/projects with Authorization: Bearer → 200
//  6. POST /ui/login again → 200, fresh cookie
//  7. Open WS to /v1/sandboxes/{id}/attach against a running sandbox → handshake OK
func TestWebUIEndToEnd(t *testing.T) {
	password := uiPassword(t)
	c := httpClientWithJar(t)

	// 1. login
	resp := postJSON(t, c, "/ui/login", map[string]string{"password": password})
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("login: status = %d, body = %s", resp.StatusCode, string(body))
	}
	resp.Body.Close()
	u, _ := url.Parse(apiURL())
	cookies := c.Jar.Cookies(u)
	var hasSession bool
	for _, ck := range cookies {
		if ck.Name == "navaris_ui_session" && ck.Value != "" {
			hasSession = true
		}
	}
	if !hasSession {
		t.Fatalf("login did not set navaris_ui_session cookie, got %+v", cookies)
	}

	// 2. cookie-authenticated API call
	req, _ := http.NewRequest("GET", apiURL()+"/v1/projects", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("cookie GET /v1/projects: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("cookie GET /v1/projects: status = %d, body = %s", resp.StatusCode, string(body))
	}
	resp.Body.Close()

	// 3. logout
	resp = postJSON(t, c, "/ui/logout", nil)
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("logout: status = %d, body = %s", resp.StatusCode, string(body))
	}
	resp.Body.Close()

	// 4. cookie is now cleared → 401
	// Go's cookiejar honors the Max-Age=-1 sent on the logout response and
	// drops navaris_ui_session from subsequent requests, so reusing c here
	// confirms that the post-logout request arrives without any session
	// cookie in the header.
	reqLoggedOut, _ := http.NewRequest("GET", apiURL()+"/v1/projects", nil)
	respLoggedOut, err := c.Do(reqLoggedOut)
	if err != nil {
		t.Fatalf("post-logout GET /v1/projects: %v", err)
	}
	if respLoggedOut.StatusCode != 401 {
		body, _ := io.ReadAll(respLoggedOut.Body)
		respLoggedOut.Body.Close()
		t.Fatalf("post-logout GET /v1/projects: status = %d, want 401, body = %s", respLoggedOut.StatusCode, string(body))
	}
	respLoggedOut.Body.Close()

	// 5. Bearer fallback — use a fresh client so there's no cookie in flight.
	bearerReq, _ := http.NewRequest("GET", apiURL()+"/v1/projects", nil)
	if tok := apiToken(); tok != "" {
		bearerReq.Header.Set("Authorization", "Bearer "+tok)
	} else {
		t.Skip("NAVARIS_TOKEN not set; skipping bearer step")
	}
	bareClient := &http.Client{Timeout: 10 * time.Second}
	bearerResp, err := bareClient.Do(bearerReq)
	if err != nil {
		t.Fatalf("bearer GET /v1/projects: %v", err)
	}
	if bearerResp.StatusCode != 200 {
		body, _ := io.ReadAll(bearerResp.Body)
		bearerResp.Body.Close()
		t.Fatalf("bearer GET /v1/projects: status = %d, body = %s", bearerResp.StatusCode, string(body))
	}
	bearerResp.Body.Close()

	// 6. Re-login so we have a fresh cookie for the WS handshake.
	c2 := httpClientWithJar(t)
	resp = postJSON(t, c2, "/ui/login", map[string]string{"password": password})
	if resp.StatusCode != 200 {
		resp.Body.Close()
		t.Fatalf("re-login: status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 7. Open WS to /v1/sandboxes/{id}/attach. We need a running sandbox for
	// this — create one with the bearer-authenticated client we already have,
	// then attach with the cookie.
	proj := createTestProject(t, newClient())
	cliClient := newClient()
	ctx := context.Background()
	sandboxID := createTestSandbox(t, cliClient, proj.ProjectID, fmt.Sprintf("webui-attach-%d", time.Now().UnixNano()))
	if _, err := cliClient.StartSandboxAndWait(ctx, sandboxID, waitOpts()); err != nil {
		t.Fatalf("start sandbox: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(apiURL(), "http") + "/v1/sandboxes/" + sandboxID + "/attach"
	// Propagate the UI session cookie onto the WS dial by pulling it out of
	// the jar and setting the Cookie header directly.
	uu, _ := url.Parse(apiURL())
	wsOpts := &websocket.DialOptions{
		HTTPClient: c2,
		HTTPHeader: http.Header{},
	}
	for _, ck := range c2.Jar.Cookies(uu) {
		if ck.Name == "navaris_ui_session" {
			wsOpts.HTTPHeader.Set("Cookie", ck.Name+"="+ck.Value)
		}
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(dialCtx, wsURL, wsOpts)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	ws.Close(websocket.StatusNormalClosure, "test done")
}
