package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestMapErrorCodeForbidden(t *testing.T) {
	if got := mapErrorCode(domain.ErrForbidden); got != 403 {
		t.Errorf("mapErrorCode(ErrForbidden) = %d, want 403", got)
	}
}

func TestRespondErrorForbiddenBody(t *testing.T) {
	rec := httptest.NewRecorder()
	respondError(rec, domain.ErrForbidden)
	if rec.Code != 403 {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var body errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != 403 || body.Error.Message != "forbidden" {
		t.Fatalf("body = %+v, want code=403 message=forbidden", body)
	}
}

func TestMapErrorCode_InvalidArgument(t *testing.T) {
	got := mapErrorCode(domain.ErrInvalidArgument)
	if got != http.StatusBadRequest {
		t.Errorf("ErrInvalidArgument → %d, want %d", got, http.StatusBadRequest)
	}

	wrapped := fmt.Errorf("cpu_limit must be 1..32: %w", domain.ErrInvalidArgument)
	if got := mapErrorCode(wrapped); got != http.StatusBadRequest {
		t.Errorf("wrapped ErrInvalidArgument → %d, want %d", got, http.StatusBadRequest)
	}
}
