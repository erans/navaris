package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/navaris/navaris/internal/provider/firecracker/vsock"
)

// SendFunc is the callback used to send vsock messages back to the client.
type SendFunc func(msg *vsock.Message) error

// HandleExec unmarshals an ExecPayload from req, runs the command, streams
// stdout/stderr as DataPayload messages, then sends an ExitPayload.
func HandleExec(req *vsock.Message, send SendFunc) {
	var payload vsock.ExecPayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		sendExit(send, req.ID, -1)
		return
	}

	if len(payload.Command) == 0 {
		sendExit(send, req.ID, -1)
		return
	}

	var args []string
	if len(payload.Command) > 1 {
		args = payload.Command[1:]
	}
	cmd := exec.Command(payload.Command[0], args...)

	if payload.WorkDir != "" {
		cmd.Dir = payload.WorkDir
	}

	for k, v := range payload.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		sendExit(send, req.ID, -1)
		return
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		sendExit(send, req.ID, -1)
		return
	}

	if err := cmd.Start(); err != nil {
		sendExit(send, req.ID, -1)
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		streamOutput(req.ID, vsock.TypeStdout, stdoutPipe, send)
	}()
	go func() {
		defer wg.Done()
		streamOutput(req.ID, vsock.TypeStderr, stderrPipe, send)
	}()

	wg.Wait()

	exitCode := 0
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	sendExit(send, req.ID, exitCode)
}

// sendExit sends a TypeExit message with the given exit code.
func sendExit(send SendFunc, id string, code int) {
	payload, err := json.Marshal(vsock.ExitPayload{Code: code})
	if err != nil {
		return
	}
	_ = send(&vsock.Message{
		Version: vsock.ProtocolVersion,
		Type:    vsock.TypeExit,
		ID:      id,
		Payload: json.RawMessage(payload),
	})
}

// streamOutput reads from r in 32KB chunks and sends each chunk as a
// DataPayload message of the given msgType.
func streamOutput(id, msgType string, r io.Reader, send SendFunc) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			payload, merr := json.Marshal(vsock.DataPayload{Data: data})
			if merr == nil {
				_ = send(&vsock.Message{
					Version: vsock.ProtocolVersion,
					Type:    msgType,
					ID:      id,
					Payload: json.RawMessage(payload),
				})
			}
		}
		if err != nil {
			break
		}
	}
}
