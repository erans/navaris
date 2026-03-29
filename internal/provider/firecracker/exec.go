//go:build firecracker

package firecracker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/jailer"
	fcvsock "github.com/navaris/navaris/internal/provider/firecracker/vsock"
	"golang.org/x/sys/unix"
)

func (p *Provider) dialAgent(cid uint32) (*fcvsock.Client, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}

	sa := &unix.SockaddrVM{CID: cid, Port: 1024}
	if err := unix.Connect(fd, sa); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("vsock connect CID %d: %w", cid, err)
	}

	f := os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d", cid))
	conn, err := net.FileConn(f)
	f.Close() // FileConn dups the fd
	if err != nil {
		return nil, fmt.Errorf("vsock fileconn CID %d: %w", cid, err)
	}

	return fcvsock.NewClientFromConn(conn), nil
}

func (p *Provider) getVMInfo(vmID string) (*VMInfo, error) {
	infoPath := jailer.VMInfoPath(p.config.ChrootBase, vmID)
	return ReadVMInfo(infoPath)
}

func (p *Provider) Exec(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.ExecHandle, error) {
	info, err := p.getVMInfo(ref.Ref)
	if err != nil {
		return domain.ExecHandle{}, fmt.Errorf("firecracker exec %s: %w", ref.Ref, err)
	}

	client, err := p.dialAgent(info.CID)
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

func (p *Provider) ExecDetached(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.DetachedExecHandle, error) {
	info, err := p.getVMInfo(ref.Ref)
	if err != nil {
		return domain.DetachedExecHandle{}, fmt.Errorf("firecracker exec-detached %s: %w", ref.Ref, err)
	}

	client, err := p.dialAgent(info.CID)
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

func (p *Provider) AttachSession(ctx context.Context, ref domain.BackendRef, req domain.SessionRequest) (domain.SessionHandle, error) {
	info, err := p.getVMInfo(ref.Ref)
	if err != nil {
		return domain.SessionHandle{}, fmt.Errorf("firecracker session %s: %w", ref.Ref, err)
	}

	client, err := p.dialAgent(info.CID)
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
