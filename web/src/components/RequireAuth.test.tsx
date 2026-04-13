import { describe, it, expect, beforeAll, afterEach, afterAll } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { RequireAuth } from "./RequireAuth";

const server = setupServer(
  http.get("/ui/me", () => HttpResponse.json({ authenticated: false }, { status: 401 })),
);

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderAt(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/login" element={<div>Login page next={new URLSearchParams(window.location.search).get("next")}</div>} />
          <Route
            path="/*"
            element={
              <RequireAuth>
                <div>Protected content</div>
              </RequireAuth>
            }
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("RequireAuth", () => {
  it("redirects to /login with next= when not authenticated", async () => {
    renderAt("/sandboxes");
    expect(await screen.findByText(/Login page/)).toBeInTheDocument();
  });

  it("renders children when authenticated", async () => {
    server.use(
      http.get("/ui/me", () =>
        HttpResponse.json({ authenticated: true, expiresAt: 1_800_000_000 }),
      ),
    );
    renderAt("/sandboxes");
    expect(await screen.findByText("Protected content")).toBeInTheDocument();
  });
});
