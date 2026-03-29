package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

	HandleExec(context.Background(), req, send)

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

	HandleExec(context.Background(), req, send)

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

func TestHandleExecEmptyCommand(t *testing.T) {
	payload, _ := json.Marshal(vsock.ExecPayload{Command: []string{}})
	req := &vsock.Message{
		Version: vsock.ProtocolVersion,
		Type:    vsock.TypeExec,
		ID:      "test-empty",
		Payload: json.RawMessage(payload),
	}

	var responses []*vsock.Message
	send := SendFunc(func(msg *vsock.Message) error {
		responses = append(responses, msg)
		return nil
	})
	HandleExec(context.Background(), req, send)

	if len(responses) == 0 {
		t.Fatal("expected at least an exit message")
	}
	last := responses[len(responses)-1]
	if last.Type != vsock.TypeExit {
		t.Fatalf("expected TypeExit, got %s", last.Type)
	}
	var ep vsock.ExitPayload
	json.Unmarshal(last.Payload, &ep)
	if ep.Code != -1 {
		t.Errorf("expected exit code -1 for empty command, got %d", ep.Code)
	}
}

func TestHandleExecInvalidPayload(t *testing.T) {
	req := &vsock.Message{
		Version: vsock.ProtocolVersion,
		Type:    vsock.TypeExec,
		ID:      "test-invalid",
		Payload: json.RawMessage([]byte("{invalid")),
	}

	var responses []*vsock.Message
	send := SendFunc(func(msg *vsock.Message) error {
		responses = append(responses, msg)
		return nil
	})
	HandleExec(context.Background(), req, send)

	if len(responses) != 1 || responses[0].Type != vsock.TypeExit {
		t.Fatalf("expected single exit message, got %d messages", len(responses))
	}
}

func TestHandleExecEnvInheritance(t *testing.T) {
	// When env overrides are set, the inherited env should still be present.
	payload, _ := json.Marshal(vsock.ExecPayload{
		Command: []string{"env"},
		Env:     map[string]string{"MY_CUSTOM_VAR": "test123"},
	})
	req := &vsock.Message{
		Version: vsock.ProtocolVersion,
		Type:    vsock.TypeExec,
		ID:      "test-env",
		Payload: json.RawMessage(payload),
	}

	var stdout []byte
	send := SendFunc(func(msg *vsock.Message) error {
		if msg.Type == vsock.TypeStdout {
			var dp vsock.DataPayload
			json.Unmarshal(msg.Payload, &dp)
			stdout = append(stdout, dp.Data...)
		}
		return nil
	})
	HandleExec(context.Background(), req, send)

	env := string(stdout)
	if !strings.Contains(env, "MY_CUSTOM_VAR=test123") {
		t.Error("custom env var not found in output")
	}
	// PATH should be inherited from the parent process.
	if !strings.Contains(env, "PATH=") {
		t.Error("inherited PATH not found — env overrides replaced instead of merging")
	}
}

func TestHandleExecSendError(t *testing.T) {
	// When send fails, streaming should stop gracefully.
	payload, _ := json.Marshal(vsock.ExecPayload{
		Command: []string{"echo", "hello"},
	})
	req := &vsock.Message{
		Version: vsock.ProtocolVersion,
		Type:    vsock.TypeExec,
		ID:      "test-sendfail",
		Payload: json.RawMessage(payload),
	}

	callCount := 0
	send := SendFunc(func(msg *vsock.Message) error {
		callCount++
		if msg.Type == vsock.TypeStdout {
			return fmt.Errorf("connection closed")
		}
		return nil
	})
	HandleExec(context.Background(), req, send)

	// Should complete without hanging.
	if callCount == 0 {
		t.Error("expected at least one send call")
	}
}
