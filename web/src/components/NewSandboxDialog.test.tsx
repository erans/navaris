import { describe, it, expect, beforeAll, afterEach, afterAll, vi } from "vitest";
import { render, screen } from "@testing-library/react";
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
