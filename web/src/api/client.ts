// ApiError is the single error type surfaced from apiFetch. Consumers can
// branch on `code` for recoverable states (e.g. "unauthorized") and fall back
// to `message` for display.
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

export interface ApiFetchOptions extends RequestInit {
  // json is a convenience: apiFetch stringifies and sets Content-Type.
  json?: unknown;
}

// apiFetch is the single entry point for talking to navarisd from the SPA.
// It always sends cookies (credentials: "include") so the signed session
// cookie flows on every call, and it normalizes error shapes into ApiError.
export async function apiFetch<T = unknown>(
  path: string,
  opts: ApiFetchOptions = {},
): Promise<T> {
  const { json, headers, ...rest } = opts;
  const init: RequestInit = {
    credentials: "include",
    ...rest,
    headers: {
      Accept: "application/json",
      ...(json !== undefined ? { "Content-Type": "application/json" } : {}),
      ...headers,
    },
  };
  if (json !== undefined) {
    init.body = JSON.stringify(json);
  }

  const res = await fetch(path, init);

  if (res.status === 204) {
    return null as T;
  }

  const contentType = res.headers.get("content-type") ?? "";
  const isJSON = contentType.includes("application/json");

  if (!res.ok) {
    if (isJSON) {
      const body = (await res.json().catch(() => null)) as
        | { code?: string; message?: string }
        | null;
      throw new ApiError(
        res.status,
        body?.code ?? `http_${res.status}`,
        body?.message ?? (res.statusText || "request failed"),
      );
    }
    throw new ApiError(res.status, `http_${res.status}`, res.statusText || "request failed");
  }

  if (!isJSON) {
    return null as T;
  }

  return (await res.json()) as T;
}
