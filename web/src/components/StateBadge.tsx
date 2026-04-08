import type { SandboxState } from "@/types/navaris";

// StateBadge renders a sandbox state as a small coloured dot plus a lowercase
// label. The map below must stay exhaustive over SandboxState — TypeScript
// enforces this via the Record<SandboxState, string> annotation.
//
// Colour policy:
// - terminal OK states (running) use the running token
// - terminal stopped state uses the stopped token
// - terminal failed state uses the failed token
// - terminal destroyed state uses the destroyed token
// - all transitional states (pending/starting/stopping) share the pending
//   token and pulse, since they represent in-flight work
const DOT: Record<SandboxState, string> = {
  pending: "bg-[var(--status-pending)] animate-pulse",
  starting: "bg-[var(--status-pending)] animate-pulse",
  running: "bg-[var(--status-running)]",
  stopping: "bg-[var(--status-pending)] animate-pulse",
  stopped: "bg-[var(--status-stopped)]",
  failed: "bg-[var(--status-failed)]",
  destroyed: "bg-[var(--status-destroyed)]",
};

export function StateBadge({ state }: { state: SandboxState }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-[12px] text-[var(--fg-secondary)]">
      <span
        data-testid="state-dot"
        aria-hidden
        className={`inline-block h-1.5 w-1.5 rounded-full ${DOT[state]}`}
      />
      {state}
    </span>
  );
}
