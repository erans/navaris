import { useCallback } from "react";

// KEY is the single localStorage key used by the web UI to remember the
// project the user most recently created a sandbox in. Writing it lives
// here so there's exactly one place to grep if the name ever needs to
// change.
const KEY = "navaris.lastProjectId";

// useLastProject exposes read/write helpers for the "last-used project id"
// preference. Both helpers are stable-reference (useCallback with no deps)
// so consumers can put them in effect dependency arrays without causing
// re-runs. Every localStorage access is wrapped in try/catch so a browser
// with storage disabled (private mode, security settings, quota exceeded)
// silently falls through to "no preference" rather than crashing the app.
export function useLastProject() {
  const readLastProject = useCallback((): string | null => {
    try {
      return localStorage.getItem(KEY);
    } catch {
      return null;
    }
  }, []);

  const writeLastProject = useCallback((id: string): void => {
    try {
      localStorage.setItem(KEY, id);
    } catch {
      // Storage disabled or quota exceeded — no-op; the user will just
      // see the default project next time they open the dialog.
    }
  }, []);

  return { readLastProject, writeLastProject };
}
