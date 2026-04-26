package api

import (
	"io"
	"net/http"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

type createSandboxRequest struct {
	ProjectID     string         `json:"project_id"`
	Name          string         `json:"name"`
	ImageID       string         `json:"image_id"`
	SnapshotID    string         `json:"snapshot_id"`
	CPULimit      *int           `json:"cpu_limit"`
	MemoryLimitMB *int           `json:"memory_limit_mb"`
	NetworkMode   string         `json:"network_mode"`
	ExpiresAt     *time.Time     `json:"expires_at"`
	Metadata      map[string]any `json:"metadata"`
	Backend       string         `json:"backend"`
}

type createSandboxFromSnapshotRequest struct {
	ProjectID     string         `json:"project_id"`
	Name          string         `json:"name"`
	SnapshotID    string         `json:"snapshot_id"`
	CPULimit      *int           `json:"cpu_limit"`
	MemoryLimitMB *int           `json:"memory_limit_mb"`
	NetworkMode   string         `json:"network_mode"`
	ExpiresAt     *time.Time     `json:"expires_at"`
	Metadata      map[string]any `json:"metadata"`
	Backend       string         `json:"backend"`
}

type createSandboxFromImageRequest struct {
	ProjectID     string         `json:"project_id"`
	Name          string         `json:"name"`
	ImageID       string         `json:"image_id"`
	CPULimit      *int           `json:"cpu_limit"`
	MemoryLimitMB *int           `json:"memory_limit_mb"`
	NetworkMode   string         `json:"network_mode"`
	ExpiresAt     *time.Time     `json:"expires_at"`
	Metadata      map[string]any `json:"metadata"`
	Backend       string         `json:"backend"`
}

type stopSandboxRequest struct {
	Force bool `json:"force"`
}

type forkSandboxRequest struct {
	Count int `json:"count"`
}

type updateResourcesRequest struct {
	CPULimit      *int `json:"cpu_limit"`
	MemoryLimitMB *int `json:"memory_limit_mb"`
}

type updateResourcesResponse struct {
	SandboxID     string `json:"sandbox_id"`
	CPULimit      *int   `json:"cpu_limit"`
	MemoryLimitMB *int   `json:"memory_limit_mb"`
	AppliedLive   bool   `json:"applied_live"`
}

func (s *Server) createSandbox(w http.ResponseWriter, r *http.Request) {
	var req createSandboxRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ProjectID == "" || req.Name == "" {
		http.Error(w, "project_id and name are required", http.StatusBadRequest)
		return
	}

	opts := service.CreateSandboxOpts{
		CPULimit:      req.CPULimit,
		MemoryLimitMB: req.MemoryLimitMB,
		NetworkMode:   domain.NetworkMode(req.NetworkMode),
		ExpiresAt:     req.ExpiresAt,
		Metadata:      req.Metadata,
		Backend:       req.Backend,
	}

	var op *domain.Operation
	var err error

	if req.SnapshotID != "" {
		op, err = s.cfg.Sandboxes.CreateFromSnapshot(r.Context(), req.ProjectID, req.Name, req.SnapshotID, opts)
	} else {
		imageID := req.ImageID
		if imageID == "" {
			imageID = "default"
		}
		op, err = s.cfg.Sandboxes.Create(r.Context(), req.ProjectID, req.Name, imageID, opts)
	}

	if err != nil {
		respondError(w, err)
		return
	}
	respondOperation(w, op)
}

func (s *Server) createSandboxFromSnapshot(w http.ResponseWriter, r *http.Request) {
	var req createSandboxFromSnapshotRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ProjectID == "" || req.Name == "" || req.SnapshotID == "" {
		http.Error(w, "project_id, name, and snapshot_id are required", http.StatusBadRequest)
		return
	}

	opts := service.CreateSandboxOpts{
		CPULimit:      req.CPULimit,
		MemoryLimitMB: req.MemoryLimitMB,
		NetworkMode:   domain.NetworkMode(req.NetworkMode),
		ExpiresAt:     req.ExpiresAt,
		Metadata:      req.Metadata,
		Backend:       req.Backend,
	}

	op, err := s.cfg.Sandboxes.CreateFromSnapshot(r.Context(), req.ProjectID, req.Name, req.SnapshotID, opts)
	if err != nil {
		respondError(w, err)
		return
	}
	respondOperation(w, op)
}

func (s *Server) createSandboxFromImage(w http.ResponseWriter, r *http.Request) {
	var req createSandboxFromImageRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ProjectID == "" || req.Name == "" || req.ImageID == "" {
		http.Error(w, "project_id, name, and image_id are required", http.StatusBadRequest)
		return
	}

	opts := service.CreateSandboxOpts{
		CPULimit:      req.CPULimit,
		MemoryLimitMB: req.MemoryLimitMB,
		NetworkMode:   domain.NetworkMode(req.NetworkMode),
		ExpiresAt:     req.ExpiresAt,
		Metadata:      req.Metadata,
		Backend:       req.Backend,
	}

	op, err := s.cfg.Sandboxes.Create(r.Context(), req.ProjectID, req.Name, req.ImageID, opts)
	if err != nil {
		respondError(w, err)
		return
	}
	respondOperation(w, op)
}

func (s *Server) listSandboxes(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		http.Error(w, "project_id query parameter is required", http.StatusBadRequest)
		return
	}

	filter := domain.SandboxFilter{ProjectID: &projectID}
	if stateStr := r.URL.Query().Get("state"); stateStr != "" {
		state := domain.SandboxState(stateStr)
		filter.State = &state
	}

	sandboxes, err := s.cfg.Sandboxes.List(r.Context(), filter)
	if err != nil {
		respondError(w, err)
		return
	}
	if sandboxes == nil {
		sandboxes = []*domain.Sandbox{}
	}
	respondList(w, http.StatusOK, sandboxes)
}

func (s *Server) getSandbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sbx, err := s.cfg.Sandboxes.Get(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	respondData(w, http.StatusOK, sbx)
}

func (s *Server) startSandbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	op, err := s.cfg.Sandboxes.Start(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	respondOperation(w, op)
}

func (s *Server) stopSandbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req stopSandboxRequest
	// Body is optional for stop; only ignore EOF (empty body)
	if err := decodeJSON(r, &req); err != nil && err != io.EOF {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	op, err := s.cfg.Sandboxes.Stop(r.Context(), id, req.Force)
	if err != nil {
		respondError(w, err)
		return
	}
	respondOperation(w, op)
}

func (s *Server) destroySandbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	op, err := s.cfg.Sandboxes.Destroy(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	respondOperation(w, op)
}

func (s *Server) forkSandbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing sandbox id", http.StatusBadRequest)
		return
	}
	var req forkSandboxRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Count < 1 {
		http.Error(w, "count must be >= 1", http.StatusBadRequest)
		return
	}
	op, err := s.cfg.Sandboxes.Fork(r.Context(), id, req.Count)
	if err != nil {
		respondError(w, err)
		return
	}
	respondOperation(w, op)
}

func (s *Server) updateSandboxResources(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing sandbox id", http.StatusBadRequest)
		return
	}
	var req updateResourcesRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.CPULimit == nil && req.MemoryLimitMB == nil {
		http.Error(w, "at least one of cpu_limit, memory_limit_mb is required", http.StatusBadRequest)
		return
	}

	res, err := s.cfg.Sandboxes.UpdateResources(r.Context(), service.UpdateResourcesOpts{
		SandboxID:     id,
		CPULimit:      req.CPULimit,
		MemoryLimitMB: req.MemoryLimitMB,
	})
	if err != nil {
		respondError(w, err)
		return
	}

	respondData(w, http.StatusOK, updateResourcesResponse{
		SandboxID:     res.Sandbox.SandboxID,
		CPULimit:      res.Sandbox.CPULimit,
		MemoryLimitMB: res.Sandbox.MemoryLimitMB,
		AppliedLive:   res.AppliedLive,
	})
}
