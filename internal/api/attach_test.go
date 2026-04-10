package api_test

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"nhooyr.io/websocket"
)

func TestAttachReturns404ForUnknownSandbox(t *testing.T) {
	env := newTestEnv(t)
	req := httptest.NewRequest("GET", "/v1/sandboxes/sbx_missing/attach", nil)
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAttachReturns409ForStoppedSandbox(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createTestSandbox(t, env, projectID)
	// Stop it so State != Running.
	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/"+sandboxID+"/stop", nil)
	if rec.Code/100 != 2 {
		t.Fatalf("stop: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	env.dispatcher.WaitIdle()

	req := httptest.NewRequest("GET", "/v1/sandboxes/"+sandboxID+"/attach", nil)
	rec = httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	if rec.Code != 409 {
		t.Fatalf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
}

// pipeConn is an io.ReadWriteCloser that the bridge consumes as if it were a
// real provider session. Internally it is two io.Pipes, wired so the test can
// drive both halves of the bidirectional stream.
//
// The bridge's view (what gets exercised in this file):
//   - bridge calls conn.Read(...)  → reads bytes the test injected as
//     "provider stdout" via providerStdoutW.
//   - bridge calls conn.Write(...) → writes bytes the test will observe as
//     "client stdin arriving at the provider" via providerStdinR.
//
// Concretely: stdoutR is the read end of one pipe (conn.Read pulls from here,
// and providerStdoutW is that pipe's write end). stdinW is the write end of a
// second pipe (conn.Write pushes into here, and providerStdinR is that pipe's
// read end). The test drives providerStdoutW and reads providerStdinR; the
// bridge sees conn and calls Read/Write through it.
type pipeConn struct {
	stdoutR *io.PipeReader // conn.Read pulls from here
	stdinW  *io.PipeWriter // conn.Write pushes to here
}

func (p *pipeConn) Read(b []byte) (int, error)  { return p.stdoutR.Read(b) }
func (p *pipeConn) Write(b []byte) (int, error) { return p.stdinW.Write(b) }
func (p *pipeConn) Close() error {
	p.stdoutR.Close()
	p.stdinW.Close()
	return nil
}

// newPipeConn returns a pipeConn plus test-side handles:
//   - providerStdoutW: write to this to simulate provider stdout (the bridge's
//     stdout-goroutine will read it via conn.Read).
//   - providerStdinR: read from this to see what the bridge forwarded as client
//     stdin (the bridge's stdin-goroutine wrote it via conn.Write).
func newPipeConn() (conn *pipeConn, providerStdoutW *io.PipeWriter, providerStdinR *io.PipeReader) {
	stdoutR, stdoutW := io.Pipe() // test-writer → conn.Read
	stdinR, stdinW := io.Pipe()   // conn.Write → test-reader
	return &pipeConn{stdoutR: stdoutR, stdinW: stdinW}, stdoutW, stdinR
}

func TestAttachBridgesStdinAndStdout(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createTestSandbox(t, env, projectID)

	conn, providerStdoutW, providerStdinR := newPipeConn()
	resizeCalls := make(chan [2]int, 4)

	env.mock.AttachSessionFn = func(_ context.Context, _ domain.BackendRef, _ domain.SessionRequest) (domain.SessionHandle, error) {
		return domain.SessionHandle{
			Conn: conn,
			Resize: func(w, h int) error {
				resizeCalls <- [2]int{w, h}
				return nil
			},
			Close: func() error { return conn.Close() },
		}, nil
	}

	srv := httptest.NewServer(env.handler)
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/sandboxes/" + sandboxID + "/attach"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	// provider → ws: the fake provider emits "hello". The bridge's
	// "stdout → ws" goroutine reads it via conn.Read and forwards it as a
	// binary WS frame.
	go func() {
		_, _ = providerStdoutW.Write([]byte("hello"))
	}()
	msgType, got, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msgType != websocket.MessageBinary {
		t.Fatalf("msgType = %v, want Binary", msgType)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q, want hello", string(got))
	}

	// ws → provider stdin: send binary from the client side and verify it
	// appears on the provider-facing pipe via providerStdinR.
	if err := c.Write(ctx, websocket.MessageBinary, []byte("world")); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(providerStdinR, buf); err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	if string(buf) != "world" {
		t.Fatalf("got %q, want world", string(buf))
	}

	// resize text frame → Resize callback.
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":132,"rows":40}`)); err != nil {
		t.Fatalf("write text: %v", err)
	}
	select {
	case wh := <-resizeCalls:
		if wh != [2]int{132, 40} {
			t.Fatalf("resize = %v, want [132 40]", wh)
		}
	case <-time.After(time.Second):
		t.Fatal("resize callback not invoked")
	}
}

func TestAttachWithSessionParam(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createTestSandbox(t, env, projectID)

	// Create a session via the service so it exists in the DB.
	sess, err := env.sessions.Create(context.Background(), sandboxID, domain.SessionBackingDirect, "/bin/bash")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	sessionID := sess.SessionID

	// Capture the SessionRequest passed to AttachSession.
	var captured domain.SessionRequest
	conn, _, _ := newPipeConn()
	env.mock.AttachSessionFn = func(_ context.Context, _ domain.BackendRef, req domain.SessionRequest) (domain.SessionHandle, error) {
		captured = req
		return domain.SessionHandle{
			Conn:  conn,
			Close: func() error { return conn.Close() },
		}, nil
	}

	srv := httptest.NewServer(env.handler)
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") +
		"/v1/sandboxes/" + sandboxID + "/attach?session=" + sessionID

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.Close(websocket.StatusNormalClosure, "")

	// Verify that for a direct session the provider gets an empty Shell
	// (so detectShell picks the right one for the container).
	if len(captured.Command) != 0 {
		t.Fatalf("Command = %v, want empty (direct session uses detectShell)", captured.Command)
	}
	if captured.Shell != "" {
		t.Fatalf("Shell = %q, want empty", captured.Shell)
	}
}
