import { useState, type FormEvent } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { ApiError } from "@/api/client";
import { useAuth } from "@/hooks/useAuth";

// Login is the only unauthenticated page in the app. It renders a single
// password field, submits to /ui/login, and bounces to ?next= (defaulting
// to /) on success. We surface API error codes with human-friendly copy.
export default function Login() {
  const { login } = useAuth();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const next = params.get("next") ?? "/";
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setPending(true);
    try {
      await login(password);
      navigate(next, { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        setError(messageForCode(err.code, err.message));
      } else {
        setError("Unable to sign in. Try again.");
      }
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-[var(--bg-base)] px-4">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-sm border border-[var(--border-strong)] bg-[var(--bg-raised)] p-8"
        aria-labelledby="login-title"
      >
        <div className="mb-8">
          <h1
            id="login-title"
            className="font-display text-[15px] font-semibold tracking-[0.16em] text-[var(--fg-primary)]"
          >
            NAVARIS
          </h1>
          <p className="mt-1 font-mono text-[9px] tracking-[0.08em] text-[var(--fg-muted)]">
            control plane
          </p>
        </div>

        <label
          htmlFor="password"
          className="mb-2 block font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]"
        >
          Password
        </label>
        <input
          id="password"
          name="password"
          type="password"
          autoFocus
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.currentTarget.value)}
          className="mb-4 w-full border border-[var(--border-subtle)] bg-transparent px-3 py-2 text-sm text-[var(--fg-primary)] outline-none focus:border-[var(--fg-primary)]"
          aria-invalid={error ? "true" : undefined}
          aria-describedby={error ? "login-error" : undefined}
        />

        {error && (
          <p
            id="login-error"
            role="alert"
            className="mb-4 text-xs text-[var(--status-failed)]"
          >
            {error}
          </p>
        )}

        <button
          type="submit"
          disabled={pending || password.length === 0}
          className="w-full border border-[var(--invert-bg)] bg-[var(--invert-bg)] px-4 py-2 text-xs font-medium tracking-[0.02em] text-[var(--fg-on-invert)] transition-opacity disabled:opacity-50"
        >
          {pending ? "Signing in…" : "Sign in"}
        </button>
      </form>
    </div>
  );
}

// messageForCode maps server error codes to user-facing copy. Unknown codes
// fall through to whatever the server said — always prefer server copy when
// we don't have a better version.
function messageForCode(code: string, fallback: string): string {
  switch (code) {
    case "unauthorized":
      return "Bad password.";
    case "too_many_requests":
      return "Too many attempts. Wait a moment and try again.";
    case "forbidden":
      return "The UI is not enabled on this server.";
    default:
      return fallback || "Unable to sign in. Try again.";
  }
}
