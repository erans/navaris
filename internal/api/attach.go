package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"nhooyr.io/websocket"
)

// attachSandbox upgrades to a WebSocket that bridges stdin/stdout between
// the browser and Provider.AttachSession. Returns 404 if the sandbox does
// not exist, 409 if it is not currently running.
func (s *Server) attachSandbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sandboxID := r.PathValue("id")

	sbx, err := s.cfg.Sandboxes.Get(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			respondError(w, domain.ErrNotFound)
			return
		}
		respondError(w, err)
		return
	}
	if sbx.State != domain.SandboxRunning {
		respondError(w, domain.ErrConflict)
		return
	}

	sessionID := r.URL.Query().Get("session")
	var sess *domain.Session
	if sessionID != "" {
		sess, err = s.cfg.Sessions.Get(ctx, sessionID)
		if err != nil {
			respondError(w, err)
			return
		}
		if sess.SandboxID != sandboxID {
			respondError(w, domain.ErrNotFound)
			return
		}
		if sess.State != domain.SessionActive && sess.State != domain.SessionDetached {
			respondError(w, domain.ErrConflict)
			return
		}
	}
	s.bridgeAttach(w, r, sbx, sess)
}

type resizeMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func (s *Server) bridgeAttach(w http.ResponseWriter, r *http.Request, sbx *domain.Sandbox, sess *domain.Session) {
	var sessReq domain.SessionRequest
	if sess != nil && sess.Backing == domain.SessionBackingTmux {
		sessReq = domain.SessionRequest{Command: []string{"tmux", "attach", "-t", sess.SessionID}}
	} else if sess != nil {
		// Direct session — bare shell. Pass empty Shell so the provider's
		// detectShell picks the right one for the container's distro.
		sessReq = domain.SessionRequest{}
	} else {
		shell := r.URL.Query().Get("shell") // optional; empty → provider default
		sessReq = domain.SessionRequest{Shell: shell}
	}

	// Accept the WebSocket handshake BEFORE calling AttachSession so a client
	// that disconnects mid-handshake does not leave a PTY orphaned on the
	// host. If Accept fails, there is nothing to clean up.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{r.Host},
	})
	if err != nil {
		s.log.Error("attach: ws accept failed", "error", err, "sandbox_id", sbx.SandboxID)
		return
	}
	// Decouple the backend attach from the request context — the request
	// context ends when websocket.Accept hijacks the connection, and we
	// want the attach to live as long as the WS does.
	bridgeCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle, err := s.cfg.Provider.AttachSession(bridgeCtx, domain.BackendRef{
		Backend: sbx.Backend,
		Ref:     sbx.BackendRef,
	}, sessReq)
	if err != nil {
		s.log.Error("attach: provider AttachSession failed", "error", err, "sandbox_id", sbx.SandboxID)
		// Close the WS with a policy-violation status so the browser sees
		// a distinct reason, not a silent bye.
		conn.Close(websocket.StatusInternalError, "attach failed")
		return
	}
	if handle.Conn == nil {
		s.log.Error("attach: provider returned nil Conn", "sandbox_id", sbx.SandboxID)
		conn.Close(websocket.StatusInternalError, "attach failed")
		return
	}

	// Ensure both sides close on exit, in the correct order: the backend
	// session first (so its goroutines unblock), then the WS.
	defer conn.Close(websocket.StatusNormalClosure, "bye")
	defer handle.Close()

	// Mark session as detached when the WebSocket closes. Placed after
	// defer conn.Close / defer handle.Close so it runs last (LIFO),
	// i.e. after the bridge loop has fully stopped.
	if sess != nil {
		defer func() {
			_ = s.cfg.Sessions.Detach(context.Background(), sess.SessionID)
		}()
	}

	// Fire ui.attach_opened on open and ui.attach_closed on close, with
	// sandbox ID and duration — matches the spec's Observability section.
	opened := time.Now()
	_ = s.cfg.Events.Publish(bridgeCtx, domain.Event{
		Type:      domain.EventUIAttachOpened,
		Timestamp: opened,
		Data: map[string]any{
			"sandbox_id": sbx.SandboxID,
			"project_id": sbx.ProjectID,
		},
	})
	defer func() {
		_ = s.cfg.Events.Publish(context.Background(), domain.Event{
			Type:      domain.EventUIAttachClosed,
			Timestamp: time.Now(),
			Data: map[string]any{
				"sandbox_id":  sbx.SandboxID,
				"project_id":  sbx.ProjectID,
				"duration_ms": time.Since(opened).Milliseconds(),
			},
		})
	}()

	// Bridge loop — two goroutines race to finish; the first error tears down both sides.
	var once sync.Once
	errCh := make(chan error, 2) // 2 so the losing goroutine's finish() doesn't block even though once.Do skips its send
	finish := func(err error) { once.Do(func() { errCh <- err }) }

	// stdout → ws
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := handle.Conn.Read(buf)
			if n > 0 {
				if wErr := conn.Write(bridgeCtx, websocket.MessageBinary, buf[:n]); wErr != nil {
					finish(wErr)
					return
				}
			}
			if err != nil {
				finish(err)
				return
			}
		}
	}()

	// ws → stdin + control
	go func() {
		for {
			msgType, data, err := conn.Read(bridgeCtx)
			if err != nil {
				finish(err)
				return
			}
			switch msgType {
			case websocket.MessageBinary:
				if _, wErr := handle.Conn.Write(data); wErr != nil {
					finish(wErr)
					return
				}
			case websocket.MessageText:
				var msg resizeMessage
				if err := json.Unmarshal(data, &msg); err != nil {
					// Silently ignore malformed control frames.
					continue
				}
				if msg.Type == "resize" && handle.Resize != nil {
					if err := handle.Resize(msg.Cols, msg.Rows); err != nil {
						s.log.Debug("attach: resize failed", "error", err)
					}
				}
			}
		}
	}()

	// Wait for either goroutine to finish, then bail.
	err = <-errCh
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		s.log.Debug("attach bridge ended", "error", err)
	}
}
