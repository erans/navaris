package agent

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunBoostProxy_ServerUnreachable verifies the proxy responds with 502
// when the upstream vsock is unavailable. Tests the path that runs in CI
// without /dev/vsock.
func TestRunBoostProxy_ServerUnreachable(t *testing.T) {
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "g.sock")

	go func() { _ = RunBoostProxy(t.Context(), sock, 65000) }()

	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1024)
	n, _ := conn.Read(buf)
	if !strings.HasPrefix(string(buf[:n]), "HTTP/1.1 502") {
		t.Fatalf("expected 502, got %s", string(buf[:n]))
	}
}
