import { describe, it, expect, beforeAll, afterEach, afterAll } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import Sandboxes from "./Sandboxes";

// Sample data uses the real PascalCase Go wire shape. CreatedAt is an ISO
// string, not Unix seconds — the formatAgo helper in Sandboxes.tsx parses it
// with Date.parse. All numbers for CPULimit / MemoryLimitMB are plain ints.
const twoMinAgo = new Date(Date.now() - 2 * 60 * 1000).toISOString();
const twoHoursAgo = new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString();

const sandboxOne = {
  SandboxID: "sbx_1",
  ProjectID: "prj_1",
  Name: "fedora-test-01",
  State: "running" as const,
  Backend: "incus",
  BackendRef: "incus-instance-abc",
  HostID: "host_1",
  SourceImageID: "img_fedora42",
  ParentSnapshotID: "",
  CreatedAt: twoMinAgo,
  UpdatedAt: twoMinAgo,
  ExpiresAt: null,
  CPULimit: 2,
  MemoryLimitMB: 1024,
  NetworkMode: "isolated" as const,
  Metadata: null,
};

const sandboxTwo = {
  SandboxID: "sbx_2",
  ProjectID: "prj_1",
  Name: "alpine-stopped",
  State: "stopped" as const,
  Backend: "firecracker",
  BackendRef: "fc-vm-def",
  HostID: "host_1",
  SourceImageID: "img_alpine319",
  ParentSnapshotID: "",
  CreatedAt: twoHoursAgo,
  UpdatedAt: twoHoursAgo,
  ExpiresAt: null,
  CPULimit: 1,
  MemoryLimitMB: 256,
  NetworkMode: "isolated" as const,
  Metadata: null,
};

const sampleProject = {
  ProjectID: "prj_1",
  Name: "default",
  CreatedAt: "2026-01-01T00:00:00Z",
  UpdatedAt: "2026-01-01T00:00:00Z",
  Metadata: null,
};

const server = setupServer(
  http.get("/v1/projects", () =>
    HttpResponse.json({ data: [sampleProject], pagination: null }),
  ),
  http.get("/v1/sandboxes", ({ request }) => {
    const projectID = new URL(request.url).searchParams.get("project_id");
    if (projectID === "prj_1") {
      return HttpResponse.json({
        data: [sandboxOne, sandboxTwo],
        pagination: null,
      });
    }
    return HttpResponse.json({ data: [], pagination: null });
  }),
);
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/sandboxes"]}>
        <Routes>
          <Route path="/sandboxes" element={<Sandboxes />} />
          <Route path="/sandboxes/:id" element={<div>Detail</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("Sandboxes list", () => {
  it("renders sandbox rows fanned-out from listProjects + listSandboxes", async () => {
    renderPage();
    expect(await screen.findByText("fedora-test-01")).toBeInTheDocument();
    expect(screen.getByText("alpine-stopped")).toBeInTheDocument();
  });

  it("shows backend and cpu · mem in monospace cells", async () => {
    renderPage();
    await screen.findByText("fedora-test-01");
    expect(screen.getByText("incus")).toBeInTheDocument();
    expect(screen.getByText("2 · 1024")).toBeInTheDocument();
  });

  it("filters by state when a chip is clicked", async () => {
    renderPage();
    await screen.findByText("fedora-test-01");
    await userEvent.click(screen.getByRole("button", { name: /^running$/i }));
    expect(screen.getByText("fedora-test-01")).toBeInTheDocument();
    expect(screen.queryByText("alpine-stopped")).not.toBeInTheDocument();
  });

  it("filters by backend", async () => {
    renderPage();
    await screen.findByText("fedora-test-01");
    await userEvent.click(screen.getByRole("button", { name: /^firecracker$/i }));
    expect(screen.queryByText("fedora-test-01")).not.toBeInTheDocument();
    expect(screen.getByText("alpine-stopped")).toBeInTheDocument();
  });

  it("shows an empty state when there are no sandboxes in any project", async () => {
    server.use(
      http.get("/v1/sandboxes", () =>
        HttpResponse.json({ data: [], pagination: null }),
      ),
    );
    renderPage();
    await waitFor(() =>
      expect(screen.getByText(/no sandboxes/i)).toBeInTheDocument(),
    );
  });
});
