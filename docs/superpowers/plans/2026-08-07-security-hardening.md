# Security Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the trusted-proxy/rate-limiter/secrets-off-argv security hardening actually work: restore compilation, enforce XFF gating fail-closed, add web UI bucket GC, and stop leaking secrets via argv and child-process environments.

**Architecture:** In-place completion. Trust logic lives in `internal/webui` (its only consumer); web UI bucket GC mirrors `api.RateLimiter.GC()`; env fallbacks mirror the existing `NAVARIS_AUTH_TOKEN` pattern; child-process env scrubbing is a helper in `internal/provider/firecracker/network`.

**Tech Stack:** Go 1.26 (stdlib `net`, `flag`, `os`), bash scripts, go test.

## Global Constraints

- Work in the worktree: `/home/eran/work/navaris/.worktrees/security-hardening`, branch `fix/security-hardening`.
- Spec: `docs/superpowers/specs/2026-08-07-security-hardening-design.md`.
- **Fail-closed XFF semantics:** XFF honored ONLY if `len(cfg.TrustedProxies) > 0` AND the direct peer IP is in a trusted CIDR AND the first XFF hop parses via `net.ParseIP`. Otherwise use the direct peer IP. Never use an XFF value that fails `net.ParseIP` — junk strings must not become rate-limit keys.
- **Env fallback pattern (exact):** flag wins; if flag is empty, use `strings.TrimSpace(os.Getenv(NAME))`. Secret env var names: `NAVARIS_AUTH_TOKEN` (existing), `NAVARIS_UI_PASSWORD`, `NAVARIS_UI_SESSION_KEY`.
- **GC sweep interval:** 5 minutes (plan-mandated). **Web UI bucket idle TTL:** 1 hour.
- **Scrubbed env vars (exactly these three):** `NAVARIS_AUTH_TOKEN`, `NAVARIS_UI_PASSWORD`, `NAVARIS_UI_SESSION_KEY`. No blanket `NAVARIS_*` filtering.
- After EVERY task: `go build ./... && go vet ./... && go test ./...` must pass from the worktree root.
- Out of scope (do NOT touch): X-Forwarded-Proto/`Secure` cookie logic in `handlers.go` Login; README; `--gc-interval`; the `parseTrustedProxies` behavior of masking host bits.

---

### Task 1: Trusted-proxy gating in `internal/webui`

**Files:**
- Modify: `internal/webui/handlers.go` (Config ~line 17, Login ~line 68, replace `extractClientIP` ~lines 166-183)
- Create: `internal/webui/proxy.go`
- Test: `internal/webui/proxy_test.go`

**Interfaces:**
- Produces (consumed by Task 3): `Config.TrustedProxies []*net.IPNet`; method `func (h *Handlers) clientIP(r *http.Request) string`; package-level `func isTrustedPeer(remoteAddr string, trusted []*net.IPNet) bool`.
- Consumes: nothing from other tasks.

- [ ] **Step 1: Write the failing tests** (`internal/webui/proxy_test.go`)

```go
package webui

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newReq(remoteAddr, xff string) *http.Request {
	r := httptest.NewRequest("POST", "/ui/login", nil)
	r.RemoteAddr = remoteAddr
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}
```

Write the full test file with the imports/helper above plus these tests:

```go
func mustCIDRs(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	var out []*net.IPNet
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func TestClientIPTurnsDownXFFWhenNoTrustedProxies(t *testing.T) {
	h := NewHandlers(Config{})
	r := newReq("192.0.2.1:1234", "203.0.113.9")
	if got := h.clientIP(r); got != "192.0.2.1" {
		t.Fatalf("clientIP = %q, want peer 192.0.2.1 (XFF must be ignored)", got)
	}
}

func TestClientIPUntrustedPeerCannotSpoof(t *testing.T) {
	h := NewHandlers(Config{TrustedProxies: mustCIDRs(t, "10.0.0.0/8")})
	r := newReq("192.0.2.1:1234", "203.0.113.9")
	if got := h.clientIP(r); got != "192.0.2.1" {
		t.Fatalf("clientIP = %q, want peer 192.0.2.1 for untrusted peer", got)
	}
}

func TestClientIPTrustedPeerHonorsFirstXFFHop(t *testing.T) {
	h := NewHandlers(Config{TrustedProxies: mustCIDRs(t, "10.0.0.0/8")})
	r := newReq("10.1.2.3:4321", "203.0.113.9, 10.9.9.9")
	if got := h.clientIP(r); got != "203.0.113.9" {
		t.Fatalf("clientIP = %q, want first XFF hop 203.0.113.9", got)
	}
}

func TestClientIPRejectsJunkXFF(t *testing.T) {
	h := NewHandlers(Config{TrustedProxies: mustCIDRs(t, "10.0.0.0/8")})
	r := newReq("10.1.2.3:4321", strings.Repeat("A", 500))
	if got := h.clientIP(r); got != "10.1.2.3" {
		t.Fatalf("clientIP = %q, want peer 10.1.2.3 for junk XFF", got)
	}
}

func TestIsTrustedPeer(t *testing.T) {
	trusted := mustCIDRs(t, "10.0.0.0/8", "192.168.1.5/32")
	cases := map[string]bool{"10.1.2.3": true, "192.168.1.5": true, "192.168.1.6": false, "bogus": false}
	for in, want := range cases {
		if got := isTrustedPeer(in, trusted); got != want {
			t.Errorf("isTrustedPeer(%q) = %v, want %v", in, got, want)
		}
	}
	if isTrustedPeer("10.1.2.3", nil) {
		t.Error("empty trusted list must trust nobody")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/eran/work/navaris/.worktrees/security-hardening && go test ./internal/webui/ -run 'TestClientIP|TestIsTrustedPeer' -v`
Expected: FAIL — `clientIP` / `isTrustedPeer` / `Config.TrustedProxies` undefined.

- [ ] **Step 3: Implement**

Create `internal/webui/proxy.go`:

```go
package webui

import (
	"net"
	"net/http"
	"strings"
)

// isTrustedPeer reports whether remoteAddr's host part is contained in any
// of the trusted CIDRs. An empty trusted list trusts nobody; an unparseable
// address is never trusted.
func isTrustedPeer(remoteAddr string, trusted []*net.IPNet) bool {
	if len(trusted) == 0 {
		return false
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	for _, cidr := range trusted {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP returns the caller's IP for rate-limit bucketing.
//
// X-Forwarded-For is honored only when the operator configured trusted
// proxies and the direct peer is one of them; then the first hop is used
// and only if it is a syntactically valid IP. In every other case the
// direct peer IP is used. This is fail-closed: with no trusted proxies
// configured, XFF is ignored entirely (untrusted clients cannot steer
// their own rate-limit bucket, and junk header values cannot become
// unbounded map keys).
func (h *Handlers) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if isTrustedPeer(r.RemoteAddr, h.cfg.TrustedProxies) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := xff
			if i := strings.IndexByte(xff, ','); i >= 0 {
				first = xff[:i]
			}
			if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
				return ip.String()
			}
		}
	}
	return host
}
```

In `internal/webui/handlers.go`:
- Add `TrustedProxies []*net.IPNet` to `Config` (keep `net` import — handlers.go already imports `net`).
- In `Login`, replace `extractClientIP(r)` with `h.clientIP(r)` (both call sites: consume and refund).
- Delete the old free function `extractClientIP` (lines ~166-183, including its stale doc comment).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/webui/ -v && go build ./internal/webui/`
Expected: all PASS (new proxy tests + all pre-existing handler/ratelimit/session tests).

- [ ] **Step 5: Commit**

```bash
git add internal/webui/proxy.go internal/webui/proxy_test.go internal/webui/handlers.go
git commit -m "feat(webui): gate X-Forwarded-For behind trusted proxies, fail-closed"
```

---

### Task 2: Web UI rate-limiter GC

**Files:**
- Modify: `internal/webui/ratelimit.go`
- Modify: `internal/webui/handlers.go` (add `GC` method)
- Test: `internal/webui/ratelimit_test.go`

**Interfaces:**
- Produces (consumed by Task 3): `func (h *Handlers) GC()` — evicts login buckets idle > 1h.
- Consumes: nothing.

- [ ] **Step 1: Write the failing test** (append to `internal/webui/ratelimit_test.go`; note the limiter's `now` field is swappable):

```go
func TestRateLimiterGCEvictsIdle(t *testing.T) {
	now := time.Now()
	rl := newRateLimiter(5, 5, time.Minute)
	rl.now = func() time.Time { return now }

	rl.consume("10.0.0.1")
	rl.consume("10.0.0.2")

	// Advance past the idle TTL for both buckets, then touch one.
	now = now.Add(2 * time.Hour)
	rl.consume("10.0.0.1")

	rl.gc(time.Hour)

	rl.mu.Lock()
	defer rl.mu.Unlock()
	if _, ok := rl.buckets["10.0.0.2"]; ok {
		t.Error("idle bucket 10.0.0.2 should have been evicted")
	}
	if _, ok := rl.buckets["10.0.0.1"]; !ok {
		t.Error("recently-active bucket 10.0.0.1 should have been kept")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/webui/ -run TestRateLimiterGCEvictsIdle -v`
Expected: FAIL — `rl.gc` undefined.

- [ ] **Step 3: Implement**

In `internal/webui/ratelimit.go`, **replace the stale doc comment** (the block claiming "buckets is never evicted… no background cleanup goroutine is warranted") with:

```go
// buckets are evicted by gc once idle past the TTL chosen by Handlers.GC;
// navarisd sweeps on a 5-minute ticker.
```

and add:

```go
// gc evicts buckets idle longer than idleTTL. Called periodically by the
// daemon (via Handlers.GC); not safe-made assumptions — takes the lock.
func (r *rateLimiter) gc(idleTTL time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := r.now().Add(-idleTTL)
	for k, b := range r.buckets {
		if b.last.Before(cutoff) {
			delete(r.buckets, k)
		}
	}
}
```

In `internal/webui/handlers.go` add:

```go
// GC evicts login rate-limit buckets idle for more than one hour.
// Safe to call periodically from the daemon's housekeeping loop.
func (h *Handlers) GC() { h.rl.gc(time.Hour) }
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/webui/ -v && go vet ./internal/webui/`
Expected: PASS, including the new GC test and the Task-1 proxy tests.

- [ ] **Step 5: Commit**

```bash
git add internal/webui/ratelimit.go internal/webui/ratelimit_test.go internal/webui/handlers.go
git commit -m "feat(webui): evict idle login rate-limit buckets via GC"
```

---

### Task 3: `cmd/navarisd` wiring — fields, GC loops, startup warning, env fallbacks, nits

**Files:**
- Modify: `cmd/navarisd/main.go` (config struct ~line 29; parseFlags env fallback ~line 122; run() GC block ~line 352; signal context ~line 501; webui.Config literal ~line 222; remove main's `isTrustedProxy` ~line 587; doc comment ~line 551; error message ~line 572)
- Test: `cmd/navarisd/main_test.go`

**Interfaces:**
- Consumes (from Task 1): `webui.Config.TrustedProxies`, `webui` no longer exports anything new. From Task 2: `(*webui.Handlers).GC()`.
- Produces: compiles cleanly; `parseTrustedProxies` (string → `[]*net.IPNet`) remains in package main.

- [ ] **Step 1: Write the failing tests** (append to `cmd/navarisd/main_test.go`; mirror the flag-swap pattern used by `TestParseFlagsUIDefaults` at line 109):

```go
func TestParseTrustedProxies(t *testing.T) {
	ok := []struct {
		in   string
		want int
	}{
		{"10.0.0.0/8", 1},
		{"1.2.3.4", 1},                 // plain IPv4 → /32
		{"::1", 1},                     // plain IPv6 → /128
		{"10.0.0.0/8, 1.2.3.4 , ::1", 3},
		{"", 0},
		{" , ,", 0},
	}
	for _, tc := range ok {
		got, err := parseTrustedProxies(tc.in)
		if err != nil {
			t.Errorf("parseTrustedProxies(%q) unexpected error: %v", tc.in, err)
		} else if len(got) != tc.want {
			t.Errorf("parseTrustedProxies(%q) returned %d nets, want %d", tc.in, len(got), tc.want)
		}
	}
	if _, err := parseTrustedProxies("not-an-ip"); err == nil {
		t.Error("parseTrustedProxies(\"not-an-ip\") should fail")
	}
}

func TestParseFlagsEnvFallbackTrimsAndPrefersFlag(t *testing.T) {
	origArgs := os.Args
	origFS := flag.CommandLine
	t.Cleanup(func() {
		os.Args = origArgs
		flag.CommandLine = origFS
	})
	t.Setenv("NAVARIS_UI_PASSWORD", "  padded-secret\n")
	t.Setenv("NAVARIS_UI_SESSION_KEY", "env-key")
	t.Setenv("NAVARIS_AUTH_TOKEN", "env-token")
	flag.CommandLine = flag.NewFlagSet("navarisd", flag.ContinueOnError)
	os.Args = []string{"navarisd", "--ui-session-key=flag-key"}

	cfg := parseFlags()
	if cfg.uiPassword != "padded-secret" {
		t.Errorf("uiPassword = %q, want trimmed env value %q", cfg.uiPassword, "padded-secret")
	}
	if cfg.uiSessionKey != "flag-key" {
		t.Errorf("uiSessionKey = %q, want flag to win over env", cfg.uiSessionKey)
	}
	if cfg.authToken != "env-token" {
		t.Errorf("authToken = %q, want env value", cfg.authToken)
	}
}
```

Also add to the same file:

```go
func TestIsLoopbackListen(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8080": true, "[::1]:8080": true, "localhost:8080": true,
		":8080": false, "0.0.0.0:8080": false, "192.168.1.10:8080": false,
	}
	for in, want := range cases {
		if got := isLoopbackListen(in); got != want {
			t.Errorf("isLoopbackListen(%q) = %v, want %v", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/navarisd/ -run 'TestParseTrustedProxies|TestParseFlagsEnvFallback|TestIsLoopbackListen' -v`
Expected: FAIL — build error (`cfg.trustedProxies` undefined etc.) from the pre-existing broken WIP state.

- [ ] **Step 3: Implement** (all edits in `cmd/navarisd/main.go`)

1. `config` struct: add field after `boostChannelDir string`:
   ```go
   	trustedProxies              []*net.IPNet
   ```
2. Env fallbacks in `parseFlags`, replace the existing auth-token block with:
   ```go
   	// Env fallbacks avoid passing secrets via argv (visible in
   	// /proc/<pid>/cmdline). Scripts should export these instead.
   	if cfg.authToken == "" {
   		cfg.authToken = strings.TrimSpace(os.Getenv("NAVARIS_AUTH_TOKEN"))
   	}
   	if cfg.uiPassword == "" {
   		cfg.uiPassword = strings.TrimSpace(os.Getenv("NAVARIS_UI_PASSWORD"))
   	}
   	if cfg.uiSessionKey == "" {
   		cfg.uiSessionKey = strings.TrimSpace(os.Getenv("NAVARIS_UI_SESSION_KEY"))
   	}
   ```
3. webui.Config literal (~line 222) already passes `TrustedProxies: cfg.trustedProxies` — now compiles thanks to Task 1. No change.
4. Fix the parse error message (~line 572): `"invalid CIDR or IP %q: %w"`.
5. Delete package-main `isTrustedProxy` (the dead copy, ~lines 587-605) — the live one now lives in `internal/webui`. Keep `parseTrustedProxies` in main.
6. Move the misplaced doc comment block (the 4 lines about `normalizeListen` converting listen addresses, ~lines 551-554) back above `func normalizeListen`.
7. Add `isLoopbackListen`:
   ```go
   // isLoopbackListen reports whether addr binds to a loopback interface.
   // Wildcard/empty hosts are NOT loopback. "localhost" counts.
   func isLoopbackListen(addr string) bool {
   	host, _, err := net.SplitHostPort(addr)
   	if err != nil {
   		return false
   	}
   	if host == "" {
   		return false
   	}
   	if host == "localhost" {
   		return true
   	}
   	ip := net.ParseIP(host)
   	return ip != nil && ip.IsLoopback()
   }
   ```
8. Startup warning in `run()`, right after logger creation (find `logger := setupLogger(...)`):
   ```go
   	if len(cfg.trustedProxies) == 0 && !isLoopbackListen(cfg.listen) {
   		logger.Warn("no --trusted-proxies configured on a non-loopback listen address; " +
   			"X-Forwarded-For will be ignored and login rate limiting applies per direct peer — " +
   			"if navarisd runs behind a reverse proxy, set --trusted-proxies")
   	}
   ```
9. Bind GC loops to the signal context. Hoist the context creation: move `ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)` + `defer stop()` from its current spot (~line 501) to BEFORE the GC goroutines (~line 352, right after `rateLim := api.NewRateLimiterDefault()`), and rewrite both goroutines:
   ```go
   	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
   	defer stop()

   	rateLim := api.NewRateLimiterDefault()
   	// Periodically evict idle rate-limiter buckets.
   	go func() {
   		ticker := time.NewTicker(5 * time.Minute)
   		defer ticker.Stop()
   		for {
   			select {
   			case <-ctx.Done():
   				return
   			case <-ticker.C:
   				rateLim.GC()
   			}
   		}
   	}()
   	if uiHandlers != nil {
   		go func() {
   			ticker := time.NewTicker(5 * time.Minute)
   			defer ticker.Stop()
   			for {
   				select {
   				case <-ctx.Done():
   					return
   				case <-ticker.C:
   					uiHandlers.GC()
   				}
   			}
   		}()
   	}
   ```
   Then delete the now-duplicate `ctx, stop := ...` / `defer stop()` at the old location so the later `select { case <-ctx.Done(): ... }` uses the hoisted ctx.

- [ ] **Step 4: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: everything PASSES — this task restores whole-repo compilation.

- [ ] **Step 5: Commit**

```bash
git add cmd/navarisd/main.go cmd/navarisd/main_test.go
git commit -m "fix(navarisd): wire trusted proxies, GC loops with shutdown ctx, env secret fallbacks"
```

---

### Task 4: Scripts — secrets off argv in entrypoint and systemd launcher

**Files:**
- Modify: `scripts/allinone-entrypoint.sh` (~lines 133-141)
- Modify: `packaging/systemd/navarisd-launch.sh` (line 31)
- Modify: `packaging/systemd/navarisd.env.example`

**Interfaces:** no Go symbols. Consumes Task 3's env fallbacks (`NAVARIS_UI_PASSWORD`, `NAVARIS_UI_SESSION_KEY` are now honored by navarisd).

- [ ] **Step 1: Edit `scripts/allinone-entrypoint.sh`.** Replace:
   ```bash
   if [ -n "${NAVARIS_UI_PASSWORD:-}" ]; then
       ARGS+=(--ui-password="$NAVARIS_UI_PASSWORD")
   fi
   if [ -n "${NAVARIS_UI_SESSION_KEY:-}" ]; then
       ARGS+=(--ui-session-key="$NAVARIS_UI_SESSION_KEY")
   fi
   ```
   with (mirroring the AUTH_TOKEN change already present above it):
   ```bash
   # Avoid passing secrets via argv (visible in /proc/<pid>/cmdline);
   # navarisd reads NAVARIS_UI_PASSWORD / NAVARIS_UI_SESSION_KEY from env when the flags are unset.
   [ -n "${NAVARIS_UI_PASSWORD:-}" ] && export NAVARIS_UI_PASSWORD
   [ -n "${NAVARIS_UI_SESSION_KEY:-}" ] && export NAVARIS_UI_SESSION_KEY
   ```

- [ ] **Step 2: Edit `packaging/systemd/navarisd-launch.sh`.** Delete the line:
   ```bash
   add_string_flag --auth-token "${NAVARIS_AUTH_TOKEN:-}"
   ```
   and add a comment where it was:
   ```bash
   # NAVARIS_AUTH_TOKEN is intentionally NOT passed as a flag: navarisd reads it
   # from the environment (EnvironmentFile) so it never appears in argv.
   ```

- [ ] **Step 3: Edit `packaging/systemd/navarisd.env.example`.** Extend the `# NAVARIS_AUTH_TOKEN=` comment block to state that secrets are delivered via env only (never CLI flags) — one comment line is enough.

- [ ] **Step 4: Validate**

```bash
bash -n scripts/allinone-entrypoint.sh && bash -n packaging/systemd/navarisd-launch.sh && echo "syntax OK"
grep -c 'auth-token\|ui-password=\|ui-session-key=' scripts/allinone-entrypoint.sh packaging/systemd/navarisd-launch.sh || true
```
Expected: syntax OK; the grep shows no remaining argv expansion of secrets (the comment mentioning flag names is fine; `--ui-session-ttl` is NOT a secret and stays on argv).

- [ ] **Step 5: Commit**

```bash
git add scripts/allinone-entrypoint.sh packaging/systemd/navarisd-launch.sh packaging/systemd/navarisd.env.example
git commit -m "fix(scripts): deliver UI secrets via env, keep tokens off argv in systemd launcher"
```

---

### Task 5: Scrub secrets from child-process environments

**Files:**
- Create: `internal/provider/firecracker/network/env.go`
- Modify: `internal/provider/firecracker/network/tap.go`, `internal/provider/firecracker/network/dnat.go` (all `exec.Command` sites)
- Test: `internal/provider/firecracker/network/env_test.go`

**Interfaces:** no exported symbols; `childEnv()` is package-private to `network`.

- [ ] **Step 1: Write the failing test** (`internal/provider/firecracker/network/env_test.go`):

```go
package network

import (
	"strings"
	"testing"
)

func TestChildEnvStripsSecrets(t *testing.T) {
	t.Setenv("NAVARIS_AUTH_TOKEN", "tok")
	t.Setenv("NAVARIS_UI_PASSWORD", "pw")
	t.Setenv("NAVARIS_UI_SESSION_KEY", "key")
	t.Setenv("NAVARIS_LOG_LEVEL", "debug") // non-secret NAVARIS_* must survive

	env := childEnv()
	joined := strings.Join(env, "\n")
	for _, name := range []string{"NAVARIS_AUTH_TOKEN", "NAVARIS_UI_PASSWORD", "NAVARIS_UI_SESSION_KEY"} {
		if strings.Contains(joined, name+"=") {
			t.Errorf("child env must not contain %s", name)
		}
	}
	if !strings.Contains(joined, "NAVARIS_LOG_LEVEL=debug") {
		t.Error("non-secret NAVARIS_LOG_LEVEL should be preserved")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/provider/firecracker/network/ -run TestChildEnvStripsSecrets -v`
Expected: FAIL — `childEnv` undefined.

- [ ] **Step 3: Implement** (`internal/provider/firecracker/network/env.go`):

```go
package network

import "os"

// secretEnvVars are stripped from child process environments. The daemon
// reads these from its own environment as an alternative to argv flags;
// short-lived helpers (ip, iptables, sysctl) have no need for them, and
// same-UID processes can read /proc/<pid>/environ.
var secretEnvVars = []string{
	"NAVARIS_AUTH_TOKEN",
	"NAVARIS_UI_PASSWORD",
	"NAVARIS_UI_SESSION_KEY",
}

// childEnv returns the current environment minus navaris secrets. It is a
// targeted strip, NOT a blanket NAVARIS_* filter: non-secret config may
// legitimately flow to children.
func childEnv() []string {
	env := os.Environ()
	for _, name := range secretEnvVars {
		prefix := name + "="
		filtered := env[:0]
		for _, e := range env {
			if !strings.HasPrefix(e, prefix) {
				filtered = append(filtered, e)
			}
		}
		env = filtered
	}
	return env
}
```

(add `"strings"` to imports — write it correctly in the file.) Then in `tap.go` and `dnat.go`, set `Env` on every command. Pattern for each site:

```go
cmd := exec.Command(args[0], args[1:]...)
cmd.Env = childEnv()
out, err := cmd.CombinedOutput()
```

Apply to: `tap.go:28` (two variants there), `tap.go:36,63,72,80,94`, `dnat.go:24,48`. Restructure each one-liner into the three-line form.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/provider/firecracker/... && go vet ./internal/provider/firecracker/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/network/env.go internal/provider/firecracker/network/env_test.go internal/provider/firecracker/network/tap.go internal/provider/firecracker/network/dnat.go
git commit -m "fix(firecracker): strip navaris secrets from child process environments"
```

---

## Self-review notes

- Spec coverage: §1→Task 1/3, §2→Task 1/3, §3→Task 2/3, §4→Task 3/4, §5→Task 5, §6 nits→Task 3/4. ✓
- Type consistency: `clientIP`, `isTrustedPeer`, `gc(idleTTL)`, `GC()`, `parseTrustedProxies`, `isLoopbackListen`, `childEnv` names consistent across tasks. ✓
- Task 3 is the compile-restore point; Tasks 1-2 build independently. ✓
