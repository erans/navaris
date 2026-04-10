import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { ClipboardAddon } from "@xterm/addon-clipboard";
import "@xterm/xterm/css/xterm.css";
import { encodeInputBytes, encodeResizeMessage } from "@/terminal/wire";
import { listSessions, createSession, destroySession } from "@/api/sandboxSessions";
import type { Session } from "@/types/navaris";

const MAX_UI_SESSIONS = 5;

const TERM_THEME = {
  background: "#0b0b0c",
  foreground: "#f4f4f5",
  cursor: "#f4f4f5",
  selectionBackground: "#2e2e33",
};

interface TermEntry {
  term: XTerm;
  fit: FitAddon;
  ws: WebSocket;
  ro: ResizeObserver;
  container: HTMLElement;
  pasteHandler: (e: ClipboardEvent) => void;
}

// Terminal opens tmux-backed sessions inside a sandbox and presents them as
// tabs. On mount it lists existing sessions, auto-creates one if none exist,
// and attaches to the first (or most recently used) session via WebSocket.
export default function Terminal() {
  const { id } = useParams<{ id: string }>();
  const [sessions, setSessions] = useState<Session[]>([]);
  const [sessionLabels, setSessionLabels] = useState<Map<string, number>>(new Map());
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [statusFlash, setStatusFlash] = useState<string | null>(null);
  const [destroyTarget, setDestroyTarget] = useState<Session | null>(null);

  const terminalContainers = useRef<Map<string, HTMLDivElement>>(new Map());
  const terminalInstances = useRef<Map<string, TermEntry>>(new Map());
  const destroyDialogRef = useRef<HTMLDialogElement>(null);

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

        let live = all.filter((s) => s.State !== "destroyed" && s.State !== "exited");
        if (live.length === 0) {
          const created = await createSession(id);
          labels.set(created.SessionID, labels.size + 1);
          live = [created];
        }
        if (cancelled) return;

        setSessionLabels(labels);
        setSessions(live);
        // Prefer the most recently attached, fall back to first.
        const byAttach = [...live].sort((a, b) => {
          const aTime = a.LastAttachedAt ?? a.CreatedAt;
          const bTime = b.LastAttachedAt ?? b.CreatedAt;
          return bTime.localeCompare(aTime);
        });
        setActiveSessionId(byAttach[0]?.SessionID ?? null);
        setLoading(false);
      } catch {
        if (!cancelled) {
          setStatusFlash("Failed to load sessions");
          setLoading(false);
        }
      }
    })();
    return () => { cancelled = true; };
  }, [id]);

  // --- attach xterm to active session ---
  useEffect(() => {
    if (!activeSessionId || !id) return;

    const existing = terminalInstances.current.get(activeSessionId);
    if (existing) {
      existing.fit.fit();
      return;
    }

    const container = terminalContainers.current.get(activeSessionId);
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
    // ClipboardAddon handles OSC 52 sequences — when tmux copies a
    // selection it sends OSC 52 and the addon writes it to the browser
    // clipboard automatically.
    term.loadAddon(new ClipboardAddon());
    term.open(container);

    // ResizeObserver fires when the container's dimensions change (layout
    // shifts, window resize, tab switch). This replaces the old window
    // resize listener and is far more reliable for keeping the PTY size
    // in sync with what xterm.js renders.
    const ro = new ResizeObserver(() => fit.fit());
    ro.observe(container);

    // Defer initial fit so the browser has finished layout. Without this
    // the very first measurement can use stale dimensions.
    requestAnimationFrame(() => fit.fit());

    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(
      `${proto}//${window.location.host}/v1/sandboxes/${encodeURIComponent(id)}/attach?session=${encodeURIComponent(activeSessionId)}`,
    );
    ws.binaryType = "arraybuffer";

    const sessionState = sessions.find((s) => s.SessionID === activeSessionId)?.State;

    ws.onopen = () => {
      // Re-fit now that the connection is live, then tell the PTY.
      fit.fit();
      ws.send(encodeResizeMessage(term.cols, term.rows));
      if (sessionState === "detached") {
        setStatusFlash("Reconnected");
        setTimeout(() => setStatusFlash(null), 2000);
      }
    };

    ws.onmessage = (msg) => {
      if (msg.data instanceof ArrayBuffer) {
        term.write(new Uint8Array(msg.data));
      }
    };

    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(encodeInputBytes(data));
    });

    // Copy: when the user Shift+selects text (bypassing tmux mouse),
    // xterm.js creates a native selection. Auto-copy it to clipboard.
    term.onSelectionChange(() => {
      const sel = term.getSelection();
      if (sel) navigator.clipboard.writeText(sel).catch(() => {});
    });

    // Paste: Ctrl+Shift+V or right-click paste → forward to terminal.
    const pasteHandler = (e: ClipboardEvent) => {
      e.preventDefault();
      const text = e.clipboardData?.getData("text");
      if (text) term.paste(text);
    };
    container.addEventListener("paste", pasteHandler);

    term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(encodeResizeMessage(cols, rows));
    });

    terminalInstances.current.set(activeSessionId, { term, fit, ws, ro, container, pasteHandler });
  }, [activeSessionId, id]);

  // --- cleanup on unmount ---
  useEffect(() => {
    return () => {
      terminalInstances.current.forEach(({ term, ws, ro, container, pasteHandler }) => {
        container.removeEventListener("paste", pasteHandler);
        ro.disconnect();
        ws.close();
        term.dispose();
      });
      terminalInstances.current.clear();
    };
  }, []);

  // --- new session ---
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
    } catch {
      setStatusFlash("Failed to create session");
      setTimeout(() => setStatusFlash(null), 3000);
    }
  }, [id, sessions.length]);

  // --- destroy session ---
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

    const inst = terminalInstances.current.get(sessionId);
    if (inst) {
      inst.ro.disconnect();
      inst.ws.close();
      inst.term.dispose();
      terminalInstances.current.delete(sessionId);
    }
    terminalContainers.current.delete(sessionId);

    setSessions((prev) => {
      const next = prev.filter((s) => s.SessionID !== sessionId);
      if (activeSessionId === sessionId && next.length > 0) {
        setActiveSessionId(next[0].SessionID);
      }
      return next;
    });
    destroyDialogRef.current?.close();
    setDestroyTarget(null);
  }, [destroyTarget, activeSessionId]);

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
        {sessions.map((s) => (
          <button
            key={s.SessionID}
            type="button"
            onClick={() => setActiveSessionId(s.SessionID)}
            className={[
              "flex items-center gap-1.5 px-3 py-1.5 font-mono text-[11px] border-r border-[var(--border-subtle)]",
              s.SessionID === activeSessionId
                ? "text-[var(--fg-primary)] bg-[var(--bg-primary)]"
                : "text-[var(--fg-muted)] hover:text-[var(--fg-secondary)]",
            ].join(" ")}
          >
            <span>Session {sessionLabels.get(s.SessionID) ?? "?"}</span>
            {sessions.length > 1 && (
              <span
                role="button"
                tabIndex={0}
                onClick={(e) => { e.stopPropagation(); openDestroyDialog(s); }}
                onKeyDown={(e) => { if (e.key === "Enter") { e.stopPropagation(); openDestroyDialog(s); } }}
                className="ml-1 text-[9px] text-[var(--fg-muted)] hover:text-[var(--status-failed)] cursor-pointer"
              >
                &times;
              </span>
            )}
          </button>
        ))}
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

      {/* Terminal panels — one per session, hide inactive.
           The outer div provides the border; the inner div is the xterm
           mount point with NO padding so FitAddon measures correctly. */}
      {sessions.map((s) => (
        <div
          key={s.SessionID}
          className={[
            "flex-1 min-h-0 border border-[var(--border-subtle)] overflow-hidden",
            s.SessionID === activeSessionId ? "" : "hidden",
          ].join(" ")}
        >
          <div
            ref={(el) => {
              if (el) terminalContainers.current.set(s.SessionID, el);
            }}
            className="h-full w-full bg-black"
          />
        </div>
      ))}

      {/* Destroy confirmation dialog */}
      <dialog
        ref={destroyDialogRef}
        onClick={(e) => { if (e.target === e.currentTarget) cancelDestroy(); }}
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
