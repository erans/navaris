package api

import (
	"errors"
	"net/http"

	"github.com/navaris/navaris/internal/domain"
)

// attachSandbox upgrades to a WebSocket that bridges stdin/stdout between
// the browser and Provider.AttachSession. Returns 404 if the sandbox does
// not exist, 409 if it is not currently running. The WebSocket upgrade
// itself is handled in Task 13.
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

	// WebSocket handshake + bridge comes in Task 13.
	s.bridgeAttach(w, r, sbx)
}

// bridgeAttach is Task 13's hook. For now it 501s to keep the file compiling.
func (s *Server) bridgeAttach(w http.ResponseWriter, r *http.Request, sbx *domain.Sandbox) {
	http.Error(w, "attach bridge not implemented", http.StatusNotImplemented)
}
