import { Component, type ErrorInfo, type ReactNode } from "react";

// ErrorBoundary is the last-resort catch for rendering errors React can't
// recover from on its own. It wraps the entire app in App.tsx so a broken
// provider or catastrophic render error doesn't leave the user staring at a
// blank page. Route-level errors are handled separately by RouteError via
// React Router's errorElement prop — this boundary mostly catches failures
// outside the router tree (ThemeProvider, QueryClientProvider, the router
// itself) and acts as the final safety net.
//
// React error boundaries must be class components; there is no hook-based
// equivalent. We keep this one small and dependency-free rather than pulling
// in react-error-boundary.
interface ErrorBoundaryProps {
  children: ReactNode;
  // Optional custom fallback. If omitted, DefaultErrorFallback is used.
  fallback?: (error: Error, reset: () => void) => ReactNode;
}

interface ErrorBoundaryState {
  error: Error | null;
}

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // Log to the console so the error lands in dev tools and is available
    // to any browser-side error reporter we might wire up later.
    console.error("ErrorBoundary caught an error", error, info.componentStack);
  }

  reset = (): void => {
    this.setState({ error: null });
  };

  render(): ReactNode {
    const { error } = this.state;
    if (!error) return this.props.children;

    if (this.props.fallback) {
      return this.props.fallback(error, this.reset);
    }

    return <DefaultErrorFallback error={error} onReset={this.reset} />;
  }
}

function DefaultErrorFallback({ error, onReset }: { error: Error; onReset: () => void }) {
  return (
    <div
      role="alert"
      className="min-h-screen flex items-center justify-center bg-[var(--bg-base)] text-[var(--fg-primary)] p-6"
    >
      <div className="w-full max-w-lg border border-[var(--status-failed)] p-6">
        <div className="font-mono text-[10px] uppercase tracking-[0.1em] text-[var(--status-failed)]">
          application error
        </div>
        <h1 className="mt-2 text-xl font-medium tracking-[-0.01em]">
          Something went wrong
        </h1>
        <p className="mt-2 text-sm text-[var(--fg-secondary)]">
          The UI hit an unexpected error and can't continue rendering this screen.
        </p>

        <pre className="mt-4 font-mono text-[11px] border border-[var(--border-subtle)] p-3 bg-[var(--bg-overlay)] overflow-auto max-h-40 text-[var(--fg-primary)] whitespace-pre-wrap break-words">
          {error.message || String(error)}
        </pre>

        {import.meta.env.DEV && error.stack && (
          <details className="mt-3">
            <summary className="font-mono text-[10px] uppercase tracking-[0.1em] text-[var(--fg-muted)] cursor-pointer">
              stack trace
            </summary>
            <pre className="mt-2 font-mono text-[10px] border border-[var(--border-subtle)] p-3 bg-[var(--bg-overlay)] overflow-auto max-h-64 text-[var(--fg-muted)] whitespace-pre-wrap break-words">
              {error.stack}
            </pre>
          </details>
        )}

        <div className="mt-5 flex gap-2">
          <button
            type="button"
            onClick={onReset}
            className="font-mono text-xs border border-[var(--border-strong)] px-3 py-1.5 hover:bg-[var(--bg-overlay)]"
          >
            Try again
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
