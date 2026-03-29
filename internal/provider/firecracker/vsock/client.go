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
	handlerMu sync.Mutex
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

func (c *Client) send(msg *Message) error {
	return c.Send(msg)
}

func (c *Client) register(id string) chan *Message {
	ch := make(chan *Message, 32)
	c.handlerMu.Lock()
	c.handlers[id] = ch
	c.handlerMu.Unlock()
	return ch
}

func (c *Client) unregister(id string) {
	c.handlerMu.Lock()
	delete(c.handlers, id)
	c.handlerMu.Unlock()
}

// closeAllHandlers closes all registered channels to signal disconnect to
// every in-flight operation.
func (c *Client) closeAllHandlers() {
	c.handlerMu.Lock()
	for id, ch := range c.handlers {
		close(ch)
		delete(c.handlers, id)
	}
	c.handlerMu.Unlock()
}

// readLoop reads messages and dispatches them to per-ID channels. On exit
// (connection closed or error), it closes all handler channels so in-flight
// operations unblock with an error instead of hanging.
func (c *Client) readLoop() {
	defer c.closeAllHandlers()
	for {
		msg, err := Decode(c.conn)
		if err != nil {
			return
		}
		c.handlerMu.Lock()
		ch, ok := c.handlers[msg.ID]
		c.handlerMu.Unlock()
		if !ok {
			continue
		}
		// Block until delivered or client closed. Guarantees TypeExit
		// is never silently dropped.
		select {
		case ch <- msg:
		case <-c.done:
			return
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
	case msg, ok := <-ch:
		if !ok {
			return fmt.Errorf("vsock ping: connection lost")
		}
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

	// Dedicated pipe-writer goroutines. Decoupled from message dispatch so
	// slow readers cannot block TypeExit processing.
	stdoutData := make(chan []byte, 256)
	stderrData := make(chan []byte, 256)
	go func() {
		defer stdoutW.Close()
		for data := range stdoutData {
			if _, err := stdoutW.Write(data); err != nil {
				// Drain remaining to unblock sender.
				for range stdoutData {
				}
				return
			}
		}
	}()
	go func() {
		defer stderrW.Close()
		for data := range stderrData {
			if _, err := stderrW.Write(data); err != nil {
				for range stderrData {
				}
				return
			}
		}
	}()

	// Message dispatch — reads from handler channel and routes data to the
	// pipe-writer goroutines. TypeExit is processed directly, unblocked by
	// pipe write speed.
	go func() {
		defer c.unregister(id)
		defer close(stdoutData)
		defer close(stderrData)

		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					errCh <- fmt.Errorf("vsock exec: connection lost")
					return
				}
				switch msg.Type {
				case TypeStdout:
					var d DataPayload
					if err := json.Unmarshal(msg.Payload, &d); err == nil {
						stdoutData <- d.Data
					}
				case TypeStderr:
					var d DataPayload
					if err := json.Unmarshal(msg.Payload, &d); err == nil {
						stderrData <- d.Data
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

	// stdin forwarding: read from caller's writer, send as TypeStdin messages.
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

	// Dedicated pipe-writer for stdout, decoupled from message dispatch.
	stdoutData := make(chan []byte, 256)
	go func() {
		defer stdoutW.Close()
		for data := range stdoutData {
			if _, err := stdoutW.Write(data); err != nil {
				for range stdoutData {
				}
				return
			}
		}
	}()

	// Message dispatch — routes stdout data to pipe-writer goroutine.
	go func() {
		defer c.unregister(id)
		defer close(stdoutData)

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
						stdoutData <- d.Data
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
