package api

import (
	"net/http"

	"github.com/navaris/navaris/internal/domain"
)

type createProjectRequest struct {
	Name     string         `json:"name"`
	Metadata map[string]any `json:"metadata"`
}

type updateProjectRequest struct {
	Name     string         `json:"name"`
	Metadata map[string]any `json:"metadata"`
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	p, err := s.cfg.Projects.Create(r.Context(), req.Name, req.Metadata)
	if err != nil {
		respondError(w, err)
		return
	}
	respondData(w, http.StatusCreated, p)
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.cfg.Projects.List(r.Context())
	if err != nil {
		respondError(w, err)
		return
	}
	if projects == nil {
		projects = []*domain.Project{}
	}
	respondList(w, http.StatusOK, projects)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.cfg.Projects.Get(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	respondData(w, http.StatusOK, p)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req updateProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	p, err := s.cfg.Projects.Update(r.Context(), id, req.Name, req.Metadata)
	if err != nil {
		respondError(w, err)
		return
	}
	respondData(w, http.StatusOK, p)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.cfg.Projects.Delete(r.Context(), id); err != nil {
		respondError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
