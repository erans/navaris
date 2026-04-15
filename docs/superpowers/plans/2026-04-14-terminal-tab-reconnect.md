# Terminal Tab Reconnect & Resilience — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the terminal UI fully restore on reload — eager-attach every live tab, land on the user's last-focused tab, show exited sessions explicitly, and auto-reconnect on network drops.

**Architecture:** Split `web/src/routes/Terminal.tsx` into three units: a `useLastActiveSession` hook (mirrors `useLastProject`), a `<TerminalPanel>` component that owns one session's xterm + WebSocket + reconnect state machine, and a shrunk `Terminal.tsx` parent that handles tab bar, active tracking, destroy dialog, and data loading. Eager attach falls out of each panel opening its own WebSocket on mount.

**Tech Stack:** React + TypeScript, xterm.js (`@xterm/xterm`, `@xterm/addon-fit`, `@xterm/addon-clipboard`), vitest + @testing-library/react + MSW for tests.

**Spec:** `docs/superpowers/specs/2026-04-14-terminal-tab-reconnect-design.md`

---

## File Structure

| File | Status | Responsibility |
|------|--------|----------------|
| `web/src/hooks/useLastActiveSession.ts` | create | Per-sandbox localStorage read/write/clear for "last active tab id". |
| `web/src/hooks/useLastActiveSession.test.ts` | create | Unit tests for the hook. |
| `web/src/terminal/TerminalPanel.tsx` | create | Owns one session's xterm, WebSocket, reconnect state machine, paste handler, status overlays. |
| `web/src/terminal/TerminalPanel.test.tsx` | create | Unit tests for the panel. |
| `web/src/routes/Terminal.tsx` | modify (refactor to ~180 lines) | Data loading, tab bar, destroy dialog, active-tab tracking, panel-status aggregation. |
| `web/src/routes/Terminal.test.tsx` | create | Route-level tests for reload/restore/exited behavior. |
| `web/MANUAL_TERMINAL_SMOKE.md` | modify | Add three manual smoke steps. |

---

## Task 1: `useLastActiveSession` hook

**Files:**
- Create: `web/src/hooks/useLastActiveSession.ts`
- Test: `web/src/hooks/useLastActiveSession.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/hooks/useLastActiveSession.test.ts`:

```ts
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useLastActiveSession } from "./useLastActiveSession";

describe("useLastActiveSession", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("returns null when no id has been written", () => {
    const { result } = renderHook(() => useLastActiveSession("sbx_1"));
    expect(result.current.read()).toBeNull();
  });

  it("round-trips a session id through localStorage", () => {
    const { result } = renderHook(() => useLastActiveSession("sbx_1"));
    act(() => result.current.write("sess_42"));
    expect(result.current.read()).toBe("sess_42");
  });

  it("uses a per-sandbox storage key", () => {
    const { result } = renderHook(() => useLastActiveSession("sbx_1"));
    act(() => result.current.write("sess_99"));
    expect(localStorage.getItem("navaris.terminal.sbx_1.activeSession")).toBe("sess_99");
  });

  it("isolates sandboxes from each other", () => {
    const { result: a } = renderHook(() => useLastActiveSession("sbx_a"));
    const { result: b } = renderHook(() => useLastActiveSession("sbx_b"));
    act(() => a.current.write("sess_a"));
    act(() => b.current.write("sess_b"));
    expect(a.current.read()).toBe("sess_a");
    expect(b.current.read()).toBe("sess_b");
  });

  it("clear() removes the entry", () => {
    const { result } = renderHook(() => useLastActiveSession("sbx_1"));
    act(() => result.current.write("sess_1"));
    act(() => result.current.clear());
    expect(result.current.read()).toBeNull();
  });

  it("swallows getItem errors (private mode)", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("SecurityError");
    });
    const { result } = renderHook(() => useLastActiveSession("sbx_1"));
    expect(result.current.read()).toBeNull();
  });

  it("swallows setItem errors (quota/private mode)", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("QuotaExceededError");
    });
    const { result } = renderHook(() => useLastActiveSession("sbx_1"));
    expect(() => act(() => result.current.write("sess_1"))).not.toThrow();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/hooks/useLastActiveSession.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement the hook**

Create `web/src/hooks/useLastActiveSession.ts`:

```ts
import { useCallback } from "react";

// Storage layout: one key per sandbox. Keeping last-focused tab
// scoped to the sandbox prevents Session 3 from sandbox A
// from bleeding into sandbox B when the user switches.
const keyFor = (sandboxId: string) => `navaris.terminal.${sandboxId}.activeSession`;

export function useLastActiveSession(sandboxId: string) {
  const read = useCallback((): string | null => {
    try {
      return localStorage.getItem(keyFor(sandboxId));
    } catch {
      return null;
    }
  }, [sandboxId]);

  const write = useCallback((sessionId: string): void => {
    try {
      localStorage.setItem(keyFor(sandboxId), sessionId);
    } catch {
      // Storage disabled / quota exceeded — silently no-op.
    }
  }, [sandboxId]);

  const clear = useCallback((): void => {
    try {
      localStorage.removeItem(keyFor(sandboxId));
    } catch {
      // Same reason as write().
    }
  }, [sandboxId]);

  return { read, write, clear };
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/hooks/useLastActiveSession.test.ts`
Expected: PASS, 7 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/hooks/useLastActiveSession.ts web/src/hooks/useLastActiveSession.test.ts
git commit -m "$(cat <<'EOF'
feat(web): add useLastActiveSession hook

Per-sandbox localStorage read/write/clear for remembering the
last-focused terminal tab across reloads.
EOF
)"
```

---

## Task 2: `TerminalPanel` — connecting → connected

**Files:**
- Create: `web/src/terminal/TerminalPanel.tsx`
- Test: `web/src/terminal/TerminalPanel.test.tsx`

- [ ] **Step 1: Write the failing test (opens WS + reports statuses)**

Create `web/src/terminal/TerminalPanel.test.tsx`:

```tsx
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
  // @ts-expect-error test mock
  globalThis.WebSocket = MockWebSocket;
  MockWebSocket.instances = [];
});
afterEach(() => {
  globalThis.WebSocket = originalWS;
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/terminal/TerminalPanel.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement the initial panel**

Create `web/src/terminal/TerminalPanel.tsx`:

```tsx
import { useEffect, useRef } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { ClipboardAddon } from "@xterm/addon-clipboard";
import "@xterm/xterm/css/xterm.css";
import { encodeInputBytes, encodeResizeMessage } from "@/terminal/wire";
import type { SessionState } from "@/types/navaris";

export type PanelStatus =
  | "connecting"
  | "connected"
  | "reconnecting"
  | "exited"
  | "failed";

export interface TerminalPanelProps {
  sandboxId: string;
  sessionId: string;
  isVisible: boolean;
  initialSessionState: SessionState;
  onStatusChange: (status: PanelStatus) => void;
}

const TERM_THEME = {
  background: "#0b0b0c",
  foreground: "#f4f4f5",
  cursor: "#f4f4f5",
  selectionBackground: "#2e2e33",
};

interface ReconnectState {
  attempt: number;
  timer: number | null;
  stopped: boolean;
}

export default function TerminalPanel({
  sandboxId,
  sessionId,
  isVisible,
  initialSessionState: _initialSessionState,
  onStatusChange,
}: TerminalPanelProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const termRef = useRef<XTerm | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const roRef = useRef<ResizeObserver | null>(null);
  const pasteHandlerRef = useRef<((e: ClipboardEvent) => void) | null>(null);
  const reconnectRef = useRef<ReconnectState>({ attempt: 0, timer: null, stopped: false });

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const term = new XTerm({
      cursorBlink: true,
      fontFamily:
        '"Commit Mono", "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 13,
      theme: TERM_THEME,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new ClipboardAddon());
    term.open(container);

    const ro = new ResizeObserver(() => fit.fit());
    ro.observe(container);
    requestAnimationFrame(() => fit.fit());

    term.onData((data) => {
      if (wsRef.current?.readyState === WebSocket.OPEN) {
        wsRef.current.send(encodeInputBytes(data));
      }
    });

    term.onSelectionChange(() => {
      const sel = term.getSelection();
      if (sel) navigator.clipboard.writeText(sel).catch(() => {});
    });

    term.onResize(({ cols, rows }) => {
      if (wsRef.current?.readyState === WebSocket.OPEN) {
        wsRef.current.send(encodeResizeMessage(cols, rows));
      }
    });

    const pasteHandler = (e: ClipboardEvent) => {
      e.preventDefault();
      const text = e.clipboardData?.getData("text");
      if (text) term.paste(text);
    };
    container.addEventListener("paste", pasteHandler);

    termRef.current = term;
    fitRef.current = fit;
    roRef.current = ro;
    pasteHandlerRef.current = pasteHandler;

    connect();

    return () => {
      reconnectRef.current.stopped = true;
      if (reconnectRef.current.timer !== null) {
        clearTimeout(reconnectRef.current.timer);
        reconnectRef.current.timer = null;
      }
      container.removeEventListener("paste", pasteHandler);
      ro.disconnect();
      wsRef.current?.close();
      term.dispose();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function connect() {
    if (reconnectRef.current.stopped) return;
    onStatusChange("connecting");
    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(
      `${proto}//${window.location.host}/v1/sandboxes/${encodeURIComponent(sandboxId)}/attach?session=${encodeURIComponent(sessionId)}`,
    );
    ws.binaryType = "arraybuffer";

    ws.onopen = () => {
      reconnectRef.current.attempt = 0;
      const term = termRef.current;
      const fit = fitRef.current;
      if (term && fit) {
        fit.fit();
        ws.send(encodeResizeMessage(term.cols, term.rows));
      }
      onStatusChange("connected");
    };

    ws.onmessage = (msg) => {
      if (msg.data instanceof ArrayBuffer) {
        termRef.current?.write(new Uint8Array(msg.data));
      }
    };

    ws.onerror = () => {
      // onclose always follows; state machine lives there.
    };

    ws.onclose = () => {
      if (reconnectRef.current.stopped) return;
      // Reconnect handling lands in Task 3; for now, no-op.
    };

    wsRef.current = ws;
  }

  return (
    <div
      className={[
        "flex-1 min-h-0 border border-[var(--border-subtle)] overflow-hidden relative",
        isVisible ? "" : "hidden",
      ].join(" ")}
    >
      <div ref={containerRef} className="h-full w-full bg-black" />
    </div>
  );
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/terminal/TerminalPanel.test.tsx`
Expected: PASS, 4 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/terminal/TerminalPanel.tsx web/src/terminal/TerminalPanel.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): add TerminalPanel component with basic WS lifecycle

TerminalPanel owns one terminal session's xterm, WebSocket, paste
handler, and resize observer. Reports connecting/connected status
via onStatusChange and opens its WS eagerly (independent of
isVisible) so reload reconnects every tab, not just the active one.
EOF
)"
```

---

## Task 3: `TerminalPanel` — reconnect with exponential backoff + jitter

**Files:**
- Modify: `web/src/terminal/TerminalPanel.tsx` (extend `ws.onclose` and add `scheduleRetry`)
- Test: `web/src/terminal/TerminalPanel.test.tsx` (add tests)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/terminal/TerminalPanel.test.tsx`, inside the existing `describe` block:

```tsx
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/terminal/TerminalPanel.test.tsx`
Expected: FAIL — `reconnecting` status never reported; only 1 WS instance.

- [ ] **Step 3: Add the reconnect logic**

In `web/src/terminal/TerminalPanel.tsx`:

a) Add these helpers inside the component, above `connect`:
```tsx
  const delayFor = (attempt: number): number => {
    const base = Math.min(1000 * Math.pow(2, attempt), 30_000);
    const jitter = 0.8 + Math.random() * 0.4;
    return base * jitter;
  };

  const scheduleRetry = () => {
    if (reconnectRef.current.stopped) return;
    const attempt = reconnectRef.current.attempt;
    reconnectRef.current.attempt = attempt + 1;
    reconnectRef.current.timer = window.setTimeout(() => {
      reconnectRef.current.timer = null;
      connect();
    }, delayFor(attempt));
  };
```

b) Replace the body of `ws.onclose` with:
```tsx
    ws.onclose = () => {
      if (reconnectRef.current.stopped) return;
      onStatusChange("reconnecting");
      // This will gain exit detection in the next task. For now, always retry.
      scheduleRetry();
    };
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/terminal/TerminalPanel.test.tsx`
Expected: PASS, 6 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/terminal/TerminalPanel.tsx web/src/terminal/TerminalPanel.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): auto-reconnect TerminalPanel on WS drop

Schedules retries with exponential backoff (1/2/4/8/16/30s) and
20% jitter, resetting the attempt counter on each successful
open. reconnect.stopped gates both onclose and the pending timer
so unmount is a clean signal.
EOF
)"
```

---

## Task 4: `TerminalPanel` — exit detection stops retry

**Files:**
- Modify: `web/src/terminal/TerminalPanel.tsx` (make `onclose` refetch session list)
- Test: `web/src/terminal/TerminalPanel.test.tsx` (add tests)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/terminal/TerminalPanel.test.tsx`:

```tsx
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/terminal/TerminalPanel.test.tsx`
Expected: FAIL — `exited` status never reported.

- [ ] **Step 3: Add exit detection to `ws.onclose`**

In `web/src/terminal/TerminalPanel.tsx`:

a) Add import at top (was deferred from Task 3):
```ts
import { listSessions } from "@/api/sandboxSessions";
```

b) Replace the `ws.onclose` assignment with:

```tsx
    ws.onclose = async () => {
      if (reconnectRef.current.stopped) return;
      onStatusChange("reconnecting");
      try {
        const all = await listSessions(sandboxId);
        if (reconnectRef.current.stopped) return;
        const me = all.find((s) => s.SessionID === sessionId);
        if (!me || me.State === "exited" || me.State === "destroyed") {
          reconnectRef.current.stopped = true;
          onStatusChange("exited");
          return;
        }
      } catch {
        // Server unreachable — treat as network blip and retry.
      }
      scheduleRetry();
    };
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/terminal/TerminalPanel.test.tsx`
Expected: PASS, 9 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/terminal/TerminalPanel.tsx web/src/terminal/TerminalPanel.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): detect session exit in TerminalPanel reconnect path

On ws.close, refetch listSessions to distinguish a network blip
(retry) from a terminated session (stop retrying, report exited).
Falls back to retry when the refetch itself fails so server
unreachability doesn't produce false-exit states.
EOF
)"
```

---

## Task 5: `TerminalPanel` — failed state + manual reconnect

**Files:**
- Modify: `web/src/terminal/TerminalPanel.tsx` (cap attempts, add failed state, add manual reconnect trigger)
- Test: `web/src/terminal/TerminalPanel.test.tsx` (add test)

- [ ] **Step 1: Write the failing test**

Append to `web/src/terminal/TerminalPanel.test.tsx`:

```tsx
  it("transitions to failed after 8 attempts and reconnects on manual trigger", async () => {
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

    const { onStatusChange, getByRole } = renderPanel();
    // Each iteration closes the current ws WITHOUT opening it (simulates
    // the server refusing the upgrade). ws.onopen is what would reset
    // attempt to 0; without it, attempt increments monotonically. After
    // 9 closes (attempts 0..8), scheduleRetry hits the cap and reports
    // failed instead of creating a 10th ws.
    for (let i = 0; i < 9; i++) {
      const ws = MockWebSocket.instances[i];
      ws.simulateClose();
      await vi.runAllTimersAsync();
    }

    expect(onStatusChange).toHaveBeenCalledWith("failed");
    expect(MockWebSocket.instances.length).toBe(9);

    vi.useRealTimers();
    const btn = getByRole("button", { name: /reconnect/i });
    btn.click();
    expect(MockWebSocket.instances.length).toBe(10);
  });
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/terminal/TerminalPanel.test.tsx`
Expected: FAIL — no `failed` status, no Reconnect button.

- [ ] **Step 3: Add attempt cap + failed state + manual reconnect**

In `web/src/terminal/TerminalPanel.tsx`:

a) Add a React state for current status (so we can render overlays based on it). At the top of the component:

```tsx
import { useEffect, useRef, useState, useCallback } from "react";
// ...existing imports...

const MAX_ATTEMPTS = 8;
```

b) Inside the component, near other refs:

```tsx
  const [status, setStatus] = useState<PanelStatus>("connecting");

  const updateStatus = useCallback(
    (s: PanelStatus) => {
      setStatus(s);
      onStatusChange(s);
    },
    [onStatusChange],
  );
```

Replace every `onStatusChange(...)` call inside `connect` and `ws.onclose` with `updateStatus(...)`.

c) Change `scheduleRetry` to check the cap:

```tsx
  const scheduleRetry = () => {
    if (reconnectRef.current.stopped) return;
    const attempt = reconnectRef.current.attempt;
    if (attempt >= MAX_ATTEMPTS) {
      updateStatus("failed");
      return;
    }
    reconnectRef.current.attempt = attempt + 1;
    reconnectRef.current.timer = window.setTimeout(() => {
      reconnectRef.current.timer = null;
      connect();
    }, delayFor(attempt));
  };
```

d) Add a `manualReconnect` handler and render the "Reconnect" button when `status === "failed"`:

```tsx
  const manualReconnect = () => {
    reconnectRef.current.attempt = 0;
    reconnectRef.current.stopped = false;
    connect();
  };
```

Update the returned JSX:

```tsx
  return (
    <div
      className={[
        "flex-1 min-h-0 border border-[var(--border-subtle)] overflow-hidden relative",
        isVisible ? "" : "hidden",
      ].join(" ")}
    >
      <div ref={containerRef} className="h-full w-full bg-black" />
      {status === "failed" && (
        <div className="absolute inset-0 flex items-center justify-center bg-black/70">
          <div className="flex flex-col items-center gap-3">
            <span className="font-mono text-xs text-[var(--fg-secondary)]">
              Disconnected
            </span>
            <button
              type="button"
              onClick={manualReconnect}
              className="px-3 py-1.5 text-xs border border-[var(--border-subtle)] text-[var(--fg-primary)] hover:bg-[var(--bg-secondary)]"
            >
              Reconnect
            </button>
          </div>
        </div>
      )}
    </div>
  );
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/terminal/TerminalPanel.test.tsx`
Expected: PASS, 10 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/terminal/TerminalPanel.tsx web/src/terminal/TerminalPanel.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): cap TerminalPanel reconnect attempts, add manual retry

After 8 failed attempts (~2 min of backoff), the panel enters
the failed state and shows a "Disconnected / Reconnect" overlay.
Clicking Reconnect resets the state machine and starts fresh.
EOF
)"
```

---

## Task 6: `TerminalPanel` — reconnecting + exited overlays

**Files:**
- Modify: `web/src/terminal/TerminalPanel.tsx` (render reconnecting pill + exited overlay)
- Test: `web/src/terminal/TerminalPanel.test.tsx` (add tests)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/terminal/TerminalPanel.test.tsx`:

```tsx
  it("shows a reconnecting pill with attempt count while retrying", async () => {
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

    const { findByText } = renderPanel();
    MockWebSocket.instances[0].simulateOpen();
    MockWebSocket.instances[0].simulateClose();
    await vi.advanceTimersByTimeAsync(0);
    vi.useRealTimers();
    expect(await findByText(/reconnecting/i)).toBeInTheDocument();
  });

  it("shows 'Session ended' overlay when exited", async () => {
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

    const { findByText } = renderPanel();
    MockWebSocket.instances[0].simulateOpen();
    MockWebSocket.instances[0].simulateClose();
    expect(await findByText(/session ended/i)).toBeInTheDocument();
  });
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/terminal/TerminalPanel.test.tsx`
Expected: FAIL — overlays not rendered.

- [ ] **Step 3: Add reconnecting + exited overlays**

Replace the JSX return in `web/src/terminal/TerminalPanel.tsx` with:

```tsx
  return (
    <div
      className={[
        "flex-1 min-h-0 border border-[var(--border-subtle)] overflow-hidden relative",
        isVisible ? "" : "hidden",
      ].join(" ")}
    >
      <div ref={containerRef} className="h-full w-full bg-black" />
      {status === "reconnecting" && (
        <div className="absolute top-2 right-2 px-2 py-0.5 text-[10px] font-mono text-[var(--fg-secondary)] bg-[var(--bg-primary)]/80 border border-[var(--border-subtle)]">
          Reconnecting… ({reconnectRef.current.attempt}/{MAX_ATTEMPTS})
        </div>
      )}
      {status === "exited" && (
        <div className="absolute inset-0 flex items-center justify-center bg-black/70">
          <div className="flex flex-col items-center gap-1">
            <span className="font-mono text-xs text-[var(--fg-primary)]">
              Session ended
            </span>
            <span className="font-mono text-[10px] text-[var(--fg-muted)]">
              Close the tab to remove it.
            </span>
          </div>
        </div>
      )}
      {status === "failed" && (
        <div className="absolute inset-0 flex items-center justify-center bg-black/70">
          <div className="flex flex-col items-center gap-3">
            <span className="font-mono text-xs text-[var(--fg-secondary)]">
              Disconnected
            </span>
            <button
              type="button"
              onClick={manualReconnect}
              className="px-3 py-1.5 text-xs border border-[var(--border-subtle)] text-[var(--fg-primary)] hover:bg-[var(--bg-secondary)]"
            >
              Reconnect
            </button>
          </div>
        </div>
      )}
    </div>
  );
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/terminal/TerminalPanel.test.tsx`
Expected: PASS, 12 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/terminal/TerminalPanel.tsx web/src/terminal/TerminalPanel.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): TerminalPanel overlays for reconnecting & exited states

Small top-right pill during reconnect attempts (shows attempt
count), full-panel "Session ended" overlay when the shell has
exited. Complements the existing failed/Reconnect overlay.
EOF
)"
```

---

## Task 7: Refactor `Terminal.tsx` to use `TerminalPanel` + `useLastActiveSession`

**Files:**
- Modify: `web/src/routes/Terminal.tsx` (full refactor)

This task is a pure refactor with no new tests — tests land in Tasks 8 & 9. The existing file has no tests today (verified).

- [ ] **Step 1: Replace `Terminal.tsx` contents**

Write to `web/src/routes/Terminal.tsx`:

```tsx
import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { listSessions, createSession, destroySession } from "@/api/sandboxSessions";
import { useLastActiveSession } from "@/hooks/useLastActiveSession";
import TerminalPanel, { type PanelStatus } from "@/terminal/TerminalPanel";
import type { Session } from "@/types/navaris";

const MAX_UI_SESSIONS = 5;

// Terminal opens tmux-backed sessions inside a sandbox and presents them as
// tabs. On mount it lists existing sessions, auto-creates one if all live
// sessions have exited, and renders a TerminalPanel per session. Each
// panel owns its own WebSocket lifecycle, so eager attach on reload falls
// out of the tree shape — every mounted panel connects immediately.
export default function Terminal() {
  const { id } = useParams<{ id: string }>();
  const [sessions, setSessions] = useState<Session[]>([]);
  const [sessionLabels, setSessionLabels] = useState<Map<string, number>>(new Map());
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
  const [panelStatus, setPanelStatus] = useState<Map<string, PanelStatus>>(new Map());
  const [loading, setLoading] = useState(true);
  const [statusFlash, setStatusFlash] = useState<string | null>(null);
  const [destroyTarget, setDestroyTarget] = useState<Session | null>(null);

  const destroyDialogRef = useRef<HTMLDialogElement>(null);
  const { read: readActive, write: writeActive, clear: clearActive } =
    useLastActiveSession(id ?? "");

  // --- data fetching + auto-create ---
  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    (async () => {
      try {
        const all = await listSessions(id);

        // Stable labels: assign from full history sorted by creation time.
        const sorted = [...all].sort((a, b) => a.CreatedAt.localeCompare(b.CreatedAt));
        const labels = new Map<string, number>();
        sorted.forEach((s, i) => labels.set(s.SessionID, i + 1));

        // visible includes exited sessions so the user sees "Session ended"
        // tabs until they close them manually.
        let visible = all.filter((s) => s.State !== "destroyed");

        const allExitedOrEmpty =
          visible.length === 0 || visible.every((s) => s.State === "exited");
        if (allExitedOrEmpty) {
          const created = await createSession(id);
          labels.set(created.SessionID, labels.size + 1);
          visible = [...visible, created];
        }
        if (cancelled) return;

        setSessionLabels(labels);
        setSessions(visible);

        // Initial active pick: remembered id wins if it still exists and
        // isn't exited. Otherwise sort non-exited first, then LastAttachedAt
        // desc, then take the head.
        const remembered = readActive();
        const rememberedMatch = remembered
          ? visible.find((s) => s.SessionID === remembered && s.State !== "exited")
          : undefined;
        let pick: string | null = rememberedMatch?.SessionID ?? null;
        if (!pick) {
          const sortedPick = [...visible].sort((a, b) => {
            const aExited = a.State === "exited" ? 1 : 0;
            const bExited = b.State === "exited" ? 1 : 0;
            if (aExited !== bExited) return aExited - bExited;
            const aTime = a.LastAttachedAt ?? a.CreatedAt;
            const bTime = b.LastAttachedAt ?? b.CreatedAt;
            return bTime.localeCompare(aTime);
          });
          pick = sortedPick[0]?.SessionID ?? null;
        }
        setActiveSessionId(pick);
        setLoading(false);
      } catch {
        if (!cancelled) {
          setStatusFlash("Failed to load sessions");
          setLoading(false);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [id, readActive]);

  const handleTabClick = useCallback(
    (s: Session) => {
      setActiveSessionId(s.SessionID);
      writeActive(s.SessionID);
    },
    [writeActive],
  );

  const handlePanelStatus = useCallback(
    (sessionId: string, status: PanelStatus) => {
      setPanelStatus((prev) => {
        const next = new Map(prev);
        next.set(sessionId, status);
        return next;
      });
    },
    [],
  );

  const handleNewSession = useCallback(async () => {
    if (!id || sessions.length >= MAX_UI_SESSIONS) return;
    try {
      const created = await createSession(id);
      setSessionLabels((prev) => {
        const next = new Map(prev);
        next.set(created.SessionID, prev.size + 1);
        return next;
      });
      setSessions((prev) => [...prev, created]);
      setActiveSessionId(created.SessionID);
      writeActive(created.SessionID);
    } catch {
      setStatusFlash("Failed to create session");
      setTimeout(() => setStatusFlash(null), 3000);
    }
  }, [id, sessions.length, writeActive]);

  const openDestroyDialog = useCallback((s: Session) => {
    setDestroyTarget(s);
    destroyDialogRef.current?.showModal();
  }, []);

  const cancelDestroy = useCallback(() => {
    destroyDialogRef.current?.close();
    setDestroyTarget(null);
  }, []);

  const confirmDestroy = useCallback(async () => {
    if (!destroyTarget) return;
    const sessionId = destroyTarget.SessionID;
    try {
      await destroySession(sessionId);
    } catch {
      setStatusFlash("Failed to close session");
      setTimeout(() => setStatusFlash(null), 3000);
      destroyDialogRef.current?.close();
      setDestroyTarget(null);
      return;
    }

    if (readActive() === sessionId) clearActive();

    setSessions((prev) => {
      const next = prev.filter((s) => s.SessionID !== sessionId);
      if (activeSessionId === sessionId && next.length > 0) {
        setActiveSessionId(next[0].SessionID);
        writeActive(next[0].SessionID);
      }
      return next;
    });
    setPanelStatus((prev) => {
      const next = new Map(prev);
      next.delete(sessionId);
      return next;
    });
    destroyDialogRef.current?.close();
    setDestroyTarget(null);
  }, [destroyTarget, activeSessionId, readActive, clearActive, writeActive]);

  if (loading) {
    return (
      <div className="flex h-full flex-col">
        <header className="flex items-center justify-between pb-3 border-b border-[var(--border-subtle)] mb-3">
          <div>
            <Link
              to={`/sandboxes/${id}`}
              className="font-mono text-[10px] tracking-[0.04em] text-[var(--fg-muted)] hover:text-[var(--fg-primary)]"
            >
              &larr; detail
            </Link>
            <h1 className="mt-1 text-lg font-medium">Terminal</h1>
          </div>
        </header>
        <div className="flex-1 flex items-center justify-center text-sm text-[var(--fg-muted)]">
          Preparing session&hellip;
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center justify-between pb-3 border-b border-[var(--border-subtle)] mb-3">
        <div>
          <Link
            to={`/sandboxes/${id}`}
            className="font-mono text-[10px] tracking-[0.04em] text-[var(--fg-muted)] hover:text-[var(--fg-primary)]"
          >
            &larr; detail
          </Link>
          <h1 className="mt-1 text-lg font-medium">Terminal</h1>
          <div className="mt-0.5 font-mono text-[10px] text-[var(--fg-muted)]">{id}</div>
        </div>
        <span className="font-mono text-[10px] text-[var(--fg-muted)]">
          {statusFlash ?? `${sessions.length} session${sessions.length !== 1 ? "s" : ""}`}
        </span>
      </header>

      {/* Tab bar */}
      <div className="flex items-center gap-0 border-b border-[var(--border-subtle)]">
        {sessions.map((s) => {
          const status = panelStatus.get(s.SessionID) ?? "connecting";
          const isExited = status === "exited" || s.State === "exited";
          const isReconnecting = status === "reconnecting";
          const isFailed = status === "failed";
          const alwaysShowClose = isExited;
          return (
            <button
              key={s.SessionID}
              type="button"
              onClick={() => handleTabClick(s)}
              className={[
                "flex items-center gap-1.5 px-3 py-1.5 font-mono text-[11px] border-r border-[var(--border-subtle)]",
                s.SessionID === activeSessionId
                  ? "text-[var(--fg-primary)] bg-[var(--bg-primary)]"
                  : "text-[var(--fg-muted)] hover:text-[var(--fg-secondary)]",
                isExited ? "opacity-60 line-through" : "",
              ].join(" ")}
            >
              <span>Session {sessionLabels.get(s.SessionID) ?? "?"}</span>
              {isReconnecting && (
                <span
                  aria-label="reconnecting"
                  className="text-[9px] text-[var(--status-pending)]"
                >
                  &bull;
                </span>
              )}
              {isFailed && (
                <span
                  aria-label="disconnected"
                  className="text-[9px] text-[var(--status-failed)]"
                >
                  &bull;
                </span>
              )}
              {(sessions.length > 1 || alwaysShowClose) && (
                <span
                  role="button"
                  tabIndex={0}
                  onClick={(e) => {
                    e.stopPropagation();
                    openDestroyDialog(s);
                  }}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.stopPropagation();
                      openDestroyDialog(s);
                    }
                  }}
                  className="ml-1 text-[9px] text-[var(--fg-muted)] hover:text-[var(--status-failed)] cursor-pointer"
                >
                  &times;
                </span>
              )}
            </button>
          );
        })}
        {sessions.length < MAX_UI_SESSIONS && (
          <button
            type="button"
            onClick={handleNewSession}
            className="px-2 py-1.5 font-mono text-[11px] text-[var(--fg-muted)] hover:text-[var(--fg-primary)]"
          >
            +
          </button>
        )}
      </div>

      {/* Copy/paste hint */}
      <div className="px-2 py-0.5 text-[10px] font-mono text-[var(--fg-muted)] border-b border-[var(--border-subtle)] bg-[var(--bg-secondary)]">
        Shift+Select to copy &middot; Ctrl+Shift+V to paste
      </div>

      {/* Terminal panels — one per session, hidden if not active. */}
      {sessions.map((s) => (
        <TerminalPanel
          key={s.SessionID}
          sandboxId={id!}
          sessionId={s.SessionID}
          isVisible={s.SessionID === activeSessionId}
          initialSessionState={s.State}
          onStatusChange={(st) => handlePanelStatus(s.SessionID, st)}
        />
      ))}

      {/* Destroy confirmation dialog */}
      <dialog
        ref={destroyDialogRef}
        onClick={(e) => {
          if (e.target === e.currentTarget) cancelDestroy();
        }}
        className="fixed inset-0 m-auto backdrop:bg-black/50 bg-[var(--bg-primary)] border border-[var(--border-subtle)] p-0 max-w-sm w-full h-fit"
      >
        <div className="p-6">
          <h2 className="text-sm font-medium text-[var(--fg-primary)] mb-2">Close session</h2>
          <p className="text-xs text-[var(--fg-secondary)] mb-1">
            Are you sure you want to close{" "}
            <span className="font-medium text-[var(--fg-primary)]">
              Session {destroyTarget ? sessionLabels.get(destroyTarget.SessionID) ?? "?" : "?"}
            </span>
            ?
          </p>
          <p className="text-xs text-[var(--fg-muted)] mb-5">
            Running processes in this session will be terminated.
          </p>
          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={cancelDestroy}
              className="px-3 py-1.5 text-xs border border-[var(--border-subtle)] text-[var(--fg-secondary)] hover:text-[var(--fg-primary)]"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={confirmDestroy}
              className="px-3 py-1.5 text-xs border border-[var(--status-failed)] bg-[var(--status-failed)] text-white hover:opacity-90"
            >
              Close
            </button>
          </div>
        </div>
      </dialog>
    </div>
  );
}
```

- [ ] **Step 2: Verify no type errors and existing build still passes**

Run: `cd web && npx tsc --noEmit && npx vitest run`
Expected: TypeScript clean. All existing tests pass (the hook + panel tests you wrote earlier; route tests come next).

- [ ] **Step 3: Commit**

```bash
git add web/src/routes/Terminal.tsx
git commit -m "$(cat <<'EOF'
refactor(web): split Terminal.tsx into parent + TerminalPanel

Parent shrinks to data loading + tab bar + destroy dialog. Each
session renders a TerminalPanel that owns its own WS, xterm,
and reconnect state. Eager attach is automatic: every live
session mounts a panel which opens its WS on mount.

Adds "last-focused tab" persistence via useLastActiveSession,
keeps exited sessions visible with greyed tab styling and an
always-visible close button.
EOF
)"
```

---

## Task 8: `Terminal.tsx` — route-level tests for load/restore/eager-attach

**Files:**
- Create: `web/src/routes/Terminal.test.tsx`

- [ ] **Step 1: Write the failing tests**

Create `web/src/routes/Terminal.test.tsx`:

```tsx
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
    CreatedAt: `2026-04-14T00:00:0${id.slice(-1)}Z`,
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
});
```

- [ ] **Step 2: Run tests to verify they fail or pass**

Run: `cd web && npx vitest run src/routes/Terminal.test.tsx`
Expected: PASS (since Task 7 already implemented the logic).

If any test fails, it indicates a bug in Task 7's refactor — fix by editing `Terminal.tsx` directly before continuing. Do not weaken the tests.

- [ ] **Step 3: Commit**

```bash
git add web/src/routes/Terminal.test.tsx
git commit -m "$(cat <<'EOF'
test(web): route-level tests for Terminal reload restore

Covers eager attach of every live session, remembered-tab pick,
fallback ordering when the remembered id is gone or exited, and
click-to-remember localStorage writes.
EOF
)"
```

---

## Task 9: `Terminal.tsx` — exited session UI behavior

**Files:**
- Modify: `web/src/routes/Terminal.test.tsx` (add tests)

- [ ] **Step 1: Write the failing tests**

Append to `web/src/routes/Terminal.test.tsx`, inside the existing `describe` block:

```tsx
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
```

- [ ] **Step 2: Run tests to verify**

Run: `cd web && npx vitest run src/routes/Terminal.test.tsx`
Expected: PASS (Task 7 already implements these behaviors). If any fail, fix `Terminal.tsx` — don't weaken tests.

- [ ] **Step 3: Commit**

```bash
git add web/src/routes/Terminal.test.tsx
git commit -m "$(cat <<'EOF'
test(web): Terminal exited-session + destroy behaviors

Verifies exited tabs render greyed with always-visible close,
auto-create fires when every session has exited, and destroying
the remembered active session clears localStorage so the next
reload uses the fallback pick.
EOF
)"
```

---

## Task 10: Manual smoke doc update

**Files:**
- Modify: `web/MANUAL_TERMINAL_SMOKE.md`

- [ ] **Step 1: Append manual steps**

Append to `web/MANUAL_TERMINAL_SMOKE.md`:

```markdown

## Reload & resilience smoke

1. **Reload preserves tabs + active tab**
   - Open 3 tabs. Click Session 2 so it's active.
   - Reload the page.
   - Verify: all 3 tabs present; Session 2 is the active tab.

2. **Auto-reconnect on server blip**
   - Attach to a session, run `while true; do date; sleep 1; done`.
   - Stop `navarisd` for 10 seconds, then restart it.
   - Verify: panel shows a small "Reconnecting…" pill during the outage; resumes streaming once `navarisd` is back; tab bar shows a yellow dot while reconnecting.

3. **Exited session UI**
   - Open 2 tabs. In Session 1, type `exit` to terminate the shell.
   - Verify: Session 1's tab becomes greyed with a line-through; the panel shows "Session ended"; Session 2 is unaffected.
   - Click Session 1's × to remove the tab.
   - Verify: only Session 2 remains.
```

- [ ] **Step 2: Commit**

```bash
git add web/MANUAL_TERMINAL_SMOKE.md
git commit -m "$(cat <<'EOF'
docs(web): smoke steps for terminal reload & reconnect resilience
EOF
)"
```

---

## Verification checklist

Before declaring the feature done, verify all of these pass:

- [ ] `cd web && npx vitest run` — all tests green.
- [ ] `cd web && npx tsc --noEmit` — no type errors.
- [ ] `cd web && npm run build` — production build succeeds.
- [ ] Manual smoke steps 1–3 in `MANUAL_TERMINAL_SMOKE.md` pass against a running `navarisd`.
