import { describe, it, expect, beforeAll, afterEach, afterAll, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import SandboxDetail from "./SandboxDetail";

// Sample uses the real PascalCase Go wire shape. No startedAt, no reason,
// no image field — those were all fabrications in the original plan.
// SourceImageID is an opaque ID like "img_fedora42".
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
  UpdatedAt: "2026-04-07T10:02:00Z",
  ExpiresAt: null,
  CPULimit: 2,
  MemoryLimitMB: 1024,
  NetworkMode: "isolated" as const,
  Metadata: null,
};

const server = setupServer(
  http.get("/v1/sandboxes/sbx_1", () => HttpResponse.json(sample)),
);

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/sandboxes/sbx_1"]}>
        <Routes>
          <Route path="/sandboxes/:id" element={<SandboxDetail />} />
          <Route path="/sandboxes" element={<div>list</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("SandboxDetail", () => {
  it("shows core sandbox fields", async () => {
    renderPage();
    expect(await screen.findByText("fedora-test-01")).toBeInTheDocument();
    expect(screen.getByText("sbx_1")).toBeInTheDocument();
    expect(screen.getByText("img_fedora42")).toBeInTheDocument();
  });

  it("calls /stop when Stop button is clicked on a running sandbox", async () => {
    const stopSeen = vi.fn();
    server.use(
      http.post("/v1/sandboxes/sbx_1/stop", () => {
        stopSeen();
        return HttpResponse.json(
          { OperationID: "op_stop", Type: "stop_sandbox", State: "pending" },
          { status: 202 },
        );
      }),
    );
    renderPage();
    await screen.findByText("fedora-test-01");
    await userEvent.click(screen.getByRole("button", { name: /^stop$/i }));
    await waitFor(() => expect(stopSeen).toHaveBeenCalled());
  });

  it("shows inline confirmation before destroy on a stopped sandbox", async () => {
    const destroySeen = vi.fn();
    server.use(
      http.get("/v1/sandboxes/sbx_1", () =>
        HttpResponse.json({ ...sample, State: "stopped" }),
      ),
      http.post("/v1/sandboxes/sbx_1/destroy", () => {
        destroySeen();
        return HttpResponse.json(
          { OperationID: "op_destroy", Type: "destroy_sandbox", State: "pending" },
          { status: 202 },
        );
      }),
    );
    renderPage();
    await screen.findByText("fedora-test-01");
    await userEvent.click(screen.getByRole("button", { name: /^delete$/i }));
    await userEvent.click(screen.getByRole("button", { name: /confirm delete/i }));
    await waitFor(() => expect(destroySeen).toHaveBeenCalled());
  });

  it("disables the Terminal link when sandbox is not running", async () => {
    server.use(
      http.get("/v1/sandboxes/sbx_1", () =>
        HttpResponse.json({ ...sample, State: "stopped" }),
      ),
    );
    renderPage();
    await screen.findByText("fedora-test-01");
    const link = screen.getByRole("link", { name: /terminal/i });
    expect(link).toHaveAttribute("aria-disabled", "true");
  });
});
