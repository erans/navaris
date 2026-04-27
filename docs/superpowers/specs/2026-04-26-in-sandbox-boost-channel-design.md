# In-Sandbox Boost Channel Design

**Status:** Draft
**Date:** 2026-04-26
**Scope:** Add a per-sandbox HTTP-over-Unix-socket channel inside running sandboxes that lets guest code request a boost (or read the sandbox's own state) without going through the operator-facing daemon API. The channel is implicitly authenticated — each connection is bound to a single sandbox at the kernel level, and all requests on that channel act as that sandbox. This is **spec #3 of three** (spec #1 shipped runtime resize as PR #13; spec #2 shipped time-bounded boost as PR #14; this spec lets the guest itself ask for a boost).

## 1. Goals

Boost is most useful when the workload itself knows it needs more resources — e.g., a CI runner about to compile, an ML script about to load a model, a build job about to fan out N parallel workers. With spec #2, only the operator (someone holding a daemon API token) can issue a boost. Guest code has to phone home to whatever orchestration sits above navarisd to ask for one. That's awkward and fragile.

This spec adds a daemon-managed channel that exposes the same boost primitives directly inside each sandbox. Guest code hits a Unix socket at `/var/run/navaris-guest.sock` with HTTP, and the request lands at `BoostService.Start`/`Cancel`/`Get` — exactly the same code path as the operator API.

## 2. Non-Goals

- **Wider self-API.** The channel exposes only the boost endpoints plus `GET /sandbox` for self-introspection. PATCH /resources, snapshot, exec, fork, port-publish, and other powerful operations are not exposed in v1. Future specs can layer them if there's demand.
- **Guest-to-guest communication.** Each sandbox's channel sees only its own state.
- **Tokens, multi-tenancy, RBAC inside the channel.** Channel identity == sandbox identity, period. Operators who need stricter policy can layer it externally (project quotas, per-project channel-disable, network-level sidecars).
- **Persistent rate-limit state.** Rate limit is in-memory; resets on daemon restart. Boost is chunky enough that this isn't an attack vector for v1.
- **Cross-restart channel survival.** The channel listener has to be re-opened by `Recover` after a daemon restart; documented in §3.5 and §4.5.
- **Spec #2 changes.** The boost service contract from spec #2 is unchanged. This spec only adds a new entry path that calls into it.

## 3. Architecture

### 3.1 Two backends, one handler

```
GUEST                                                  HOST
─────                                                  ────

Firecracker:
  curl --unix-socket /var/run/navaris-guest.sock ...
       │
       ▼
  navaris-agent (extended)
   - HTTP server on /var/run/navaris-guest.sock
   - on each inbound request: open AF_VSOCK
     conn to CID=2 port=1025; pipe bytes
       │
       ▼ (vsock)                              <vmDir>/vsock_1025
                                                            │
                                                            ▼
                                                       BoostHTTPHandler
                                                         (per-VM listener;
                                                          sandbox_id is the
                                                          listener's binding)


Incus:
  curl --unix-socket /var/run/navaris-guest.sock ...
       │ (host kernel — bind-mounted via incus device add)
       ▼
  /var/lib/navaris/boost-channels/<sandbox-id>.sock
                                                            │
                                                            ▼
                                                       BoostHTTPHandler
                                                         (per-sandbox listener)
```

Two backend-specific listeners, one shared HTTP handler. Each backend's natural mechanism is used: Firecracker uses vsock + a guest-side proxy in `navaris-agent`; Incus uses an Incus-managed bind-mount of a host UDS into the container. Both produce the same property: each accepted connection is unambiguously the boost channel for one specific sandbox.

### 3.2 New components

- `internal/api/boost_channel.go` — `BoostHTTPHandler` plus the `SandboxResolver` indirection. Backend-agnostic.
- `internal/provider/firecracker/vsock_listener.go` — per-VM `vsock_1025` UDS listener; reads HTTP from the conn and dispatches to `BoostHTTPHandler.Serve(conn, sandboxID)`.
- `internal/provider/incus/boost_socket.go` — per-sandbox `<sandbox-id>.sock` listener; same dispatch shape; manages `incus config device add/remove`.
- `cmd/navaris-agent/agent/boost_proxy.go` — guest-side HTTP-on-Unix-socket → vsock proxy (Firecracker only). A dumb byte-pipe; doesn't parse HTTP.
- `internal/api/ratelimit.go` — per-sandbox token bucket, in-memory.
- New SQLite migration `004_boost_channel.sql` adds `enable_boost_channel` to `sandboxes`.
- New daemon flag `--boost-channel-enabled` (default `true`).
- New daemon flag `--boost-channel-dir` (default `/var/lib/navaris/boost-channels`) — Incus only.

### 3.3 HTTP API

All routes are at the root of the in-sandbox socket — there's no `/v1` prefix, and there's no `{sandbox_id}` in the path because the channel resolves it implicitly.

```
GET    /sandbox    → 200 with the requesting sandbox's state + current limits + active_boost (mirrors the external getSandbox shape)
POST   /boost      → 200 with the boost record on success; mirrors the external POST /v1/sandboxes/{id}/boost
GET    /boost      → 200 with the active boost; 404 if none
DELETE /boost      → 204 on success; 404 if no active boost
```

Request and response payloads are byte-identical to the external API. Examples:

```jsonc
// POST /boost (in-sandbox)
{
  "cpu_limit": 4,
  "memory_limit_mb": 4096,
  "duration_seconds": 300
}

// 200 OK response
{
  "boost_id": "bst_...",
  "sandbox_id": "...",
  "original_cpu_limit": 2,
  "original_memory_limit_mb": 1024,
  "boosted_cpu_limit": 4,
  "boosted_memory_limit_mb": 4096,
  "started_at": "2026-04-26T12:00:00Z",
  "expires_at": "2026-04-26T12:05:00Z",
  "state": "active"
}
```

Error mapping is the same as the external API's `mapErrorCode`:

| Status | Trigger |
|---|---|
| 400 | malformed JSON, both limit fields omitted, `duration_seconds <= 0` |
| 404 | DELETE/GET when no active boost |
| 409 | sandbox not running (defense-in-depth — channel exists only for running sandboxes, but mid-lifecycle race possible) |
| 422 | bounds violation, `duration_seconds > --boost-max-duration` |
| 429 | per-sandbox token bucket exhausted; includes `Retry-After: <seconds>` |
| 500 | provider error during boost apply |

### 3.4 Daemon-side handler

`internal/api/boost_channel.go`:

```go
type BoostHTTPHandler struct {
    boosts    *service.BoostService
    sandboxes domain.SandboxStore
    limiter   *RateLimiter
}

// Serve handles one HTTP request on a connection bound to sandboxID. Closes
// the conn after the response is written. The transport (vsock or Incus
// shared UDS) is the caller's responsibility.
func (h *BoostHTTPHandler) Serve(ctx context.Context, conn net.Conn, sandboxID string) {
    defer conn.Close()
    // 1. Rate limit check (per-sandbox bucket)
    // 2. Read HTTP request from conn
    // 3. Route on Method+Path:
    //      POST   /boost    → boosts.Start(sandboxID, ...)
    //      GET    /boost    → boosts.Get(sandboxID)
    //      DELETE /boost    → boosts.Cancel(sandboxID)
    //      GET    /sandbox  → sandboxes.Get(sandboxID) + active boost
    // 4. Write HTTP response back to conn
}
```

The handler doesn't use Go's `net/http` server because each connection is one-shot per request — no Keep-Alive negotiation, no per-conn goroutine pool needed. A small handcoded request reader / response writer is simpler than wrestling `http.Serve` onto a one-conn-at-a-time listener. Total handler ≈ 150 LOC.

### 3.5 Firecracker topology

#### Direction

The existing FC vsock protocol on port 1024 has the host issuing commands to the guest agent. The new channel inverts the role: **guest connects, host accepts**, on a separate port (1025).

#### Listener lifecycle

- `firecracker.Provider.CreateSandbox` (when `EnableBoostChannel` is true): create `<vmDir>/vsock_1025` UDS, register a `*BoostListener` that accepts conns and dispatches to `BoostHTTPHandler.Serve(conn, vmID)`.
- `firecracker.Provider.DestroySandbox`: close the listener; unlink the UDS file. Existing VM-directory cleanup catches anything missed.
- `firecracker.Provider.recover()` (daemon restart): for each recovered VM where `EnableBoostChannel` is true and the VM is alive, recreate the listener.

The mapping vsock-port-1025 → UDS-file is automatic per Firecracker's vsock device contract: guest connects to CID=2, port=1025, host sees an inbound conn on `<vmDir>/vsock_1025`.

#### Guest agent extension

`cmd/navaris-agent/agent/boost_proxy.go` adds:

```go
// runBoostProxy serves HTTP on /var/run/navaris-guest.sock. Each request
// opens a fresh AF_VSOCK conn to (CID=2, port=1025) and pipes bytes
// through. The proxy never parses HTTP — both sides are the host.
func runBoostProxy(ctx context.Context, listenPath string) error {
    listener, err := net.Listen("unix", listenPath)
    if err != nil { return err }
    for {
        conn, err := listener.Accept()
        if err != nil { ... }
        go pipeToVsock(ctx, conn)
    }
}

func pipeToVsock(ctx context.Context, in net.Conn) {
    defer in.Close()
    out, err := vsock.Dial(2, 1025)
    if err != nil { ... write 502 ...; return }
    defer out.Close()
    // Bidirectional copy: in→out, out→in
    go io.Copy(out, in)
    io.Copy(in, out)
}
```

The guest agent owns `/var/run/navaris-guest.sock` and sets it to mode `0666` so any process inside the sandbox can connect.

### 3.6 Incus topology

#### Mechanism

Each sandbox gets a dedicated host UDS at `<--boost-channel-dir>/<sandbox-id>.sock` and an Incus device bind-mounting it into the container at `/var/run/navaris-guest.sock`:

```
incus config device add <container> navaris-boost \
  unix-socket \
  source=/var/lib/navaris/boost-channels/<sandbox-id>.sock \
  path=/var/run/navaris-guest.sock
```

The host kernel handles the bind. No agent or proxy runs inside the container.

#### Listener lifecycle

- `incus.IncusProvider.CreateSandbox` (when `EnableBoostChannel` is true): create the host UDS, register a `*BoostListener` (same shape as Firecracker), call `incus config device add`.
- `incus.IncusProvider.DestroySandbox`: `incus config device remove` (best-effort; container destruction releases the bind regardless), close the listener, unlink the UDS.
- Daemon restart: `incus.IncusProvider` already iterates known sandboxes during init. For each `running`/`stopped` sandbox with `EnableBoostChannel`, recreate the host UDS + listener. The Incus device binding survives container restart on its own; we just need our listener back.

#### Why per-sandbox UDS (not shared with peer-credential routing)

- **Routing simplicity.** A shared UDS would force `SO_PEERPID` → cgroup parent → container ID lookups on every connection. That's a Linux-specific dance with edge cases (PID namespaces, cgroup v1 vs v2). Per-sandbox UDS sidesteps it entirely — the listener IS the routing.
- **Backend symmetry.** Firecracker uses per-VM listeners for the same reason. Both backends end up plumbing the same `BoostHTTPHandler` into a per-sandbox `net.Listener`, and the handler doesn't care which backend it came from.

#### Why `/var/lib/navaris/...` and not under the existing chroot/Incus dirs

The boost-channel dir is host-side state owned by navarisd, not by Incus. Keeping it under a navaris-owned path keeps the directory layout backend-agnostic — Firecracker stores per-VM state under `/srv/firecracker`, but the Incus boost-channel dir lives separately under `/var/lib/navaris/boost-channels/`.

### 3.7 Sandbox identity resolution

`SandboxResolver` is the single-method interface that maps an inbound connection to a sandbox ID:

```go
type SandboxResolver interface {
    SandboxFor(conn net.Conn) string
}
```

In v1, neither backend actually inspects the conn — both return the bound-at-listener-creation sandbox ID:

```go
type fixedResolver struct{ sandboxID string }
func (f fixedResolver) SandboxFor(conn net.Conn) string { return f.sandboxID }
```

Per-sandbox listeners + this trivial resolver = no protocol parsing, no peer-credential games, no shared state. The interface is in place so a future spec could swap in a shared-listener resolver if requirements change — but spec #3 doesn't go there.

### 3.8 Rate limiting

Per-sandbox token bucket inside `BoostHTTPHandler`:

```go
type RateLimiter struct {
    mu      sync.Mutex
    buckets map[string]*bucket   // keyed by sandbox_id
    nowFn   func() time.Time
}

type bucket struct {
    tokens   float64
    lastFill time.Time
}
```

Refill rate: 1 token/second. Burst capacity: 10. Both configurable later if needed; flagged for now as constants.

Behavior:
- On request: take 1 token. If unavailable, return `429` with `Retry-After: <ceil(seconds_until_next_token)>`.
- Idle buckets older than 1 hour are dropped by a background sweeper running once per minute. Stops the map from growing unbounded.

In-memory only; no SQLite persistence. Daemon restart resets all buckets — acceptable for v1, since the boost itself has hard caps (`--boost-max-duration` + per-sandbox replace semantics) that bound any abuse.

### 3.9 Opt-in / opt-out

Two-layer policy:

1. **Daemon flag:** `--boost-channel-enabled` (`bool`, default `true`). When `false`, no listener is ever created, regardless of per-sandbox value.
2. **Per-sandbox override:** new field on `domain.CreateSandboxRequest` and `service.CreateSandboxOpts`:

```go
EnableBoostChannel *bool   // nil = inherit daemon flag value; non-nil = explicit override
```

The service layer materializes the pointer at create time (resolves nil to the daemon flag value) and persists the resolved value on `domain.Sandbox`:

```go
EnableBoostChannel bool   // resolved at create time
```

At provider-level CreateSandbox, both backends read this field; when `false`, they skip the listener+socket setup. Inside the sandbox, `/var/run/navaris-guest.sock` simply doesn't exist.

#### SQLite migration `004_boost_channel.sql`

```sql
ALTER TABLE sandboxes ADD COLUMN enable_boost_channel INTEGER NOT NULL DEFAULT 1;
```

Default `1` (true) matches the daemon-on-by-default model. Existing sandbox rows after migration have the channel enabled in the database, but listeners aren't created until the next sandbox start (running sandboxes don't get a retroactive channel until they're recreated; we don't try to live-add it).

### 3.10 Events

Reuse the spec #2 event types (`EventBoostStarted`, `EventBoostExpired`, `EventBoostRevertFailed`). Add one field to all three payloads:

```jsonc
{
  "boost_id": "...",
  "sandbox_id": "...",
  // ...existing fields...
  "source": "external" | "in_sandbox"   // NEW
}
```

`source: "external"` is set by the existing `/v1/sandboxes/{id}/boost` handler. `source: "in_sandbox"` is set by `BoostHTTPHandler`. Web UI and external observers can distinguish operator-initiated from guest-initiated boosts.

`BoostService.Start` gains an optional `Source string` field on `StartBoostOpts`:

```go
type StartBoostOpts struct {
    SandboxID       string
    CPULimit        *int
    MemoryLimitMB   *int
    DurationSeconds int
    Source          string   // NEW; "external" | "in_sandbox"; defaults to "external" if empty
}
```

### 3.11 CLI / SDK

Out of scope for the in-sandbox channel. The user-facing surface inside the sandbox is the HTTP-over-Unix-socket protocol; users hit it with `curl` or any HTTP client. We could add an `examples/` snippet showing typical usage in shell, Python, and Node, but no compiled binary or package gets shipped.

Operators continue to use the external `/v1/sandboxes/{id}/boost` endpoint, the existing `navaris sandbox boost {start,show,cancel}` CLI, and the web UI.

### 3.12 Web UI

Two changes to the existing Boost subsection (added in spec #2):

1. The "Boost active" view shows `source` ("started by user" vs. "started from inside sandbox") next to the expiry countdown.
2. The "New sandbox" dialog gains a checkbox: "Allow in-sandbox boost requests" (default checked, reflecting the daemon flag default). Mapped to the `EnableBoostChannel` field on the create request.

## 4. Testing

### 4.1 Unit tests

- `internal/api/boost_channel_test.go`:
    - happy path: POST/GET/DELETE /boost, GET /sandbox; correct status codes; correct response shapes.
    - 400/422/429 paths.
    - rate-limit deterministic test using a fake clock: 11 rapid requests → 10 succeed (with replace semantics), 11th gets 429 with `Retry-After`.
    - 429 → wait → success after token refill.
- `internal/api/ratelimit_test.go`:
    - bucket fills at 1 token/sec, caps at 10.
    - idle buckets get GC'd after 1 hour.
- `internal/provider/firecracker/boost_listener_test.go`:
    - listener opens at CreateSandbox, closes at DestroySandbox (mocked accept loop).
    - `EnableBoostChannel: false` → no listener, no UDS file.
- `internal/provider/incus/boost_socket_test.go`:
    - `incus config device add` is invoked exactly once with the right args (mock the Incus client interface — see spec #2 task 10's note about no in-tree Incus mocks; the mock pattern there is to use a small wrapper interface; do the same here).
    - Cleanup on Destroy.

### 4.2 Integration tests (`test/integration/boost_channel_test.go`, `//go:build integration`)

**Firecracker only.** Per spec #2's lessons, Incus integration tests that set `limits.memory` on create are flaky in the docker-in-docker CI environment; we apply the same skip on Incus images. Service-layer unit tests cover the Incus channel path adequately for v1.

Tests:

1. `TestBoostChannel_FC_StartFromInside` — create FC sandbox, exec `curl -X POST --unix-socket /var/run/navaris-guest.sock http://_/boost -d ...`, verify 200 from inside, verify external `GET /v1/sandboxes/{id}/boost` shows the boost active with `source: "in_sandbox"`.
2. `TestBoostChannel_FC_RateLimit` — rapid-fire 12 boost POSTs from inside, expect 10 successes (the spec #2 replace semantic means each succeeds at the API level even if the prior boost is overwritten) and 2 with 429.
3. `TestBoostChannel_FC_OptOut` — create with `enable_boost_channel: false`, exec `test -e /var/run/navaris-guest.sock`, expect failure (file does not exist).
4. `TestBoostChannel_FC_GetSandbox` — `curl http://_/sandbox` returns the sandbox's own state including current limits.

## 5. Open Questions

None at the time of writing. Two earlier candidates resolved during brainstorming:

- **`GET /sandbox` self-introspection** — included. It's a 5-line addition that makes the API self-sufficient (guest code can read its own current limits before deciding whether/how much to boost).
- **Guest-initiated PATCH /resources** — explicitly out of scope. Future spec if demand emerges.

## 6. Migration / Compatibility

- New SQLite migration `004_boost_channel.sql`. Defaults existing sandbox rows to `enable_boost_channel=1`. Listeners aren't retroactively added to currently-running sandboxes — they get added on the next sandbox start (so a stop/start cycle picks up the host-side listener after the daemon upgrade).
- New daemon flags `--boost-channel-enabled` (default `true`) and `--boost-channel-dir` (default `/var/lib/navaris/boost-channels`). Defaults preserve backward-compat for fresh installs that don't set the flag.
- API additions are pure-additive: new endpoints (in-sandbox only — the external API is unchanged), new field on create request (`enable_boost_channel`), new field on event payloads (`source`). No external client breakage.
- `service.StartBoostOpts` gains a new optional `Source string` field. Existing callers (the external POST handler in `internal/api/boost.go`) need to pass `Source: "external"`. The empty-string default materializes to `"external"` in `BoostService.Start`, so any missed call site is correct by default.
- **Existing Firecracker rootfs images don't have the new `navaris-agent` proxy.** A sandbox started from a pre-upgrade rootfs will have the host-side `vsock_1025` listener (since `enable_boost_channel=1` by default), but nothing inside the guest serves `/var/run/navaris-guest.sock` — so guest code that tries to use the channel hits "file not found" on the socket. The host listener idles harmlessly. Operators who want the channel must rebuild rootfs images with the updated agent. There's no agent-version mismatch failure mode that affects sandbox start or any existing functionality.
- **Existing Incus rootfs images** are unaffected — Incus uses a host-mounted UDS, no agent involved. The new channel works for any Incus sandbox started after the daemon upgrade, regardless of the rootfs image age.

## 7. Out-of-scope notes for future specs

- **Project-scoped channel disable.** Some operators may want to disable the in-sandbox channel for whole projects (e.g. untrusted workloads). Doable as a `Project.EnableBoostChannel` field that overrides the daemon flag; defer until requested.
- **Guest snapshot / fork / exec of self.** Same self-API surface, more capabilities. The implicit-auth model handles them naturally; the security review is non-trivial. Separate spec when needed.
- **Sandbox-to-daemon webhook.** Reverse direction (guest pushes events to host). Useful for autoscaling on guest-side metrics. Out of scope here.
