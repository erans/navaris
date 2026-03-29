# Docker-Based Integration Test Design

## 1. Overview

A comprehensive, Docker-based integration test suite for Navaris that runs the full stack — Incus, navarisd, and tests — inside Docker Compose. The suite is the CI gate for every push and PR, and doubles as a local dev tool for debugging.

The core idea: Incus runs in a privileged Docker container, navarisd connects to it via a shared Unix socket volume, and a test-runner container exercises the entire API surface through both the Go SDK and the `navaris` CLI binary.

## 2. Goals

- Run the full Navaris e2e lifecycle (project, sandbox, exec, snapshot, clone, destroy) in CI without Incus on the host
- Test every API surface: REST endpoints, WebSocket events, auth middleware, error paths, concurrent operations
- Test the `navaris` CLI binary end-to-end, not just the Go SDK
- One command to run (`make integration-test`), zero host dependencies beyond Docker
- Runnable locally with a dev-friendly workflow for iterative debugging

## 3. Non-goals

- Testing Incus itself (we trust it as a dependency)
- Performance benchmarking (separate concern)
- Multi-host or fleet testing (v1 is single-node)
- Testing the macOS VM layer (Lima/Virtualization.framework)

## 4. Architecture

```
Docker Compose
├── incus (privileged)
│   ├── incusd daemon
│   ├── auto-initialized storage + network
│   └── exports /run/incus/ via named volume
│
├── navarisd
│   ├── built from project source (-tags incus)
│   ├── mounts incus socket volume (read-only)
│   ├── --incus-socket /run/incus/unix.socket
│   ├── --auth-token test-token
│   └── health check on /v1/health
│
└── test-runner
    ├── compiled integration test binary
    ├── navaris CLI binary
    ├── connects to http://navarisd:8080
    └── exit code = test result
```

### 4.1 Container dependencies

```
incus (healthy) --> navarisd (healthy) --> test-runner (runs tests)
```

Each container waits for its dependency's health check before starting.

### 4.2 Shared volumes

| Volume | Purpose | Mounted by |
|--------|---------|------------|
| `incus-socket` | Unix socket for incusd communication | incus (rw), navarisd (ro) |
| `incus-data` | Incus storage pool + cached images | incus only |

## 5. Incus Container

### 5.1 Image

`Dockerfile.incus` based on Ubuntu 24.04 with the Zabbly Incus PPA:

1. Install `incus` package from Zabbly repository
2. Copy entrypoint script
3. Entrypoint: run `incus admin init --auto` (idempotent), then exec `incusd --group incus-admin` in foreground

### 5.2 Docker privileges

```yaml
privileged: true
cgroupns: host
```

Required because Incus manages cgroups, mount namespaces, and network devices for system containers.

### 5.3 Health check

```yaml
healthcheck:
  test: ["CMD", "incus", "info"]
  interval: 2s
  timeout: 5s
  retries: 15
  start_period: 10s
```

### 5.4 Image caching

The `incus-data` named volume persists the storage pool across local runs. First run pulls `images:alpine/3.19` (~30s); subsequent runs reuse the cache. In CI the volume is ephemeral — the cold-start cost is accepted.

## 6. navarisd Container

### 6.1 Image

`Dockerfile.navarisd` — multi-stage build:

- **Build stage**: `golang:1.26`, compiles `go build -tags incus -o /navarisd ./cmd/navarisd`
- **Runtime stage**: `debian:bookworm-slim`, copies binary

### 6.2 Configuration

```yaml
command:
  - --listen=:8080
  - --db-path=/tmp/navaris.db
  - --incus-socket=/run/incus/unix.socket
  - --auth-token=test-token
  - --log-level=debug
```

### 6.3 Health check

```yaml
healthcheck:
  test: ["CMD", "wget", "-q", "--spider", "http://localhost:8080/v1/health"]
  interval: 2s
  timeout: 5s
  retries: 15
```

## 7. Test Runner Container

### 7.1 Image

`Dockerfile.test` — multi-stage build producing two binaries:

- `navaris` CLI: `go build -o /navaris ./cmd/navaris`
- Integration test binary: `go test -tags integration -c -o /integration.test ./test/integration/`

Runtime image: `debian:bookworm-slim` with both binaries.

### 7.2 Environment

```yaml
environment:
  NAVARIS_API_URL: http://navarisd:8080
  NAVARIS_TOKEN: test-token
  NAVARIS_BASE_IMAGE: images:alpine/3.19
  NAVARIS_CLI: /usr/local/bin/navaris
```

### 7.3 Entrypoint

Runs the compiled test binary with verbose output:

```
/integration.test -test.v -test.timeout 10m
```

## 8. Test Suite

All test files live in `test/integration/`, behind the `integration` build tag.

### 8.1 Test files

| File | Coverage |
|------|----------|
| `e2e_test.go` | Full sandbox lifecycle: project -> sandbox -> exec -> stop -> snapshot -> clone -> destroy (existing, kept as-is) |
| `cli_test.go` | CLI binary via `os/exec`: project CRUD, sandbox create/list/destroy, snapshot, exec, JSON output parsing |
| `auth_test.go` | No token -> 401, wrong token -> 401, valid token -> success |
| `concurrent_test.go` | Parallel sandbox creation, concurrent operations, no races |
| `events_test.go` | WebSocket `/v1/events`: subscribe, create sandbox, verify lifecycle events arrive |
| `error_test.go` | 404 on missing resources, 400 on bad input, 409 on duplicate names |
| `helpers_test.go` | Shared utilities: client setup, CLI runner, cleanup helpers |

### 8.2 TestMain warm-up

`TestMain` pre-pulls the base container image before any tests run, so individual test timeouts are not consumed by the image download.

### 8.3 CLI testing approach

A `cliRunner` helper that:
1. Shells out to the `navaris` binary (path from `NAVARIS_CLI` env var)
2. Sets `--api-url`, `--token`, and `-o json` flags
3. Captures stdout, stderr, and exit code
4. Parses JSON output for assertions

### 8.4 Test isolation

Each test creates its own project with a unique timestamped name. No shared sandbox state between tests. Cleanup via `defer` in each test function.

## 9. CI Integration

### 9.1 GitHub Actions

`.github/workflows/integration.yml`:

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

GitHub Actions `ubuntu-latest` runners support privileged Docker containers natively.

### 9.2 Makefile targets

| Target | Description |
|--------|-------------|
| `make integration-test` | Full CI path: build, run, tear down. Exit code = test result. |
| `make integration-env` | Start incus + navarisd only (detached), publish port 8080 to host. |
| `make integration-env-down` | Tear down the dev environment. |
| `make integration-logs` | Tail logs from all integration containers. |

### 9.3 `make integration-test` implementation

```makefile
integration-test:
	docker compose -f docker-compose.integration.yml up \
		--build --abort-on-container-exit --exit-code-from test-runner
	docker compose -f docker-compose.integration.yml down -v
```

### 9.4 `make integration-env` implementation

```makefile
integration-env:
	docker compose -f docker-compose.integration.yml up -d --build incus navarisd
	@echo "Navaris API: http://localhost:8080"
	@echo "Token: test-token"
	@echo "Run tests: NAVARIS_API_URL=http://localhost:8080 NAVARIS_TOKEN=test-token go test -tags integration ./test/integration/ -v"
```

In dev mode, navarisd publishes port 8080 to the host so tests can run from the host machine.

## 10. Local Dev Workflow

1. `make integration-env` — starts Incus + navarisd in Docker
2. Run specific tests from host:
   ```
   NAVARIS_API_URL=http://localhost:8080 NAVARIS_TOKEN=test-token \
     go test -tags integration ./test/integration/ -v -run TestEndToEndLifecycle
   ```
3. Or use the CLI directly:
   ```
   go run ./cmd/navaris --api-url http://localhost:8080 --token test-token sandbox list -o json
   ```
4. `make integration-env-down` when done

## 11. Error Handling & Edge Cases

**Incus image pull latency**: First run pulls base container images (~30-60s). Mitigated by `TestMain` warm-up and the persistent `incus-data` volume for local runs.

**Privileged container requirement**: Incus-in-Docker requires `privileged: true`. This is supported on GitHub Actions `ubuntu-latest`. Environments that restrict privileged containers cannot run these tests — documented in the Makefile and README.

**Flaky container startup**: Health checks with generous retry counts (15 retries x 2s = 30s) absorb slow initialization. The `start_period` on the Incus container adds further buffer.

**Cleanup on failure**: `defer`-based cleanup in each test. Orphaned resources are confined to Docker — `docker compose down -v` is the ultimate cleanup.

**Test isolation**: Unique project names per test prevent cross-test interference. No global state shared between test functions.

## 12. Files to Create/Modify

| File | Action |
|------|--------|
| `Dockerfile.incus` | Create — Incus container image |
| `Dockerfile.navarisd` | Create — navarisd container image |
| `Dockerfile.test` | Create — test runner container image |
| `docker-compose.integration.yml` | Create — orchestration |
| `scripts/incus-entrypoint.sh` | Create — Incus container entrypoint |
| `Makefile` | Create — integration-test, integration-env targets |
| `.github/workflows/integration.yml` | Create — CI workflow |
| `test/integration/cli_test.go` | Create — CLI tests |
| `test/integration/auth_test.go` | Create — auth tests |
| `test/integration/concurrent_test.go` | Create — concurrency tests |
| `test/integration/events_test.go` | Create — WebSocket event tests |
| `test/integration/error_test.go` | Create — error path tests |
| `test/integration/helpers_test.go` | Create — shared test utilities |
| `test/integration/e2e_test.go` | Modify — add TestMain with image warm-up |
