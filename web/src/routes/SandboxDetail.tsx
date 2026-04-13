import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  destroySandbox,
  getSandbox,
  startSandbox,
  stopSandbox,
} from "@/api/sandboxes";
import { ApiError } from "@/api/client";
import { StateBadge } from "@/components/StateBadge";

// SandboxDetail is the per-sandbox drill-down. It shows the sandbox's core
// metadata and exposes the three lifecycle actions Navaris supports today:
// start, stop, destroy. Destroy is gated behind an inline confirm step so
// a single mis-click cannot nuke a workload.
//
// All lifecycle calls return 202 with an Operation body — the real state
// transitions arrive asynchronously via the websocket event stream. We
// invalidate the relevant query keys on success so any open list view also
// refetches.
export default function SandboxDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  const { data, isLoading, error } = useQuery({
    queryKey: ["sandbox", id],
    queryFn: () => getSandbox(id!),
    enabled: !!id,
  });

  const start = useMutation({
    mutationFn: () => startSandbox(id!),
    onSuccess: () => {
      toast.success("Start requested");
      qc.invalidateQueries({ queryKey: ["sandbox", id] });
      qc.invalidateQueries({ queryKey: ["sandboxes"] });
    },
    onError: (e) =>
      toast.error(e instanceof ApiError ? e.message : "Failed to start"),
  });

  const stop = useMutation({
    mutationFn: () => stopSandbox(id!),
    onSuccess: () => {
      toast.success("Stop requested");
      qc.invalidateQueries({ queryKey: ["sandbox", id] });
      qc.invalidateQueries({ queryKey: ["sandboxes"] });
    },
    onError: (e) =>
      toast.error(e instanceof ApiError ? e.message : "Failed to stop"),
  });

  const destroy = useMutation({
    mutationFn: () => destroySandbox(id!),
    onSuccess: () => {
      toast.success("Destroy requested");
      qc.invalidateQueries({ queryKey: ["sandboxes"] });
      navigate("/sandboxes");
    },
    onError: (e) =>
      toast.error(e instanceof ApiError ? e.message : "Failed to destroy"),
  });

  if (isLoading) {
    return <div className="text-sm text-[var(--fg-muted)]">Loading…</div>;
  }
  if (error || !data) {
    return (
      <div className="border border-[var(--status-failed)] p-3 text-sm text-[var(--status-failed)]">
        Failed to load sandbox.
      </div>
    );
  }

  const running = data.State === "running";
  const terminal =
    data.State === "stopped" ||
    data.State === "failed" ||
    data.State === "destroyed";
  const actionsPending = start.isPending || stop.isPending || destroy.isPending;

  return (
    <div>
      <header className="flex items-start justify-between pb-4 border-b border-[var(--border-subtle)] mb-5">
        <div>
          <Link
            to="/sandboxes"
            className="font-mono text-[10px] tracking-[0.04em] text-[var(--fg-muted)] hover:text-[var(--fg-primary)]"
          >
            ← sandboxes
          </Link>
          <h1 className="mt-1 text-xl font-medium tracking-[-0.01em]">
            {data.Name}
          </h1>
          <div className="mt-1 font-mono text-[10px] tracking-[0.04em] text-[var(--fg-muted)]">
            {data.SandboxID}
          </div>
        </div>
        <StateBadge state={data.State} />
      </header>

      {(data.State === "starting" || data.State === "stopping" || data.State === "pending") && (
        <div className="mb-5 flex items-center gap-3 border border-[var(--status-pending)] bg-[var(--status-pending)]/8 px-4 py-3">
          <svg className="h-4 w-4 animate-spin text-[var(--status-pending)]" viewBox="0 0 24 24" fill="none">
            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="3" />
            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
          </svg>
          <span className="text-sm text-[var(--status-pending)]">
            {data.State === "starting" && "Sandbox is starting…"}
            {data.State === "stopping" && "Sandbox is stopping…"}
            {data.State === "pending" && "Sandbox is pending…"}
          </span>
        </div>
      )}

      <section className="mb-6 grid grid-cols-2 gap-3 border border-[var(--border-subtle)] p-4">
        <Field label="Backend" value={data.Backend} />
        <Field label="Image" value={data.SourceImageID} />
        <Field label="CPU" value={data.CPULimit != null ? String(data.CPULimit) : "—"} />
        <Field label="Memory (MB)" value={data.MemoryLimitMB != null ? String(data.MemoryLimitMB) : "—"} />
        <Field label="Project" value={data.ProjectID} />
        <Field label="Network" value={data.NetworkMode || "—"} />
      </section>

      <section className="flex gap-2">
        <button
          type="button"
          onClick={() => start.mutate()}
          disabled={actionsPending || running || data.State === "pending" || data.State === "starting"}
          className="font-mono text-xs border border-[var(--border-strong)] px-3 py-1.5 disabled:opacity-50 hover:bg-[var(--bg-overlay)]"
        >
          Start
        </button>
        <button
          type="button"
          onClick={() => stop.mutate()}
          disabled={actionsPending || !running}
          className="font-mono text-xs border border-[var(--border-strong)] px-3 py-1.5 disabled:opacity-50 hover:bg-[var(--bg-overlay)]"
        >
          Stop
        </button>
        <Link
          to={`/sandboxes/${data.SandboxID}/terminal`}
          aria-disabled={!running}
          className={[
            "font-mono text-xs border border-[var(--border-strong)] px-3 py-1.5 hover:bg-[var(--bg-overlay)]",
            running ? "" : "opacity-50 pointer-events-none",
          ].join(" ")}
        >
          Terminal
        </Link>

        {!confirmingDelete ? (
          <button
            type="button"
            onClick={() => setConfirmingDelete(true)}
            disabled={actionsPending || !terminal}
            className="ml-auto font-mono text-xs border border-[var(--status-failed)] text-[var(--status-failed)] px-3 py-1.5 disabled:opacity-50 hover:bg-[var(--bg-overlay)]"
          >
            Delete
          </button>
        ) : (
          <div className="ml-auto flex items-center gap-2">
            <span className="font-mono text-[11px] text-[var(--fg-muted)]">
              destroy this sandbox?
            </span>
            <button
              type="button"
              onClick={() => setConfirmingDelete(false)}
              className="font-mono text-xs border border-[var(--border-subtle)] px-3 py-1.5 hover:bg-[var(--bg-overlay)]"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={() => destroy.mutate()}
              className="font-mono text-xs border border-[var(--status-failed)] bg-[var(--status-failed)] text-white px-3 py-1.5"
            >
              Confirm delete
            </button>
          </div>
        )}
      </section>
    </div>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">
        {label}
      </div>
      <div className="mt-1 text-sm text-[var(--fg-primary)]">{value}</div>
    </div>
  );
}
