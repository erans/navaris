package api

import (
	"net/http"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

type startBoostRequest struct {
	CPULimit        *int `json:"cpu_limit"`
	MemoryLimitMB   *int `json:"memory_limit_mb"`
	DurationSeconds int  `json:"duration_seconds"`
}

type boostResponse struct {
	BoostID               string `json:"boost_id"`
	SandboxID             string `json:"sandbox_id"`
	OriginalCPULimit      *int   `json:"original_cpu_limit"`
	OriginalMemoryLimitMB *int   `json:"original_memory_limit_mb"`
	BoostedCPULimit       *int   `json:"boosted_cpu_limit"`
	BoostedMemoryLimitMB  *int   `json:"boosted_memory_limit_mb"`
	StartedAt             string `json:"started_at"`
	ExpiresAt             string `json:"expires_at"`
	State                 string `json:"state"`
	Source                string `json:"source,omitempty"`
	RevertAttempts        int    `json:"revert_attempts,omitempty"`
	LastError             string `json:"last_error,omitempty"`
}

func boostToResponse(b *domain.Boost) boostResponse {
	return boostResponse{
		BoostID:               b.BoostID,
		SandboxID:             b.SandboxID,
		OriginalCPULimit:      b.OriginalCPULimit,
		OriginalMemoryLimitMB: b.OriginalMemoryLimitMB,
		BoostedCPULimit:       b.BoostedCPULimit,
		BoostedMemoryLimitMB:  b.BoostedMemoryLimitMB,
		StartedAt:             b.StartedAt.UTC().Format(timeFormatJSON),
		ExpiresAt:             b.ExpiresAt.UTC().Format(timeFormatJSON),
		State:                 string(b.State),
		Source:                b.Source,
		RevertAttempts:        b.RevertAttempts,
		LastError:             b.LastError,
	}
}

const timeFormatJSON = "2006-01-02T15:04:05.999999999Z07:00"

func (s *Server) getBoost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, err := s.cfg.Boosts.Get(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	respondData(w, http.StatusOK, boostToResponse(b))
}

func (s *Server) startBoost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing sandbox id", http.StatusBadRequest)
		return
	}
	var req startBoostRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.CPULimit == nil && req.MemoryLimitMB == nil {
		http.Error(w, "at least one of cpu_limit, memory_limit_mb is required", http.StatusBadRequest)
		return
	}
	if req.DurationSeconds <= 0 {
		http.Error(w, "duration_seconds must be > 0", http.StatusBadRequest)
		return
	}

	b, err := s.cfg.Boosts.Start(r.Context(), service.StartBoostOpts{
		SandboxID:       id,
		CPULimit:        req.CPULimit,
		MemoryLimitMB:   req.MemoryLimitMB,
		DurationSeconds: req.DurationSeconds,
		Source:          "external",
	})
	if err != nil {
		respondError(w, err)
		return
	}
	respondData(w, http.StatusOK, boostToResponse(b))
}

func (s *Server) deleteBoost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.cfg.Boosts.Cancel(r.Context(), id); err != nil {
		respondError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
