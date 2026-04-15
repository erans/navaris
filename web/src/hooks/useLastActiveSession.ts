import { useCallback } from "react";

// Storage layout: one key per sandbox. Keeping last-focused tab
// scoped to the sandbox prevents Session 3 from sandbox A
// from bleeding into sandbox B when the user switches.
const keyFor = (sandboxId: string) => `navaris.terminal.${sandboxId}.activeSession`;

// useLastActiveSession exposes read/write/clear helpers for the
// "last-active terminal session id" preference, scoped per sandbox.
// All callbacks are stable-reference (useCallback with [sandboxId] dep)
// so consumers can put them in effect dependency arrays without causing
// re-runs. Every localStorage access is wrapped in try/catch so a browser
// with storage disabled (private mode, security settings, quota exceeded)
// silently falls through to "no preference" rather than crashing the app.
export function useLastActiveSession(sandboxId: string) {
  const read = useCallback((): string | null => {
    try {
      return localStorage.getItem(keyFor(sandboxId));
    } catch {
      return null;
    }
  }, [sandboxId]);

  const write = useCallback((sessionId: string): void => {
    try {
      localStorage.setItem(keyFor(sandboxId), sessionId);
    } catch {
      // Storage disabled / quota exceeded — silently no-op.
    }
  }, [sandboxId]);

  const clear = useCallback((): void => {
    try {
      localStorage.removeItem(keyFor(sandboxId));
    } catch {
      // Same reason as write().
    }
  }, [sandboxId]);

  return { read, write, clear };
}
