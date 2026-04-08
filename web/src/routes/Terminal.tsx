import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { encodeInputBytes, encodeResizeMessage } from "@/terminal/wire";

// Terminal opens a WebSocket to /v1/sandboxes/:id/attach and wires it up to
// an xterm.js instance. The wire protocol is defined in spec §Wire protocol
// and implemented on the Go side by bridgeAttach in internal/api/attach.go:
//   - Binary frames (client→server) = raw stdin bytes
//   - Binary frames (server→client) = raw stdout/stderr bytes
//   - Text frames (client→server)   = JSON control messages; only
//                                     {"type":"resize","cols":N,"rows":M} is
//                                     defined today, everything else is ignored
//   - Text frames (server→client)   = not emitted in v1
//
// The theme is hardcoded dark — xterm.js can't read CSS variables, and a
// terminal looks correct against a dark canvas regardless of the app theme.
// We can revisit theme-linking when we add a light terminal mode.
export default function Terminal() {
  const { id } = useParams<{ id: string }>();
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [state, setState] = useState<"connecting" | "open" | "closed">("connecting");

  useEffect(() => {
    if (!id || !containerRef.current) return;

    const term = new XTerm({
      cursorBlink: true,
      fontFamily:
        '"Commit Mono", "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 13,
      theme: {
        background: "#0b0b0c",
        foreground: "#f4f4f5",
        cursor: "#f4f4f5",
        selectionBackground: "#2e2e33",
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(containerRef.current);
    fit.fit();

    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(
      `${proto}//${window.location.host}/v1/sandboxes/${encodeURIComponent(id)}/attach`,
    );
    ws.binaryType = "arraybuffer";

    ws.onopen = () => {
      setState("open");
      // Send initial resize so the PTY matches the visible viewport.
      ws.send(encodeResizeMessage(term.cols, term.rows));
    };

    ws.onmessage = (msg) => {
      // Only binary frames carry data in v1. Text frames are reserved for
      // server → client control messages, which the spec doesn't define yet.
      if (msg.data instanceof ArrayBuffer) {
        term.write(new Uint8Array(msg.data));
      }
    };

    ws.onclose = () => setState("closed");
    ws.onerror = () => setState("closed");

    const dataDisposable = term.onData((data) => {
      if (ws.readyState !== WebSocket.OPEN) return;
      // send() with a Uint8Array emits a binary frame; the server reads it
      // verbatim into handle.Conn (sandbox stdin).
      ws.send(encodeInputBytes(data));
    });

    const resizeDisposable = term.onResize(({ cols, rows }) => {
      if (ws.readyState !== WebSocket.OPEN) return;
      // send() with a string emits a text frame; the server parses it as
      // JSON and dispatches to handle.Resize.
      ws.send(encodeResizeMessage(cols, rows));
    });

    const onWindowResize = () => fit.fit();
    window.addEventListener("resize", onWindowResize);

    return () => {
      window.removeEventListener("resize", onWindowResize);
      dataDisposable.dispose();
      resizeDisposable.dispose();
      ws.close();
      term.dispose();
    };
  }, [id]);

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center justify-between pb-3 border-b border-[var(--border-subtle)] mb-3">
        <div>
          <Link
            to={`/sandboxes/${id}`}
            className="font-mono text-[10px] tracking-[0.04em] text-[var(--fg-muted)] hover:text-[var(--fg-primary)]"
          >
            ← detail
          </Link>
          <h1 className="mt-1 text-lg font-medium">Terminal</h1>
          <div className="mt-0.5 font-mono text-[10px] text-[var(--fg-muted)]">{id}</div>
        </div>
        <span className="font-mono text-[10px] text-[var(--fg-muted)]">ws · {state}</span>
      </header>

      <div
        ref={containerRef}
        className="flex-1 min-h-[400px] border border-[var(--border-subtle)] bg-black p-2"
      />
    </div>
  );
}
