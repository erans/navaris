import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { listProjects } from "@/api/projects";
import { listSandboxes } from "@/api/sandboxes";
import type { Sandbox, SandboxState } from "@/types/navaris";
import { StateBadge } from "@/components/StateBadge";
import NewSandboxDialog from "@/components/NewSandboxDialog";

type StateFilter = "all" | SandboxState;
type BackendFilter = "all" | string;

// Filter chips are deliberately narrow — we surface the 4 states users
// actually want to triage by. The remaining transitional states
// (starting/stopping/pending/destroyed) still render in the table with a
// StateBadge, but don't get their own quick filter.
const STATE_FILTERS: StateFilter[] = ["all", "running", "stopped", "failed"];

// Backends are a tiny, known set in the current codebase. If we add more
// later, we can switch this to be derived from the data.
const BACKEND_FILTERS: BackendFilter[] = ["all", "incus", "firecracker"];

// fetchAllSandboxes fans out across every project the caller can see.
// There is no "list all sandboxes" endpoint — see internal/api/sandbox.go
// listSandboxes, which returns 400 without a project_id. We do the fan-out
// inside the queryFn so the whole thing lives under a single ["sandboxes"]
// query key, which matches what useEventStream invalidates on state change.
async function fetchAllSandboxes(): Promise<Sandbox[]> {
  const projects = await listProjects();
  if (projects.length === 0) return [];
  const perProject = await Promise.all(
    projects.map((p) => listSandboxes(p.ProjectID)),
  );
  return perProject.flat();
}

export default function Sandboxes() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["sandboxes"],
    queryFn: fetchAllSandboxes,
  });
  const [stateFilter, setStateFilter] = useState<StateFilter>("all");
  const [backendFilter, setBackendFilter] = useState<BackendFilter>("all");
  const [newDialogOpen, setNewDialogOpen] = useState(false);

  const rows = useMemo(() => {
    const all = data ?? [];
    return all.filter((s) => {
      if (stateFilter !== "all" && s.State !== stateFilter) return false;
      if (backendFilter !== "all" && s.Backend !== backendFilter) return false;
      return true;
    });
  }, [data, stateFilter, backendFilter]);

  const runningCount = useMemo(
    () => (data ?? []).filter((s) => s.State === "running").length,
    [data],
  );

  return (
    <div>
      <header className="flex items-start justify-between pb-4 border-b border-[var(--border-subtle)] mb-5">
        <div>
          <h1 className="text-xl font-medium tracking-[-0.01em]">Sandboxes</h1>
          <div className="mt-1 font-mono text-[10px] tracking-[0.04em] text-[var(--fg-muted)]">
            {(data ?? []).length} total · {runningCount} running
          </div>
        </div>
        <button
          type="button"
          onClick={() => setNewDialogOpen(true)}
          className="border border-[var(--invert-bg)] bg-[var(--invert-bg)] px-4 py-2 text-xs font-medium tracking-[0.02em] text-[var(--fg-on-invert)]"
        >
          New sandbox
        </button>
      </header>

      <div className="mb-4 flex items-center gap-3 font-mono text-[11px]">
        <span className="text-[var(--fg-muted)]">state</span>
        {STATE_FILTERS.map((f) => (
          <Chip key={f} active={stateFilter === f} onClick={() => setStateFilter(f)}>
            {f}
          </Chip>
        ))}
        <span className="ml-3 text-[var(--fg-muted)]">backend</span>
        {BACKEND_FILTERS.map((f) => (
          <Chip key={f} active={backendFilter === f} onClick={() => setBackendFilter(f)}>
            {f}
          </Chip>
        ))}
      </div>

      {error && (
        <div className="mb-4 border border-[var(--status-failed)] p-3 text-sm text-[var(--status-failed)]">
          Failed to load sandboxes.
        </div>
      )}

      {isLoading && <div className="text-sm text-[var(--fg-muted)]">Loading…</div>}

      {!isLoading && rows.length === 0 && (
        <div className="border border-dashed border-[var(--border-subtle)] p-8 text-center text-sm text-[var(--fg-muted)]">
          No sandboxes match these filters.
        </div>
      )}

      {rows.length > 0 && <SandboxTable rows={rows} />}

      {newDialogOpen && (
        <NewSandboxDialog onClose={() => setNewDialogOpen(false)} />
      )}
    </div>
  );
}

function Chip({
  active,
  children,
  onClick,
}: {
  active: boolean;
  children: React.ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={[
        "px-2 py-1 border text-[11px] transition-colors",
        active
          ? "border-[var(--fg-primary)] text-[var(--fg-primary)]"
          : "border-[var(--border-subtle)] text-[var(--fg-secondary)]",
      ].join(" ")}
    >
      {children}
    </button>
  );
}

function SandboxTable({ rows }: { rows: Sandbox[] }) {
  return (
    <table className="w-full border-collapse">
      <thead>
        <tr>
          {["Name / ID", "Backend", "CPU · Mem", "Created", "State"].map((h) => (
            <th
              key={h}
              className="text-left font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)] py-2 pr-3 border-b border-[var(--border-subtle)] font-medium"
            >
              {h}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((s) => (
          <tr key={s.SandboxID} className="group hover:bg-[var(--bg-overlay)]">
            <td
              className={[
                "py-2.5 pl-3 pr-3 border-b border-[var(--border-subtle)] relative",
                s.State === "running"
                  ? "before:content-[''] before:absolute before:left-0 before:top-2.5 before:bottom-2.5 before:w-0.5 before:bg-[var(--status-running)]"
                  : "",
                s.State === "failed"
                  ? "before:content-[''] before:absolute before:left-0 before:top-2.5 before:bottom-2.5 before:w-0.5 before:bg-[var(--status-failed)]"
                  : "",
              ].join(" ")}
            >
              <div className="flex flex-col">
                <Link
                  to={`/sandboxes/${s.SandboxID}`}
                  className="text-[13px] font-medium text-[var(--fg-primary)] hover:underline"
                >
                  {s.Name}
                </Link>
                <span className="font-mono text-[10px] text-[var(--fg-muted)] mt-0.5">
                  {s.SandboxID}
                </span>
              </div>
            </td>
            <td className="py-2.5 pr-3 border-b border-[var(--border-subtle)] font-mono text-[11px] text-[var(--fg-secondary)]">
              {s.Backend}
            </td>
            <td className="py-2.5 pr-3 border-b border-[var(--border-subtle)] font-mono text-[11px] text-[var(--fg-secondary)]">
              {s.CPULimit ?? "—"} · {s.MemoryLimitMB ?? "—"}
            </td>
            <td className="py-2.5 pr-3 border-b border-[var(--border-subtle)] font-mono text-[11px] text-[var(--fg-secondary)]">
              {formatAgo(s.CreatedAt)}
            </td>
            <td className="py-2.5 pr-3 border-b border-[var(--border-subtle)]">
              <StateBadge state={s.State} />
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// formatAgo takes an ISO-8601 timestamp (which is what the Go backend
// actually emits for CreatedAt — not Unix seconds) and renders a compact
// "Ns ago" / "Nm ago" / "Nh NNm" label.
function formatAgo(iso: string): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return "—";
  const deltaSec = Math.max(0, Math.floor((Date.now() - then) / 1000));
  if (deltaSec < 60) return `${deltaSec}s ago`;
  if (deltaSec < 3600) return `${Math.floor(deltaSec / 60)}m ago`;
  const h = Math.floor(deltaSec / 3600);
  const m = Math.floor((deltaSec % 3600) / 60);
  return `${h}h ${m.toString().padStart(2, "0")}m`;
}
