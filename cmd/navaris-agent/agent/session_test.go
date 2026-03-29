package agent

import (
	"testing"
	"time"
)

func TestHandleSessionEcho(t *testing.T) {
	pty, err := allocPTY("/bin/sh")
	if err != nil {
		t.Skipf("allocPTY: %v (no PTY available, skipping)", err)
	}
	defer pty.Close()

	// Write a command to the PTY.
	cmd := "echo hello-from-pty\n"
	if _, err := pty.Write([]byte(cmd)); err != nil {
		t.Fatalf("write to PTY: %v", err)
	}

	// Read output with a timeout using a buffered channel.
	type readResult struct {
		data []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		buf := make([]byte, 4096)
		n, err := pty.Read(buf)
		ch <- readResult{data: buf[:n], err: err}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("read from PTY: %v", res.err)
		}
		if len(res.data) == 0 {
			t.Fatal("expected non-empty output from PTY, got nothing")
		}
		t.Logf("PTY output: %q", res.data)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for PTY output")
	}
}
