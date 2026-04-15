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
