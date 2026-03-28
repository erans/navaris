//go:build incus

package incus

import (
	"bytes"
	"context"
	"fmt"
	"io"

	incusclient "github.com/lxc/incus/v6/client"
	incusapi "github.com/lxc/incus/v6/shared/api"
	"github.com/navaris/navaris/internal/domain"
)

// Exec runs a command inside the container with separated stdout/stderr
// streams and returns an ExecHandle for reading output and waiting.
func (p *IncusProvider) Exec(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.ExecHandle, error) {
	var stdoutBuf, stderrBuf bytes.Buffer
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()

	execReq := incusapi.InstanceExecPost{
		Command:     req.Command,
		Environment: req.Env,
		WaitForWS:   true,
		Interactive: false,
		Cwd:         req.WorkDir,
	}

	args := incusclient.InstanceExecArgs{
		Stdout: io.MultiWriter(stdoutW, &stdoutBuf),
		Stderr: io.MultiWriter(stderrW, &stderrBuf),
	}

	op, err := p.client.ExecInstance(ref.Ref, execReq, &args)
	if err != nil {
		stdoutW.Close()
		stderrW.Close()
		return domain.ExecHandle{}, fmt.Errorf("incus exec %s: %w", ref.Ref, err)
	}

	handle := domain.ExecHandle{
		Stdout: stdoutR,
		Stderr: stderrR,
		Wait: func() (int, error) {
			if err := op.WaitContext(ctx); err != nil {
				stdoutW.Close()
				stderrW.Close()
				return -1, err
			}
			stdoutW.Close()
			stderrW.Close()

			opAPI := op.Get()
			exitCode := 0
			if code, ok := opAPI.Metadata["return"].(float64); ok {
				exitCode = int(code)
			}
			return exitCode, nil
		},
		Cancel: func() error {
			stdoutW.Close()
			stderrW.Close()
			return op.Cancel()
		},
	}

	return handle, nil
}

// ExecDetached runs a command inside the container with a PTY and returns a
// DetachedExecHandle allowing the caller to write to stdin and read from
// stdout.
func (p *IncusProvider) ExecDetached(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.DetachedExecHandle, error) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	execReq := incusapi.InstanceExecPost{
		Command:     req.Command,
		Environment: req.Env,
		WaitForWS:   true,
		Interactive: true,
		Cwd:         req.WorkDir,
		Width:       80,
		Height:      24,
	}

	args := incusclient.InstanceExecArgs{
		Stdin:  stdinR,
		Stdout: stdoutW,
		Stderr: stdoutW, // PTY merges stderr into stdout.
	}

	op, err := p.client.ExecInstance(ref.Ref, execReq, &args)
	if err != nil {
		stdinR.Close()
		stdinW.Close()
		stdoutR.Close()
		stdoutW.Close()
		return domain.DetachedExecHandle{}, fmt.Errorf("incus exec detached %s: %w", ref.Ref, err)
	}

	handle := domain.DetachedExecHandle{
		Stdin:  stdinW,
		Stdout: stdoutR,
		Resize: func(w, h int) error {
			// Incus window resize is done via the control socket which
			// is available through the operation's websocket.
			// For now, this is a placeholder -- full resize requires
			// the control websocket channel.
			_ = w
			_ = h
			return nil
		},
		Close: func() error {
			stdinR.Close()
			stdinW.Close()
			stdoutR.Close()
			stdoutW.Close()
			_ = op.Cancel()
			return nil
		},
	}

	return handle, nil
}

// AttachSession attaches an interactive shell session to the container and
// returns a SessionHandle with a bidirectional connection.
func (p *IncusProvider) AttachSession(ctx context.Context, ref domain.BackendRef, req domain.SessionRequest) (domain.SessionHandle, error) {
	shell := req.Shell
	if shell == "" {
		shell = "/bin/sh"
	}

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	execReq := incusapi.InstanceExecPost{
		Command:     []string{shell},
		WaitForWS:   true,
		Interactive: true,
		Width:       80,
		Height:      24,
	}

	args := incusclient.InstanceExecArgs{
		Stdin:  stdinR,
		Stdout: stdoutW,
		Stderr: stdoutW,
	}

	op, err := p.client.ExecInstance(ref.Ref, execReq, &args)
	if err != nil {
		stdinR.Close()
		stdinW.Close()
		stdoutR.Close()
		stdoutW.Close()
		return domain.SessionHandle{}, fmt.Errorf("incus attach %s: %w", ref.Ref, err)
	}

	conn := &sessionConn{
		reader: stdoutR,
		writer: stdinW,
		closeFn: func() error {
			stdinR.Close()
			stdinW.Close()
			stdoutR.Close()
			stdoutW.Close()
			return nil
		},
	}

	handle := domain.SessionHandle{
		Conn: conn,
		Resize: func(w, h int) error {
			_ = w
			_ = h
			return nil
		},
		Close: func() error {
			conn.Close()
			_ = op.Cancel()
			return nil
		},
	}

	return handle, nil
}

// sessionConn implements io.ReadWriteCloser for an attached session.
type sessionConn struct {
	reader  io.Reader
	writer  io.Writer
	closeFn func() error
}

func (c *sessionConn) Read(p []byte) (int, error)  { return c.reader.Read(p) }
func (c *sessionConn) Write(p []byte) (int, error)  { return c.writer.Write(p) }
func (c *sessionConn) Close() error                  { return c.closeFn() }
