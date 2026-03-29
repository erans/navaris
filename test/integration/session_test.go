//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

func TestSessionCreateListGetDelete(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)
	sandboxID := createTestSandbox(t, c, proj.ProjectID, "session-test-sbx")

	sess, err := c.CreateSession(ctx, sandboxID, client.CreateSessionRequest{
		Shell: "/bin/sh",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Logf("created session %s", sess.SessionID)

	t.Cleanup(func() {
		_ = c.DestroySession(context.Background(), sess.SessionID)
	})

	got, err := c.GetSession(ctx, sess.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.SandboxID != sandboxID {
		t.Fatalf("expected sandbox ID %s, got %s", sandboxID, got.SandboxID)
	}

	sessions, err := c.ListSessions(ctx, sandboxID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("expected at least one session")
	}
	found := false
	for _, s := range sessions {
		if s.SessionID == sess.SessionID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("created session not in list")
	}

	if err := c.DestroySession(ctx, sess.SessionID); err != nil {
		t.Fatalf("destroy session: %v", err)
	}

	_, err = c.GetSession(ctx, sess.SessionID)
	if err == nil {
		t.Fatal("expected error getting deleted session")
	}
}
