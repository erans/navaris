//go:build incus

package incus

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

type fakeIncusBoostServer struct {
	got []string
}

func (f *fakeIncusBoostServer) Serve(_ context.Context, conn net.Conn, sandboxID string) {
	f.got = append(f.got, sandboxID)
	conn.Close()
}

// TestIncusBoostListener_AcceptLoop tests the accept→dispatch path
// independently of incus client interactions.
func TestIncusBoostListener_AcceptLoop(t *testing.T) {
	tmp := t.TempDir()
	udsPath := filepath.Join(tmp, "sbx-1.sock")

	ln, err := net.Listen("unix", udsPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeIncusBoostServer{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	bl := &incusBoostListener{containerName: "c1", sandboxID: "sbx-1", udsPath: udsPath, listener: ln, cancel: cancel}
	go bl.acceptLoop(ctx, server)

	conn, err := net.Dial("unix", udsPath)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(server.got) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(server.got) != 1 || server.got[0] != "sbx-1" {
		t.Fatalf("server.got = %v, want [sbx-1]", server.got)
	}
}
