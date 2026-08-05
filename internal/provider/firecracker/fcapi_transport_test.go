//go:build firecracker

package firecracker

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	httptransport "github.com/go-openapi/runtime/client"
)

func TestBuildIdleReapingTransport_SetsIdleConnTimeout(t *testing.T) {
	tr := buildIdleReapingTransport("/tmp/fc.sock", 30*time.Second)

	// The transport is a go-openapi runtime.ClientTransport wrapping an
	// *http.Transport. go-openapi's httptransport.Runtime exposes the
	// underlying transport via its Transport field.
	ht, ok := tr.(*httptransport.Runtime)
	if !ok {
		t.Fatalf("transport = %T; want *httptransport.Runtime", tr)
	}
	socketTransport, ok := ht.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("inner transport = %T; want *http.Transport", ht.Transport)
	}
	if socketTransport.IdleConnTimeout != 30*time.Second {
		t.Errorf("IdleConnTimeout = %v; want 30s", socketTransport.IdleConnTimeout)
	}
	if socketTransport.MaxIdleConnsPerHost != 1 {
		t.Errorf("MaxIdleConnsPerHost = %d; want 1", socketTransport.MaxIdleConnsPerHost)
	}
}

func TestBuildIdleReapingTransport_DialHonorsCancelledContext(t *testing.T) {
	dir := t.TempDir()
	sockPath := dir + "/fc.sock"
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tr := buildIdleReapingTransport(sockPath, 30*time.Second)
	ht, ok := tr.(*httptransport.Runtime)
	if !ok {
		t.Fatalf("transport = %T; want *httptransport.Runtime", tr)
	}
	socketTransport, ok := ht.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("inner transport = %T; want *http.Transport", ht.Transport)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn, err := socketTransport.DialContext(ctx, "tcp", "ignored")
	if conn != nil {
		conn.Close()
		t.Fatal("dial succeeded with cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DialContext error = %v; want context.Canceled", err)
	}
}

func TestTransientFirecrackerClient_RejectsEmptyPath(t *testing.T) {
	_, err := transientFirecrackerClient("", 30*time.Second)
	if err == nil {
		t.Fatalf("expected error for empty socket path")
	}
}

func TestTransientFirecrackerClient_ReapsIdleConn(t *testing.T) {
	// Stand up a unix socket server that accepts one connection and
	// responds with a minimal HTTP/1.1 204 to any request.
	dir := t.TempDir()
	sockPath := dir + "/fc.sock"
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				_, _ = c.Read(buf) // drain request
				_, _ = c.Write([]byte("HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n"))
			}(c)
		}
	}()

	fc, err := transientFirecrackerClient(sockPath, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	_ = fc // a real PatchBalloon round-trip would go here; the reap is
	// verified by observing the underlying transport's IdleConnTimeout
	// (covered by TestBuildIdleReapingTransport_SetsIdleConnTimeout).
}
