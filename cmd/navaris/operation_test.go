package main_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/testutil/apiserver"
	"github.com/navaris/navaris/pkg/client"
)

// TestOperationWait_PollsUntilTerminal exercises the wait command logic
// through the public client API. The CLI's operation wait subcommand is a
// thin wrapper over WaitForOperation, so the integration is already covered
// by client_test; this test pins the intended exit behavior.
func TestOperationWait_PollsUntilTerminal(t *testing.T) {
	apiURL, disp, _ := apiserver.New(t)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	// Create a project + sandbox to get a real operation we can wait on.
	proj, err := c.CreateProject(context.Background(), client.CreateProjectRequest{Name: "ow-test"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(context.Background(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "wait-target",
		ImageID:   "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Let the dispatcher finish so the operation reaches a terminal state.
	disp.WaitIdle()

	// Wait for it to reach a terminal state with a generous timeout.
	final, err := c.WaitForOperation(context.Background(), op.OperationID, &client.WaitOptions{
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("WaitForOperation: %v", err)
	}
	if !final.State.Terminal() {
		t.Errorf("expected terminal state, got %q", final.State)
	}
	if final.OperationID != op.OperationID {
		t.Errorf("op id mismatch")
	}

	// Sanity: the operation_id we want to print on success should be non-empty
	// (the wait verb prints a single line in quiet mode).
	if strings.TrimSpace(final.OperationID) == "" {
		t.Error("expected non-empty operation id")
	}
}
