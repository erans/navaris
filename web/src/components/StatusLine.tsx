import { useEventStream } from "@/hooks/useEventStream";

// StatusLine is the bottom-of-viewport bar that mirrors the design mockup.
// It shows the websocket connection state as a colored dot and exposes a
// short status string. Monospace throughout.
export function StatusLine() {
  const { state } = useEventStream();
  return (
    <div
      data-testid="status-line"
      className="h-[26px] border-t border-[var(--border-subtle)] bg-[var(--bg-raised)] px-4 flex items-center gap-4 font-mono text-[10px] tracking-[0.02em] text-[var(--fg-secondary)]"
    >
      <span className="font-medium tracking-[0.16em] text-[var(--fg-primary)]">
        NAVARIS
      </span>
      <span className="flex items-center gap-1.5">
        <span
          className={
            state === "open"
              ? "inline-block h-1.5 w-1.5 rounded-full bg-[var(--status-running)]"
              : state === "connecting"
              ? "inline-block h-1.5 w-1.5 rounded-full bg-[var(--status-pending)]"
              : "inline-block h-1.5 w-1.5 rounded-full bg-[var(--status-failed)]"
          }
          aria-hidden
        />
        {state === "open" ? "connected" : state === "connecting" ? "connecting" : "disconnected"}
      </span>
      <span className="text-[var(--fg-muted)]">·</span>
      <span>incus + firecracker</span>
      <span className="ml-auto text-[var(--fg-muted)]">::</span>
    </div>
  );
}
