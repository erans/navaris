import { describe, it, expect, beforeEach, beforeAll, afterEach, afterAll, vi } from "vitest";
import React from "react";
import { render } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";

// xterm.js touches canvas measurement and clipboard APIs that jsdom does
// not implement. Replace the library with a structural stub that exposes
// only the methods TerminalPanel actually calls.
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

// jsdom has no ResizeObserver. Stub with an instance that records calls
// but never fires — our tests don't need actual resize events.
class StubResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}
(globalThis as unknown as { ResizeObserver: typeof StubResizeObserver }).ResizeObserver = StubResizeObserver;

import TerminalPanel from "./TerminalPanel";

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
  simulateOpen() {
    this.readyState = MockWebSocket.OPEN;
    this.onopen?.(new Event("open"));
  }
  simulateClose() {
    this.readyState = MockWebSocket.CLOSED;
    this.onclose?.(new CloseEvent("close"));
  }
}

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

beforeEach(() => {
  vi.stubGlobal("WebSocket", MockWebSocket);
  MockWebSocket.instances = [];
});
afterEach(() => {
  vi.unstubAllGlobals();
});

function renderPanel(overrides: Partial<React.ComponentProps<typeof TerminalPanel>> = {}) {
  const onStatusChange = vi.fn();
  const utils = render(
    <TerminalPanel
      sandboxId="sbx_1"
      sessionId="sess_1"
      isVisible={true}
      initialSessionState="active"
      onStatusChange={onStatusChange}
      {...overrides}
    />,
  );
  return { ...utils, onStatusChange };
}

describe("TerminalPanel", () => {
  it("opens a WebSocket on mount with correct URL", () => {
    renderPanel();
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0].url).toMatch(
      /\/v1\/sandboxes\/sbx_1\/attach\?session=sess_1$/,
    );
  });

  it("reports connecting, then connected on ws.onopen", () => {
    const { onStatusChange } = renderPanel();
    expect(onStatusChange).toHaveBeenCalledWith("connecting");
    MockWebSocket.instances[0].simulateOpen();
    expect(onStatusChange).toHaveBeenCalledWith("connected");
  });

  it("eagerly connects even when isVisible is false", () => {
    renderPanel({ isVisible: false });
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it("closes the WebSocket on unmount without reporting further status", () => {
    const { onStatusChange, unmount } = renderPanel();
    MockWebSocket.instances[0].simulateOpen();
    const callsBefore = onStatusChange.mock.calls.length;
    unmount();
    expect(MockWebSocket.instances[0].readyState).toBe(MockWebSocket.CLOSED);
    // After unmount, no further status should be reported from the
    // triggered onclose (reconnect.stopped must gate it).
    expect(onStatusChange.mock.calls.length).toBe(callsBefore);
  });

  it("retries on ws.close with exponential backoff + jitter", async () => {
    vi.useFakeTimers();
    vi.spyOn(Math, "random").mockReturnValue(0.5); // jitter = 1.0 exactly
    server.use(
      http.get("/v1/sandboxes/sbx_1/sessions", () =>
        HttpResponse.json({
          data: [
            {
              SessionID: "sess_1",
              SandboxID: "sbx_1",
              Backing: "tmux",
              Shell: "",
              State: "detached",
              CreatedAt: "2026-04-14T00:00:00Z",
              UpdatedAt: "2026-04-14T00:00:00Z",
              LastAttachedAt: null,
              IdleTimeout: null,
              Metadata: null,
            },
          ],
        }),
      ),
    );

    const { onStatusChange } = renderPanel();
    MockWebSocket.instances[0].simulateOpen();
    MockWebSocket.instances[0].simulateClose();

    // refetch listSessions runs async; flush microtasks + the 1s timer.
    await vi.runAllTimersAsync();

    expect(onStatusChange).toHaveBeenCalledWith("reconnecting");
    expect(MockWebSocket.instances.length).toBeGreaterThanOrEqual(2);
    vi.useRealTimers();
  });

  it("resets attempt count on successful reconnect", async () => {
    vi.useFakeTimers();
    vi.spyOn(Math, "random").mockReturnValue(0.5);
    server.use(
      http.get("/v1/sandboxes/sbx_1/sessions", () =>
        HttpResponse.json({
          data: [
            {
              SessionID: "sess_1",
              SandboxID: "sbx_1",
              Backing: "tmux",
              Shell: "",
              State: "detached",
              CreatedAt: "2026-04-14T00:00:00Z",
              UpdatedAt: "2026-04-14T00:00:00Z",
              LastAttachedAt: null,
              IdleTimeout: null,
              Metadata: null,
            },
          ],
        }),
      ),
    );

    renderPanel();
    MockWebSocket.instances[0].simulateOpen();
    MockWebSocket.instances[0].simulateClose();
    await vi.runAllTimersAsync();
    // A second ws is created by the retry; open + close it again. The
    // third attempt should still be the first-delay (1s * jitter=1.0).
    const second = MockWebSocket.instances[1];
    second.simulateOpen();
    second.simulateClose();
    // Advance 1s; if attempt was reset, the retry fires at this time.
    await vi.advanceTimersByTimeAsync(1000);
    expect(MockWebSocket.instances.length).toBe(3);
    vi.useRealTimers();
  });

  it("transitions to exited when refetch shows session exited", async () => {
    server.use(
      http.get("/v1/sandboxes/sbx_1/sessions", () =>
        HttpResponse.json({
          data: [
            {
              SessionID: "sess_1",
              SandboxID: "sbx_1",
              Backing: "tmux",
              Shell: "",
              State: "exited",
              CreatedAt: "2026-04-14T00:00:00Z",
              UpdatedAt: "2026-04-14T00:00:00Z",
              LastAttachedAt: null,
              IdleTimeout: null,
              Metadata: null,
            },
          ],
        }),
      ),
    );

    const { onStatusChange } = renderPanel();
    MockWebSocket.instances[0].simulateOpen();
    MockWebSocket.instances[0].simulateClose();
    await new Promise((r) => setTimeout(r, 0));
    await new Promise((r) => setTimeout(r, 0));
    expect(onStatusChange).toHaveBeenCalledWith("exited");
    // No new WS instance created (retry suppressed).
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it("transitions to exited when refetch shows session destroyed", async () => {
    server.use(
      http.get("/v1/sandboxes/sbx_1/sessions", () =>
        HttpResponse.json({
          data: [
            {
              SessionID: "sess_1",
              SandboxID: "sbx_1",
              Backing: "tmux",
              Shell: "",
              State: "destroyed",
              CreatedAt: "2026-04-14T00:00:00Z",
              UpdatedAt: "2026-04-14T00:00:00Z",
              LastAttachedAt: null,
              IdleTimeout: null,
              Metadata: null,
            },
          ],
        }),
      ),
    );

    const { onStatusChange } = renderPanel();
    MockWebSocket.instances[0].simulateOpen();
    MockWebSocket.instances[0].simulateClose();
    await new Promise((r) => setTimeout(r, 0));
    await new Promise((r) => setTimeout(r, 0));
    expect(onStatusChange).toHaveBeenCalledWith("exited");
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it("transitions to exited when session missing from list", async () => {
    server.use(
      http.get("/v1/sandboxes/sbx_1/sessions", () =>
        HttpResponse.json({ data: [] }),
      ),
    );

    const { onStatusChange } = renderPanel();
    MockWebSocket.instances[0].simulateOpen();
    MockWebSocket.instances[0].simulateClose();
    await new Promise((r) => setTimeout(r, 0));
    await new Promise((r) => setTimeout(r, 0));
    expect(onStatusChange).toHaveBeenCalledWith("exited");
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it("retries when refetch itself fails", async () => {
    vi.useFakeTimers();
    vi.spyOn(Math, "random").mockReturnValue(0.5);
    server.use(
      http.get("/v1/sandboxes/sbx_1/sessions", () =>
        HttpResponse.json({ message: "boom" }, { status: 500 }),
      ),
    );

    const { onStatusChange } = renderPanel();
    MockWebSocket.instances[0].simulateOpen();
    MockWebSocket.instances[0].simulateClose();
    await vi.runAllTimersAsync();

    expect(onStatusChange).toHaveBeenCalledWith("reconnecting");
    expect(onStatusChange).not.toHaveBeenCalledWith("exited");
    expect(MockWebSocket.instances.length).toBeGreaterThanOrEqual(2);
    vi.useRealTimers();
  });
});
