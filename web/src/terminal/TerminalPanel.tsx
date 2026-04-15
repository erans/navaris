import { useEffect, useRef, useState, useCallback } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { ClipboardAddon } from "@xterm/addon-clipboard";
import "@xterm/xterm/css/xterm.css";
import { encodeInputBytes, encodeResizeMessage } from "@/terminal/wire";
import { listSessions } from "@/api/sandboxSessions";
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

const MAX_ATTEMPTS = 8;

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
  const onStatusChangeRef = useRef(onStatusChange);
  useEffect(() => {
    onStatusChangeRef.current = onStatusChange;
  });

  const [status, setStatus] = useState<PanelStatus>("connecting");

  const updateStatus = useCallback((s: PanelStatus) => {
    setStatus(s);
    onStatusChangeRef.current(s);
  }, []);

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

  const delayFor = (attempt: number): number => {
    const base = Math.min(1000 * Math.pow(2, attempt), 30_000);
    const jitter = 0.8 + Math.random() * 0.4;
    return base * jitter;
  };

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

  function connect() {
    if (reconnectRef.current.stopped) return;
    updateStatus("connecting");
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
      updateStatus("connected");
    };

    ws.onmessage = (msg) => {
      if (msg.data instanceof ArrayBuffer) {
        termRef.current?.write(new Uint8Array(msg.data));
      }
    };

    ws.onerror = () => {
      // onclose always follows; state machine lives there.
    };

    ws.onclose = async () => {
      if (reconnectRef.current.stopped) return;
      updateStatus("reconnecting");
      try {
        const all = await listSessions(sandboxId);
        if (reconnectRef.current.stopped) return;
        const me = all.find((s) => s.SessionID === sessionId);
        if (!me || me.State === "exited" || me.State === "destroyed") {
          reconnectRef.current.stopped = true;
          updateStatus("exited");
          return;
        }
      } catch {
        // Server unreachable — treat as network blip and retry.
      }
      scheduleRetry();
    };

    wsRef.current = ws;
  }

  // Only invoked via the "Reconnect" button in the failed-state overlay.
  // stopped=true came from scheduleRetry hitting MAX_ATTEMPTS — not from
  // exit detection — so resetting it here is safe.
  const manualReconnect = () => {
    reconnectRef.current.attempt = 0;
    reconnectRef.current.stopped = false;
    connect();
  };

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
}
