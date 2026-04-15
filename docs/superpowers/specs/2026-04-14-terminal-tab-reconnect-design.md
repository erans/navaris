# Terminal tab reconnect & resilience — design spec

**Date:** 2026-04-14
**Scope:** `web/src/routes/Terminal.tsx` and supporting hooks/components
**Related:** `2026-04-09-tabbed-terminal-sessions-design.md` (original tabbed terminal design)

## Problem

Today, reloading the Terminal UI partially reconnects:

- All non-destroyed, non-exited server-side sessions get tabs rebuilt (good — tmux keeps them alive).
- Only the **active** tab opens a WebSocket. Other tabs are lazy-attached on first click.
- The active tab on reload is picked by server-side `LastAttachedAt`, which is not necessarily the tab the user last had focused in the UI.
- Sessions whose shell has exited are silently filtered out — the user has no feedback explaining why a tab disappeared.
- When a WebSocket drops (network blip, server restart) there is no retry — the tab is effectively dead until the user reloads the page.

## Goals

1. **Eager attach on reload.** All live tabs open their WebSockets immediately so switching tabs is instant and background output continues to flow.
2. **Persist last-focused tab.** The user lands on the tab they were last looking at, per-sandbox.
3. **Surface exited sessions.** Greyed-out tabs with a clear "Session ended" panel replace silent filtering, with an always-visible close affordance.
4. **Auto-reconnect on drop.** Exponential backoff with jitter, bounded attempts, manual "Reconnect" fallback on failure.

## Non-goals

- Client-side persistence of xterm scrollback across page reloads (tmux replay continues to be the source of truth).
- Reopening exited sessions in-place (user can create a new tab with `+`).
- Server protocol changes — everything works against the current `/v1/sandboxes/{id}/attach` WebSocket and `/v1/sandboxes/{id}/sessions` REST endpoint.

## Architecture

The current `Terminal.tsx` (391 lines) mixes five concerns: data loading, xterm lifecycle, WebSocket lifecycle, tab-bar UI, and the destroy dialog. The new features would push it past 600 lines and entangle per-session state machines with the parent's tab-list state. Splitting along natural seams:

### Files

| File | Purpose |
|------|---------|
| `web/src/hooks/useLastActiveSession.ts` *(new)* | Read/write per-sandbox "last active tab id" to localStorage. |
| `web/src/terminal/TerminalPanel.tsx` *(new)* | One component per session. Owns xterm, WebSocket, reconnect state, paste handler. Renders the xterm mount div plus status overlays. |
| `web/src/routes/Terminal.tsx` *(refactored, ~180 lines)* | Data loading, tab bar, destroy dialog, active-tab tracking, per-session status aggregation. No direct xterm/WS management. |

Each panel owns a WebSocket for its entire mount lifetime → eager attach falls out naturally (every live session has a mounted panel, so every panel opens its WS on mount).

## Components

### `useLastActiveSession(sandboxId)`

Modeled exactly after `useLastProject.ts`. Same shape, same try/catch fallback, same stable refs via `useCallback`.

```ts
const keyFor = (sandboxId: string) => `navaris.terminal.${sandboxId}.activeSession`;

export function useLastActiveSession(sandboxId: string) {
  const read = useCallback((): string | null => {
    try { return localStorage.getItem(keyFor(sandboxId)); } catch { return null; }
  }, [sandboxId]);

  const write = useCallback((sessionId: string): void => {
    try { localStorage.setItem(keyFor(sandboxId), sessionId); } catch {}
  }, [sandboxId]);

  const clear = useCallback((): void => {
    try { localStorage.removeItem(keyFor(sandboxId)); } catch {}
  }, [sandboxId]);

  return { read, write, clear };
}
```

### `<TerminalPanel>` component

**Props:**

```ts
interface TerminalPanelProps {
  sandboxId: string;
  sessionId: string;
  isVisible: boolean;
  initialSessionState: SessionState;
  onStatusChange: (status: PanelStatus) => void;
}

type PanelStatus = "connecting" | "connected" | "reconnecting" | "exited" | "failed";
```

**Internal state (refs, not React state, to avoid re-renders for WS traffic):**

- `term: XTerm`, `fit: FitAddon`, `ro: ResizeObserver` — created once on mount.
- `ws: WebSocket | null` — replaced across reconnects.
- `reconnect: { attempt: number; timer: number | null; stopped: boolean }` — reconnect bookkeeping. `stopped = true` when unmounting or when the session is confirmed exited.

**Render output (in order, inside the panel's root div):**

- If `status === "exited"`: centered overlay — "Session ended" message. Close action lives in the parent tab bar's `×`.
- If `status === "reconnecting"`: subtle top-right pill — "Reconnecting… (N/8)".
- If `status === "failed"`: centered overlay — "Disconnected" + "Reconnect" button.
- Always: the xterm mount div (`bg-black`, `h-full w-full`), kept mounted across all states so the buffer is preserved.

**Unmount cleanup:** `reconnect.stopped = true`, clear pending `reconnect.timer`, remove paste listener, disconnect `ro`, close `ws` (which will fire onclose but `stopped` blocks retry), dispose `term`.

### `Terminal.tsx` (parent, refactored)

**State:**

```ts
const [sessions, setSessions] = useState<Session[]>([]);
const [sessionLabels, setSessionLabels] = useState<Map<string, number>>(new Map());
const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
const [panelStatus, setPanelStatus] = useState<Map<string, PanelStatus>>(new Map());
const [loading, setLoading] = useState(true);
const [statusFlash, setStatusFlash] = useState<string | null>(null);
const [destroyTarget, setDestroyTarget] = useState<Session | null>(null);
const { read: readActive, write: writeActive, clear: clearActive } = useLastActiveSession(id ?? "");
```

The `terminalInstances` / `terminalContainers` refs disappear — panels own that now.

**Load effect:**

1. `listSessions(id)` → `all`.
2. Compute stable labels from full creation-time-sorted history (unchanged from today).
3. `visible = all.filter(s => s.State !== "destroyed")` — includes `exited` so tabs remain until user closes them.
4. If `visible.every(s => s.State === "exited")` or `visible.length === 0`: auto-create one session.
5. `setSessions(visible)`.
6. Initial active: `readActive()` if it exists in `visible` and its state is not `exited`; else fall back: sort `visible` with non-exited first, then by `LastAttachedAt` desc (so an exited session is only selected if no live session exists).
7. `setActiveSessionId(pick)`; `setLoading(false)`.

**Tab click handler:**

```ts
const handleTabClick = (s: Session) => {
  setActiveSessionId(s.SessionID);
  writeActive(s.SessionID);
};
```

**Tab styling by status:**

- `exited`: `opacity-60 line-through`; `×` always visible regardless of tab count.
- `reconnecting`: small dot in `var(--status-pending)`.
- `failed`: small dot in `var(--status-failed)`.

**Destroy handler:**

- Calls `destroySession(sessionId)`.
- Removes session from `sessions` + `panelStatus`. Panel unmounts; panel's own cleanup closes WS and disposes term.
- If destroyed id equals the remembered active id, `clearActive()`.
- Active-tab fallback logic unchanged from today.

**Panel rendering:**

```tsx
{sessions.map((s) => (
  <TerminalPanel
    key={s.SessionID}
    sandboxId={id!}
    sessionId={s.SessionID}
    isVisible={s.SessionID === activeSessionId}
    initialSessionState={s.State}
    onStatusChange={(st) =>
      setPanelStatus((prev) => new Map(prev).set(s.SessionID, st))
    }
  />
))}
```

Panel handles its own `hidden` class based on `isVisible`.

## Reconnect state machine

Per-panel state transitions:

```
 mount
   │
   ▼
connecting ─── ws.onopen ──▶ connected
   │                           │
   │                           │ ws.onclose
   │                           ▼
   │                      (refetch sessions)
   │                       │         │
   │                       │         └──▶ session missing/exited/destroyed
   │                       │              │
   │                       │              ▼
   │                       │            exited  (terminal)
   │                       ▼
   │                   session still live
   │                       │
   │                       ▼
   │                   reconnecting ──▶ schedule connect(attempt+1)
   │                       │                   │
   │                       │                   └──▶ ws.onopen ──▶ connected
   │                       │
   │                       └── attempt > 8 ──▶ failed
   │                                              │
   │                                              │ user clicks "Reconnect"
   │                                              ▼
   └──────────────────────────────────────── connecting
```

**Backoff schedule:** `delayFor(n) = min(1000 * 2^n, 30_000)` with jitter `* (0.8 + Math.random() * 0.4)`. 8 attempts total (~2 min) before `failed`.

**Exit detection (on `ws.close`):** any `SessionState` other than `exited` or `destroyed` (i.e., `active` or `detached`) is treated as "still live" and triggers retry.

```ts
async function handleClose() {
  if (reconnect.stopped) return;
  try {
    const all = await listSessions(sandboxId);
    const me = all.find((s) => s.SessionID === sessionId);
    if (!me || me.State === "exited" || me.State === "destroyed") {
      reconnect.stopped = true;
      onStatusChange("exited");
      return;
    }
  } catch {
    // listSessions itself failed (server unreachable). Treat as a blip and retry.
  }
  scheduleRetry();
}
```

**Intentional closes:** parent destroy triggers panel unmount, which sets `reconnect.stopped = true` *before* `ws.close()`. The resulting onclose does nothing. No separate `intentionalClose` flag needed.

**Onopen:** resets `reconnect.attempt = 0`, status → `"connected"`, sends initial resize. A clean reconnect does not carry backoff debt into its next drop.

## Data flow

```
listSessions(sandboxId) ────▶ Terminal.tsx
                              │
                              │ sessions, activeSessionId
                              ▼
                          <TerminalPanel> (×N)
                              │
                              │ WebSocket /v1/sandboxes/{id}/attach?session={sid}
                              ▼
                            tmux ptmx
```

On `ws.close` → panel refetches `listSessions` for exit detection → panel calls `onStatusChange` → parent rerenders tab styling.

## Error handling

| Failure mode | Handling |
|--------------|----------|
| `listSessions` fails on initial load | Unchanged: show "Failed to load sessions" flash, `loading = false`. |
| `createSession` fails on auto-create | Unchanged: show error flash. |
| `ws.onopen` never fires (immediate close) | Counts as attempt #1 of retry. Falls through to `ws.onclose` handler. |
| `ws.onerror` fires | Log only. `onclose` always follows and drives the state machine. |
| `listSessions` fails during exit-detection refetch | Treated as blip → retry. Next drop will refetch again. |
| All 8 retries exhausted | Status → `failed`. Manual "Reconnect" button. |
| `localStorage.setItem` throws (quota, private mode) | `useLastActiveSession.write` silently no-ops. Next reload uses fallback pick. |
| Browser tab backgrounded | No special handling. WebSocket stays open. Browsers may throttle timers but not WebSocket delivery. |

## Edge cases & known behaviors

- **tmux replay on reconnect:** after successful reconnect, tmux replays its scrollback. The xterm buffer shows duplicated content (pre-drop + replay). Inherent to tmux-attach semantics; not fixed by this design.
- **Multiple panels reconnecting simultaneously (server restart):** jitter (±20%) smooths the thundering herd.
- **`isVisible` toggle during reconnecting:** existing `ResizeObserver` fires when `hidden` is removed, re-running `fit.fit()`. No extra logic needed.
- **User destroys a session while it's reconnecting:** panel unmounts, `reconnect.stopped = true`, pending timer cleared. Clean.
- **Active tab was exited at load time:** parent's initial-active pick prefers non-exited tabs, so user lands on a live tab by default even if their remembered tab exited while they were away.

## Testing

### Unit tests

**`web/src/hooks/useLastActiveSession.test.ts`** (mirrors `useLastProject.test.ts`):
- `write(id)` then `read()` returns the id.
- `read()` with empty storage returns `null`.
- Storage-disabled path (mock `localStorage.setItem` to throw) — `write` doesn't throw, `read` returns `null`.
- `clear()` removes the entry.
- Different `sandboxId` values use different keys.

**`web/src/terminal/TerminalPanel.test.tsx`** (new):
- Mock `WebSocket` globally; stub `listSessions`.
- On mount, opens WS with correct URL, reports `connecting` → `connected`.
- On `ws.close` with live session, reports `reconnecting` and schedules retry.
- On `ws.close` with exited session in refetch, reports `exited` and does NOT schedule retry.
- After 8 failed attempts, reports `failed`; clicking "Reconnect" resets and reconnects.
- On unmount, closes WS; no further `onStatusChange` after unmount.
- `isVisible={false}` does not gate WS open.

**`web/src/routes/Terminal.test.tsx`** (extend existing):
- Reload with remembered active session id → lands on that tab.
- Reload with remembered id absent from server list → falls back to `LastAttachedAt` sort.
- Reload with remembered id present but `exited` → falls back to non-exited pick.
- Clicking a tab writes localStorage.
- Destroying the currently-remembered session clears localStorage.
- Exited sessions render with disabled styling and always-visible `×`.
- All live sessions render a panel on load (eager attach — verified by panel DOM count).

### What we do NOT test

- Exact backoff delays in ms (brittle). Verify retry count progression via fake timers.
- tmux replay behavior on reconnect (server-side).

### Jitter testing

Mock `Math.random` to return `0.5` for deterministic delays.

### Manual smoke (add to `web/MANUAL_TERMINAL_SMOKE.md`)

1. Open 3 tabs, reload page → all 3 present, active tab matches pre-reload choice.
2. Stop `navarisd` briefly while attached → status pill shows "Reconnecting…"; restart → reconnects.
3. Kill shell in one tab (`exit`) → that tab greys out with "Session ended"; other tabs unaffected.

## Migration

Pure additive changes. No server changes. No breaking changes to existing localStorage keys.

Old behavior's single-active-tab attach becomes eager attach — the first reload after deploy will open WebSockets for all existing tabs. Each server accepts one WS per session, independent of other sessions on the same sandbox. No capacity concern at current scale (5-tab UI cap).
