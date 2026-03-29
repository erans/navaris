//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

func TestAuthNoToken(t *testing.T) {
	c := client.NewClient(
		client.WithURL(apiURL()),
		client.WithToken(""),
	)
	_, err := c.ListProjects(context.Background())
	if err == nil {
		t.Fatal("expected error for request without token")
	}
	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", apiErr.StatusCode)
	}
}

func TestAuthWrongToken(t *testing.T) {
	c := client.NewClient(
		client.WithURL(apiURL()),
		client.WithToken("wrong-token"),
	)
	_, err := c.ListProjects(context.Background())
	if err == nil {
		t.Fatal("expected error for request with wrong token")
	}
	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", apiErr.StatusCode)
	}
}

func TestAuthValidToken(t *testing.T) {
	c := newClient()
	_, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("expected success with valid token: %v", err)
	}
}
