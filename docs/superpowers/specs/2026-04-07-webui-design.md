# Navaris Web UI Design

## Overview

A web-based management console for Navaris, served by `navarisd` itself as a single-binary deployment. Provides a read + lifecycle surface for projects and sandboxes, a live event stream, and an in-browser terminal attach — all gated by a single shared password. Aimed at developers running Navaris locally or on a shared dev box who want a visual window into the control plane without writing REST calls by hand.

## Requirements

- Node 22+ in the build environment (for `npm ci && npm run build` during the Docker build)
- Go 1.26+ (already required)
- Docker Engine 25+ for the all-in-one image
- Modern browser supporting `fetch`, WebSocket, and ES2022 (Chrome 110+, Firefox 109+, Safari 16.4+)

## Goals

- Single binary deployment — the UI is embedded via `go:embed` into `navarisd`
- Zero additional services to run; works with the existing all-in-one Docker image
- One shared password controlled by `--ui-password` flag or `NAVARIS_UI_PASSWORD` env var
- Graceful opt-out: UI is disabled by default and only activated when a password is set
- Full management console for v1: projects + sandboxes read views, sandbox lifecycle actions (start/stop/destroy/create), live event stream panel, and terminal attach via xterm.js
- Two themes (dark and light), minimal color, status-only accents
- No handler duplication — the same `/v1` REST API serves both programmatic clients (Bearer token) and the browser (cookie)
- Does not break any existing integration tests, CLI workflows, or the all-in-one Docker image

## Non-Goals

- Multi-user authentication or user management (single shared password only)
- OAuth / OIDC / SAML / external identity providers
- Per-session revocation (stateless cookies — logout clears only the local cookie)
- TLS termination in `navarisd` itself (use a reverse proxy)
- End-to-end browser tests via Playwright or similar
- Management UI for snapshots, images, ports, projects, or operations in v1 (read-only display only)
- Hot-swap of the xterm theme without losing scrollback
- Terminal size propagation into Incus PTY (wire protocol supports it; provider layer remains a no-op — see Known Limitations)
- Dashboard / landing page with aggregated stats or visualizations
- Command palette (⌘K), keyboard shortcut system, compact density toggle, multi-terminal split view, file transfer through terminal, notification center

## Architecture

### High-level component map

```
web/                             ← React+Vite+TS source (new, top-level dir)
  package.json, vite.config.ts, tailwind.config.ts, tsconfig.json
  index.html
  src/
    main.tsx, App.tsx
    api/, hooks/, routes/, components/, lib/

internal/webui/                  ← new Go package
  embed.go                       ← go:embed web/dist behind `withui` build tag
  embed_noui.go                  ← stub for builds without `withui` tag
  assets.go                      ← http.Handler for index.html + /assets/*
  session.go                     ← cookie sign/verify, login/logout/me handlers
  session_test.go, assets_test.go

internal/api/
  middleware.go                  ← authMiddleware learns "cookie OR bearer"
  attach.go                      ← NEW: WebSocket terminal bridge
  server.go                      ← registers /v1/sandboxes/{id}/attach
  middleware_test.go, attach_test.go

cmd/navarisd/main.go             ← new flags: --ui-password, --ui-session-key,
                                   --ui-session-ttl; wires webui package

scripts/allinone-entrypoint.sh   ← propagates NAVARIS_UI_* env vars to CLI flags
docker-compose.yml               ← exposes NAVARIS_UI_PASSWORD, NAVARIS_UI_SESSION_KEY
Dockerfile                       ← new Node build stage; `-tags withui,...`
Makefile                         ← new targets: web-deps, web-dev, web-build, web-clean
```

### Request routing

`navarisd` listens on a single port (default `:8080`) and the mux composition is:

| Path pattern | Handler | Auth |
|---|---|---|
| `GET /v1/health` | existing | Bearer or cookie |
| `POST/GET/DELETE /v1/projects/*` | existing | Bearer or cookie |
| `POST/GET/DELETE /v1/sandboxes/*` | existing | Bearer or cookie |
| `POST/GET/DELETE /v1/snapshots/*` | existing | Bearer or cookie |
| `POST/GET/DELETE /v1/images/*` | existing | Bearer or cookie |
| `POST/GET /v1/sandboxes/{id}/sessions` etc. | existing | Bearer or cookie |
| `GET /v1/events` (WebSocket) | existing | Bearer or cookie |
| `POST /v1/sandboxes/{id}/exec` | existing | Bearer or cookie |
| `GET /v1/sandboxes/{id}/attach` (WebSocket) | **NEW** | Bearer or cookie |
| `POST /ui/login` | **NEW** | public (validates password) |
| `POST /ui/logout` | **NEW** | cookie required |
| `GET /ui/me` | **NEW** | public (returns authenticated: bool) |
| `GET /assets/*` | **NEW** embedded SPA assets | public |
| `GET /` (and any unknown path not under /v1, /ui, /assets) | **NEW** embedded index.html | public (SPA gates content itself) |

The "unknown path → index.html" fallback is the SPA deep-link rule required for React Router to handle client-side routing like `/sandboxes/abc/terminal`.

### Sub-mux composition

To keep `/ui/login` reachable without auth while `/v1/*` stays protected, `(*Server).Handler()` builds two sub-muxes:

```go
api := http.NewServeMux()                   // gets authMiddleware
// ... all existing /v1 routes registered here, plus:
api.HandleFunc("GET /v1/sandboxes/{id}/attach", s.attachSandbox)

root := http.NewServeMux()
root.Handle("/v1/", authMiddleware(token, sessionKey)(loggingMiddleware(s.log)(api)))
root.HandleFunc("POST /ui/login",  s.uiLogin)     // rate-limited, public
root.HandleFunc("POST /ui/logout", s.uiLogout)
root.HandleFunc("GET  /ui/me",     s.uiMe)
if webui.Assets != nil {
    root.Handle("/", webui.NewAssetHandler(webui.Assets))
}
return requestIDMiddleware(root)
```

The existing telemetry middleware (`newTracingMiddleware`, `newMetricsMiddleware`) wraps `root` at the outermost layer when telemetry is enabled, unchanged from today.

## Authentication and Session

### Configuration

Three new flags on `navarisd`, each also readable as an env var in the all-in-one entrypoint:

| Flag | Env var | Default | Notes |
|---|---|---|---|
| `--ui-password` | `NAVARIS_UI_PASSWORD` | *empty* | Empty disables the UI entirely. |
| `--ui-session-key` | `NAVARIS_UI_SESSION_KEY` | *auto-generated* | 32-byte hex. Empty triggers random generation + warning log. |
| `--ui-session-ttl` | `NAVARIS_UI_SESSION_TTL` | `24h` | Go `time.ParseDuration` format. |

The entrypoint in `scripts/allinone-entrypoint.sh` appends these to the `ARGS` array conditionally, matching the existing `NAVARIS_AUTH_TOKEN` pattern.

`docker-compose.yml` adds these to the `environment:` block alongside existing vars.

### Enabled/disabled rule

If `--ui-password` is empty at startup:
- `internal/webui` package is effectively a no-op (all its handlers return 404)
- `/`, `/assets/*`, `/ui/*` return 404
- A single info log line is emitted: `"web UI disabled (--ui-password not set)"`
- The `attachSandbox` handler is still registered (it's a `/v1` route, not a UI route) — CLI clients with Bearer tokens can still use it

### Session key

If `--ui-session-key` is empty at startup, `navarisd` generates a random 32-byte key and logs a warning:
`"ui-session-key not set; generated ephemeral key; sessions will not survive restart"`

Users who want persistent sessions set the key explicitly. The key is used as the HMAC-SHA256 secret for signing session cookies.

### Cookie structure

```
Name:      navaris_ui_session
Value:     base64url(issued_at_unix|expires_at_unix) + "." + base64url(hmac_sha256(value, key))
HttpOnly:  true
SameSite:  Lax
Secure:    true IF X-Forwarded-Proto=https (from a trusted reverse proxy)
Path:      /
Max-Age:   matches --ui-session-ttl (default 24h)
```

Cookie is **stateless** — no server-side session store. Server validates on every request by HMAC-ing the value portion with the key and constant-time-comparing signatures, then checking `expires_at` against wall clock. No refresh / sliding expiration in v1; when the cookie expires the user re-logs in.

### Login flow

1. Browser loads `/` → SPA boots → calls `GET /ui/me`.
2. If `{authenticated: false}`, SPA renders `/login` page.
3. User types password → `POST /ui/login {password: "..."}`.
4. Server compares with `subtle.ConstantTimeCompare`. On mismatch: 401 with an artificial ~200ms delay and the in-memory rate-limit bucket is decremented. On match: sets the signed cookie, returns `200 {authenticated: true}`.
5. SPA re-calls `GET /ui/me`, gets 200, navigates to the stored `next` param or `/sandboxes`.

### Logout

`POST /ui/logout` sets `Max-Age=0` on the cookie. Stateless, so there's no server-side "revoke" — if the cookie leaked before expiry it remains valid until expiry. Accepted trade-off for v1.

### Dual-auth middleware

The existing `authMiddleware` in `internal/api/middleware.go` is extended:

```
1. If an Authorization: Bearer <token> header is present:
     - If navarisd has no --auth-token configured: 401
     - Else compare token; mismatch → 401; match → allow
2. Else if a navaris_ui_session cookie is present:
     - Verify HMAC, check expiry
     - If valid and method is unsafe (POST/PUT/DELETE/PATCH):
         - Check that the Origin (or Referer fallback) matches the request Host
         - On mismatch: 401
     - Allow
3. Else if both --auth-token and --ui-session-key are empty:
     - Allow (test/dev convenience — preserves existing behavior)
4. Else:
     - 401
```

Bearer always wins over cookie when both are present; a programmatic client explicitly sending a Bearer token shouldn't have its request cookie-evaluated.

### CSRF protection

Cookie is `SameSite=Lax + HttpOnly`, which blocks cross-site form POST and blocks JavaScript access. As belt-and-suspenders for non-safe methods, the middleware also verifies `Origin` matches `Host` (falling back to `Referer` if Origin is absent). Bearer-token requests bypass this check. This is documented in the spec as "the chosen trade-off for a single-trust-domain dev tool" — we do not issue anti-CSRF tokens.

### WebSocket authentication

Browsers don't send `Authorization` headers on WebSocket handshakes, but cookies are forwarded automatically on same-origin `new WebSocket(...)` calls. The middleware logic above works unchanged for the WebSocket routes. The existing `/v1/events` also accepts a `?token=...` query parameter for CLI use; the browser uses the cookie path exclusively.

### Rate limiting

The `/ui/login` endpoint has an in-memory token-bucket limiter keyed by client IP (extracted from `X-Forwarded-For` first hop, fallback `RemoteAddr`). 5 attempts per minute per IP, 429 on overflow. This is in addition to the ~200ms constant-time delay on every login attempt. Successful logins do not consume bucket capacity.

## Terminal Attach

### Backend

A new WebSocket endpoint at `GET /v1/sandboxes/{id}/attach` in `internal/api/attach.go`:

1. Fetches the sandbox by ID. 404 if not found.
2. Rejects with 409 if `sbx.State != SandboxRunning`.
3. Accepts the WebSocket with `OriginPatterns: []string{r.Host}` (same-origin only).
4. Reads optional `?shell=` query parameter (default handled by provider).
5. Calls `s.cfg.Provider.AttachSession(ctx, ref, domain.SessionRequest{Shell: shell})`.
6. Runs a bridge goroutine pair:
   - **stdout → ws:** reads from `handle.Conn`, writes binary frames to WebSocket.
   - **ws → stdin / control:** reads WebSocket frames; binary frames are written verbatim to `handle.Conn`, text frames are parsed as JSON control messages.
7. Exits on first error from either goroutine. Defers `handle.Close()` and `conn.Close()`.

### Wire protocol

- **Client → server binary frame:** raw bytes to write to sandbox stdin.
- **Client → server text frame (JSON):** `{"type": "resize", "cols": N, "rows": M}`. Other message types are accepted silently but ignored.
- **Server → client binary frame:** raw bytes from sandbox stdout/stderr.
- **Server → client text frame:** not emitted in v1.

### Resize handling

Resize messages arrive cleanly and the bridge dispatches them to `handle.Resize(cols, rows)`. The existing provider implementations (`internal/provider/incus/exec.go:196` and `internal/provider/firecracker/exec.go`) are no-ops for resize today. This is accepted as a v1 limitation and tracked as future work — the wire protocol is forward-compatible.

### Frontend component

A new route `/sandboxes/:id/terminal` renders `SandboxTerminal.tsx`, a full-bleed layout variant (sidebar visible, main area has no padding and no scroll).

- `@xterm/xterm` + `@xterm/addon-fit` lazy-loaded via `React.lazy` so they don't inflate the dashboard bundle.
- On mount: instantiate `Terminal` with theme values read from CSS custom properties on `:root`; instantiate `FitAddon`; open terminal in container ref; call `fit.fit()`.
- Open a WebSocket to `${location.origin.replace(/^http/, 'ws')}/v1/sandboxes/${id}/attach`, set `binaryType = 'arraybuffer'`.
- On `ws.onopen`: emit initial resize control message.
- On `ws.onmessage`: write `new Uint8Array(e.data)` into the terminal.
- On `term.onData`: encode with `TextEncoder` and send as binary frame.
- On `term.onResize`: send `{type: 'resize', cols, rows}` as text frame.
- `ResizeObserver` on the container calls `fit.fit()` on size changes.
- Cleanup on unmount: dispose terminal, close WebSocket, disconnect observer.

### Terminal shell UI

A thin header bar above the xterm canvas shows:
- Sandbox name + short ID (`alpine-builder-07` · `sbx_01HW9K2ZR3PQ`)
- Connection status (`● connected` green, `⟳ connecting` amber, `× closed` red)
- A `Detach` button that navigates back to `/sandboxes/:id`

### Disconnect behavior

| Cause | Behavior |
|---|---|
| User closes tab | Browser closes WS → bridge exits. Shell inside sandbox: killed for direct backing; remains for tmux backing (no UI-level session tracking in v1). |
| Server-side crash / timeout | WS closes → component shows "✕ disconnected — reconnect?" button that re-opens the WS without reloading the page. |
| Sandbox stops while attached | Provider EOF → bridge ends → WS closes normally → component shows `[sandbox stopped]` inline with detach button still working. |
| Sandbox destroyed while attached | Same as "sandbox stops" — provider EOF → WS close → inline message. |

### Theme integration

`resolveXtermTheme()` reads current theme tokens from `document.documentElement` via `getComputedStyle` and maps them to xterm's theme object (background, foreground, cursor, selection, ANSI 16). On theme toggle, the component tears down and re-creates the terminal with the new theme — scrollback loss on toggle is accepted in v1.

## Frontend

### Project structure

```
web/
  package.json              — React 18, Vite 5, TS 5, Tailwind 4, shadcn/ui,
                              @tanstack/react-query, react-router-dom,
                              @xterm/xterm, @xterm/addon-fit, next-themes,
                              @fontsource-variable/mona-sans, @fontsource/commit-mono,
                              @fontsource/jetbrains-mono, sonner
  vite.config.ts            — dev proxy: /v1, /ui → http://localhost:8080
  tailwind.config.ts
  tsconfig.json
  index.html
  src/
    main.tsx                — router + QueryClientProvider + ThemeProvider
    App.tsx                 — shell layout (sidebar + outlet)
    api/
      client.ts             — fetch wrapper: credentials: 'include', error normalization
      sandboxes.ts          — typed wrappers for /v1/sandboxes/*
      projects.ts           — /v1/projects/*
      images.ts, snapshots.ts, ports.ts, sessions.ts   — read-only for v1
      events.ts             — WebSocket subscribe helper
      session.ts            — /ui/login, /ui/logout, /ui/me
    hooks/
      useAuth.ts            — wraps /ui/me query + login/logout mutations
      useSandboxes.ts       — list query, invalidated by event stream
      useProject.ts, useProjects.ts
      useEvents.ts          — opens /v1/events WS, exposes event stream callback
      useTheme.ts           — wraps next-themes for ergonomic consumer API
    routes/
      Login.tsx
      Projects.tsx
      ProjectDetail.tsx
      Sandboxes.tsx         — list + filters (project, state, backend)
      SandboxDetail.tsx     — tabs: Overview / Snapshots / Ports / Metadata
      SandboxTerminal.tsx   — full-bleed xterm container
      Events.tsx            — live tail of /v1/events ring buffer
      NotFound.tsx
    components/
      AppShell.tsx          — sidebar + outlet + status line
      StatusLine.tsx        — bottom modeline
      Sidebar.tsx
      RequireAuth.tsx       — auth gate
      SandboxTable.tsx
      SandboxStateBadge.tsx
      EventRow.tsx
      CreateSandboxDialog.tsx
      ConfirmDialog.tsx     — generic confirm dialog used for destroy etc.
      ui/                   — shadcn/ui generated components
    lib/
      format.ts             — time, bytes, duration formatters
      cn.ts                 — shadcn className helper
      xterm-theme.ts        — resolveXtermTheme() reading CSS vars
```

### Routing

React Router v7 data router:

```
/login                              → Login
/                                   → RequireAuth → redirect to /sandboxes
/projects                           → Projects list
/projects/:id                       → ProjectDetail
/sandboxes                          → Sandboxes list (default: all projects)
/sandboxes/:id                      → SandboxDetail
/sandboxes/:id/terminal             → SandboxTerminal (full-bleed variant)
/events                             → Events live tail
/*                                  → NotFound
```

`RequireAuth` wraps everything except `/login`. It calls `GET /ui/me` on mount; while loading it shows a centered spinner; on `{authenticated: false}` it redirects to `/login?next=<pathname+search>`.

### Data fetching

Every API call is a TanStack Query query or mutation. List queries are keyed by filter (e.g. `['sandboxes', {projectID, state, backend}]`). Mutations (`start`, `stop`, `destroy`, `create`, `login`, `logout`) use `useMutation` with `onSuccess` invalidating the relevant list. Mutation errors surface as toast notifications via `sonner`.

### Live updates via event stream

`useEvents()` opens `/v1/events` as a WebSocket on app mount (after auth passes). Incoming events dispatch to both:

1. React Query cache invalidation:
   - `sandbox.created`, `sandbox.state_changed`, `sandbox.destroyed` → invalidate `['sandboxes']`
   - `project.*` → invalidate `['projects']`
   - `snapshot.*` → invalidate `['snapshots', sandboxID]`
2. An in-memory ring buffer (max 1000 events) that drives the `/events` page.

If the WS drops, the hook reconnects with exponential backoff (1s → 2s → 4s → 8s → max 30s). On successful reconnect, it invalidates all queries to resync. The status line connection indicator reflects WS state.

### Auth error handling

`apiClient` normalizes responses into `{status, code, message}`. On 401, it fires a custom `navaris:unauthorized` event. `useAuth` listens for it, clears all React Query cache, and navigates to `/login?next=<current>`.

## Visual Design

### Aesthetic direction

Refined monochromatic minimalism — "instrument" voice. The UI reads like a professional control panel: dense when it needs to be, restrained where possible, with a clear typographic hierarchy doing the work that color normally does elsewhere.

Core principles:
1. Monochrome base. No brand color.
2. Color is reserved for status signal only.
3. Sharp corners (max `2px` radius), thin hairline dividers, confident negative space.
4. Typographic voice: a characterful humanist sans for UI, a precise monospace for data.
5. Button emphasis via contrast inversion (dark-on-light or light-on-dark), not hue.
6. Motion only as state feedback, respecting `prefers-reduced-motion`.

### Typography

Self-hosted via `@fontsource-variable/mona-sans` and `@fontsource/commit-mono` — no runtime Google Fonts fetch.

- **Display / UI:** Mona Sans (400, 500, 600)
- **Monospace:** Commit Mono (400, 500)

### Color tokens

**Dark theme (default):**
```
bg.base        #0b0b0c
bg.raised      #141416
bg.overlay     #1c1c1f
fg.primary     #f4f4f5
fg.secondary   #a1a1aa
fg.muted       #52525b
border.subtle  #1f1f22
border.strong  #2e2e33
```

**Light theme (warm, stone-based):**
```
bg.base        #fafaf9
bg.raised      #ffffff
bg.overlay     #f4f4f5
fg.primary     #18181b
fg.secondary   #52525b
fg.muted       #a1a1aa
border.subtle  #e4e4e7
border.strong  #d4d4d8
```

**Status colors (identical across themes, WCAG AA tuned):**
```
status.running    #22c55e    soft green
status.pending    #f59e0b    amber (animated pulse)
status.stopped    #71717a    neutral gray
status.failed     #ef4444    soft red
status.destroyed  #3f3f46    dim gray
```

Status color appears only as:
- A 6px filled dot prefix on state labels
- A 2px left border on running and failed table rows (nothing else)
- The connection dot in the status line

Buttons, links, headings, focus rings — all monochrome.

### Layout

Sidebar + main content shell with a fixed 26px status line at the bottom.

```
┌──────────┬─────────────────────────────────────┐
│  NAVARIS │  Sandboxes                          │
│ control  │  project: default · 12 · 3 running  │
│ plane    │                                     │
│          │  [filters]           [+ New]        │
│  Work    │                                     │
│  Projects│  Name/ID   Backend  CPU·Mem  State  │
│  Sandbox │  ─────────────────────────────────  │
│          │  ● fedora-test-01  incus  ...       │
│  Obs     │  ○ debian-build-02 fc     ...       │
│  Events  │                                     │
│  Ops     │                                     │
│          │                                     │
│ eran     │                                     │
│    ◐dark │                                     │
├──────────┴─────────────────────────────────────┤
│ NAVARIS ●connected · events 1247 · running 3/5 │
└────────────────────────────────────────────────┘
```

### Signature detail — the status line

A persistent 26px monospace strip at the bottom of every page:

```
NAVARIS  ●connected   events 1247   running 3/5   ::   incus + firecracker
```

- The `●` dot blinks once every 3s when the event stream WS is open, dims and stops blinking when disconnected
- Event counter updates as events flow through `useEvents()`
- Running/total counts pull from the sandboxes query cache
- Right side shows detected backends from `/v1/health`

### Theme switching

`next-themes` drives `data-theme="dark"` or `data-theme="light"` on `<html>`. User toggles via a button in the sidebar footer. Initial theme is `system` → respects `prefers-color-scheme`. Choice persists to `localStorage`. An inline pre-hydration script prevents flash of wrong theme.

### Density and motion

- Density: comfortable by default (40px row height on tables). No compact toggle in v1.
- Motion: `duration-150 ease-out` for color and background transitions only. No page-transition animations, no entrance effects. State feedback only — connection dot pulse, button press, dialog open.
- `prefers-reduced-motion: reduce` → all transitions set to `0ms`, the dot blink animation is disabled.

## Build and Deployment

### Go build tag

`internal/webui` ships two files:

```go
// embed.go
//go:build withui

package webui

import (
    "embed"
    "io/fs"
)

//go:embed all:dist
var embedded embed.FS
var Assets, _ = fs.Sub(embedded, "dist")
```

```go
// embed_noui.go
//go:build !withui

package webui

import "io/fs"
var Assets fs.FS = nil
```

The `main.go` file checks `webui.Assets != nil` to decide whether to register the asset handler and the `/ui/*` routes.

### Dockerfile changes

A new Node build stage is inserted between the existing `fc-artifacts` alias and the Go build stage:

```dockerfile
# ---- Stage 1: Build frontend ----
FROM node:22-bookworm-slim AS web-build
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build
```

The Go build stage gains:
```dockerfile
COPY --from=web-build /web/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -tags withui,firecracker,incus -o /navarisd ./cmd/navarisd
```

The `web/dist` output is copied into `internal/webui/dist` so the `//go:embed all:dist` directive picks it up.

Added image size: approximately 400–600 KB (fonts + xterm + SPA bundle gzipped).

### Makefile targets

```makefile
.PHONY: web-deps web-dev web-build web-clean

web-deps:
	cd web && npm ci

web-dev: web-deps
	cd web && npm run dev

web-build: web-deps
	cd web && npm run build

web-clean:
	rm -rf web/dist web/node_modules internal/webui/dist
```

`docker-build` already exists; it gains no direct Makefile dependency on `web-build` because the Docker build does its own Node stage. Developers building locally outside Docker run `make web-build` before `go build -tags withui`.

### Local development workflow

Two processes:
1. `make run` (existing) — runs `navarisd` without the `withui` build tag. Hitting `/` returns 404 because `webui.Assets == nil`.
2. `make web-dev` — runs Vite on `:5173` with a proxy to `navarisd:8080` for `/v1` and `/ui`. SPA cookies and WebSockets work because the proxy forwards them.

Developers open `http://localhost:5173` during dev. The production embedded UI is only tested during `make docker-build` and subsequent `docker compose up`.

## Observability

- Successful logins log at info level with client IP and request ID, never the password. They also emit a `ui.login` event on the existing `domain.EventBus`.
- Failed logins log at info level and emit a `ui.login_failed` event.
- Each terminal attach emits `ui.attach_opened` on open and `ui.attach_closed` on close (with sandbox ID and duration).
- Existing tracing and metrics middleware wrap the new routes unchanged.
- No new metric names added in v1.

## Security Notes

1. **Password storage.** Kept in memory as plaintext for the lifetime of the process. Compared with `subtle.ConstantTimeCompare`. Rotation = restart `navarisd`.
2. **Session key rotation.** Restart with a new `--ui-session-key` invalidates all existing sessions.
3. **HTTPS assumption.** `navarisd` does not terminate TLS. A reverse proxy (Caddy, nginx, Traefik) is expected. The `Secure` cookie flag is set when `X-Forwarded-Proto: https` is observed. If `--ui-password` is set and `navarisd` detects it's listening on a non-loopback interface with no `X-Forwarded-Proto` header seen in practice, it logs a warning at startup.
4. **Rate limiting.** Login only. IP-keyed token bucket, 5/minute. 429 on overflow.
5. **CSRF.** `SameSite=Lax` cookie + `Origin`/`Referer` host check on non-safe methods. No token-based CSRF. Single-trust-domain trade-off.
6. **Password comparison timing.** Login endpoint adds an unconditional ~200ms delay before responding, whether the password matched or not.

## Error Handling

### HTTP error taxonomy

| Status | When | Body |
|---|---|---|
| `401 Unauthorized` | Missing/invalid Bearer, missing/invalid cookie | `{"error":"unauthorized"}` |
| `403 Forbidden` | Cookie valid but Origin/Referer mismatch on unsafe method | `{"error":"forbidden"}` |
| `409 Conflict` | Attach on non-running sandbox; destroy on already-destroyed | `{"error":"conflict", "message":"..."}` |
| `429 Too Many Requests` | Login rate limit exceeded | `{"error":"rate_limited","retry_after":60}` |
| `502 Bad Gateway` | Attach bridge lost provider connection | `{"error":"bad_gateway"}` |

### Frontend error handling

- Every API call goes through `apiClient`; errors are normalized into `{status, code, message}`.
- React Query's `error` state drives `sonner` toasts for mutations and inline error cards for queries.
- Unhandled promise rejections and React error boundary both log to console and show a "something went wrong — reload?" full-page fallback.
- 401 is special-cased: `apiClient` dispatches `navaris:unauthorized`, `useAuth` clears query cache and navigates to `/login?next=<current>`.

### Edge cases explicitly handled

1. **Event stream disconnect** — exponential backoff reconnect; on reconnect, invalidate all queries.
2. **Sandbox destroyed while viewing detail** — detail query returns 404, UI shows "no longer exists" card + back button.
3. **Sandbox destroyed while terminal attached** — WS closes with `1000`, terminal shows `[sandbox stopped]` inline.
4. **Session cookie expired mid-use** — next API call returns 401, auth flow redirects to `/login`.
5. **UI disabled at runtime** — static assets and `/ui/*` return 404, CLI with Bearer still works.
6. **First-run ephemeral session key** — warning logged; sessions don't survive restart.
7. **Multiple tabs** — each tab has its own event stream WS; mutations from one tab update another via the shared event stream.

## Testing Strategy

### Backend (Go)

- `internal/webui/session_test.go`
  - Cookie sign → verify round-trip with matching key passes.
  - Tampered signature fails verification.
  - Expired cookie fails verification.
  - Wrong key fails verification.
- `internal/webui/assets_test.go`
  - `/` returns embedded index.html (with withui tag).
  - `/assets/<real-file>` returns 200 with correct Content-Type.
  - `/sandboxes/abc` (deep link) falls through to index.html.
  - Without withui tag, handler is a no-op (already implied by build tags).
- `internal/api/attach_test.go`
  - WebSocket handshake succeeds with valid cookie.
  - WebSocket handshake succeeds with valid Bearer.
  - 409 returned when sandbox is not running.
  - Mock provider drives the bridge: bytes written on client side appear on stdin pipe; bytes from stdout pipe appear in client WS messages.
  - Resize text message dispatches to provider `Resize` callback.
- `internal/api/middleware_test.go` additions
  - Cookie auth alone passes.
  - Bearer auth alone passes.
  - Bearer takes precedence over cookie.
  - Cookie + unsafe method + mismatched Origin → 401/403.
  - Cookie + unsafe method + matching Origin → allowed.
  - Cookie + safe method + mismatched Origin → allowed.
  - Both `--auth-token` and `--ui-session-key` empty → all requests pass (test-mode preservation).
- `cmd/navarisd/main_test.go` additions
  - New flags parse correctly.
  - `--ui-password` empty → `webui.Assets` unused, no `/ui/*` routes registered.

### Frontend (Vitest + React Testing Library + MSW)

- `api/client.test.ts` — fetch wrapper error normalization, 401 event dispatch.
- `hooks/useAuth.test.ts` — login → `/ui/me` → protected view flow using MSW.
- `components/SandboxTable.test.tsx` — renders rows, applies status-row-border class for running/failed, filter chips update query keys.
- `components/StatusLine.test.tsx` — connection state reflects in the dot class; counts update when query data changes.
- `routes/Login.test.tsx` — form submit, error display, redirect to `next` on success.
- `routes/SandboxTerminal.test.tsx` — mocks WebSocket, verifies xterm `write` called with incoming bytes, resize triggers text frame.

### Integration tests (existing Go harness)

New file `test/integration/webui_test.go` (tagged `integration`):

1. Start navarisd with `--ui-password=test --auth-token=test-token`.
2. `POST /ui/login {password: "test"}` → 200 + `Set-Cookie`.
3. `GET /v1/sandboxes?project_id=default` using the cookie → 200.
4. `POST /ui/logout` → 200.
5. Same `GET /v1/sandboxes` with the (now-deleted) cookie → 401.
6. `GET /v1/sandboxes` with `Authorization: Bearer test-token` → 200.
7. Open WebSocket to `/v1/sandboxes/{id}/attach` with the cookie (after re-login) and confirm the handshake completes against a running sandbox.

### Manual smoke test (documented in the plan)

After implementation: `make docker-build && NAVARIS_UI_PASSWORD=test docker compose --profile kvm up`. Open `http://localhost:8080`, log in, list sandboxes, create one, attach its terminal, watch the event stream update, toggle the theme, log out.

## Known Limitations

- Terminal resize does not propagate into Incus or Firecracker PTYs. The wire protocol carries the message but the provider layer at `internal/provider/incus/exec.go:196` and `internal/provider/firecracker/exec.go` both ignore it. To be tracked as a follow-up after v1 ships.
- Theme toggle destroys xterm scrollback history.
- Login rate limiting is in-memory per-process; it does not survive restart or distribute across replicas (single-process deployment only).
- Stateless cookies mean no per-session revocation — only global revocation via session key rotation.
- No Playwright / Cypress browser E2E.

## Future Work

**Management features not in v1:**
- Snapshot create / restore / delete UI
- Image promote / register / delete UI
- Port create / delete UI
- Project CRUD UI
- Operations list with cancel action

**UX:**
- Compact density toggle
- ⌘K command palette
- Multi-terminal split view
- Full E2E browser tests
- Dashboard landing page with aggregated stats
- Notification center / persistent event log UI
- File upload/download through terminal

**Auth/security:**
- Multi-user authentication
- External IdP (OAuth/OIDC/SAML)
- Per-session revocation
- TLS termination in navarisd itself
- Audit log persistence

**Terminal:**
- Resize propagation into Incus/Firecracker PTY
- Attach to existing named session (not just fresh creation)

## Configuration Summary

New user-facing configuration introduced by this spec:

| Flag | Env var | Default | Purpose |
|---|---|---|---|
| `--ui-password` | `NAVARIS_UI_PASSWORD` | *empty* | Enables the UI when set; shared password for login. |
| `--ui-session-key` | `NAVARIS_UI_SESSION_KEY` | *ephemeral* | HMAC key for signing session cookies. |
| `--ui-session-ttl` | `NAVARIS_UI_SESSION_TTL` | `24h` | Session cookie lifetime. |

All three are propagated through `scripts/allinone-entrypoint.sh` and `docker-compose.yml` following the existing `NAVARIS_AUTH_TOKEN` pattern.
