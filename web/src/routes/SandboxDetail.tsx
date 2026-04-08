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
        <div className="flex items-center gap-3">
          <StateBadge state={data.State} />
          <Link
            to={`/sandboxes/${data.SandboxID}/terminal`}
            className={[
              "font-mono text-[11px] border px-3 py-1.5",
              running
                ? "border-[var(--border-strong)] text-[var(--fg-primary)] hover:bg-[var(--bg-overlay)]"
                : "border-[var(--border-subtle)] text-[var(--fg-muted)] pointer-events-none",
            ].join(" ")}
            aria-disabled={!running}
          >
            Terminal
          </Link>
        </div>
      </header>

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
