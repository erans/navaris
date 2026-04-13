//go:build firecracker

package firecracker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/navaris/navaris/internal/domain"
	fcvsock "github.com/navaris/navaris/internal/provider/firecracker/vsock"
	"github.com/navaris/navaris/internal/telemetry"
)

// agentConn holds a vsock UDS connection after the CONNECT handshake.
type agentConn struct {
	conn net.Conn
}

func (p *Provider) connectAgent(vmID string) (*agentConn, error) {
	udsPath := p.vsockPath(vmID)

	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		if attempt > 0 {
			time.Sleep(500 * time.Millisecond)
		}

		conn, err := net.DialTimeout("unix", udsPath, 2*time.Second)
		if err != nil {
			lastErr = fmt.Errorf("vsock dial %s: %w", vmID, err)
			continue
		}

		// Firecracker vsock handshake: send CONNECT <port>, read OK <port>.
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := fmt.Fprintf(conn, "CONNECT 1024\n"); err != nil {
			conn.Close()
			lastErr = fmt.Errorf("vsock connect msg %s: %w", vmID, err)
			continue
		}

		// Read OK ack byte-by-byte to avoid buffering past the newline.
		var ack [32]byte
		ackLen := 0
		for ackLen < len(ack) {
			n, readErr := conn.Read(ack[ackLen : ackLen+1])
			if readErr != nil {
				conn.Close()
				lastErr = fmt.Errorf("vsock ack read %s: %w", vmID, readErr)
				break
			}
			ackLen += n
			if ack[ackLen-1] == '\n' {
				break
			}
		}
		if lastErr != nil {
			continue
		}
		if ackLen < 3 || string(ack[:3]) != "OK " {
			conn.Close()
			lastErr = fmt.Errorf("vsock ack %s: unexpected %q", vmID, string(ack[:ackLen]))
			continue
		}

		conn.SetDeadline(time.Time{}) // clear deadline
		return &agentConn{conn: conn}, nil
	}
	return nil, lastErr
}

func (p *Provider) dialAgent(vmID string) (*fcvsock.Client, error) {
	ac, err := p.connectAgent(vmID)
	if err != nil {
		return nil, err
	}
	return fcvsock.NewClientFromConn(ac.conn), nil
}

func (p *Provider) getVMInfo(vmID string) (*VMInfo, error) {
	infoPath := p.vmInfoPath(vmID)
	return ReadVMInfo(infoPath)
}

func (p *Provider) Exec(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (_ domain.ExecHandle, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "Exec")
	defer func() { endSpan(retErr) }()

	client, err := p.dialAgent(ref.Ref)
	if err != nil {
		return domain.ExecHandle{}, fmt.Errorf("firecracker exec %s: %w", ref.Ref, err)
	}

	handle, err := client.Exec(fcvsock.ExecPayload{
		Command: req.Command,
		Env:     req.Env,
		WorkDir: req.WorkDir,
	})
	if err != nil {
		client.Close()
		return domain.ExecHandle{}, fmt.Errorf("firecracker exec %s: %w", ref.Ref, err)
	}

	return domain.ExecHandle{
		Stdout: handle.Stdout,
		Stderr: handle.Stderr,
		Wait: func() (int, error) {
			code, err := handle.Wait()
			client.Close()
			return code, err
		},
		Cancel: func() error {
			return client.Close()
		},
	}, nil
}

func (p *Provider) ExecDetached(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (_ domain.DetachedExecHandle, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "ExecDetached")
	defer func() { endSpan(retErr) }()

	client, err := p.dialAgent(ref.Ref)
	if err != nil {
		return domain.DetachedExecHandle{}, fmt.Errorf("firecracker exec-detached %s: %w", ref.Ref, err)
	}

	// Start exec -- stdin is streamed via vsock TypeStdin messages.
	execHandle, err := client.Exec(fcvsock.ExecPayload{
		Command: req.Command,
		Env:     req.Env,
		WorkDir: req.WorkDir,
	})
	if err != nil {
		client.Close()
		return domain.DetachedExecHandle{}, fmt.Errorf("firecracker exec-detached %s: %w", ref.Ref, err)
	}
	execID := execHandle.ID() // correlation ID for stdin routing

	// Wrap stdin writes to forward to vsock.
	stdinR, stdinW := io.Pipe()
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stdinR.Read(buf)
			if n > 0 {
				data, _ := json.Marshal(fcvsock.DataPayload{Data: buf[:n]})
				if sendErr := client.Send(&fcvsock.Message{
					Version: fcvsock.ProtocolVersion,
					Type:    fcvsock.TypeStdin,
					ID:      execID,
					Payload: data,
				}); sendErr != nil {
					stdinR.CloseWithError(sendErr)
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	return domain.DetachedExecHandle{
		Stdin:  stdinW,
		Stdout: execHandle.Stdout,
		Resize: func(w, h int) error { return nil }, // Not PTY-based
		Close: func() error {
			stdinW.Close()
			return client.Close()
		},
	}, nil
}

func (p *Provider) AttachSession(ctx context.Context, ref domain.BackendRef, req domain.SessionRequest) (_ domain.SessionHandle, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "AttachSession")
	defer func() { endSpan(retErr) }()

	client, err := p.dialAgent(ref.Ref)
	if err != nil {
		return domain.SessionHandle{}, fmt.Errorf("firecracker session %s: %w", ref.Ref, err)
	}

	payload := fcvsock.SessionPayload{
		Shell:   req.Shell,
		Command: req.Command,
	}
	session, err := client.Session(payload)
	if err != nil {
		client.Close()
		return domain.SessionHandle{}, fmt.Errorf("firecracker session %s: %w", ref.Ref, err)
	}

	// Adapt session Stdin/Stdout into a single io.ReadWriteCloser
	// to satisfy domain.SessionHandle.Conn.
	conn := &sessionConn{r: session.Stdout, w: session.Stdin}

	return domain.SessionHandle{
		Conn: conn,
		Resize: func(w, h int) error {
			session.Resize(w, h)
			return nil
		},
		Close: func() error {
			session.Close()
			return client.Close()
		},
	}, nil
}

// sessionConn adapts separate read/write streams into io.ReadWriteCloser.
type sessionConn struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (s *sessionConn) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s *sessionConn) Write(p []byte) (int, error) { return s.w.Write(p) }
func (s *sessionConn) Close() error {
	s.w.Close()
	return s.r.Close()
}
