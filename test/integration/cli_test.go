//go:build integration

package integration

import (
	"testing"
)

func TestCLIProjectCRUD(t *testing.T) {
	if cliPath() == "" {
		t.Skip("NAVARIS_CLI not set")
	}

	result := runCLI(t, "project", "create", "--name", "cli-test-proj")
	if result.ExitCode != 0 {
		t.Fatalf("create exit %d: %s", result.ExitCode, result.Stderr)
	}

	var proj map[string]any
	parseCLIJSON(t, result, &proj)
	projID, ok := proj["ProjectID"].(string)
	if !ok || projID == "" {
		t.Fatalf("expected ProjectID in output: %s", result.Stdout)
	}
	t.Logf("CLI created project %s", projID)

	defer func() {
		runCLI(t, "project", "delete", projID)
	}()

	result = runCLI(t, "project", "list")
	if result.ExitCode != 0 {
		t.Fatalf("list exit %d: %s", result.ExitCode, result.Stderr)
	}

	result = runCLI(t, "project", "get", projID)
	if result.ExitCode != 0 {
		t.Fatalf("get exit %d: %s", result.ExitCode, result.Stderr)
	}

	result = runCLI(t, "project", "delete", projID)
	if result.ExitCode != 0 {
		t.Fatalf("delete exit %d: %s", result.ExitCode, result.Stderr)
	}
}

func TestCLISandboxCreateAndExec(t *testing.T) {
	if cliPath() == "" {
		t.Skip("NAVARIS_CLI not set")
	}

	c := newClient()
	proj := createTestProject(t, c)

	result := runCLI(t, "sandbox", "create",
		"--project", proj.ProjectID,
		"--name", "cli-sbx-test",
		"--image", baseImage(),
		"--wait",
	)
	if result.ExitCode != 0 {
		t.Fatalf("sandbox create exit %d: %s", result.ExitCode, result.Stderr)
	}

	var sbx map[string]any
	parseCLIJSON(t, result, &sbx)
	sandboxID, ok := sbx["SandboxID"].(string)
	if !ok || sandboxID == "" {
		t.Fatalf("expected SandboxID: %s", result.Stdout)
	}
	t.Logf("CLI created sandbox %s", sandboxID)

	defer func() {
		runCLI(t, "sandbox", "destroy", sandboxID, "--wait")
	}()

	result = runCLI(t, "sandbox", "list", "--project", proj.ProjectID)
	if result.ExitCode != 0 {
		t.Fatalf("sandbox list exit %d: %s", result.ExitCode, result.Stderr)
	}

	// Exec via CLI (no -o json — exec streams stdout directly).
	result = runCLIRaw(t, "sandbox", "exec", sandboxID, "--", "echo", "hello-cli")
	if result.ExitCode != 0 {
		t.Fatalf("exec exit %d: %s", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "hello-cli\n" {
		t.Fatalf("exec stdout: got %q, want %q", result.Stdout, "hello-cli\n")
	}
}
