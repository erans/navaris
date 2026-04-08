import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { AppShell } from "./AppShell";

// jsdom ships a real-ish WebSocket that tries to dial the URL and hangs in
// CONNECTING. We don't want a hanging socket per test, so stub it to a noop
// class for the duration of this file. The class needs to satisfy
// useEventStream's usage: a constructor and assignable on{open,message,close,error}.
class NoopWebSocket {
  static instances: NoopWebSocket[] = [];
  url: string;
  readyState = 0;
  onopen: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onclose: ((e: CloseEvent) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  constructor(url: string) {
    this.url = url;
    NoopWebSocket.instances.push(this);
  }
  send(_: string) {}
  close() {
    this.readyState = 3;
  }
}

let originalWS: typeof WebSocket;
beforeEach(() => {
  originalWS = globalThis.WebSocket;
  // @ts-expect-error NoopWebSocket is a partial stub, not assignable to typeof WebSocket
  globalThis.WebSocket = NoopWebSocket;
  NoopWebSocket.instances = [];
});
afterEach(() => {
  globalThis.WebSocket = originalWS;
});

function renderShell(child: ReactNode = <div>child</div>) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/sandboxes"]}>
        <Routes>
          <Route path="/*" element={<AppShell>{child}</AppShell>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("AppShell", () => {
  it("renders the NAVARIS brand label and sidebar nav links", () => {
    renderShell();
    // Both the sidebar header and the StatusLine render "NAVARIS"; we just
    // assert at least one is present.
    expect(screen.getAllByText("NAVARIS").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByRole("link", { name: /Projects/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Sandboxes/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Events/i })).toBeInTheDocument();
  });

  it("renders children inside the main content area", () => {
    renderShell(<div>my-content</div>);
    expect(screen.getByText("my-content")).toBeInTheDocument();
  });

  it("renders the status line region", () => {
    renderShell();
    expect(screen.getByTestId("status-line")).toBeInTheDocument();
  });
});
