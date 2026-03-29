package vsock

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

func mockAgent(t *testing.T, conn net.Conn) {
	t.Helper()
	defer conn.Close()
	for {
		msg, err := Decode(conn)
		if err != nil {
			return
		}
		switch msg.Type {
		case TypePing:
			Encode(conn, &Message{Version: ProtocolVersion, Type: TypePong, ID: msg.ID})
		case TypeExec:
			data, _ := json.Marshal(DataPayload{Data: []byte("output\n")})
			Encode(conn, &Message{Version: ProtocolVersion, Type: TypeStdout, ID: msg.ID, Payload: data})
			exitData, _ := json.Marshal(ExitPayload{Code: 0})
			Encode(conn, &Message{Version: ProtocolVersion, Type: TypeExit, ID: msg.ID, Payload: exitData})
		}
	}
}

func TestClientPing(t *testing.T) {
	server, client := net.Pipe()
	go mockAgent(t, server)
	c := NewClientFromConn(client)
	defer c.Close()
	if err := c.Ping(time.Second); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestClientExec(t *testing.T) {
	server, client := net.Pipe()
	go mockAgent(t, server)
	c := NewClientFromConn(client)
	defer c.Close()

	handle, err := c.Exec(ExecPayload{Command: []string{"echo", "hello"}})
	if err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 1024)
	n, _ := handle.Stdout.Read(buf)
	if n == 0 {
		t.Error("no stdout data")
	}

	code, err := handle.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Errorf("exit code: got %d, want 0", code)
	}
}

func TestClientPingDisconnect(t *testing.T) {
	server, client := net.Pipe()
	c := NewClientFromConn(client)

	// Close server immediately — simulates peer disconnect.
	server.Close()

	err := c.Ping(time.Second)
	if err == nil {
		t.Fatal("expected error on disconnect, got nil")
	}
	c.Close()
}

func TestClientExecDisconnect(t *testing.T) {
	server, client := net.Pipe()
	go func() {
		// Read the exec request then close — simulates crash after receiving.
		Decode(server)
		server.Close()
	}()
	c := NewClientFromConn(client)
	defer c.Close()

	handle, err := c.Exec(ExecPayload{Command: []string{"sleep", "10"}})
	if err != nil {
		t.Fatal(err)
	}

	// Wait should return an error instead of hanging.
	done := make(chan struct{})
	go func() {
		_, waitErr := handle.Wait()
		if waitErr == nil {
			t.Error("expected error from Wait on disconnect, got nil")
		}
		close(done)
	}()

	select {
	case <-done:
		// Good — Wait returned.
	case <-time.After(5 * time.Second):
		t.Fatal("Wait() hung on disconnect")
	}
}
