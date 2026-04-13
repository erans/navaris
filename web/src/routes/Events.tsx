import { useEffect, useRef, useState } from "react";
import type { Event as NavarisEvent } from "@/types/navaris";

// Events renders a tail of recent events the SPA has seen on its own
// websocket connection to /v1/events. This is intentionally a local ring
// buffer (last 200) because the server-side history API is not part of v1 —
// we just record what flows past.
//
// A second websocket (on top of the one useEventStream owns in AppShell) is
// acceptable: useEventStream does not expose the raw event stream to
// subscribers, only fans out to TanStack Query invalidation. Rather than
// refactor it into a pub-sub, we open a dedicated socket for the live feed.
//
// Event field names are PascalCase because internal/domain/event.go has no
// json tags. Data is an untyped snake_case map and is where identifiers like
// sandbox_id / operation_id / project_id live — see internal/worker/
// dispatcher.go for the publishers.
const MAX_EVENTS = 200;

interface Entry {
  key: number;
  event: NavarisEvent;
}

export default function Events() {
  const [entries, setEntries] = useState<Entry[]>([]);
  const [state, setState] = useState<"connecting" | "open" | "closed">("connecting");
  const nextKeyRef = useRef(0);

  useEffect(() => {
    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${proto}//${window.location.host}/v1/events`);
    ws.onopen = () => setState("open");
    ws.onclose = () => setState("closed");
    ws.onmessage = (msg) => {
      try {
        const evt = JSON.parse(msg.data as string) as NavarisEvent;
        setEntries((prev) => {
          const entry: Entry = { key: nextKeyRef.current++, event: evt };
          const next = [entry, ...prev];
          return next.length > MAX_EVENTS ? next.slice(0, MAX_EVENTS) : next;
        });
      } catch {
        // Ignore malformed frames.
      }
    };
    return () => ws.close();
  }, []);

  return (
    <div>
      <header className="pb-4 border-b border-[var(--border-subtle)] mb-5 flex items-start justify-between">
        <div>
          <h1 className="text-xl font-medium tracking-[-0.01em]">Events</h1>
          <div className="mt-1 font-mono text-[10px] tracking-[0.04em] text-[var(--fg-muted)]">
            live stream · {entries.length} buffered
          </div>
        </div>
        <span className="font-mono text-[10px] text-[var(--fg-muted)]">ws · {state}</span>
      </header>

      {entries.length === 0 && (
        <div className="border border-dashed border-[var(--border-subtle)] p-8 text-center text-sm text-[var(--fg-muted)]">
          Waiting for events. Trigger a sandbox action to populate this feed.
        </div>
      )}

      {entries.length > 0 && (
        <ol className="font-mono text-[11px]">
          {entries.map(({ key, event }) => (
            <li
              key={key}
              className="py-1.5 border-b border-[var(--border-subtle)] grid grid-cols-[auto_auto_1fr] gap-3"
            >
              <span className="text-[var(--fg-muted)]">{formatTime(event.Timestamp)}</span>
              <span className="text-[var(--fg-primary)]">{event.Type}</span>
              <span className="text-[var(--fg-secondary)] truncate">{dataPreview(event.Data)}</span>
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}

// formatTime extracts HH:MM:SS from an ISO-8601 string. Returns placeholder
// dashes for unparseable input so the row still renders.
function formatTime(iso: string): string {
  const parsed = Date.parse(iso);
  if (Number.isNaN(parsed)) return "--:--:--";
  return new Date(parsed).toISOString().slice(11, 19);
}

// dataPreview picks the most useful identifier from an event's Data map.
// Publishers in internal/worker/dispatcher.go and internal/service/sandbox.go
// set snake_case keys; we look for the common ones in priority order.
function dataPreview(data: Record<string, unknown> | null): string {
  if (!data) return "";
  for (const k of ["sandbox_id", "operation_id", "project_id", "snapshot_id", "image_id"]) {
    const v = data[k];
    if (typeof v === "string" && v !== "") return v;
  }
  return "";
}
