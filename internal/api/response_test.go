package api

import (
	"encoding/json"
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
