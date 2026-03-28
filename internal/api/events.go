package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/navaris/navaris/internal/domain"
	"nhooyr.io/websocket"
)

func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.log.Error("websocket accept failed", "error", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "bye")

	filter := domain.EventFilter{}
	if sandboxID := r.URL.Query().Get("sandbox_id"); sandboxID != "" {
		filter.SandboxID = &sandboxID
	}
	if projectID := r.URL.Query().Get("project_id"); projectID != "" {
		filter.ProjectID = &projectID
	}

	ch, cancel, err := s.cfg.Events.Subscribe(r.Context(), filter)
	if err != nil {
		s.log.Error("event subscribe failed", "error", err)
		return
	}
	defer cancel()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				slog.Error("marshal event", "error", err)
				continue
			}
			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				return
			}
		}
	}
}
