//go:build firecracker

package firecracker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/jailer"
	fcvsock "github.com/navaris/navaris/internal/provider/firecracker/vsock"
	"github.com/navaris/navaris/internal/telemetry"
)

func (p *Provider) connectAgent(vmID string) (*agentConn, error) {
	udsPath := filepath.Join(jailer.ChrootPath(p.config.ChrootBase, vmID), "root", "vsock")

	conn, err := net.DialTimeout("unix", udsPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("vsock dial %s: %w", vmID, err)
	}

	// Firecracker vsock handshake: send CONNECT <port>, read OK <port>.
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintf(conn, "CONNECT 1024\n"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("vsock connect msg %s: %w", vmID, err)
	}

	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("vsock ack read %s: %w", vmID, err)
	}
	if !strings.HasPrefix(line, "OK ") {
		conn.Close()
		return nil, fmt.Errorf("vsock ack %s: unexpected %q", vmID, line)
	}

	conn.SetDeadline(time.Time{}) // clear deadline
	return &agentConn{conn: conn, br: br}, nil
}

func (p *Provider) dialAgent(vmID string) (*fcvsock.Client, error) {
	p.agentMu.Lock()
	ac, ok := p.agentConns[vmID]
	if ok {
		delete(p.agentConns, vmID)
	}
	p.agentMu.Unlock()

	if ac == nil {
		var err error
		ac, err = p.connectAgent(vmID)
		if err != nil {
			return nil, err
		}
	}

	return fcvsock.NewClientFromConn(bufferedConn{Conn: ac.conn, r: ac.br}), nil
}

// bufferedConn wraps a net.Conn so that reads drain the bufio.Reader first,
// ensuring bytes consumed during the CONNECT handshake aren't lost.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c bufferedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

func (p *Provider) getVMInfo(vmID string) (*VMInfo, error) {
	infoPath := jailer.VMInfoPath(p.config.ChrootBase, vmID)
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

	session, err := client.Session(fcvsock.SessionPayload{Shell: req.Shell})
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
