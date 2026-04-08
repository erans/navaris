import { describe, it, expect, beforeAll, afterEach, afterAll, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useParams } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import NewSandboxDialog from "./NewSandboxDialog";

const sampleProject = {
  ProjectID: "prj_1",
  Name: "default",
  CreatedAt: "2026-01-01T00:00:00Z",
  UpdatedAt: "2026-01-01T00:00:00Z",
  Metadata: null,
};

const secondProject = {
  ProjectID: "prj_2",
  Name: "other",
  CreatedAt: "2026-01-01T00:00:00Z",
  UpdatedAt: "2026-01-01T00:00:00Z",
  Metadata: null,
};

const server = setupServer(
  http.get("/v1/projects", () =>
    HttpResponse.json({ data: [sampleProject, secondProject], pagination: null }),
  ),
);

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  server.resetHandlers();
  localStorage.clear();
});
afterAll(() => server.close());

function DetailStub() {
  const { id } = useParams<{ id: string }>();
  return <div>Detail for {id}</div>;
}

function renderDialog(onClose = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/sandboxes"]}>
        <Routes>
          <Route
            path="/sandboxes"
            element={<NewSandboxDialog onClose={onClose} />}
          />
          <Route path="/sandboxes/:id" element={<DetailStub />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...utils, onClose };
}

describe("NewSandboxDialog — scaffold", () => {
  it("renders a title and the core form fields", async () => {
    renderDialog();
    expect(await screen.findByText(/new sandbox/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/name/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/cpu/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/memory/i)).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /isolated/i })).toBeChecked();
    expect(
      screen.getByRole("radio", { name: /published/i }),
    ).not.toBeChecked();
  });

  it("disables Create when the name is empty and enables it once filled", async () => {
    renderDialog();
    const create = await screen.findByRole("button", { name: /^create$/i });
    expect(create).toBeDisabled();
    await userEvent.type(screen.getByLabelText(/name/i), "my-sandbox");
    expect(create).toBeEnabled();
    await userEvent.clear(screen.getByLabelText(/name/i));
    expect(create).toBeDisabled();
  });

  it("trims whitespace-only names and keeps Create disabled", async () => {
    renderDialog();
    const create = await screen.findByRole("button", { name: /^create$/i });
    await userEvent.type(screen.getByLabelText(/name/i), "   ");
    expect(create).toBeDisabled();
  });

  it("calls onClose when the Cancel button is clicked", async () => {
    const { onClose } = renderDialog();
    await screen.findByRole("button", { name: /^create$/i });
    await userEvent.click(screen.getByRole("button", { name: /^cancel$/i }));
    expect(onClose).toHaveBeenCalled();
  });

  it("shows the dialog element with the open attribute after mount", async () => {
    renderDialog();
    await screen.findByText(/new sandbox/i);
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("open");
  });

  it("shows an empty-state message and keeps Create disabled when /v1/projects returns zero projects", async () => {
    server.use(
      http.get("/v1/projects", () =>
        HttpResponse.json({ data: [], pagination: null }),
      ),
    );
    renderDialog();
    await screen.findByText(/no projects/i);
    const create = screen.getByRole("button", { name: /^create$/i });
    await userEvent.type(screen.getByLabelText(/name/i), "my-sandbox");
    expect(create).toBeDisabled();
  });

  it("shows an error message and keeps Create disabled when /v1/projects fails", async () => {
    server.use(
      http.get("/v1/projects", () =>
        HttpResponse.json(
          { error: { code: 500, message: "boom" } },
          { status: 500 },
        ),
      ),
    );
    renderDialog();
    await screen.findByText(/failed to load projects/i);
    const create = screen.getByRole("button", { name: /^create$/i });
    await userEvent.type(screen.getByLabelText(/name/i), "my-sandbox");
    expect(create).toBeDisabled();
  });
});

describe("NewSandboxDialog — project defaulting", () => {
  it("defaults to the first project when no id is stored", async () => {
    renderDialog();
    await screen.findByText(/new sandbox/i);
    const select = screen.getByLabelText(/project/i) as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe("prj_1"));
  });

  it("honors a stored project id when it still exists", async () => {
    localStorage.setItem("navaris.lastProjectId", "prj_2");
    renderDialog();
    await screen.findByText(/new sandbox/i);
    const select = screen.getByLabelText(/project/i) as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe("prj_2"));
  });

  it("falls back to the first project when the stored id is stale", async () => {
    localStorage.setItem("navaris.lastProjectId", "prj_gone");
    renderDialog();
    await screen.findByText(/new sandbox/i);
    const select = screen.getByLabelText(/project/i) as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe("prj_1"));
  });
});

describe("NewSandboxDialog — image picker", () => {
  it("defaults to alpine/3.21 preset", async () => {
    renderDialog();
    const alpine = await screen.findByRole("button", { name: /alpine\/3\.21/i });
    // Selected preset uses the --fg-primary color class; non-selected uses
    // --fg-secondary. We assert via the class list to avoid coupling to
    // the specific hex, which lives in index.css.
    expect(alpine.className).toContain("border-[var(--fg-primary)]");
  });

  it("switches selection when a different preset is clicked", async () => {
    renderDialog();
    const debian = await screen.findByRole("button", { name: /debian-12/i });
    await userEvent.click(debian);
    expect(debian.className).toContain("border-[var(--fg-primary)]");
    const alpine = screen.getByRole("button", { name: /alpine\/3\.21/i });
    expect(alpine.className).not.toContain("border-[var(--fg-primary)]");
  });

  it("reveals a text input when Custom… is selected", async () => {
    renderDialog();
    const custom = await screen.findByRole("button", { name: /custom…/i });
    expect(screen.queryByLabelText(/custom image ref/i)).not.toBeInTheDocument();
    await userEvent.click(custom);
    expect(screen.getByLabelText(/custom image ref/i)).toBeInTheDocument();
  });

  it("disables Create when Custom… is selected but the text input is empty", async () => {
    renderDialog();
    const custom = await screen.findByRole("button", { name: /custom…/i });
    await userEvent.click(custom);
    await userEvent.type(screen.getByLabelText(/name/i), "my-sandbox");
    // Wait for the project dropdown to default to prj_1 before asserting
    // the Create button state — otherwise canSubmit is gated by the empty
    // projectId and not by the empty custom ref.
    const select = screen.getByLabelText(/project/i) as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe("prj_1"));
    expect(screen.getByRole("button", { name: /^create$/i })).toBeDisabled();
  });
});

describe("NewSandboxDialog — submit", () => {
  it("POSTs the form, invalidates sandboxes, writes last project, and navigates", async () => {
    let seenBody: Record<string, unknown> = {};
    server.use(
      http.post("/v1/sandboxes", async ({ request }) => {
        seenBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(
          {
            OperationID: "op_1",
            ResourceType: "sandbox",
            ResourceID: "sbx_created",
            SandboxID: "sbx_created",
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
    const { onClose } = renderDialog();
    await screen.findByText(/new sandbox/i);
    await userEvent.type(screen.getByLabelText(/name/i), "integration-test");
    await userEvent.type(screen.getByLabelText(/cpu/i), "2");
    await userEvent.type(screen.getByLabelText(/memory/i), "512");
    await userEvent.click(screen.getByRole("button", { name: /^create$/i }));
    // Navigating to /sandboxes/:id renders the DetailStub.
    expect(await screen.findByText("Detail for sbx_created")).toBeInTheDocument();
    expect(onClose).toHaveBeenCalled();
    expect(localStorage.getItem("navaris.lastProjectId")).toBe("prj_1");
    expect(seenBody).toEqual({
      project_id: "prj_1",
      name: "integration-test",
      image_id: "alpine/3.21",
      cpu_limit: 2,
      memory_limit_mb: 512,
      network_mode: "isolated",
    });
  });

  it("omits cpu_limit and memory_limit_mb when the inputs are empty", async () => {
    let seenBody: Record<string, unknown> = {};
    server.use(
      http.post("/v1/sandboxes", async ({ request }) => {
        seenBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(
          {
            OperationID: "op_2",
            ResourceType: "sandbox",
            ResourceID: "sbx_nolimits",
            SandboxID: "sbx_nolimits",
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
    renderDialog();
    await screen.findByText(/new sandbox/i);
    await userEvent.type(screen.getByLabelText(/name/i), "no-limits");
    await userEvent.click(screen.getByRole("button", { name: /^create$/i }));
    await screen.findByText("Detail for sbx_nolimits");
    expect(seenBody).not.toHaveProperty("cpu_limit");
    expect(seenBody).not.toHaveProperty("memory_limit_mb");
  });

  it("sends a custom image ref when Custom… is picked", async () => {
    let seenBody: Record<string, unknown> = {};
    server.use(
      http.post("/v1/sandboxes", async ({ request }) => {
        seenBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(
          {
            OperationID: "op_3",
            ResourceType: "sandbox",
            ResourceID: "sbx_custom",
            SandboxID: "sbx_custom",
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
    renderDialog();
    await screen.findByText(/new sandbox/i);
    await userEvent.click(screen.getByRole("button", { name: /custom…/i }));
    await userEvent.type(
      screen.getByLabelText(/custom image ref/i),
      "images:ubuntu/24.04",
    );
    await userEvent.type(screen.getByLabelText(/name/i), "custom-image");
    await userEvent.click(screen.getByRole("button", { name: /^create$/i }));
    await screen.findByText("Detail for sbx_custom");
    expect(seenBody.image_id).toBe("images:ubuntu/24.04");
  });
});
