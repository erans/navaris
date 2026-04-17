package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/testutil/apiserver"
	"github.com/navaris/navaris/pkg/client"
)

func TestWaitState_ReachesRunning(t *testing.T) {
	apiURL, _ := apiserver.New(t)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	proj, err := c.CreateProject(context.Background(), client.CreateProjectRequest{Name: "ws-test"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(context.Background(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "wait-state-target",
		ImageID:   "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(context.Background(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	final, err := PollSandboxState(ctx, c, op.ResourceID, "running", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("PollSandboxState: %v", err)
	}
	if final.State != "running" {
		t.Errorf("got state %q, want running", final.State)
	}
}

func TestWaitState_TimesOutWhenStateNeverReached(t *testing.T) {
	apiURL, _ := apiserver.New(t)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	proj, err := c.CreateProject(context.Background(), client.CreateProjectRequest{Name: "ws-test2"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(context.Background(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "wait-state-target2",
		ImageID:   "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(context.Background(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err = PollSandboxState(ctx, c, op.ResourceID, "no-such-state", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected error wrapping context.DeadlineExceeded, got: %v", err)
	}
}
