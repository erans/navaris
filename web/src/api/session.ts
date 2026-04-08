import { apiFetch, ApiError } from "./client";

export interface Me {
  authenticated: boolean;
  expiresAt?: number;
}

// login exchanges a password for a signed session cookie set by navarisd.
// The server responds with 200 and a Me-shaped body on success (matching
// the Task 6 handler); errors surface as ApiError instances.
export async function login(password: string): Promise<Me> {
  return apiFetch<Me>("/ui/login", {
    method: "POST",
    json: { password },
  });
}

// logout clears the session cookie server-side. Always resolves cleanly —
// even if the cookie was already gone (401).
export async function logout(): Promise<void> {
  try {
    await apiFetch<unknown>("/ui/logout", { method: "POST" });
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) return;
    throw err;
  }
}

// getMe asks whether the current cookie is valid. A 401 response is not an
// error in the usual sense — it's the answer "no, you're not logged in" —
// so we translate it into { authenticated: false } for easier consumption.
export async function getMe(): Promise<Me> {
  try {
    return await apiFetch<Me>("/ui/me");
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      return { authenticated: false };
    }
    throw err;
  }
}
