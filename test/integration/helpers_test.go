//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/navaris/navaris/pkg/client"
)

// --- Environment helpers ---

func apiURL() string {
	if v := os.Getenv("NAVARIS_API_URL"); v != "" {
		return v
	}
	return "http://localhost:8080"
}

func apiToken() string {
	return os.Getenv("NAVARIS_TOKEN")
}

func baseImage() string {
	if v := os.Getenv("NAVARIS_BASE_IMAGE"); v != "" {
		return v
	}
	return "alpine/3.21"
}

func cliPath() string {
	return os.Getenv("NAVARIS_CLI")
}

// --- Client helpers ---

func newClient() *client.Client {
	return client.NewClient(
		client.WithURL(apiURL()),
		client.WithToken(apiToken()),
	)
}

func waitOpts() *client.WaitOptions {
	return &client.WaitOptions{Timeout: 3 * time.Minute}
}

// --- Test scaffolding helpers ---

// createTestProject creates a project with a unique name and registers cleanup.
func createTestProject(t *testing.T, c *client.Client) *client.Project {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("test-%s-%d", t.Name(), time.Now().UnixNano())
	proj, err := c.CreateProject(ctx, client.CreateProjectRequest{
		Name:     name,
		Metadata: map[string]any{"purpose": "integration-test"},
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		if err := c.DeleteProject(context.Background(), proj.ProjectID); err != nil {
			t.Logf("warning: cleanup project %s: %v", proj.ProjectID, err)
		}
	})
	return proj
}

// createTestSandbox creates a sandbox from the base image and registers cleanup.
func createTestSandbox(t *testing.T, c *client.Client, projectID, name string) string {
	t.Helper()
	ctx := context.Background()
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: projectID,
		Name:      name,
		ImageID:   baseImage(),
	}, waitOpts())
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create sandbox op failed: state=%s error=%s", op.State, op.ErrorText)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() {
		_, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts())
	})
	return sandboxID
}

// --- CLI runner ---

// cliResult holds the output of a CLI command.
type cliResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// runCLIWithArgs executes the navaris CLI binary with the given arguments as-is.
func runCLIWithArgs(t *testing.T, args ...string) cliResult {
	t.Helper()
	cmd := exec.Command(cliPath(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run CLI %v: %v", args, err)
	}

	return cliResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}
}

// runCLI executes the navaris CLI with --api-url, --token, -o json prepended.
func runCLI(t *testing.T, args ...string) cliResult {
	t.Helper()
	fullArgs := append([]string{
		"--api-url", apiURL(),
		"--token", apiToken(),
		"-o", "json",
	}, args...)
	return runCLIWithArgs(t, fullArgs...)
}

// runCLIRaw executes the navaris CLI with --api-url and --token only (no -o json).
func runCLIRaw(t *testing.T, args ...string) cliResult {
	t.Helper()
	fullArgs := append([]string{
		"--api-url", apiURL(),
		"--token", apiToken(),
	}, args...)
	return runCLIWithArgs(t, fullArgs...)
}

// parseCLIJSON parses the JSON output of a CLI command into v.
func parseCLIJSON(t *testing.T, result cliResult, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(result.Stdout), v); err != nil {
		t.Fatalf("parse CLI JSON: %v\nstdout: %s\nstderr: %s", err, result.Stdout, result.Stderr)
	}
}

// --- TestMain ---

func TestMain(m *testing.M) {
	c := newClient()

	// Short context for the health check.
	healthCtx, healthCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer healthCancel()

	health, err := c.Health(healthCtx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "health check failed (is navarisd running?): %v\n", err)
		os.Exit(1)
	}
	if !health.Healthy {
		fmt.Fprintf(os.Stderr, "backend unhealthy: %s\n", health.Error)
		os.Exit(1)
	}
	fmt.Printf("integration test warm-up: backend=%s healthy=%v latency=%dms\n",
		health.Backend, health.Healthy, health.LatencyMS)

	// Longer context for the image pre-pull (cold pulls can take minutes).
	fmt.Printf("pre-pulling base image %s...\n", baseImage())
	warmCtx, warmCancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer warmCancel()

	proj, err := c.CreateProject(warmCtx, client.CreateProjectRequest{
		Name: fmt.Sprintf("warmup-%d", time.Now().UnixNano()),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warmup: create project: %v\n", err)
		os.Exit(1)
	}

	// Best-effort cleanup helper using a fresh context.
	cleanupWarmup := func(sandboxID string) {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanCancel()
		if sandboxID != "" {
			if _, err := c.DestroySandboxAndWait(cleanCtx, sandboxID, &client.WaitOptions{Timeout: 2 * time.Minute}); err != nil {
				fmt.Fprintf(os.Stderr, "warmup: cleanup sandbox: %v\n", err)
			}
		}
		if err := c.DeleteProject(cleanCtx, proj.ProjectID); err != nil {
			fmt.Fprintf(os.Stderr, "warmup: cleanup project: %v\n", err)
		}
	}

	op, err := c.CreateSandboxAndWait(warmCtx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "warmup",
		ImageID:   baseImage(),
	}, &client.WaitOptions{Timeout: 5 * time.Minute})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warmup: create sandbox: %v\n", err)
		resID := ""
		if op != nil {
			resID = op.ResourceID
		}
		cleanupWarmup(resID)
		os.Exit(1)
	}
	if op.State != client.OpSucceeded {
		fmt.Fprintf(os.Stderr, "warmup: sandbox create failed: %s %s\n", op.State, op.ErrorText)
		cleanupWarmup(op.ResourceID)
		os.Exit(1)
	}
	cleanupWarmup(op.ResourceID)
	fmt.Println("warm-up complete")

	os.Exit(m.Run())
}
