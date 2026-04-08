import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { listProjects } from "./projects";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

describe("listProjects", () => {
  it("unwraps the {data, pagination} envelope and returns the array", async () => {
    server.use(
      http.get("/v1/projects", () =>
        HttpResponse.json({
          data: [
            {
              ProjectID: "prj_1",
              Name: "default",
              CreatedAt: "2026-04-07T10:00:00Z",
              UpdatedAt: "2026-04-07T10:00:00Z",
              Metadata: null,
            },
          ],
          pagination: null,
        }),
      ),
    );
    const projects = await listProjects();
    expect(projects).toHaveLength(1);
    expect(projects[0].ProjectID).toBe("prj_1");
    expect(projects[0].Name).toBe("default");
  });
});
