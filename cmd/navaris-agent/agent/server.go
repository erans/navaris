package agent

import (
	"encoding/json"
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
func (s *Server) Serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Listener closed — stop serving.
			return
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	var mu sync.Mutex
	send := SendFunc(func(msg *vsock.Message) error {
		mu.Lock()
		defer mu.Unlock()
		return vsock.Encode(conn, msg)
	})

	for {
		msg, err := vsock.Decode(conn)
		if err != nil {
			if err != io.EOF {
				log.Printf("agent: decode error: %v", err)
			}
			return
		}

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
			go HandleExec(msg, send)

		case vsock.TypeSession:
			go HandleSession(msg, send, conn)

		default:
			log.Printf("agent: unknown message type %q", msg.Type)
			// Send an exit with code -1 to signal unrecognised request.
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
