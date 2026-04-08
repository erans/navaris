import { describe, it, expect, beforeAll, afterEach, afterAll, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import Login from "./Login";
import { handlers } from "@/test/handlers";

const server = setupServer(...handlers);
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderLogin(initialEntries = ["/login"]) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={initialEntries}>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/" element={<div>Home</div>} />
          <Route path="/sandboxes" element={<div>Sandboxes</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("Login route", () => {
  it("renders a password field and submit button", () => {
    renderLogin();
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /sign in/i })).toBeInTheDocument();
  });

  it("calls /ui/login on submit and navigates to next param on success", async () => {
    const seen = vi.fn();
    server.use(
      http.post("/ui/login", async ({ request }) => {
        const body = (await request.json()) as { password: string };
        seen(body.password);
        return HttpResponse.json({ authenticated: true, expiresAt: 1_800_000_000 });
      }),
      http.get("/ui/me", () =>
        HttpResponse.json({ authenticated: true, expiresAt: 1_800_000_000 }),
      ),
    );
    renderLogin(["/login?next=/sandboxes"]);
    await userEvent.type(screen.getByLabelText(/password/i), "hunter2");
    await userEvent.click(screen.getByRole("button", { name: /sign in/i }));
    expect(await screen.findByText("Sandboxes")).toBeInTheDocument();
    expect(seen).toHaveBeenCalledWith("hunter2");
  });

  it("shows an error message on bad password", async () => {
    server.use(
      http.post("/ui/login", () =>
        HttpResponse.json({ code: "unauthorized", message: "bad password" }, { status: 401 }),
      ),
    );
    renderLogin();
    await userEvent.type(screen.getByLabelText(/password/i), "nope");
    await userEvent.click(screen.getByRole("button", { name: /sign in/i }));
    expect(await screen.findByText(/bad password/i)).toBeInTheDocument();
  });

  it("shows a rate-limit message on too_many_requests", async () => {
    server.use(
      http.post("/ui/login", () =>
        HttpResponse.json(
          { code: "too_many_requests", message: "slow down" },
          { status: 429 },
        ),
      ),
    );
    renderLogin();
    await userEvent.type(screen.getByLabelText(/password/i), "nope");
    await userEvent.click(screen.getByRole("button", { name: /sign in/i }));
    expect(await screen.findByText(/too many attempts/i)).toBeInTheDocument();
  });
});
