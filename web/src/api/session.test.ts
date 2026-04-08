import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { login, logout, getMe } from "./session";
import { handlers } from "@/test/handlers";

const server = setupServer(...handlers);

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

describe("session API", () => {
  it("login posts password and returns Me on 200", async () => {
    const seen = vi.fn();
    server.use(
      http.post("/ui/login", async ({ request }) => {
        const body = (await request.json()) as { password: string };
        seen(body.password);
        return HttpResponse.json({ authenticated: true, expiresAt: 1_800_000_000 });
      }),
    );
    const me = await login("hunter2");
    expect(seen).toHaveBeenCalledWith("hunter2");
    expect(me.authenticated).toBe(true);
  });

  it("login rejects with ApiError on 401", async () => {
    server.use(
      http.post("/ui/login", () =>
        HttpResponse.json({ code: "unauthorized", message: "bad password" }, { status: 401 }),
      ),
    );
    await expect(login("wrong")).rejects.toMatchObject({
      code: "unauthorized",
      status: 401,
    });
  });

  it("getMe returns authenticated=true when server says so", async () => {
    server.use(
      http.get("/ui/me", () =>
        HttpResponse.json({ authenticated: true, expiresAt: 1_800_000_000 }),
      ),
    );
    const me = await getMe();
    expect(me).toEqual({ authenticated: true, expiresAt: 1_800_000_000 });
  });

  it("getMe returns authenticated=false on 401 without throwing", async () => {
    server.use(
      http.get("/ui/me", () =>
        HttpResponse.json({ authenticated: false }, { status: 401 }),
      ),
    );
    const me = await getMe();
    expect(me.authenticated).toBe(false);
  });

  it("logout posts and resolves on 200", async () => {
    const seen = vi.fn();
    server.use(
      http.post("/ui/logout", () => {
        seen();
        return HttpResponse.json({ ok: true });
      }),
    );
    await logout();
    expect(seen).toHaveBeenCalled();
  });
});
