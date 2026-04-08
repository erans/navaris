import { useEffect, useRef, useState } from "react";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import type { Event as NavarisEvent } from "@/types/navaris";

export type StreamState = "connecting" | "open" | "closed";

// useEventStream owns a single websocket connection to /v1/events. It
// auto-reconnects with exponential backoff, and on each inbound message it
// invalidates the relevant TanStack Query keys so open pages refetch. This
// is the fan-out point — individual components do not subscribe directly.
export function useEventStream(): { state: StreamState } {
  const qc = useQueryClient();
  const [state, setState] = useState<StreamState>("connecting");
  const attemptRef = useRef(0);

  useEffect(() => {
    let ws: WebSocket | null = null;
    let reconnectTimer: number | null = null;
    let disposed = false;

    function connect() {
      if (disposed) return;
      const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
      const url = `${proto}//${window.location.host}/v1/events`;
      setState("connecting");
      ws = new WebSocket(url);

      ws.onopen = () => {
        attemptRef.current = 0;
        setState("open");
      };

      ws.onmessage = (msg) => {
        try {
          const evt = JSON.parse(msg.data as string) as NavarisEvent;
          applyEvent(qc, evt);
        } catch {
          // Ignore malformed frames — server won't send them, but be safe.
        }
      };

      ws.onclose = () => {
        setState("closed");
        if (disposed) return;
        const attempt = attemptRef.current++;
        // Exponential backoff capped at 30s. 0.5s → 1s → 2s → 4s → 8s → 16s → 30s.
        const delay = Math.min(500 * 2 ** attempt, 30_000);
        reconnectTimer = window.setTimeout(connect, delay);
      };

      ws.onerror = () => {
        // onclose will fire right after, which handles backoff.
      };
    }

    connect();

    return () => {
      disposed = true;
      if (reconnectTimer !== null) window.clearTimeout(reconnectTimer);
      ws?.close();
    };
  }, [qc]);

  return { state };
}

// applyEvent fans a single event out to the query caches it affects. We
// invalidate broadly rather than patching in place — simpler, and the data
// volumes are small enough that a refetch is cheap. Event payloads come
// from internal/domain/event.go: Type, Timestamp, and an untyped Data map.
// The publishers in internal/service/sandbox.go and internal/worker/
// dispatcher.go put snake_case keys (sandbox_id, project_id, operation_id)
// into Data, so we read those by string lookup.
function applyEvent(qc: QueryClient, evt: NavarisEvent): void {
  qc.invalidateQueries({ queryKey: ["events"] });

  if (evt.Type === "sandbox_state_changed") {
    qc.invalidateQueries({ queryKey: ["sandboxes"] });
    const sandboxID = evt.Data?.["sandbox_id"];
    if (typeof sandboxID === "string" && sandboxID !== "") {
      qc.invalidateQueries({ queryKey: ["sandbox", sandboxID] });
    }
    return;
  }

  if (evt.Type === "operation_state_changed") {
    qc.invalidateQueries({ queryKey: ["operations"] });
    return;
  }
}
