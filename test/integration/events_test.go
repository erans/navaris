//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/navaris/navaris/pkg/client"
	"nhooyr.io/websocket"
)

func TestEventStreamReceivesSandboxEvents(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	// Connect to event stream via WebSocket.
	u, err := url.Parse(apiURL())
	if err != nil {
		t.Fatalf("parse API URL: %v", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = "/v1/events"
	wsURL := u.String()

	headers := http.Header{}
	if tok := apiToken(); tok != "" {
		headers.Set("Authorization", "Bearer "+tok)
	}

	wsCtx, wsCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer wsCancel()

	conn, _, err := websocket.Dial(wsCtx, wsURL, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		// Skip only when the server can't do WebSocket upgrades (missing
		// http.Hijacker support, returns 501). Fail on any other dial error.
		if strings.Contains(err.Error(), "got 501") {
			t.Skipf("server does not support WebSocket upgrades (501): %v", err)
		}
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	// Collect events in background.
	type event struct {
		Type       string `json:"type"`
		ResourceID string `json:"resource_id"`
	}
	eventCh := make(chan event, 100)
	go func() {
		for {
			_, msg, err := conn.Read(wsCtx)
			if err != nil {
				return
			}
			var ev event
			if json.Unmarshal(msg, &ev) == nil {
				eventCh <- ev
			}
		}
	}()

	// Create a sandbox to trigger events.
	proj := createTestProject(t, c)
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "events-test-sbx",
		ImageID:   baseImage(),
	}, waitOpts())
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create failed: %s", op.ErrorText)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() {
		_, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts())
	})

	// Wait for at least one event related to our sandbox.
	timeout := time.After(30 * time.Second)
	received := false
	for !received {
		select {
		case ev := <-eventCh:
			if ev.ResourceID == sandboxID {
				t.Logf("received event: type=%s resource=%s", ev.Type, ev.ResourceID)
				received = true
			}
		case <-timeout:
			t.Fatal("timeout waiting for sandbox event on WebSocket")
		}
	}
}
