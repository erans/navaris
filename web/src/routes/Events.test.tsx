import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Events from "./Events";

// jsdom's WebSocket tries to dial for real and hangs. Stub it for this file.
// Mirrors the NoopWebSocket pattern in AppShell.test.tsx.
class NoopWebSocket {
  url: string;
  readyState = 0;
  onopen: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onclose: ((e: CloseEvent) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  constructor(url: string) {
    this.url = url;
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
});
afterEach(() => {
  globalThis.WebSocket = originalWS;
});

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/events"]}>
      <Routes>
        <Route path="/events" element={<Events />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("Events route", () => {
  it("renders the Events title and empty state before any frames arrive", () => {
    renderPage();
    expect(screen.getByRole("heading", { name: /Events/i })).toBeInTheDocument();
    expect(screen.getByText(/Waiting for events/i)).toBeInTheDocument();
  });
});
