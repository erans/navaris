import { describe, it, expect, beforeAll, afterEach, afterAll, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import NewProjectDialog from "./NewProjectDialog";

// NewProjectDialog is a much simpler cousin of NewSandboxDialog: one field
// (name) and one POST. Tests here focus on (a) the canSubmit gate around
// the name input, (b) the pending-state guards that match the sandbox
// dialog's behavior, and (c) the error branches of messageForCreateError.

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  server.resetHandlers();
});
afterAll(() => server.close());

function renderDialog(onClose = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={qc}>
      <NewProjectDialog onClose={onClose} />
    </QueryClientProvider>,
  );
  return { ...utils, onClose };
}

describe("NewProjectDialog — scaffold", () => {
  it("renders the title and name field", async () => {
    renderDialog();
    expect(await screen.findByText(/new project/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/name/i)).toBeInTheDocument();
  });

  it("disables Create until the name has non-whitespace content", async () => {
    renderDialog();
    const create = await screen.findByRole("button", { name: /^create$/i });
    expect(create).toBeDisabled();
    await userEvent.type(screen.getByLabelText(/name/i), "   ");
    expect(create).toBeDisabled();
    await userEvent.type(screen.getByLabelText(/name/i), "proj");
    expect(create).toBeEnabled();
  });

  it("calls onClose when Cancel is clicked", async () => {
    const { onClose } = renderDialog();
    await screen.findByRole("button", { name: /^create$/i });
    await userEvent.click(screen.getByRole("button", { name: /^cancel$/i }));
    expect(onClose).toHaveBeenCalled();
  });

  it("opens the <dialog> element on mount", async () => {
    renderDialog();
    await screen.findByText(/new project/i);
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("open");
  });
});

describe("NewProjectDialog — submit", () => {
  it("POSTs the trimmed name and closes on success", async () => {
    let seenBody: Record<string, unknown> = {};
    server.use(
      http.post("/v1/projects", async ({ request }) => {
        seenBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(
          {
            ProjectID: "prj_new",
            Name: "proj",
            CreatedAt: "2026-04-08T12:00:00Z",
            UpdatedAt: "2026-04-08T12:00:00Z",
            Metadata: null,
          },
          { status: 201 },
        );
      }),
    );
    const { onClose } = renderDialog();
    await screen.findByText(/new project/i);
    await userEvent.type(screen.getByLabelText(/name/i), "  proj  ");
    await userEvent.click(screen.getByRole("button", { name: /^create$/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(seenBody).toEqual({ name: "proj" });
  });
});

describe("NewProjectDialog — errors", () => {
  it("shows a specific message on 409 conflict and stays open", async () => {
    server.use(
      http.post("/v1/projects", () =>
        HttpResponse.json(
          { error: { code: 409, message: "conflict" } },
          { status: 409 },
        ),
      ),
    );
    const { onClose } = renderDialog();
    await screen.findByText(/new project/i);
    await userEvent.type(screen.getByLabelText(/name/i), "dup");
    await userEvent.click(screen.getByRole("button", { name: /^create$/i }));
    expect(
      await screen.findByText(/already exists/i),
    ).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /^create$/i })).toBeEnabled();
  });

  it("shows a fallback on 500", async () => {
    server.use(
      http.post("/v1/projects", () =>
        HttpResponse.json(
          { error: { code: 500, message: "internal" } },
          { status: 500 },
        ),
      ),
    );
    renderDialog();
    await screen.findByText(/new project/i);
    await userEvent.type(screen.getByLabelText(/name/i), "boom");
    await userEvent.click(screen.getByRole("button", { name: /^create$/i }));
    expect(
      await screen.findByText(/server error\. try again/i),
    ).toBeInTheDocument();
  });

  it("shows a generic message on network failure", async () => {
    server.use(
      http.post("/v1/projects", () => HttpResponse.error()),
    );
    renderDialog();
    await screen.findByText(/new project/i);
    await userEvent.type(screen.getByLabelText(/name/i), "offline");
    await userEvent.click(screen.getByRole("button", { name: /^create$/i }));
    expect(
      await screen.findByText(/unable to create project/i),
    ).toBeInTheDocument();
  });
});
