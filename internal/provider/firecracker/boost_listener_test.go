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
	calls chan string
}

func (f *fakeBoostServer) Serve(_ context.Context, conn net.Conn, sandboxID string) {
	defer conn.Close()
	f.calls <- sandboxID
}

func TestBoostListener_AcceptsAndDispatches(t *testing.T) {
	tmp := t.TempDir()
	udsPath := filepath.Join(tmp, "vsock_1025")

	server := &fakeBoostServer{calls: make(chan string, 1)}
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

	select {
	case got := <-server.calls:
		if got != "sbx-1" {
			t.Fatalf("sandboxID = %q; want sbx-1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("boost listener did not dispatch connection")
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
