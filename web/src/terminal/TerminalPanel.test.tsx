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

let originalWS: typeof WebSocket;
beforeEach(() => {
  originalWS = globalThis.WebSocket;
  Object.defineProperty(globalThis, "WebSocket", {
    value: MockWebSocket,
    writable: true,
    configurable: true,
  });
  MockWebSocket.instances = [];
});
afterEach(() => {
  Object.defineProperty(globalThis, "WebSocket", {
    value: originalWS,
    writable: true,
    configurable: true,
  });
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
});
