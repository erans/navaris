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
