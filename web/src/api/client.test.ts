import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { apiFetch, ApiError } from "./client";
import { handlers } from "@/test/handlers";

const server = setupServer(...handlers);

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

describe("apiFetch", () => {
  it("returns parsed JSON on success", async () => {
    server.use(
      http.get("/v1/projects", () =>
        HttpResponse.json({ projects: [{ id: "prj_1" }] }),
      ),
    );
    const data = await apiFetch<{ projects: { id: string }[] }>("/v1/projects");
    expect(data.projects[0].id).toBe("prj_1");
  });

  it("throws ApiError with code + message for structured errors", async () => {
    server.use(
      http.get("/v1/projects/bad", () =>
        HttpResponse.json(
          { code: "not_found", message: "project not found" },
          { status: 404 },
        ),
      ),
    );
    await expect(apiFetch("/v1/projects/bad")).rejects.toMatchObject({
      name: "ApiError",
      status: 404,
      code: "not_found",
      message: "project not found",
    });
  });

  it("uses fallback message for malformed error bodies", async () => {
    server.use(
      http.get("/v1/broken", () =>
        HttpResponse.text("gateway timeout", { status: 504 }),
      ),
    );
    await expect(apiFetch("/v1/broken")).rejects.toMatchObject({
      status: 504,
      code: "http_504",
    });
  });

  it("returns null for 204 no-content", async () => {
    server.use(
      http.delete("/v1/projects/prj_1", () => new HttpResponse(null, { status: 204 })),
    );
    const data = await apiFetch<null>("/v1/projects/prj_1", { method: "DELETE" });
    expect(data).toBeNull();
  });

  it("throws ApiError instance that extends Error", async () => {
    server.use(
      http.get("/v1/boom", () =>
        HttpResponse.json({ code: "unauthorized", message: "nope" }, { status: 401 }),
      ),
    );
    try {
      await apiFetch("/v1/boom");
      throw new Error("expected rejection");
    } catch (err) {
      expect(err).toBeInstanceOf(ApiError);
      expect(err).toBeInstanceOf(Error);
    }
  });
});
