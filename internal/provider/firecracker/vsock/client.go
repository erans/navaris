package vsock

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Client multiplexes concurrent operations over a single vsock connection.
type Client struct {
	conn      net.Conn
	mu        sync.Mutex // write lock
	handlers  map[string]chan *Message
	handlerMu sync.RWMutex
	done      chan struct{}
	closeOnce sync.Once
}

// NewClientFromConn wraps an existing connection and starts the read loop.
func NewClientFromConn(conn net.Conn) *Client {
	c := &Client{
		conn:     conn,
		handlers: make(map[string]chan *Message),
		done:     make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// Close shuts down the client. Safe to call multiple times.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		err = c.conn.Close()
	})
	return err
}

// Send encodes a message to the connection under the write lock.
func (c *Client) Send(msg *Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Encode(c.conn, msg)
}

// send is the internal alias used by the package.
func (c *Client) send(msg *Message) error {
	return c.Send(msg)
}

// register creates and stores a buffered channel for the given correlation ID.
func (c *Client) register(id string) chan *Message {
	ch := make(chan *Message, 16)
	c.handlerMu.Lock()
	c.handlers[id] = ch
	c.handlerMu.Unlock()
	return ch
}

// unregister removes the channel for the given correlation ID.
func (c *Client) unregister(id string) {
	c.handlerMu.Lock()
	delete(c.handlers, id)
	c.handlerMu.Unlock()
}

// readLoop reads messages from the connection and dispatches them to the
// appropriate per-ID channel. It exits when the connection is closed or done.
func (c *Client) readLoop() {
	for {
		msg, err := Decode(c.conn)
		if err != nil {
			// Connection closed or read error — drain is handled by callers
			// watching their channels after Close().
			return
		}
		c.handlerMu.RLock()
		ch, ok := c.handlers[msg.ID]
		c.handlerMu.RUnlock()
		if ok {
			select {
			case ch <- msg:
			default:
				// Drop if the consumer is not keeping up; prevents readLoop stall.
			}
		}
	}
}

// Ping sends a ping and waits up to timeout for a pong response.
func (c *Client) Ping(timeout time.Duration) error {
	id := uuid.NewString()
	ch := c.register(id)
	defer c.unregister(id)

	if err := c.send(&Message{Version: ProtocolVersion, Type: TypePing, ID: id}); err != nil {
		return fmt.Errorf("vsock ping send: %w", err)
	}

	select {
	case msg := <-ch:
		if msg.Type != TypePong {
			return fmt.Errorf("vsock ping: unexpected message type %q", msg.Type)
		}
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("vsock ping: timeout after %s", timeout)
	case <-c.done:
		return fmt.Errorf("vsock ping: client closed")
	}
}

// ExecHandle represents an in-flight exec operation.
type ExecHandle struct {
	id     string
	Stdout io.ReadCloser
	Stderr io.ReadCloser
	waitCh chan int
	errCh  chan error
}

// ID returns the correlation ID for this exec.
func (h *ExecHandle) ID() string { return h.id }

// Wait blocks until the remote process exits and returns its exit code.
func (h *ExecHandle) Wait() (int, error) {
	select {
	case code := <-h.waitCh:
		return code, nil
	case err := <-h.errCh:
		return -1, err
	}
}

// Exec sends an exec request and returns a handle for streaming the output.
func (c *Client) Exec(payload ExecPayload) (*ExecHandle, error) {
	id := uuid.NewString()
	ch := c.register(id)

	raw, err := json.Marshal(payload)
	if err != nil {
		c.unregister(id)
		return nil, fmt.Errorf("vsock exec marshal: %w", err)
	}
	if err := c.send(&Message{Version: ProtocolVersion, Type: TypeExec, ID: id, Payload: raw}); err != nil {
		c.unregister(id)
		return nil, fmt.Errorf("vsock exec send: %w", err)
	}

	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	waitCh := make(chan int, 1)
	errCh := make(chan error, 1)

	handle := &ExecHandle{
		id:     id,
		Stdout: stdoutR,
		Stderr: stderrR,
		waitCh: waitCh,
		errCh:  errCh,
	}

	go func() {
		defer c.unregister(id)
		defer stdoutW.Close()
		defer stderrW.Close()

		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					errCh <- fmt.Errorf("vsock exec: channel closed")
					return
				}
				switch msg.Type {
				case TypeStdout:
					var d DataPayload
					if err := json.Unmarshal(msg.Payload, &d); err == nil {
						stdoutW.Write(d.Data) //nolint:errcheck
					}
				case TypeStderr:
					var d DataPayload
					if err := json.Unmarshal(msg.Payload, &d); err == nil {
						stderrW.Write(d.Data) //nolint:errcheck
					}
				case TypeExit:
					var ep ExitPayload
					if err := json.Unmarshal(msg.Payload, &ep); err != nil {
						errCh <- fmt.Errorf("vsock exec exit decode: %w", err)
						return
					}
					waitCh <- ep.Code
					return
				}
			case <-c.done:
				errCh <- fmt.Errorf("vsock exec: client closed")
				return
			}
		}
	}()

	return handle, nil
}

// SessionHandle represents an interactive shell session.
type SessionHandle struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Resize func(width, height int)
	Close  func()
}

// Session opens an interactive session and returns a handle for
// reading/writing to the remote shell.
func (c *Client) Session(payload SessionPayload) (*SessionHandle, error) {
	id := uuid.NewString()
	ch := c.register(id)

	raw, err := json.Marshal(payload)
	if err != nil {
		c.unregister(id)
		return nil, fmt.Errorf("vsock session marshal: %w", err)
	}
	if err := c.send(&Message{Version: ProtocolVersion, Type: TypeSession, ID: id, Payload: raw}); err != nil {
		c.unregister(id)
		return nil, fmt.Errorf("vsock session send: %w", err)
	}

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	// stdin forwarding: read from caller's writer, send as TypeStdin messages
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdinR.Read(buf)
			if n > 0 {
				d, _ := json.Marshal(DataPayload{Data: buf[:n]})
				if sendErr := c.send(&Message{Version: ProtocolVersion, Type: TypeStdin, ID: id, Payload: d}); sendErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// stdout routing: dispatch inbound messages to the caller's reader
	go func() {
		defer c.unregister(id)
		defer stdoutW.Close()

		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
				}
				switch msg.Type {
				case TypeStdout:
					var d DataPayload
					if err := json.Unmarshal(msg.Payload, &d); err == nil {
						stdoutW.Write(d.Data) //nolint:errcheck
					}
				case TypeExit:
					return
				}
			case <-c.done:
				return
			}
		}
	}()

	resize := func(width, height int) {
		d, _ := json.Marshal(ResizePayload{Width: width, Height: height})
		c.send(&Message{Version: ProtocolVersion, Type: TypeResize, ID: id, Payload: d}) //nolint:errcheck
	}

	closeSession := func() {
		stdinW.Close()
		c.send(&Message{Version: ProtocolVersion, Type: TypeSignal, ID: id}) //nolint:errcheck
	}

	return &SessionHandle{
		Stdin:  stdinW,
		Stdout: stdoutR,
		Resize: resize,
		Close:  closeSession,
	}, nil
}
