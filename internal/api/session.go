package api

import (
	"net/http"

	"github.com/navaris/navaris/internal/domain"
)

type createSessionRequest struct {
	Backing string `json:"backing"`
	Shell   string `json:"shell"`
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")
	var req createSessionRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	backing := domain.SessionBacking(req.Backing)
	sess, err := s.cfg.Sessions.Create(r.Context(), sandboxID, backing, req.Shell)
	if err != nil {
		respondError(w, err)
		return
	}
	respondData(w, http.StatusCreated, sess)
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")
	sessions, err := s.cfg.Sessions.ListBySandbox(r.Context(), sandboxID)
	if err != nil {
		respondError(w, err)
		return
	}
	if sessions == nil {
		sessions = []*domain.Session{}
	}
	respondList(w, http.StatusOK, sessions)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := s.cfg.Sessions.Get(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	respondData(w, http.StatusOK, sess)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.cfg.Sessions.Destroy(r.Context(), id); err != nil {
		respondError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
