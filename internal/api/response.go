package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/navaris/navaris/internal/domain"
)

type errorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type listResponse struct {
	Data       any `json:"data"`
	Pagination any `json:"pagination"`
}

func respondData(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondList(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(listResponse{Data: data, Pagination: nil})
}

func respondOperation(w http.ResponseWriter, op *domain.Operation) {
	respondData(w, http.StatusAccepted, op)
}

func respondError(w http.ResponseWriter, err error) {
	code := mapErrorCode(err)
	resp := errorResponse{}
	resp.Error.Code = code
	switch {
	case code == http.StatusServiceUnavailable:
		resp.Error.Message = "service temporarily unavailable"
		slog.Warn("api error", "status", code, "error", err.Error())
	case code >= 500:
		resp.Error.Message = "internal server error"
		slog.Error("api error", "status", code, "error", err.Error())
	default:
		resp.Error.Message = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	if code == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "1")
	}
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(resp)
}

func mapErrorCode(err error) int {
	if errors.Is(err, domain.ErrNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, domain.ErrConflict) {
		return http.StatusConflict
	}
	if errors.Is(err, domain.ErrInvalidState) {
		return http.StatusUnprocessableEntity
	}
	if errors.Is(err, domain.ErrUnauthorized) {
		return http.StatusUnauthorized
	}
	if errors.Is(err, domain.ErrBusy) {
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
