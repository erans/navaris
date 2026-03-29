package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"sync"

	"github.com/navaris/navaris/internal/provider/firecracker/vsock"
)

// Server accepts connections on a net.Listener and dispatches vsock messages.
type Server struct {
	listener net.Listener
}

// NewServer creates a Server that will accept connections on ln.
func NewServer(ln net.Listener) *Server {
	return &Server{listener: ln}
}

// Serve accepts connections in a loop until the listener is closed.
// Transient Accept errors are logged and retried; only a closed listener
// causes the loop to exit.
func (s *Server) Serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("agent: accept error: %v", err)
			continue
		}
		go s.handleConn(conn)
	}
}

// msgRouter maintains per-ID message channels for active sessions/execs.
type msgRouter struct {
	mu     sync.Mutex
	routes map[string]chan<- *vsock.Message
}

func newMsgRouter() *msgRouter {
	return &msgRouter{routes: make(map[string]chan<- *vsock.Message)}
}

// register adds a route for the given ID and returns the receive end.
func (r *msgRouter) register(id string) <-chan *vsock.Message {
	ch := make(chan *vsock.Message, 64)
	r.mu.Lock()
	r.routes[id] = ch
	r.mu.Unlock()
	return ch
}

// unregister removes the route for id and closes its channel.
func (r *msgRouter) unregister(id string) {
	r.mu.Lock()
	if ch, ok := r.routes[id]; ok {
		close(ch)
		delete(r.routes, id)
	}
	r.mu.Unlock()
}

// route delivers msg to the registered handler. Returns true if a route
// exists (even if the channel is full and the message is dropped).
func (r *msgRouter) route(msg *vsock.Message) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.routes[msg.ID]
	if !ok {
		return false
	}
	select {
	case ch <- msg:
	default:
		log.Printf("agent: message dropped for %s (channel full)", msg.ID)
	}
	return true
}

// closeAll closes all registered channels, signalling disconnect to handlers.
func (r *msgRouter) closeAll() {
	r.mu.Lock()
	for id, ch := range r.routes {
		close(ch)
		delete(r.routes, id)
	}
	r.mu.Unlock()
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	router := newMsgRouter()
	defer router.closeAll()

	var mu sync.Mutex
	send := SendFunc(func(msg *vsock.Message) error {
		mu.Lock()
		defer mu.Unlock()
		return vsock.Encode(conn, msg)
	})

	// Single decode loop — all messages on this connection go through here.
	for {
		msg, err := vsock.Decode(conn)
		if err != nil {
			if err != io.EOF && !errors.Is(err, net.ErrClosed) {
				log.Printf("agent: decode error: %v", err)
			}
			return
		}

		// Try to route to an existing handler first (stdin, resize, signal).
		if router.route(msg) {
			continue
		}

		// Not routed — handle as a new request.
		switch msg.Type {
		case vsock.TypePing:
			pong := &vsock.Message{
				Version: vsock.ProtocolVersion,
				Type:    vsock.TypePong,
				ID:      msg.ID,
			}
			if err := send(pong); err != nil {
				log.Printf("agent: send pong error: %v", err)
				return
			}

		case vsock.TypeExec:
			go HandleExec(ctx, msg, send)

		case vsock.TypeSession:
			inbox := router.register(msg.ID)
			go func() {
				defer router.unregister(msg.ID)
				HandleSession(ctx, msg, send, inbox)
			}()

		default:
			log.Printf("agent: unknown message type %q", msg.Type)
			payload, _ := json.Marshal(vsock.ExitPayload{Code: -1})
			_ = send(&vsock.Message{
				Version: vsock.ProtocolVersion,
				Type:    vsock.TypeExit,
				ID:      msg.ID,
				Payload: json.RawMessage(payload),
			})
		}
	}
}
