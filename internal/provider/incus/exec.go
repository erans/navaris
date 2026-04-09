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
	"github.com/navaris/navaris/internal/telemetry"
)

// Exec runs a command inside the container with separated stdout/stderr
// streams and returns an ExecHandle for reading output and waiting.
func (p *IncusProvider) Exec(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (_ domain.ExecHandle, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "Exec")
	defer func() { endSpan(retErr) }()

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

	// Wait in background so pipe writers close when exec finishes,
	// allowing readers to receive EOF without circular dependency.
	type waitResult struct {
		exitCode int
		err      error
	}
	waitCh := make(chan waitResult, 1)
	go func() {
		err := op.WaitContext(ctx)
		stdoutW.Close()
		stderrW.Close()
		if err != nil {
			waitCh <- waitResult{-1, err}
			return
		}
		opAPI := op.Get()
		exitCode := 0
		if code, ok := opAPI.Metadata["return"].(float64); ok {
			exitCode = int(code)
		}
		waitCh <- waitResult{exitCode, nil}
	}()

	handle := domain.ExecHandle{
		Stdout: stdoutR,
		Stderr: stderrR,
		Wait: func() (int, error) {
			res := <-waitCh
			return res.exitCode, res.err
		},
		Cancel: func() error {
			return op.Cancel()
		},
	}

	return handle, nil
}

// ExecDetached runs a command inside the container with a PTY and returns a
// DetachedExecHandle allowing the caller to write to stdin and read from
// stdout.
func (p *IncusProvider) ExecDetached(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (_ domain.DetachedExecHandle, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "ExecDetached")
	defer func() { endSpan(retErr) }()

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
func (p *IncusProvider) AttachSession(ctx context.Context, ref domain.BackendRef, req domain.SessionRequest) (_ domain.SessionHandle, retErr error) {
	ctx, endSpan := telemetry.ProviderSpan(ctx, backendName, "AttachSession")
	defer func() { endSpan(retErr) }()

	shell := req.Shell
	if shell == "" {
		shell = p.detectShell(ref)
	}

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	cmd := req.Command
	if len(cmd) == 0 {
		cmd = []string{shell}
	}

	execReq := incusapi.InstanceExecPost{
		Command:     cmd,
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

// detectShell probes the container for a usable interactive shell.
// It prefers /bin/bash and falls back to /bin/sh.
func (p *IncusProvider) detectShell(ref domain.BackendRef) string {
	stdout := new(bytes.Buffer)
	execReq := incusapi.InstanceExecPost{
		Command:     []string{"test", "-x", "/bin/bash"},
		WaitForWS:   false,
		Interactive: false,
	}
	args := incusclient.InstanceExecArgs{
		Stdout: stdout,
		Stderr: stdout,
	}
	op, err := p.client.ExecInstance(ref.Ref, execReq, &args)
	if err != nil {
		return "/bin/sh"
	}
	if err := op.Wait(); err != nil {
		return "/bin/sh"
	}
	meta := op.Get()
	if rc, ok := meta.Metadata["return"].(float64); ok && rc == 0 {
		return "/bin/bash"
	}
	return "/bin/sh"
}
