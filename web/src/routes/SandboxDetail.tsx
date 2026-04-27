import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  cancelBoost,
  destroySandbox,
  getSandbox,
  startBoost,
  startSandbox,
  stopSandbox,
  updateSandboxResources,
} from "@/api/sandboxes";
import type { StartBoostRequest, UpdateSandboxResourcesRequest } from "@/api/sandboxes";
import { ApiError } from "@/api/client";
import { StateBadge } from "@/components/StateBadge";
import type { Sandbox } from "@/types/navaris";

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

      <ResourcesPanel sandbox={data} sandboxId={id!} />

      <BoostSection sandbox={data} sandboxId={id!} />

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

function ResourcesPanel({
  sandbox,
  sandboxId,
}: {
  sandbox: Sandbox;
  sandboxId: string;
}) {
  const qc = useQueryClient();
  const [cpu, setCpu] = useState<string>(
    sandbox.CPULimit != null ? String(sandbox.CPULimit) : "",
  );
  const [mem, setMem] = useState<string>(
    sandbox.MemoryLimitMB != null ? String(sandbox.MemoryLimitMB) : "",
  );
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function apply() {
    setErr(null);
    setBusy(true);
    try {
      const body: UpdateSandboxResourcesRequest = {};
      const cpuN = cpu === "" ? undefined : Number(cpu);
      const memN = mem === "" ? undefined : Number(mem);
      if (cpuN !== undefined && cpuN !== sandbox.CPULimit)
        body.cpu_limit = cpuN;
      if (memN !== undefined && memN !== sandbox.MemoryLimitMB)
        body.memory_limit_mb = memN;
      if (Object.keys(body).length === 0) {
        setBusy(false);
        return;
      }
      await updateSandboxResources(sandboxId, body);
      await qc.invalidateQueries({ queryKey: ["sandbox", sandboxId] });
    } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : "resize failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="mb-6 border border-[var(--border-subtle)] p-4">
      <div className="mb-3 font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">
        Resources
      </div>
      <div className="flex flex-wrap items-end gap-4">
        <label className="flex flex-col gap-1">
          <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">
            CPU limit
          </span>
          <input
            type="number"
            min="1"
            value={cpu}
            onChange={(e) => setCpu(e.currentTarget.value)}
            disabled={busy}
            className="w-24 border border-[var(--border-strong)] bg-transparent px-2 py-1 text-sm text-[var(--fg-primary)] disabled:opacity-50"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">
            Memory (MB)
          </span>
          <input
            type="number"
            min="64"
            value={mem}
            onChange={(e) => setMem(e.currentTarget.value)}
            disabled={busy}
            className="w-28 border border-[var(--border-strong)] bg-transparent px-2 py-1 text-sm text-[var(--fg-primary)] disabled:opacity-50"
          />
        </label>
        <button
          type="button"
          onClick={apply}
          disabled={busy}
          className="font-mono text-xs border border-[var(--border-strong)] px-3 py-1.5 disabled:opacity-50 hover:bg-[var(--bg-overlay)]"
        >
          {busy ? "Applying…" : "Apply"}
        </button>
      </div>
      {err !== null && (
        <p role="alert" className="mt-2 text-xs text-[var(--status-failed)]">
          {err}
        </p>
      )}
    </section>
  );
}

function parseDuration(s: string): number {
  const m = /^(\d+)\s*(s|m|h)$/.exec(s.trim());
  if (!m) {
    const n = Number(s);
    if (Number.isFinite(n) && n > 0) return Math.floor(n);
    throw new Error(`invalid duration: ${s}`);
  }
  const n = Number(m[1]);
  return n * (m[2] === "h" ? 3600 : m[2] === "m" ? 60 : 1);
}

function BoostSection({
  sandbox,
  sandboxId,
}: {
  sandbox: Sandbox;
  sandboxId: string;
}) {
  const qc = useQueryClient();
  const [cpu, setCpu] = useState<string>("");
  const [mem, setMem] = useState<string>("");
  const [duration, setDuration] = useState<string>("5m");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const active = sandbox.active_boost;

  async function start() {
    setErr(null);
    setBusy(true);
    try {
      const durSec = parseDuration(duration);
      const body: StartBoostRequest = { duration_seconds: durSec };
      const cpuN = cpu === "" ? undefined : Number(cpu);
      const memN = mem === "" ? undefined : Number(mem);
      if (cpuN !== undefined) body.cpu_limit = cpuN;
      if (memN !== undefined) body.memory_limit_mb = memN;
      if (cpuN === undefined && memN === undefined) {
        throw new Error("set at least one of CPU or memory");
      }
      await startBoost(sandboxId, body);
      await qc.invalidateQueries({ queryKey: ["sandbox", sandboxId] });
    } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : "boost failed");
    } finally {
      setBusy(false);
    }
  }

  async function cancel() {
    setErr(null);
    setBusy(true);
    try {
      await cancelBoost(sandboxId);
      await qc.invalidateQueries({ queryKey: ["sandbox", sandboxId] });
    } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : "cancel failed");
    } finally {
      setBusy(false);
    }
  }

  if (active) {
    return (
      <section className="mb-6 border border-[var(--border-subtle)] p-4">
        <div className="mb-3 font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">
          Boost active
        </div>
        <div className="mb-3 flex flex-wrap gap-4">
          <div>
            <div className="font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">CPU</div>
            <div className="mt-1 text-sm text-[var(--fg-primary)]">{active.boosted_cpu_limit ?? "—"}</div>
          </div>
          <div>
            <div className="font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">Memory (MB)</div>
            <div className="mt-1 text-sm text-[var(--fg-primary)]">{active.boosted_memory_limit_mb ?? "—"}</div>
          </div>
          <div>
            <div className="font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">Expires</div>
            <div className="mt-1 text-sm text-[var(--fg-primary)]">{active.expires_at}</div>
          </div>
          {active.source && (
            <div>
              <div className="font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">Source</div>
              <div className="mt-1 text-sm text-[var(--fg-primary)]">
                {active.source === "in_sandbox" ? "in-sandbox" : "external"}
              </div>
            </div>
          )}
        </div>
        {active.state === "revert_failed" && (
          <p role="alert" className="mb-3 text-xs text-[var(--status-failed)]">
            Revert failed ({active.revert_attempts ?? 0}x): {active.last_error}
          </p>
        )}
        <button
          type="button"
          onClick={cancel}
          disabled={busy}
          className="font-mono text-xs border border-[var(--border-strong)] px-3 py-1.5 disabled:opacity-50 hover:bg-[var(--bg-overlay)]"
        >
          {busy ? "Cancelling…" : "Cancel boost"}
        </button>
        {err !== null && (
          <p role="alert" className="mt-2 text-xs text-[var(--status-failed)]">
            {err}
          </p>
        )}
      </section>
    );
  }

  return (
    <section className="mb-6 border border-[var(--border-subtle)] p-4">
      <div className="mb-3 font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">
        Boost
      </div>
      <div className="flex flex-wrap items-end gap-4">
        <label className="flex flex-col gap-1">
          <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">
            CPU limit
          </span>
          <input
            type="number"
            min="1"
            value={cpu}
            onChange={(e) => setCpu(e.currentTarget.value)}
            disabled={busy}
            className="w-24 border border-[var(--border-strong)] bg-transparent px-2 py-1 text-sm text-[var(--fg-primary)] disabled:opacity-50"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">
            Memory (MB)
          </span>
          <input
            type="number"
            min="64"
            value={mem}
            onChange={(e) => setMem(e.currentTarget.value)}
            disabled={busy}
            className="w-28 border border-[var(--border-strong)] bg-transparent px-2 py-1 text-sm text-[var(--fg-primary)] disabled:opacity-50"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">
            Duration
          </span>
          <input
            value={duration}
            onChange={(e) => setDuration(e.currentTarget.value)}
            disabled={busy}
            placeholder="5m"
            className="w-20 border border-[var(--border-strong)] bg-transparent px-2 py-1 text-sm text-[var(--fg-primary)] disabled:opacity-50"
          />
        </label>
        <button
          type="button"
          onClick={start}
          disabled={busy}
          className="font-mono text-xs border border-[var(--border-strong)] px-3 py-1.5 disabled:opacity-50 hover:bg-[var(--bg-overlay)]"
        >
          {busy ? "Boosting…" : "Boost"}
        </button>
      </div>
      {err !== null && (
        <p role="alert" className="mt-2 text-xs text-[var(--status-failed)]">
          {err}
        </p>
      )}
    </section>
  );
}
