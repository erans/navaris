//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

func TestErrorNotFoundSandbox(t *testing.T) {
	c := newClient()
	_, err := c.GetSandbox(context.Background(), "nonexistent-id")
	assertAPIError(t, err, 404)
}

func TestErrorNotFoundProject(t *testing.T) {
	c := newClient()
	_, err := c.GetProject(context.Background(), "nonexistent-id")
	assertAPIError(t, err, 404)
}

func TestErrorNotFoundSnapshot(t *testing.T) {
	c := newClient()
	_, err := c.GetSnapshot(context.Background(), "nonexistent-id")
	assertAPIError(t, err, 404)
}

func TestErrorNotFoundImage(t *testing.T) {
	c := newClient()
	_, err := c.GetImage(context.Background(), "nonexistent-id")
	assertAPIError(t, err, 404)
}

func TestErrorNotFoundSession(t *testing.T) {
	c := newClient()
	_, err := c.GetSession(context.Background(), "nonexistent-id")
	assertAPIError(t, err, 404)
}

func TestErrorDuplicateProjectName(t *testing.T) {
	c := newClient()
	proj := createTestProject(t, c)

	_, err := c.CreateProject(context.Background(), client.CreateProjectRequest{
		Name: proj.Name,
	})
	assertAPIError(t, err, 409)
}

// assertAPIError checks that err is an *client.APIError with the expected status code.
func assertAPIError(t *testing.T, err error, expectedStatus int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with status %d, got nil", expectedStatus)
	}
	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("expected *client.APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != expectedStatus {
		t.Fatalf("expected status %d, got %d: %s", expectedStatus, apiErr.StatusCode, apiErr.Message)
	}
}
