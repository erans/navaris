package api

import (
	"net/http"

	"github.com/navaris/navaris/internal/domain"
)

type promoteImageRequest struct {
	SnapshotID string `json:"snapshot_id"`
	Name       string `json:"name"`
	Version    string `json:"version"`
}

type registerImageRequest struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Backend      string `json:"backend"`
	BackendRef   string `json:"backend_ref"`
	Architecture string `json:"architecture"`
}

func (s *Server) promoteImage(w http.ResponseWriter, r *http.Request) {
	var req promoteImageRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.SnapshotID == "" || req.Name == "" || req.Version == "" {
		http.Error(w, "snapshot_id, name, and version are required", http.StatusBadRequest)
		return
	}
	op, err := s.cfg.Images.PromoteSnapshot(r.Context(), req.SnapshotID, req.Name, req.Version)
	if err != nil {
		respondError(w, err)
		return
	}
	respondOperation(w, op)
}

func (s *Server) registerImage(w http.ResponseWriter, r *http.Request) {
	var req registerImageRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Version == "" || req.Backend == "" || req.BackendRef == "" {
		http.Error(w, "name, version, backend, and backend_ref are required", http.StatusBadRequest)
		return
	}
	arch := req.Architecture
	if arch == "" {
		arch = "amd64"
	}
	img, err := s.cfg.Images.Register(r.Context(), req.Name, req.Version, req.Backend, req.BackendRef, arch)
	if err != nil {
		respondError(w, err)
		return
	}
	respondData(w, http.StatusCreated, img)
}

func (s *Server) listImages(w http.ResponseWriter, r *http.Request) {
	filter := domain.ImageFilter{}
	if name := r.URL.Query().Get("name"); name != "" {
		filter.Name = &name
	}
	if arch := r.URL.Query().Get("architecture"); arch != "" {
		filter.Architecture = &arch
	}
	images, err := s.cfg.Images.List(r.Context(), filter)
	if err != nil {
		respondError(w, err)
		return
	}
	if images == nil {
		images = []*domain.BaseImage{}
	}
	respondList(w, http.StatusOK, images)
}

func (s *Server) getImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	img, err := s.cfg.Images.Get(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	respondData(w, http.StatusOK, img)
}

func (s *Server) deleteImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	op, err := s.cfg.Images.Delete(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	respondOperation(w, op)
}
