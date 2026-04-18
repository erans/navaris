package client_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/testutil/apiserver"
	"github.com/navaris/navaris/pkg/client"
)

// fakeSessionConn implements net.Conn-ish bidirectional pipe semantics that
// the bridge in internal/api/attach.go expects: Read returns server output,
// Write accepts client input.
type fakeSessionConn struct {
	mu       sync.Mutex
	outBuf   bytes.Buffer // server → client
	inBuf    bytes.Buffer // client → server
	closedCh chan struct{}
}

func newFakeSessionConn() *fakeSessionConn {
	return &fakeSessionConn{closedCh: make(chan struct{})}
}

// Read blocks until data is available in outBuf or the conn is closed.
// The bridge's stdout→ws goroutine calls this in a loop with a 4 KB buffer.
func (f *fakeSessionConn) Read(p []byte) (int, error) {
	for {
		f.mu.Lock()
		if f.outBuf.Len() > 0 {
			n, err := f.outBuf.Read(p)
			f.mu.Unlock()
			return n, err
		}
		f.mu.Unlock()
		select {
		case <-f.closedCh:
			return 0, io.EOF
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Write captures stdin from the client.
func (f *fakeSessionConn) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inBuf.Write(p)
}

func (f *fakeSessionConn) Close() error {
	select {
	case <-f.closedCh:
	default:
		close(f.closedCh)
	}
	return nil
}

// pushOutput simulates the PTY producing output for the client.
func (f *fakeSessionConn) pushOutput(b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outBuf.Write(b)
}

func (f *fakeSessionConn) capturedInput() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.inBuf.Bytes()...)
}

func TestAttach_ConnectsAndExchangesData(t *testing.T) {
	apiURL, _, mock := apiserver.New(t)

	fake := newFakeSessionConn()
	resizeCh := make(chan [2]int, 1)

	mock.AttachSessionFn = func(_ context.Context, _ domain.BackendRef, _ domain.SessionRequest) (domain.SessionHandle, error) {
		return domain.SessionHandle{
			Conn: fake,
			Resize: func(cols, rows int) error {
				resizeCh <- [2]int{cols, rows}
				return nil
			},
			Close: fake.Close,
		}, nil
	}

	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	proj, err := c.CreateProject(context.Background(), client.CreateProjectRequest{Name: "attach-test"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(context.Background(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "attach-target",
		ImageID:   "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(context.Background(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	sess, err := c.CreateSession(context.Background(), op.ResourceID, client.CreateSessionRequest{Shell: "bash", Backing: "direct"})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := c.AttachSandbox(ctx, op.ResourceID, sess.SessionID)
	if err != nil {
		t.Fatalf("AttachSandbox: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// Push fake server output, then expect the client Read to receive it.
	want := []byte("hello-from-pty\r\n")
	fake.pushOutput(want)

	got := make([]byte, 64)
	n, err := conn.Read(got)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(got[:n], want) {
		t.Fatalf("output mismatch: got %q, want %q", got[:n], want)
	}

	// Send client input, then verify the fake captured it.
	in := []byte("ls\n")
	if _, err := conn.Write(in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Allow the bridge goroutine to forward.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bytes.Contains(fake.capturedInput(), in) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !bytes.Contains(fake.capturedInput(), in) {
		t.Fatalf("server did not receive %q within deadline; got %q", in, fake.capturedInput())
	}

	// Send a resize; verify the fake's Resize was called.
	if err := conn.Resize(80, 24); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	select {
	case got := <-resizeCh:
		if got != [2]int{80, 24} {
			t.Errorf("resize: got %v, want [80 24]", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("resize never propagated")
	}
}

func TestAttach_ReturnsEOFOnServerClose(t *testing.T) {
	apiURL, _, mock := apiserver.New(t)
	fake := newFakeSessionConn()

	mock.AttachSessionFn = func(_ context.Context, _ domain.BackendRef, _ domain.SessionRequest) (domain.SessionHandle, error) {
		return domain.SessionHandle{Conn: fake, Close: fake.Close}, nil
	}

	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))
	proj, _ := c.CreateProject(context.Background(), client.CreateProjectRequest{Name: "attach-eof"})
	op, _ := c.CreateSandbox(context.Background(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "eof", ImageID: "mock-image",
	})
	if _, err := c.WaitForOperation(context.Background(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	sess, _ := c.CreateSession(context.Background(), op.ResourceID, client.CreateSessionRequest{Backing: "direct"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := c.AttachSandbox(ctx, op.ResourceID, sess.SessionID)
	if err != nil {
		t.Fatalf("AttachSandbox: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// Close the server-side fake → bridge tears down → client Read gets EOF.
	fake.Close()
	got := make([]byte, 64)
	_, err = conn.Read(got)
	if err == nil {
		t.Fatal("expected error after server close")
	}
	if !errors.Is(err, io.EOF) {
		t.Errorf("expected io.EOF, got %v", err)
	}
}
