package agent

import (
	"encoding/json"
	"testing"

	"github.com/navaris/navaris/internal/provider/firecracker/vsock"
)

func TestHandleExec(t *testing.T) {
	payload, err := json.Marshal(vsock.ExecPayload{
		Command: []string{"echo", "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := &vsock.Message{
		Version: vsock.ProtocolVersion,
		Type:    vsock.TypeExec,
		ID:      "test-1",
		Payload: json.RawMessage(payload),
	}

	var responses []*vsock.Message
	send := SendFunc(func(msg *vsock.Message) error {
		responses = append(responses, msg)
		return nil
	})

	HandleExec(req, send)

	// Verify we received at least one stdout message and an exit message.
	var foundHello bool
	var exitCode *int
	for _, msg := range responses {
		switch msg.Type {
		case vsock.TypeStdout:
			var dp vsock.DataPayload
			if err := json.Unmarshal(msg.Payload, &dp); err != nil {
				t.Fatalf("unmarshal DataPayload: %v", err)
			}
			if string(dp.Data) == "hello\n" {
				foundHello = true
			}
		case vsock.TypeExit:
			var ep vsock.ExitPayload
			if err := json.Unmarshal(msg.Payload, &ep); err != nil {
				t.Fatalf("unmarshal ExitPayload: %v", err)
			}
			code := ep.Code
			exitCode = &code
		}
	}

	if !foundHello {
		t.Errorf("expected stdout to contain 'hello\\n', responses: %v", responses)
	}
	if exitCode == nil {
		t.Error("expected exit message, got none")
	} else if *exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", *exitCode)
	}
}

func TestHandleExecFailure(t *testing.T) {
	payload, err := json.Marshal(vsock.ExecPayload{
		Command: []string{"/nonexistent-binary-xyz"},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := &vsock.Message{
		Version: vsock.ProtocolVersion,
		Type:    vsock.TypeExec,
		ID:      "test-2",
		Payload: json.RawMessage(payload),
	}

	var responses []*vsock.Message
	send := SendFunc(func(msg *vsock.Message) error {
		responses = append(responses, msg)
		return nil
	})

	HandleExec(req, send)

	var exitCode *int
	for _, msg := range responses {
		if msg.Type == vsock.TypeExit {
			var ep vsock.ExitPayload
			if err := json.Unmarshal(msg.Payload, &ep); err != nil {
				t.Fatalf("unmarshal ExitPayload: %v", err)
			}
			code := ep.Code
			exitCode = &code
		}
	}

	if exitCode == nil {
		t.Error("expected exit message, got none")
	} else if *exitCode == 0 {
		t.Errorf("expected non-zero exit code for nonexistent binary, got 0")
	}
}
