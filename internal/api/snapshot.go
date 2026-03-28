package api

import (
	"net/http"

	"github.com/navaris/navaris/internal/domain"
)

type createSnapshotRequest struct {
	Label           string `json:"label"`
	ConsistencyMode string `json:"consistency_mode"`
}

func (s *Server) createSnapshot(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")
	var req createSnapshotRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Label == "" {
		http.Error(w, "label is required", http.StatusBadRequest)
		return
	}

	mode := domain.ConsistencyMode(req.ConsistencyMode)
	op, err := s.cfg.Snapshots.Create(r.Context(), sandboxID, req.Label, mode)
	if err != nil {
		respondError(w, err)
		return
	}
	respondOperation(w, op)
}

func (s *Server) listSnapshots(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")
	snaps, err := s.cfg.Snapshots.ListBySandbox(r.Context(), sandboxID)
	if err != nil {
		respondError(w, err)
		return
	}
	if snaps == nil {
		snaps = []*domain.Snapshot{}
	}
	respondList(w, http.StatusOK, snaps)
}

func (s *Server) getSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap, err := s.cfg.Snapshots.Get(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	respondData(w, http.StatusOK, snap)
}

func (s *Server) restoreSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshotID := r.PathValue("id")

	snap, err := s.cfg.Snapshots.Get(r.Context(), snapshotID)
	if err != nil {
		respondError(w, err)
		return
	}

	op, err := s.cfg.Snapshots.Restore(r.Context(), snap.SandboxID, snapshotID)
	if err != nil {
		respondError(w, err)
		return
	}
	respondOperation(w, op)
}

func (s *Server) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	op, err := s.cfg.Snapshots.Delete(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	respondOperation(w, op)
}
