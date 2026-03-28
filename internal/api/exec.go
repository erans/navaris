package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/navaris/navaris/internal/domain"
)

type execRequest struct {
	Command []string          `json:"command"`
	Env     map[string]string `json:"env"`
	WorkDir string            `json:"work_dir"`
}

type execResponse struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

func (s *Server) execInSandbox(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")
	var req execRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if len(req.Command) == 0 {
		http.Error(w, "command is required", http.StatusBadRequest)
		return
	}

	sbx, err := s.cfg.Sandboxes.Get(r.Context(), sandboxID)
	if err != nil {
		respondError(w, err)
		return
	}

	ref := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}
	handle, err := s.cfg.Provider.Exec(r.Context(), ref, domain.ExecRequest{
		Command: req.Command,
		Env:     req.Env,
		WorkDir: req.WorkDir,
	})
	if err != nil {
		respondError(w, err)
		return
	}

	stdout, stdoutErr := io.ReadAll(io.LimitReader(handle.Stdout, 10<<20)) // 10 MB limit
	stderr, stderrErr := io.ReadAll(io.LimitReader(handle.Stderr, 1<<20))  // 1 MB limit
	exitCode, waitErr := handle.Wait()

	if stdoutErr != nil || stderrErr != nil || waitErr != nil {
		http.Error(w, "exec stream failed", http.StatusInternalServerError)
		return
	}

	resp := execResponse{
		ExitCode: exitCode,
		Stdout:   string(stdout),
		Stderr:   string(stderr),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}
