import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { listProjects } from "@/api/projects";
import NewProjectDialog from "@/components/NewProjectDialog";

// Projects renders the list of projects known to navarisd and a button to
// create new ones. Field names are PascalCase because the backend's
// domain.Project has no json tags; see web/src/types/navaris.ts for the
// full shape.
export default function Projects() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["projects"],
    queryFn: listProjects,
  });
  const [newDialogOpen, setNewDialogOpen] = useState(false);

  return (
    <div>
      <header className="flex items-start justify-between pb-4 border-b border-[var(--border-subtle)] mb-5">
        <div>
          <h1 className="text-xl font-medium tracking-[-0.01em]">Projects</h1>
          <div className="mt-1 font-mono text-[10px] tracking-[0.04em] text-[var(--fg-muted)]">
            {(data ?? []).length} total
          </div>
        </div>
        <button
          type="button"
          onClick={() => setNewDialogOpen(true)}
          className="border border-[var(--invert-bg)] bg-[var(--invert-bg)] px-4 py-2 text-xs font-medium tracking-[0.02em] text-[var(--fg-on-invert)]"
        >
          New project
        </button>
      </header>

      {isLoading && <div className="text-sm text-[var(--fg-muted)]">Loading…</div>}
      {error && (
        <div className="border border-[var(--status-failed)] p-3 text-sm text-[var(--status-failed)]">
          Failed to load projects.
        </div>
      )}

      {(data ?? []).length > 0 && (
        <table className="w-full border-collapse">
          <thead>
            <tr>
              {["Name", "ID", "Created"].map((h) => (
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
            {(data ?? []).map((p) => (
              <tr key={p.ProjectID} className="hover:bg-[var(--bg-overlay)]">
                <td className="py-2.5 pr-3 border-b border-[var(--border-subtle)] text-sm font-medium">
                  {p.Name}
                </td>
                <td className="py-2.5 pr-3 border-b border-[var(--border-subtle)] font-mono text-[11px] text-[var(--fg-secondary)]">
                  {p.ProjectID}
                </td>
                <td className="py-2.5 pr-3 border-b border-[var(--border-subtle)] font-mono text-[11px] text-[var(--fg-secondary)]">
                  {formatCreated(p.CreatedAt)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {newDialogOpen && (
        <NewProjectDialog onClose={() => setNewDialogOpen(false)} />
      )}
    </div>
  );
}

// formatCreated renders an ISO-8601 string as a compact "YYYY-MM-DD HH:MM:SS"
// label. Returns "—" for unparseable input so we never blow up the row.
function formatCreated(iso: string): string {
  const parsed = Date.parse(iso);
  if (Number.isNaN(parsed)) return "—";
  return new Date(parsed).toISOString().slice(0, 19).replace("T", " ");
}
