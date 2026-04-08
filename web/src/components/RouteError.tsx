import { useNavigate, useRouteError } from "react-router-dom";

// RouteError is the errorElement React Router renders when a lazy import
// fails, a loader throws, or rendering a descendant panics. It's designed
// to slot into the AppShell main column so the sidebar stays navigable and
// the user can reach another route without a full reload.
//
// This complements ErrorBoundary: route errors land here, everything
// outside the router tree falls through to ErrorBoundary at the App root.
export function RouteError() {
  const error = useRouteError();
  const navigate = useNavigate();
  const message = errorMessage(error);

  return (
    <div role="alert">
      <div className="border border-[var(--status-failed)] p-5 max-w-xl">
        <div className="font-mono text-[10px] uppercase tracking-[0.1em] text-[var(--status-failed)]">
          page error
        </div>
        <h1 className="mt-2 text-lg font-medium tracking-[-0.01em]">
          This screen failed to load
        </h1>
        <p className="mt-2 text-sm text-[var(--fg-secondary)]">
          The rest of the app is still working — use the sidebar or try one of
          the actions below.
        </p>

        <pre className="mt-4 font-mono text-[11px] border border-[var(--border-subtle)] p-3 bg-[var(--bg-overlay)] overflow-auto max-h-40 text-[var(--fg-primary)] whitespace-pre-wrap break-words">
          {message}
        </pre>

        <div className="mt-5 flex gap-2">
          <button
            type="button"
            onClick={() => navigate("/sandboxes")}
            className="font-mono text-xs border border-[var(--border-strong)] px-3 py-1.5 hover:bg-[var(--bg-overlay)]"
          >
            Back to sandboxes
          </button>
          <button
            type="button"
            onClick={() => window.location.reload()}
            className="font-mono text-xs border border-[var(--border-strong)] px-3 py-1.5 hover:bg-[var(--bg-overlay)]"
          >
            Reload page
          </button>
        </div>
      </div>
    </div>
  );
}

// React Router can throw arbitrary values (Response, strings, unknown) from
// loaders, so normalise to a human-readable string before display.
function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message || "unknown error";
  if (typeof error === "string") return error;
  if (error && typeof error === "object") {
    if ("statusText" in error && typeof (error as { statusText: unknown }).statusText === "string") {
      return (error as { statusText: string }).statusText;
    }
    if ("message" in error && typeof (error as { message: unknown }).message === "string") {
      return (error as { message: string }).message;
    }
  }
  return "unknown error";
}
