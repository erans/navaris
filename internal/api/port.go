package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/navaris/navaris/internal/domain"
)

type createPortRequest struct {
	TargetPort int `json:"target_port"`
}

func (s *Server) createPort(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")
	var req createPortRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.TargetPort <= 0 {
		http.Error(w, "target_port must be positive", http.StatusBadRequest)
		return
	}

	// Look up sandbox to get backend ref
	sbx, err := s.cfg.Sandboxes.Get(r.Context(), sandboxID)
	if err != nil {
		respondError(w, err)
		return
	}

	ref := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}
	endpoint, err := s.cfg.Provider.PublishPort(r.Context(), ref, req.TargetPort, domain.PublishPortOptions{})
	if err != nil {
		respondError(w, err)
		return
	}

	pb := &domain.PortBinding{
		SandboxID:     sandboxID,
		TargetPort:    req.TargetPort,
		PublishedPort: endpoint.PublishedPort,
		HostAddress:   endpoint.HostAddress,
		CreatedAt:     time.Now().UTC(),
	}
	if err := s.cfg.Ports.Create(r.Context(), pb); err != nil {
		// Rollback: unpublish the port we just published
		s.cfg.Provider.UnpublishPort(r.Context(), ref, endpoint.PublishedPort)
		respondError(w, err)
		return
	}
	respondData(w, http.StatusCreated, pb)
}

func (s *Server) listPorts(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")
	ports, err := s.cfg.Ports.ListBySandbox(r.Context(), sandboxID)
	if err != nil {
		respondError(w, err)
		return
	}
	if ports == nil {
		ports = []*domain.PortBinding{}
	}
	respondList(w, http.StatusOK, ports)
}

func (s *Server) deletePort(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")
	targetPortStr := r.PathValue("target_port")
	targetPort, err := strconv.Atoi(targetPortStr)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid target_port: %s", targetPortStr), http.StatusBadRequest)
		return
	}

	// Look up sandbox to get backend ref for unpublish
	sbx, err := s.cfg.Sandboxes.Get(r.Context(), sandboxID)
	if err != nil {
		respondError(w, err)
		return
	}

	// Find the port binding to get published port for unpublish
	ports, err := s.cfg.Ports.ListBySandbox(r.Context(), sandboxID)
	if err != nil {
		respondError(w, err)
		return
	}
	var publishedPort int
	for _, p := range ports {
		if p.TargetPort == targetPort {
			publishedPort = p.PublishedPort
			break
		}
	}

	if publishedPort > 0 {
		ref := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}
		if err := s.cfg.Provider.UnpublishPort(r.Context(), ref, publishedPort); err != nil {
			respondError(w, err)
			return
		}
	}

	if err := s.cfg.Ports.Delete(r.Context(), sandboxID, targetPort); err != nil {
		respondError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
