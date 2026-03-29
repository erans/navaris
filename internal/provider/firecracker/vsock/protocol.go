package vsock

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const ProtocolVersion uint8 = 1

const (
	TypePing    = "ping"
	TypePong    = "pong"
	TypeExec    = "exec"
	TypeSession = "session"
	TypeStdout  = "stdout"
	TypeStderr  = "stderr"
	TypeStdin   = "stdin"
	TypeExit    = "exit"
	TypeResize  = "resize"
	TypeSignal  = "signal"
)

type Message struct {
	Version uint8           `json:"v"`
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func Encode(w io.Writer, msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("vsock encode: %w", err)
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(data))); err != nil {
		return fmt.Errorf("vsock encode length: %w", err)
	}
	_, err = w.Write(data)
	return err
}

func Decode(r io.Reader) (*Message, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, fmt.Errorf("vsock decode length: %w", err)
	}
	if length > 4*1024*1024 {
		return nil, fmt.Errorf("vsock message too large: %d bytes", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("vsock decode body: %w", err)
	}
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("vsock decode json: %w", err)
	}
	return &msg, nil
}

type ExecPayload struct {
	Command []string          `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
	WorkDir string            `json:"workdir,omitempty"`
}

type SessionPayload struct {
	Shell string `json:"shell"`
}

type ExitPayload struct {
	Code int `json:"code"`
}

type ResizePayload struct {
	Width  int `json:"w"`
	Height int `json:"h"`
}

type SignalPayload struct {
	Signal string `json:"signal"`
}

type DataPayload struct {
	Data []byte `json:"data"`
}
