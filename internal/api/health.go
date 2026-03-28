package api

import "net/http"

func (s *Server) healthCheck(w http.ResponseWriter, r *http.Request) {
	health := s.cfg.Provider.Health(r.Context())
	respondData(w, http.StatusOK, health)
}
