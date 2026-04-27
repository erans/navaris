//go:build firecracker

package firecracker

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

type fakeBoostServer struct {
	calls []string
}

func (f *fakeBoostServer) Serve(_ context.Context, conn net.Conn, sandboxID string) {
	f.calls = append(f.calls, sandboxID)
	conn.Close()
}

func TestBoostListener_AcceptsAndDispatches(t *testing.T) {
	tmp := t.TempDir()
	udsPath := filepath.Join(tmp, "vsock_1025")

	server := &fakeBoostServer{}
	p := &Provider{
		boostHandler:   server,
		boostListeners: make(map[string]*boostListener),
	}

	ln, err := net.Listen("unix", udsPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bl := &boostListener{vmID: "vm-1", sandboxID: "sbx-1", udsPath: udsPath, listener: ln, cancel: cancel}
	p.boostListeners["vm-1"] = bl
	go bl.acceptLoop(ctx, server)

	conn, err := net.Dial("unix", udsPath)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(server.calls) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(server.calls) != 1 || server.calls[0] != "sbx-1" {
		t.Fatalf("server.calls = %v, want [sbx-1]", server.calls)
	}
}

func TestBoostListener_PathDerivation_NoJailer(t *testing.T) {
	tmp := t.TempDir()
	p := &Provider{config: Config{ChrootBase: tmp, EnableJailer: false}}
	got := p.boostUDSPath("vm-x")
	want := filepath.Join(tmp, "vm-x", "vsock_1025")
	if got != want {
		t.Errorf("boostUDSPath = %s, want %s", got, want)
	}
}
