import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useEventStream } from "./useEventStream";

class MockWebSocket {
  static instances: MockWebSocket[] = [];
  static OPEN = 1;
  readyState = 0;
  url: string;
  onopen: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onclose: ((e: CloseEvent) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  sent: string[] = [];
  constructor(url: string) {
    this.url = url;
    MockWebSocket.instances.push(this);
  }
  send(data: string) {
    this.sent.push(data);
  }
  close() {
    this.readyState = 3;
    this.onclose?.(new CloseEvent("close"));
  }
  simulateOpen() {
    this.readyState = MockWebSocket.OPEN;
    this.onopen?.(new Event("open"));
  }
  simulateMessage(data: unknown) {
    this.onmessage?.(new MessageEvent("message", { data: JSON.stringify(data) }));
  }
}

describe("useEventStream", () => {
  let originalWS: typeof WebSocket;
  beforeEach(() => {
    originalWS = globalThis.WebSocket;
    // @ts-expect-error test mock
    globalThis.WebSocket = MockWebSocket;
    MockWebSocket.instances = [];
  });
  afterEach(() => {
    globalThis.WebSocket = originalWS;
  });

  function wrap() {
    const qc = new QueryClient();
    return ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    );
  }

  it("opens a websocket to /v1/events", () => {
    renderHook(() => useEventStream(), { wrapper: wrap() });
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0].url).toMatch(/\/v1\/events$/);
  });

  it("invalidates sandbox queries on sandbox_state_changed", async () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    );
    renderHook(() => useEventStream(), { wrapper });
    act(() => {
      MockWebSocket.instances[0].simulateOpen();
      MockWebSocket.instances[0].simulateMessage({
        Type: "sandbox_state_changed",
        Timestamp: "2026-04-07T10:00:00Z",
        Data: {
          sandbox_id: "sbx_1",
          project_id: "prj_1",
          state: "running",
        },
      });
    });
    await waitFor(() => {
      expect(spy).toHaveBeenCalledWith({ queryKey: ["sandboxes"] });
      expect(spy).toHaveBeenCalledWith({ queryKey: ["sandbox", "sbx_1"] });
    });
  });

  it("invalidates operation queries on operation_state_changed", async () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    );
    renderHook(() => useEventStream(), { wrapper });
    act(() => {
      MockWebSocket.instances[0].simulateOpen();
      MockWebSocket.instances[0].simulateMessage({
        Type: "operation_state_changed",
        Timestamp: "2026-04-07T10:00:00Z",
        Data: {
          operation_id: "op_1",
          state: "succeeded",
          type: "start_sandbox",
        },
      });
    });
    await waitFor(() => {
      expect(spy).toHaveBeenCalledWith({ queryKey: ["operations"] });
    });
  });
});
