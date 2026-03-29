package api

import (
	"net/http"

	"github.com/navaris/navaris/internal/domain"
)

func (s *Server) getOperation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	op, err := s.cfg.Operations.Get(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	respondData(w, http.StatusOK, op)
}

func (s *Server) listOperations(w http.ResponseWriter, r *http.Request) {
	filter := domain.OperationFilter{}
	if sandboxID := r.URL.Query().Get("sandbox_id"); sandboxID != "" {
		filter.SandboxID = &sandboxID
	}
	if state := r.URL.Query().Get("state"); state != "" {
		opState := domain.OperationState(state)
		filter.State = &opState
	}
	ops, err := s.cfg.Operations.List(r.Context(), filter)
	if err != nil {
		respondError(w, err)
		return
	}
	if ops == nil {
		ops = []*domain.Operation{}
	}
	respondList(w, http.StatusOK, ops)
}

func (s *Server) cancelOperation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.cfg.Operations.Cancel(r.Context(), id); err != nil {
		respondError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
