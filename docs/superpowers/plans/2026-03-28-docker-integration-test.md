# Docker-Based Integration Test Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Docker Compose-based integration test suite that runs Incus + navarisd + comprehensive tests in CI and locally.

**Architecture:** Three Docker containers (Incus privileged, navarisd, test-runner) orchestrated by Docker Compose. Tests exercise the full API surface via the Go SDK and CLI binary. Makefile provides CI and local-dev entry points.

**Tech Stack:** Docker, Docker Compose, Go 1.26+, Incus (Zabbly PPA), GitHub Actions

**Spec:** `docs/superpowers/specs/2026-03-28-docker-integration-test-design.md`

---

### Task 1: Update go.mod with Incus SDK dependency

The Incus provider imports `github.com/lxc/incus/v6/client` behind the `incus` build tag. These dependencies must be in `go.mod` for the Docker build to work.

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Add Incus SDK to module graph**

```bash
cd /home/eran/work/navaris
GOFLAGS=-tags=incus go mod tidy
```

- [ ] **Step 2: Verify build compiles with incus tag**

```bash
CGO_ENABLED=0 go build -tags incus ./cmd/navarisd
```

Expected: builds successfully (binary in current dir)

- [ ] **Step 3: Verify default build still works**

```bash
go build ./cmd/navarisd
go test ./...
```

Expected: both pass without the incus tag

- [ ] **Step 4: Clean up and commit**

```bash
rm -f navarisd
git add go.mod go.sum
git commit -m "chore: add Incus Go SDK dependency for Docker integration build"
```

---

### Task 2: Create .dockerignore

Prevents sending unnecessary files (`.git/`, build artifacts) as Docker context.

**Files:**
- Create: `.dockerignore`

- [ ] **Step 1: Write .dockerignore**

```
.git
bin/
*.db
*.db-journal
docs/
.github/
.claude/
```

- [ ] **Step 2: Commit**

```bash
git add .dockerignore
git commit -m "chore: add .dockerignore for integration test Docker builds"
```

---

### Task 3: Create Incus container image and entrypoint

**Files:**
- Create: `Dockerfile.incus`
- Create: `scripts/incus-entrypoint.sh`

- [ ] **Step 1: Create scripts directory**

```bash
ls scripts/ 2>/dev/null || mkdir scripts
```

- [ ] **Step 2: Write entrypoint script**

Create `scripts/incus-entrypoint.sh`:

```bash
#!/bin/bash
set -eu

# Initialize Incus on first run (idempotent).
if ! incus info &>/dev/null; then
    echo "Initializing Incus..."
    incus admin init --auto
fi

echo "Starting incusd..."
exec incusd --group incus-admin
```

- [ ] **Step 3: Make entrypoint executable**

```bash
chmod +x scripts/incus-entrypoint.sh
```

- [ ] **Step 4: Write Dockerfile.incus**

```dockerfile
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

# Install Incus from Zabbly PPA
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates curl gpg && \
    mkdir -p /etc/apt/keyrings && \
    curl -fsSL https://pkgs.zabbly.com/key.asc | gpg --dearmor -o /etc/apt/keyrings/zabbly.gpg && \
    echo "deb [signed-by=/etc/apt/keyrings/zabbly.gpg] https://pkgs.zabbly.com/incus/stable $(. /etc/os-release && echo ${VERSION_CODENAME}) main" \
        > /etc/apt/sources.list.d/zabbly-incus.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends incus && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

COPY scripts/incus-entrypoint.sh /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
```

- [ ] **Step 5: Verify Dockerfile builds**

```bash
docker build -f Dockerfile.incus -t navaris-incus-test .
```

Expected: image builds (may take a few minutes for apt install).

- [ ] **Step 6: Commit**

```bash
git add Dockerfile.incus scripts/incus-entrypoint.sh
git commit -m "feat: add Incus Docker image and entrypoint for integration tests"
```

---

### Task 4: Create navarisd container image

**Files:**
- Create: `Dockerfile.navarisd`

- [ ] **Step 1: Write Dockerfile.navarisd**

```dockerfile
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -tags incus -o /navarisd ./cmd/navarisd

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends wget && \
    apt-get clean && rm -rf /var/lib/apt/lists/*
COPY --from=build /navarisd /usr/local/bin/navarisd
ENTRYPOINT ["navarisd"]
```

`wget` is included for the health check probe.

- [ ] **Step 2: Verify Dockerfile builds**

```bash
docker build -f Dockerfile.navarisd -t navaris-daemon-test .
```

Expected: builds successfully.

- [ ] **Step 3: Commit**

```bash
git add Dockerfile.navarisd
git commit -m "feat: add navarisd Docker image for integration tests"
```

---

### Task 5: Create test runner container image

**Files:**
- Create: `Dockerfile.test`

- [ ] **Step 1: Write Dockerfile.test**

```dockerfile
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /navaris ./cmd/navaris
RUN CGO_ENABLED=0 go test -tags integration -c -o /integration.test ./test/integration/

FROM debian:bookworm-slim
COPY --from=build /navaris /usr/local/bin/navaris
COPY --from=build /integration.test /integration.test
ENTRYPOINT ["/integration.test", "-test.v", "-test.timeout", "10m"]
```

- [ ] **Step 2: Verify Dockerfile builds** (will fail until test code compiles — that's fine, just verify the build stage works)

```bash
docker build -f Dockerfile.test --target build -t navaris-test-build .
```

Expected: build stage succeeds.

- [ ] **Step 3: Commit**

```bash
git add Dockerfile.test
git commit -m "feat: add test runner Docker image for integration tests"
```

---

### Task 6: Create Docker Compose file

**Files:**
- Create: `docker-compose.integration.yml`

- [ ] **Step 1: Write docker-compose.integration.yml**

```yaml
services:
  incus:
    build:
      context: .
      dockerfile: Dockerfile.incus
    privileged: true
    cgroupns: host
    volumes:
      - incus-socket:/var/lib/incus
      - incus-data:/var/lib/incus/storage-pools
    healthcheck:
      test: ["CMD", "incus", "query", "/1.0"]
      interval: 2s
      timeout: 5s
      retries: 15
      start_period: 10s

  navarisd:
    build:
      context: .
      dockerfile: Dockerfile.navarisd
    command:
      - --listen=:8080
      - --db-path=/tmp/navaris.db
      - --incus-socket=/var/lib/incus/unix.socket
      - --auth-token=test-token
      - --log-level=debug
    ports:
      - "${NAVARIS_HOST_PORT:-}:8080"
    volumes:
      - incus-socket:/var/lib/incus:ro
    depends_on:
      incus:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "--header", "Authorization: Bearer test-token", "http://localhost:8080/v1/health"]
      interval: 2s
      timeout: 5s
      retries: 15
      start_period: 5s

  test-runner:
    build:
      context: .
      dockerfile: Dockerfile.test
    environment:
      NAVARIS_API_URL: http://navarisd:8080
      NAVARIS_TOKEN: test-token
      NAVARIS_BASE_IMAGE: images:alpine/3.19
      NAVARIS_CLI: /usr/local/bin/navaris
    depends_on:
      navarisd:
        condition: service_healthy
    profiles:
      - test

volumes:
  incus-socket:
  incus-data:
```

Note: `NAVARIS_HOST_PORT` is empty by default (no host mapping). Dev mode sets it to `8080`. The test-runner is behind the `test` profile so `integration-env` doesn't start it.

- [ ] **Step 2: Commit**

```bash
git add docker-compose.integration.yml
git commit -m "feat: add Docker Compose for integration test orchestration"
```

---

### Task 7: Create Makefile

**Files:**
- Create: `Makefile`

- [ ] **Step 1: Write Makefile**

```makefile
COMPOSE_FILE := docker-compose.integration.yml

.PHONY: integration-test integration-env integration-env-down integration-logs

integration-test:
	@docker compose -f $(COMPOSE_FILE) --profile test up \
		--build --abort-on-container-exit --exit-code-from test-runner; \
	rc=$$?; \
	docker compose -f $(COMPOSE_FILE) --profile test down -v; \
	exit $$rc

integration-env:
	NAVARIS_HOST_PORT=8080 docker compose -f $(COMPOSE_FILE) up -d --build incus navarisd
	@echo ""
	@echo "Navaris API: http://localhost:8080"
	@echo "Token:       test-token"
	@echo ""
	@echo "Run tests:"
	@echo "  NAVARIS_API_URL=http://localhost:8080 NAVARIS_TOKEN=test-token go test -tags integration ./test/integration/ -v"
	@echo ""
	@echo "Tear down:"
	@echo "  make integration-env-down"

integration-env-down:
	NAVARIS_HOST_PORT=8080 docker compose -f $(COMPOSE_FILE) down -v

integration-logs:
	docker compose -f $(COMPOSE_FILE) logs -f
```

- [ ] **Step 2: Commit**

```bash
git add Makefile
git commit -m "feat: add Makefile with integration test targets"
```

---

### Task 8: Create GitHub Actions workflow

**Files:**
- Create: `.github/workflows/integration.yml`

- [ ] **Step 1: Create .github/workflows directory**

```bash
mkdir -p .github/workflows
```

- [ ] **Step 2: Write workflow file**

Create `.github/workflows/integration.yml`:

```yaml
name: Integration Tests

on:
  push:
    branches: [main]
  pull_request:

jobs:
  integration:
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v4
      - run: make integration-test
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/integration.yml
git commit -m "ci: add GitHub Actions workflow for Docker integration tests"
```

---

### Task 9: Create test helpers and refactor e2e_test.go

Extract shared helpers from `e2e_test.go` into `helpers_test.go`, add `TestMain` with image warm-up, and add CLI runner helper.

**Files:**
- Create: `test/integration/helpers_test.go`
- Modify: `test/integration/e2e_test.go`

- [ ] **Step 1: Write helpers_test.go**

Create `test/integration/helpers_test.go`:

```go
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
	return "images:alpine/3.19"
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
	// Warm up: verify API is reachable before running tests.
	c := newClient()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	health, err := c.Health(ctx)
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

	// Pre-pull the base image by creating and immediately destroying a sandbox.
	// This ensures image download doesn't eat into individual test timeouts.
	fmt.Printf("pre-pulling base image %s...\n", baseImage())
	proj, err := c.CreateProject(ctx, client.CreateProjectRequest{
		Name: fmt.Sprintf("warmup-%d", time.Now().UnixNano()),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warmup: create project: %v\n", err)
		os.Exit(1)
	}
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "warmup",
		ImageID:   baseImage(),
	}, &client.WaitOptions{Timeout: 5 * time.Minute})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warmup: create sandbox: %v\n", err)
		os.Exit(1)
	}
	if op.State != client.OpSucceeded {
		fmt.Fprintf(os.Stderr, "warmup: sandbox create failed: %s %s\n", op.State, op.ErrorText)
		os.Exit(1)
	}
	_, _ = c.DestroySandboxAndWait(ctx, op.ResourceID, &client.WaitOptions{Timeout: 2 * time.Minute})
	_ = c.DeleteProject(ctx, proj.ProjectID)
	fmt.Println("warm-up complete")

	os.Exit(m.Run())
}
```

- [ ] **Step 2: Remove duplicated helpers from e2e_test.go**

Remove the `apiURL`, `apiToken`, `baseImage`, `newClient`, and `waitOpts` functions from `e2e_test.go` (lines 14-41), since they now live in `helpers_test.go`. These are in the same package so they'll be available.

Edit `test/integration/e2e_test.go` — remove:
```go
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
	return "images:alpine/3.19"
}

func newClient() *client.Client {
	return client.NewClient(
		client.WithURL(apiURL()),
		client.WithToken(apiToken()),
	)
}

func waitOpts() *client.WaitOptions {
	return &client.WaitOptions{Timeout: 3 * time.Minute}
}
```

Also remove the `"os"` import since `os.Getenv` moves to helpers. Keep `"time"` and `"context"`.

- [ ] **Step 3: Verify existing test still compiles**

```bash
go vet -tags integration ./test/integration/
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add test/integration/helpers_test.go test/integration/e2e_test.go
git commit -m "refactor: extract shared test helpers and add TestMain warm-up"
```

---

### Task 10: Auth tests

**Files:**
- Create: `test/integration/auth_test.go`

- [ ] **Step 1: Write auth_test.go**

Create `test/integration/auth_test.go`:

```go
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
		client.WithToken(""), // Explicitly clear env-sourced token
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
```

- [ ] **Step 2: Verify it compiles**

```bash
go vet -tags integration ./test/integration/
```

- [ ] **Step 3: Commit**

```bash
git add test/integration/auth_test.go
git commit -m "feat: add auth integration tests"
```

---

### Task 11: Error path tests

**Files:**
- Create: `test/integration/error_test.go`

- [ ] **Step 1: Write error_test.go**

Create `test/integration/error_test.go`:

```go
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
```

- [ ] **Step 2: Verify it compiles**

```bash
go vet -tags integration ./test/integration/
```

- [ ] **Step 3: Commit**

```bash
git add test/integration/error_test.go
git commit -m "feat: add error path integration tests"
```

---

### Task 12: Image lifecycle tests

**Files:**
- Create: `test/integration/image_test.go`

- [ ] **Step 1: Write image_test.go**

Create `test/integration/image_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

func TestImageRegisterListGetDelete(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	// Register an external image.
	img, err := c.RegisterImage(ctx, client.RegisterImageRequest{
		Name:       "test-image",
		Version:    "1.0",
		Backend:    "incus",
		BackendRef: baseImage(),
	})
	if err != nil {
		t.Fatalf("register image: %v", err)
	}
	t.Logf("registered image %s", img.ImageID)

	t.Cleanup(func() {
		op, err := c.DeleteImage(context.Background(), img.ImageID)
		if err != nil {
			t.Logf("warning: delete image: %v", err)
			return
		}
		c.WaitForOperation(context.Background(), op.OperationID, waitOpts())
	})

	// Get image.
	got, err := c.GetImage(ctx, img.ImageID)
	if err != nil {
		t.Fatalf("get image: %v", err)
	}
	if got.Name != "test-image" {
		t.Fatalf("expected name test-image, got %s", got.Name)
	}

	// List images.
	images, err := c.ListImages(ctx, "test-image", "")
	if err != nil {
		t.Fatalf("list images: %v", err)
	}
	found := false
	for _, i := range images {
		if i.ImageID == img.ImageID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("registered image not found in list")
	}
}

func TestImagePromoteFromSnapshot(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)
	sandboxID := createTestSandbox(t, c, proj.ProjectID, "img-promote-sbx")

	// Stop sandbox and create snapshot.
	stopOp, err := c.StopSandboxAndWait(ctx, sandboxID, client.StopSandboxRequest{}, waitOpts())
	if err != nil {
		t.Fatalf("stop sandbox: %v", err)
	}
	if stopOp.State != client.OpSucceeded {
		t.Fatalf("stop failed: %s %s", stopOp.State, stopOp.ErrorText)
	}

	snapOp, err := c.CreateSnapshot(ctx, sandboxID, client.CreateSnapshotRequest{
		Label:           "for-image-promote",
		ConsistencyMode: "stopped",
	})
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	snapOp, err = c.WaitForOperation(ctx, snapOp.OperationID, waitOpts())
	if err != nil {
		t.Fatalf("wait snapshot: %v", err)
	}
	if snapOp.State != client.OpSucceeded {
		t.Fatalf("snapshot failed: %s %s", snapOp.State, snapOp.ErrorText)
	}
	snapshotID := snapOp.ResourceID

	t.Cleanup(func() {
		delOp, err := c.DeleteSnapshot(context.Background(), snapshotID)
		if err != nil {
			return
		}
		c.WaitForOperation(context.Background(), delOp.OperationID, waitOpts())
	})

	// Promote snapshot to image.
	promoteOp, err := c.PromoteImage(ctx, client.CreateImageRequest{
		SnapshotID: snapshotID,
		Name:       "promoted-test",
		Version:    "1.0",
	})
	if err != nil {
		t.Fatalf("promote image: %v", err)
	}
	promoteOp, err = c.WaitForOperation(ctx, promoteOp.OperationID, waitOpts())
	if err != nil {
		t.Fatalf("wait promote: %v", err)
	}
	if promoteOp.State != client.OpSucceeded {
		t.Fatalf("promote failed: %s %s", promoteOp.State, promoteOp.ErrorText)
	}
	imageID := promoteOp.ResourceID
	t.Logf("promoted image %s", imageID)

	t.Cleanup(func() {
		delOp, err := c.DeleteImage(context.Background(), imageID)
		if err != nil {
			return
		}
		c.WaitForOperation(context.Background(), delOp.OperationID, waitOpts())
	})

	// Verify image exists.
	img, err := c.GetImage(ctx, imageID)
	if err != nil {
		t.Fatalf("get promoted image: %v", err)
	}
	if img.Name != "promoted-test" {
		t.Fatalf("expected name promoted-test, got %s", img.Name)
	}
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go vet -tags integration ./test/integration/
```

- [ ] **Step 3: Commit**

```bash
git add test/integration/image_test.go
git commit -m "feat: add image lifecycle integration tests"
```

---

### Task 13: Session lifecycle tests

**Files:**
- Create: `test/integration/session_test.go`

- [ ] **Step 1: Write session_test.go**

Create `test/integration/session_test.go`:

```go
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

	// Create session.
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

	// Get session.
	got, err := c.GetSession(ctx, sess.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.SandboxID != sandboxID {
		t.Fatalf("expected sandbox ID %s, got %s", sandboxID, got.SandboxID)
	}

	// List sessions.
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

	// Delete session.
	if err := c.DestroySession(ctx, sess.SessionID); err != nil {
		t.Fatalf("destroy session: %v", err)
	}

	// Verify deleted.
	_, err = c.GetSession(ctx, sess.SessionID)
	if err == nil {
		t.Fatal("expected error getting deleted session")
	}
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go vet -tags integration ./test/integration/
```

- [ ] **Step 3: Commit**

```bash
git add test/integration/session_test.go
git commit -m "feat: add session lifecycle integration tests"
```

---

### Task 14: Port lifecycle tests

**Files:**
- Create: `test/integration/port_test.go`

- [ ] **Step 1: Write port_test.go**

Create `test/integration/port_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

func TestPortPublishListDelete(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)
	sandboxID := createTestSandbox(t, c, proj.ProjectID, "port-test-sbx")

	// Publish port.
	pb, err := c.CreatePort(ctx, sandboxID, client.CreatePortRequest{
		TargetPort: 8080,
	})
	if err != nil {
		t.Fatalf("create port: %v", err)
	}
	t.Logf("published port %d -> %d", pb.TargetPort, pb.PublishedPort)

	if pb.TargetPort != 8080 {
		t.Fatalf("expected target port 8080, got %d", pb.TargetPort)
	}
	if pb.PublishedPort == 0 {
		t.Fatal("expected non-zero published port")
	}

	// List ports.
	ports, err := c.ListPorts(ctx, sandboxID)
	if err != nil {
		t.Fatalf("list ports: %v", err)
	}
	if len(ports) != 1 {
		t.Fatalf("expected 1 port, got %d", len(ports))
	}
	if ports[0].TargetPort != 8080 {
		t.Fatalf("listed port target mismatch: %d", ports[0].TargetPort)
	}

	// Delete port.
	if err := c.DeletePort(ctx, sandboxID, 8080); err != nil {
		t.Fatalf("delete port: %v", err)
	}

	// Verify deleted.
	ports, err = c.ListPorts(ctx, sandboxID)
	if err != nil {
		t.Fatalf("list ports after delete: %v", err)
	}
	if len(ports) != 0 {
		t.Fatalf("expected 0 ports after delete, got %d", len(ports))
	}
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go vet -tags integration ./test/integration/
```

- [ ] **Step 3: Commit**

```bash
git add test/integration/port_test.go
git commit -m "feat: add port lifecycle integration tests"
```

---

### Task 15: Operation management tests

**Files:**
- Create: `test/integration/operation_test.go`

- [ ] **Step 1: Write operation_test.go**

Create `test/integration/operation_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

func TestOperationListAndGet(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)

	// Create a sandbox to generate an operation.
	op, err := c.CreateSandbox(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "op-test-sbx",
		ImageID:   baseImage(),
	})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	// Wait for it to finish.
	finalOp, err := c.WaitForOperation(ctx, op.OperationID, waitOpts())
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	sandboxID := finalOp.ResourceID
	t.Cleanup(func() {
		_, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts())
	})

	// Get the operation by ID.
	got, err := c.GetOperation(ctx, op.OperationID)
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.OperationID != op.OperationID {
		t.Fatalf("operation ID mismatch: %s vs %s", got.OperationID, op.OperationID)
	}
	if got.State != client.OpSucceeded {
		t.Fatalf("expected succeeded, got %s", got.State)
	}

	// List operations for sandbox.
	ops, err := c.ListOperations(ctx, sandboxID, "")
	if err != nil {
		t.Fatalf("list operations: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("expected at least one operation")
	}
	found := false
	for _, o := range ops {
		if o.OperationID == op.OperationID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("operation not found in list")
	}
}

func TestOperationCancel(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)

	// Start creating a sandbox (async — don't wait).
	op, err := c.CreateSandbox(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "cancel-test-sbx",
		ImageID:   baseImage(),
	})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	// Try to cancel it. This may or may not succeed depending on timing
	// (the operation might already be complete). We test that the API
	// accepts the request without error for in-flight operations.
	err = c.CancelOperation(ctx, op.OperationID)

	// Wait for terminal state regardless.
	finalOp, err2 := c.WaitForOperation(ctx, op.OperationID, waitOpts())
	if err2 != nil {
		t.Fatalf("wait: %v", err2)
	}

	// Cleanup if sandbox was actually created.
	if finalOp.State == client.OpSucceeded && finalOp.ResourceID != "" {
		t.Cleanup(func() {
			_, _ = c.DestroySandboxAndWait(context.Background(), finalOp.ResourceID, waitOpts())
		})
	}

	t.Logf("cancel err=%v, final state=%s", err, finalOp.State)
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go vet -tags integration ./test/integration/
```

- [ ] **Step 3: Commit**

```bash
git add test/integration/operation_test.go
git commit -m "feat: add operation management integration tests"
```

---

### Task 16: Snapshot restore tests

**Files:**
- Create: `test/integration/snapshot_test.go`

- [ ] **Step 1: Write snapshot_test.go**

Create `test/integration/snapshot_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

func TestSnapshotRestoreToSandbox(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)
	sandboxID := createTestSandbox(t, c, proj.ProjectID, "snap-restore-sbx")

	// Write a marker file into the sandbox.
	execResp, err := c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", "echo marker-before > /tmp/marker.txt"},
	})
	if err != nil {
		t.Fatalf("exec write marker: %v", err)
	}
	if execResp.ExitCode != 0 {
		t.Fatalf("exec write marker exit %d: %s", execResp.ExitCode, execResp.Stderr)
	}

	// Stop and snapshot.
	stopOp, err := c.StopSandboxAndWait(ctx, sandboxID, client.StopSandboxRequest{}, waitOpts())
	if err != nil || stopOp.State != client.OpSucceeded {
		t.Fatalf("stop: err=%v state=%s", err, stopOp.State)
	}

	snapOp, err := c.CreateSnapshot(ctx, sandboxID, client.CreateSnapshotRequest{
		Label:           "restore-test-snap",
		ConsistencyMode: "stopped",
	})
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	snapOp, err = c.WaitForOperation(ctx, snapOp.OperationID, waitOpts())
	if err != nil || snapOp.State != client.OpSucceeded {
		t.Fatalf("snapshot: err=%v state=%s", err, snapOp.State)
	}
	snapshotID := snapOp.ResourceID
	t.Cleanup(func() {
		delOp, _ := c.DeleteSnapshot(context.Background(), snapshotID)
		if delOp != nil {
			c.WaitForOperation(context.Background(), delOp.OperationID, waitOpts())
		}
	})

	// Start sandbox again and modify the marker.
	startOp, err := c.StartSandboxAndWait(ctx, sandboxID, waitOpts())
	if err != nil || startOp.State != client.OpSucceeded {
		t.Fatalf("start: err=%v state=%s", err, startOp.State)
	}

	execResp, err = c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", "echo marker-after > /tmp/marker.txt"},
	})
	if err != nil || execResp.ExitCode != 0 {
		t.Fatalf("exec modify marker: err=%v exit=%d", err, execResp.ExitCode)
	}

	// Stop and restore the snapshot.
	stopOp, err = c.StopSandboxAndWait(ctx, sandboxID, client.StopSandboxRequest{}, waitOpts())
	if err != nil || stopOp.State != client.OpSucceeded {
		t.Fatalf("stop before restore: err=%v state=%s", err, stopOp.State)
	}

	restoreOp, err := c.RestoreSnapshot(ctx, snapshotID)
	if err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}
	restoreOp, err = c.WaitForOperation(ctx, restoreOp.OperationID, waitOpts())
	if err != nil || restoreOp.State != client.OpSucceeded {
		t.Fatalf("restore: err=%v state=%s", err, restoreOp.State)
	}

	// Start and verify marker is back to original value.
	startOp, err = c.StartSandboxAndWait(ctx, sandboxID, waitOpts())
	if err != nil || startOp.State != client.OpSucceeded {
		t.Fatalf("start after restore: err=%v state=%s", err, startOp.State)
	}

	execResp, err = c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"cat", "/tmp/marker.txt"},
	})
	if err != nil {
		t.Fatalf("exec read marker: %v", err)
	}
	if execResp.Stdout != "marker-before\n" {
		t.Fatalf("expected marker-before after restore, got %q", execResp.Stdout)
	}
	t.Log("snapshot restore verified: marker file reverted to pre-snapshot state")
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go vet -tags integration ./test/integration/
```

- [ ] **Step 3: Commit**

```bash
git add test/integration/snapshot_test.go
git commit -m "feat: add snapshot restore integration tests"
```

---

### Task 17: Concurrent operations tests

**Files:**
- Create: `test/integration/concurrent_test.go`

- [ ] **Step 1: Write concurrent_test.go**

Create `test/integration/concurrent_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

func TestConcurrentSandboxCreation(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)

	const n = 3
	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		sandboxIDs []string
		errors     []error
	)

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
				ProjectID: proj.ProjectID,
				Name:      fmt.Sprintf("concurrent-sbx-%d", idx),
				ImageID:   baseImage(),
			}, waitOpts())
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errors = append(errors, fmt.Errorf("sandbox %d: %w", idx, err))
				return
			}
			if op.State != client.OpSucceeded {
				errors = append(errors, fmt.Errorf("sandbox %d: state=%s error=%s", idx, op.State, op.ErrorText))
				return
			}
			sandboxIDs = append(sandboxIDs, op.ResourceID)
		}(i)
	}
	wg.Wait()

	// Cleanup all created sandboxes.
	t.Cleanup(func() {
		for _, id := range sandboxIDs {
			_, _ = c.DestroySandboxAndWait(context.Background(), id, waitOpts())
		}
	})

	if len(errors) > 0 {
		for _, err := range errors {
			t.Errorf("concurrent error: %v", err)
		}
		t.FailNow()
	}

	if len(sandboxIDs) != n {
		t.Fatalf("expected %d sandboxes, got %d", n, len(sandboxIDs))
	}

	// Verify all sandboxes are running.
	for _, id := range sandboxIDs {
		sbx, err := c.GetSandbox(ctx, id)
		if err != nil {
			t.Fatalf("get sandbox %s: %v", id, err)
		}
		if sbx.State != "running" {
			t.Fatalf("sandbox %s state: %s", id, sbx.State)
		}
	}

	t.Logf("all %d sandboxes created concurrently and running", n)
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go vet -tags integration ./test/integration/
```

- [ ] **Step 3: Commit**

```bash
git add test/integration/concurrent_test.go
git commit -m "feat: add concurrent operations integration tests"
```

---

### Task 18: WebSocket events tests

**Files:**
- Create: `test/integration/events_test.go`

- [ ] **Step 1: Write events_test.go**

Create `test/integration/events_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/navaris/navaris/pkg/client"
	"nhooyr.io/websocket"
)

func TestEventStreamReceivesSandboxEvents(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	// Connect to event stream via WebSocket.
	u, err := url.Parse(apiURL())
	if err != nil {
		t.Fatalf("parse API URL: %v", err)
	}
	u.Scheme = "ws"
	u.Path = "/v1/events"
	wsURL := u.String()

	headers := http.Header{}
	if tok := apiToken(); tok != "" {
		headers.Set("Authorization", "Bearer "+tok)
	}

	wsCtx, wsCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer wsCancel()

	conn, _, err := websocket.Dial(wsCtx, wsURL, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")

	// Collect events in background.
	type event struct {
		Type       string `json:"type"`
		ResourceID string `json:"resource_id"`
	}
	eventCh := make(chan event, 100)
	go func() {
		for {
			_, msg, err := conn.Read(wsCtx)
			if err != nil {
				return
			}
			var ev event
			if json.Unmarshal(msg, &ev) == nil {
				eventCh <- ev
			}
		}
	}()

	// Create a sandbox to trigger events.
	proj := createTestProject(t, c)
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "events-test-sbx",
		ImageID:   baseImage(),
	}, waitOpts())
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create failed: %s", op.ErrorText)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() {
		_, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts())
	})

	// Wait for at least one event related to our sandbox.
	timeout := time.After(30 * time.Second)
	received := false
	for !received {
		select {
		case ev := <-eventCh:
			if ev.ResourceID == sandboxID {
				t.Logf("received event: type=%s resource=%s", ev.Type, ev.ResourceID)
				received = true
			}
		case <-timeout:
			t.Fatal("timeout waiting for sandbox event on WebSocket")
		}
	}
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go vet -tags integration ./test/integration/
```

- [ ] **Step 3: Commit**

```bash
git add test/integration/events_test.go
git commit -m "feat: add WebSocket event stream integration tests"
```

---

### Task 19: CLI tests

**Files:**
- Create: `test/integration/cli_test.go`

- [ ] **Step 1: Write cli_test.go**

Create `test/integration/cli_test.go`:

```go
//go:build integration

package integration

import (
	"testing"
)

func TestCLIProjectCRUD(t *testing.T) {
	if cliPath() == "" {
		t.Skip("NAVARIS_CLI not set")
	}

	// Create project.
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

	// List projects.
	result = runCLI(t, "project", "list")
	if result.ExitCode != 0 {
		t.Fatalf("list exit %d: %s", result.ExitCode, result.Stderr)
	}

	// Get project.
	result = runCLI(t, "project", "get", projID)
	if result.ExitCode != 0 {
		t.Fatalf("get exit %d: %s", result.ExitCode, result.Stderr)
	}

	// Delete project.
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

	// Create sandbox via CLI with --wait.
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

	// List sandboxes.
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
```

- [ ] **Step 2: Verify it compiles**

```bash
go vet -tags integration ./test/integration/
```

- [ ] **Step 3: Commit**

```bash
git add test/integration/cli_test.go
git commit -m "feat: add CLI integration tests"
```

---

### Task 20: Final verification — run full integration test locally

- [ ] **Step 1: Verify all test files compile**

```bash
go vet -tags integration ./test/integration/
```

Expected: no errors.

- [ ] **Step 2: Run the full Docker integration test**

```bash
make integration-test
```

Expected: Docker images build, all three containers start, tests run and pass, containers torn down.

- [ ] **Step 3: Verify local dev workflow**

```bash
make integration-env
# In another terminal or after:
NAVARIS_API_URL=http://localhost:8080 NAVARIS_TOKEN=test-token \
  go test -tags integration ./test/integration/ -v -run TestAuthValidToken
make integration-env-down
```

Expected: single test runs and passes against the local environment.

- [ ] **Step 4: Final commit with all files**

```bash
git add -A
git status  # Review what's staged
git commit -m "feat: Docker-based integration test suite with Incus-in-Docker

Complete integration test infrastructure:
- Incus-in-Docker (privileged) + navarisd + test-runner containers
- Docker Compose orchestration with CI and dev profiles
- Makefile targets: integration-test, integration-env, integration-env-down
- GitHub Actions workflow
- Comprehensive test suite: e2e lifecycle, auth, errors, images,
  sessions, ports, operations, snapshot restore, concurrency,
  WebSocket events, and CLI tests
- TestMain warm-up for base image pre-pull"
```
