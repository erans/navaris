package vsock

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	msg := Message{
		Version: ProtocolVersion,
		Type:    TypeExec,
		ID:      "abc-123",
		Payload: []byte(`{"command":["/bin/ls"]}`),
	}

	var buf bytes.Buffer
	if err := Encode(&buf, &msg); err != nil {
		t.Fatal(err)
	}

	got, err := Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != msg.Version {
		t.Errorf("version: got %d, want %d", got.Version, msg.Version)
	}
	if got.Type != msg.Type {
		t.Errorf("type: got %q, want %q", got.Type, msg.Type)
	}
	if got.ID != msg.ID {
		t.Errorf("id: got %q, want %q", got.ID, msg.ID)
	}
	if !bytes.Equal(got.Payload, msg.Payload) {
		t.Errorf("payload mismatch")
	}
}

func TestDecodeEmptyBuffer(t *testing.T) {
	var buf bytes.Buffer
	_, err := Decode(&buf)
	if err == nil {
		t.Error("expected error on empty buffer")
	}
}

func TestEncodeDecodeAllTypes(t *testing.T) {
	types := []string{
		TypePing, TypePong, TypeExec, TypeSession,
		TypeStdout, TypeStderr, TypeStdin,
		TypeExit, TypeResize, TypeSignal,
	}
	for _, typ := range types {
		msg := Message{Version: ProtocolVersion, Type: typ, ID: "test"}
		var buf bytes.Buffer
		if err := Encode(&buf, &msg); err != nil {
			t.Errorf("encode %s: %v", typ, err)
			continue
		}
		got, err := Decode(&buf)
		if err != nil {
			t.Errorf("decode %s: %v", typ, err)
			continue
		}
		if got.Type != typ {
			t.Errorf("type: got %q, want %q", got.Type, typ)
		}
	}
}
