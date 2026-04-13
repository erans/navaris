import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import {
  listSandboxes,
  getSandbox,
  startSandbox,
  stopSandbox,
  destroySandbox,
  createSandbox,
} from "./sandboxes";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const sample = {
  SandboxID: "sbx_1",
  ProjectID: "prj_1",
  Name: "fedora-test-01",
  State: "running" as const,
  Backend: "incus",
  BackendRef: "incus-instance-abc",
  HostID: "host_1",
  SourceImageID: "img_fedora42",
  ParentSnapshotID: "",
  CreatedAt: "2026-04-07T10:00:00Z",
  UpdatedAt: "2026-04-07T10:00:00Z",
  ExpiresAt: null,
  CPULimit: 2,
  MemoryLimitMB: 1024,
  NetworkMode: "isolated" as const,
  Metadata: null,
};

describe("sandboxes API", () => {
  it("listSandboxes scopes by project and unwraps the envelope", async () => {
    const seen = vi.fn();
    server.use(
      http.get("/v1/sandboxes", ({ request }) => {
        seen(new URL(request.url).searchParams.get("project_id"));
        return HttpResponse.json({ data: [sample], pagination: null });
      }),
    );
    const out = await listSandboxes("prj_1");
    expect(out).toHaveLength(1);
    expect(out[0].SandboxID).toBe("sbx_1");
    expect(out[0].Name).toBe("fedora-test-01");
    expect(seen).toHaveBeenCalledWith("prj_1");
  });

  it("getSandbox returns one sandbox by id", async () => {
    server.use(
      http.get("/v1/sandboxes/sbx_1", () => HttpResponse.json(sample)),
    );
    const out = await getSandbox("sbx_1");
    expect(out.SandboxID).toBe("sbx_1");
    expect(out.State).toBe("running");
  });

  it("startSandbox POSTs to /start", async () => {
    const seen = vi.fn();
    server.use(
      http.post("/v1/sandboxes/sbx_1/start", () => {
        seen();
        return HttpResponse.json(
          { OperationID: "op_1", Type: "start_sandbox", State: "pending" },
          { status: 202 },
        );
      }),
    );
    await startSandbox("sbx_1");
    expect(seen).toHaveBeenCalled();
  });

  it("stopSandbox POSTs to /stop", async () => {
    const seen = vi.fn();
    server.use(
      http.post("/v1/sandboxes/sbx_1/stop", () => {
        seen();
        return HttpResponse.json(
          { OperationID: "op_2", Type: "stop_sandbox", State: "pending" },
          { status: 202 },
        );
      }),
    );
    await stopSandbox("sbx_1");
    expect(seen).toHaveBeenCalled();
  });

  it("destroySandbox POSTs to /destroy (no DELETE route exists)", async () => {
    const seen = vi.fn();
    server.use(
      http.post("/v1/sandboxes/sbx_1/destroy", () => {
        seen();
        return HttpResponse.json(
          { OperationID: "op_3", Type: "destroy_sandbox", State: "pending" },
          { status: 202 },
        );
      }),
    );
    await destroySandbox("sbx_1");
    expect(seen).toHaveBeenCalled();
  });
});

describe("createSandbox", () => {
  it("POSTs to /v1/sandboxes with the request body and returns the Operation", async () => {
    let seenBody: unknown = null;
    server.use(
      http.post("/v1/sandboxes", async ({ request }) => {
        seenBody = await request.json();
        return HttpResponse.json(
          {
            OperationID: "op_1",
            ResourceType: "sandbox",
            ResourceID: "sbx_new",
            SandboxID: "sbx_new",
            SnapshotID: "",
            Type: "create_sandbox",
            State: "pending",
            StartedAt: "2026-04-08T12:00:00Z",
            FinishedAt: null,
            ErrorText: "",
            Metadata: null,
          },
          { status: 202 },
        );
      }),
    );

    const op = await createSandbox({
      project_id: "prj_1",
      name: "test-1",
      image_id: "alpine/3.21",
      cpu_limit: 2,
      memory_limit_mb: 512,
      network_mode: "isolated",
    });

    expect(op.ResourceID).toBe("sbx_new");
    expect(op.Type).toBe("create_sandbox");
    expect(seenBody).toEqual({
      project_id: "prj_1",
      name: "test-1",
      image_id: "alpine/3.21",
      cpu_limit: 2,
      memory_limit_mb: 512,
      network_mode: "isolated",
    });
  });

  it("omits cpu_limit and memory_limit_mb when not provided", async () => {
    let seenBody: Record<string, unknown> = {};
    server.use(
      http.post("/v1/sandboxes", async ({ request }) => {
        seenBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(
          {
            OperationID: "op_2",
            ResourceType: "sandbox",
            ResourceID: "sbx_2",
            SandboxID: "sbx_2",
            SnapshotID: "",
            Type: "create_sandbox",
            State: "pending",
            StartedAt: "2026-04-08T12:00:00Z",
            FinishedAt: null,
            ErrorText: "",
            Metadata: null,
          },
          { status: 202 },
        );
      }),
    );

    await createSandbox({
      project_id: "prj_1",
      name: "test-2",
      image_id: "debian-12",
      network_mode: "published",
    });

    expect(seenBody).not.toHaveProperty("cpu_limit");
    expect(seenBody).not.toHaveProperty("memory_limit_mb");
    expect(seenBody.image_id).toBe("debian-12");
    expect(seenBody.network_mode).toBe("published");
  });
});
