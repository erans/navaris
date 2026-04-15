import { describe, it, expect, beforeAll, beforeEach, afterEach, afterAll, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";

// Same xterm/ResizeObserver stubs as TerminalPanel.test.tsx — the route
// mounts TerminalPanel instances and those pull xterm in.
vi.mock("@xterm/xterm", () => {
  class Terminal {
    cols = 80;
    rows = 24;
    loadAddon() {}
    open() {}
    write() {}
    paste() {}
    dispose() {}
    getSelection() { return ""; }
    onData() { return { dispose() {} }; }
    onSelectionChange() { return { dispose() {} }; }
    onResize() { return { dispose() {} }; }
  }
  return { Terminal };
});
vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class { fit() {} },
}));
vi.mock("@xterm/addon-clipboard", () => ({
  ClipboardAddon: class {},
}));
vi.mock("@xterm/xterm/css/xterm.css", () => ({}));

class StubResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}
(globalThis as unknown as { ResizeObserver: typeof StubResizeObserver }).ResizeObserver = StubResizeObserver;

import Terminal from "./Terminal";

class MockWebSocket {
  static instances: MockWebSocket[] = [];
  static OPEN = 1;
  static CLOSED = 3;
  readyState = 0;
  url: string;
  binaryType = "blob";
  onopen: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onclose: ((e: CloseEvent) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  sent: unknown[] = [];
  constructor(url: string) {
    this.url = url;
    MockWebSocket.instances.push(this);
  }
  send(data: unknown) { this.sent.push(data); }
  close() {
    this.readyState = MockWebSocket.CLOSED;
    this.onclose?.(new CloseEvent("close"));
  }
}

function sess(id: string, state: "active" | "detached" | "exited" | "destroyed" = "active", attachedAt: string | null = null) {
  return {
    SessionID: id,
    SandboxID: "sbx_1",
    Backing: "tmux",
    Shell: "",
    State: state,
    CreatedAt: "2026-04-14T00:00:00Z",
    UpdatedAt: "2026-04-14T00:00:00Z",
    LastAttachedAt: attachedAt,
    IdleTimeout: null,
    Metadata: null,
  };
}

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  server.resetHandlers();
  localStorage.clear();
});
afterAll(() => server.close());

beforeEach(() => {
  vi.stubGlobal("WebSocket", MockWebSocket);
  MockWebSocket.instances = [];
});
afterEach(() => {
  vi.unstubAllGlobals();
});

function renderRoute() {
  return render(
    <MemoryRouter initialEntries={["/sandboxes/sbx_1/terminal"]}>
      <Routes>
        <Route path="/sandboxes/:id/terminal" element={<Terminal />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("Terminal route — reload restore", () => {
  it("opens a WebSocket for every live session on load (eager attach)", async () => {
    server.use(
      http.get("/v1/sandboxes/sbx_1/sessions", () =>
        HttpResponse.json({ data: [sess("sess_1"), sess("sess_2"), sess("sess_3")] }),
      ),
    );
    renderRoute();
    await waitFor(() => expect(MockWebSocket.instances).toHaveLength(3));
    expect(MockWebSocket.instances.map((w) => w.url)).toEqual([
      expect.stringMatching(/session=sess_1$/),
      expect.stringMatching(/session=sess_2$/),
      expect.stringMatching(/session=sess_3$/),
    ]);
  });

  it("lands on the remembered active tab when it exists", async () => {
    localStorage.setItem("navaris.terminal.sbx_1.activeSession", "sess_2");
    server.use(
      http.get("/v1/sandboxes/sbx_1/sessions", () =>
        HttpResponse.json({ data: [sess("sess_1"), sess("sess_2"), sess("sess_3")] }),
      ),
    );
    renderRoute();
    await waitFor(() => expect(screen.getByText("Session 2")).toBeInTheDocument());
    const activeTab = screen.getByText("Session 2").closest("button")!;
    // Active tab carries the fg-primary class; simplest assertion is that
    // it has a background color class distinguishing it from inactive.
    expect(activeTab.className).toContain("bg-[var(--bg-primary)]");
  });

  it("falls back to LastAttachedAt sort when remembered id is absent", async () => {
    localStorage.setItem("navaris.terminal.sbx_1.activeSession", "sess_gone");
    server.use(
      http.get("/v1/sandboxes/sbx_1/sessions", () =>
        HttpResponse.json({
          data: [
            sess("sess_1", "active", "2026-04-14T00:00:01Z"),
            sess("sess_2", "active", "2026-04-14T00:05:00Z"),
          ],
        }),
      ),
    );
    renderRoute();
    await waitFor(() => {
      const active = screen.getByText("Session 2").closest("button")!;
      expect(active.className).toContain("bg-[var(--bg-primary)]");
    });
  });

  it("prefers a non-exited tab when the remembered tab has exited", async () => {
    localStorage.setItem("navaris.terminal.sbx_1.activeSession", "sess_1");
    server.use(
      http.get("/v1/sandboxes/sbx_1/sessions", () =>
        HttpResponse.json({
          data: [
            sess("sess_1", "exited"),
            sess("sess_2", "active", "2026-04-14T00:00:05Z"),
          ],
        }),
      ),
    );
    renderRoute();
    await waitFor(() => {
      const active = screen.getByText("Session 2").closest("button")!;
      expect(active.className).toContain("bg-[var(--bg-primary)]");
    });
  });

  it("writes localStorage when a tab is clicked", async () => {
    const user = userEvent.setup();
    server.use(
      http.get("/v1/sandboxes/sbx_1/sessions", () =>
        HttpResponse.json({ data: [sess("sess_1"), sess("sess_2")] }),
      ),
    );
    renderRoute();
    await waitFor(() => expect(screen.getByText("Session 2")).toBeInTheDocument());
    await user.click(screen.getByText("Session 2"));
    expect(localStorage.getItem("navaris.terminal.sbx_1.activeSession")).toBe("sess_2");
  });

  it("renders exited sessions with always-visible close button", async () => {
    server.use(
      http.get("/v1/sandboxes/sbx_1/sessions", () =>
        HttpResponse.json({ data: [sess("sess_1", "exited")] }),
      ),
    );
    renderRoute();
    await waitFor(() => expect(screen.getByText("Session 1")).toBeInTheDocument());
    const tab = screen.getByText("Session 1").closest("button")!;
    expect(tab.className).toContain("opacity-60");
    expect(tab.className).toContain("line-through");
    // Close affordance (×) is present even though there's only one tab.
    expect(tab.textContent).toContain("×");
  });

  it("auto-creates a session when all existing sessions are exited", async () => {
    let createCalled = false;
    server.use(
      http.get("/v1/sandboxes/sbx_1/sessions", () =>
        HttpResponse.json({ data: [sess("sess_old", "exited")] }),
      ),
      http.post("/v1/sandboxes/sbx_1/sessions", () => {
        createCalled = true;
        return HttpResponse.json(sess("sess_new"));
      }),
    );
    renderRoute();
    await waitFor(() => {
      expect(createCalled).toBe(true);
      expect(screen.getByText("Session 2")).toBeInTheDocument();
    });
  });

  it("clears remembered tab when destroying the remembered session", async () => {
    const user = userEvent.setup();
    localStorage.setItem("navaris.terminal.sbx_1.activeSession", "sess_1");
    server.use(
      http.get("/v1/sandboxes/sbx_1/sessions", () =>
        HttpResponse.json({ data: [sess("sess_1"), sess("sess_2")] }),
      ),
      http.delete("/v1/sessions/sess_1", () => new HttpResponse(null, { status: 204 })),
    );
    renderRoute();
    await waitFor(() => expect(screen.getByText("Session 1")).toBeInTheDocument());
    const closeX = screen.getAllByText("×")[0];
    await user.click(closeX);
    const confirmBtn = await screen.findByRole("button", { name: "Close" });
    await user.click(confirmBtn);
    await waitFor(() =>
      expect(localStorage.getItem("navaris.terminal.sbx_1.activeSession")).toBeNull(),
    );
  });
});
