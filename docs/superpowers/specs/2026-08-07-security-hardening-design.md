# Security Hardening: Trusted Proxies, Rate-Limiter GC, Secrets Off argv

**Date:** 2026-08-07
**Branch:** `fix/security-hardening` (worktree `.worktrees/security-hardening`)
**Origin:** Adversarial review of uncommitted security-hardening WIP (plan `.agents/plans/2026-08-05-security-hardening.md` items 2a–2c), adjudicated by an independent reviewer.

## Problem

The WIP on this branch (a) does not compile — it references `config.trustedProxies`, `webui.Config.TrustedProxies`, and `(*webui.Handlers).GC()`, none of which exist; (b) even if it compiled, `--trusted-proxies` would be a placebo: `isTrustedProxy` has zero callers and `extractClientIP` trusts the first `X-Forwarded-For` hop unconditionally, so `/ui/login` brute-force throttling is bypassable by rotating XFF, and the webui rate-limiter's `buckets` map grows unboundedly under attacker-chosen (not even IP-shaped) keys; (c) secrets still leak via argv in the systemd launcher and for `--ui-password`/`--ui-session-key` in the all-in-one entrypoint; (d) navarisd's child processes (`ip`, `iptables`, `sysctl` in `internal/provider/firecracker/network`) inherit the daemon's full environment, including `NAVARIS_AUTH_TOKEN`.

## Design Decisions (approved)

1. **Fail-closed XFF, loudly.** X-Forwarded-For is honored only when `--trusted-proxies` is non-empty **and** the direct peer is in a trusted CIDR **and** the first XFF hop parses as a valid IP. Any other case falls back to the direct peer IP. At startup, a non-loopback listen address with an empty trusted list logs a one-time warning telling the operator rate limiting is shared/proxied-XFF ignored.
2. **In-place completion.** No new plumbing packages. Trust logic moves from `package main` to `internal/webui` (its only consumer). Web UI bucket GC mirrors the proven `api.RateLimiter.GC()` pattern with a 1h idle TTL, swept every 5 minutes (plan-mandated interval). Both GC loops bind to the daemon's signal context for clean shutdown.
3. **Secrets via env, consistently.** `NAVARIS_UI_PASSWORD`/`NAVARIS_UI_SESSION_KEY` env fallbacks mirror the AUTH_TOKEN pattern (flag wins; empty env = unset). All three env-read secrets are `strings.TrimSpace`'d. The all-in-one entrypoint exports instead of passing argv; the systemd launcher stops expanding the token into argv (the unit's EnvironmentFile already carries it).
4. **Targeted env scrubbing for children.** Helper in `internal/provider/firecracker/network` returns `os.Environ()` minus exactly `NAVARIS_AUTH_TOKEN`, `NAVARIS_UI_PASSWORD`, `NAVARIS_UI_SESSION_KEY`; applied at all `exec.Command` sites in that package. Not a blanket `NAVARIS_*` filter.

## Out of scope

X-Forwarded-Proto / `Secure`-cookie gating (plan 5a), README/flag-table doc refresh, `--gc-interval` unification, and the invalidated `net.ParseCIDR` host-masking nit.

## Testing

TDD per task: `TestClientIP*` gating matrix, `TestRateLimiterGCEvictsIdle`, `TestParseTrustedProxies`, env-fallback precedence/TrimSpace tests mirroring `TestParseFlagsUIExplicit`, env-scrub unit test, plus `bash -n` on edited scripts. Gate: `go build ./... && go vet ./... && go test ./...` green.

## Success criteria

Branch compiles; XFF spoofing cannot open fresh login buckets unless the peer is trusted; webui buckets are evicted after 1h idle; no secret appears in navarisd's argv in any shipped launcher; spaawned network helpers cannot see navaris secrets in their environment; all tests pass.
