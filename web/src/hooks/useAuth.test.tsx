import { describe, it, expect, beforeAll, afterEach, afterAll } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import type { ReactNode } from "react";
import { useAuth } from "./useAuth";
import { handlers } from "@/test/handlers";

const server = setupServer(...handlers);

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe("useAuth", () => {
  it("reports authenticated=false while loading then after 401", async () => {
    const { result } = renderHook(() => useAuth(), { wrapper: wrap() });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.authenticated).toBe(false);
  });

  it("reports authenticated=true when /ui/me says so", async () => {
    server.use(
      http.get("/ui/me", () =>
        HttpResponse.json({ authenticated: true, expiresAt: 1_800_000_000 }),
      ),
    );
    const { result } = renderHook(() => useAuth(), { wrapper: wrap() });
    await waitFor(() => expect(result.current.authenticated).toBe(true));
    expect(result.current.expiresAt).toBe(1_800_000_000);
  });
});
