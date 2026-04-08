import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type SyntheticEvent,
} from "react";
import { useQuery } from "@tanstack/react-query";
import { listProjects } from "@/api/projects";
import { useLastProject } from "@/hooks/useLastProject";
import type { NetworkMode } from "@/types/navaris";

// IMAGE_PRESETS is the curated set shipped by the all-in-one Docker image
// (see docker-compose.yml INCUS_PRELOAD_IMAGE and
// Dockerfile.navarisd-firecracker). The backend auto-selects a provider
// from the image ref ("/" → Incus, otherwise Firecracker — see
// internal/service/sandbox.go resolveBackend), so the only thing the UI
// needs to pick is the ref itself.
interface ImagePreset {
  ref: string;
  label: string;
  backend: string;
}

const IMAGE_PRESETS: readonly ImagePreset[] = [
  { ref: "alpine/3.21", label: "alpine/3.21", backend: "incus" },
  { ref: "debian/12", label: "debian/12", backend: "incus" },
  { ref: "alpine-3.21", label: "alpine-3.21", backend: "firecracker" },
  { ref: "debian-12", label: "debian-12", backend: "firecracker" },
] as const;

const CUSTOM_SENTINEL = "__custom__";

export interface NewSandboxDialogProps {
  onClose: () => void;
}

export default function NewSandboxDialog({ onClose }: NewSandboxDialogProps) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const { readLastProject } = useLastProject();

  // Form state lives on the component so each mount starts with a fresh
  // form. The parent uses `{open && <NewSandboxDialog ... />}` so closing
  // the dialog unmounts this tree — no need to reset state manually.
  const [name, setName] = useState("");
  const [projectId, setProjectId] = useState<string>("");
  const [imageSelection, setImageSelection] = useState<string>(
    IMAGE_PRESETS[0].ref,
  );
  const [customImage, setCustomImage] = useState<string>("");
  const [cpuLimit, setCpuLimit] = useState<string>("");
  const [memoryLimitMB, setMemoryLimitMB] = useState<string>("");
  const [networkMode, setNetworkMode] = useState<NetworkMode>("isolated");
  const [pending] = useState(false);
  const [error] = useState<string | null>(null);

  // Fetch projects once so the dropdown can populate. Retry is already
  // disabled app-wide but we set it explicitly here too because a failing
  // projects query should surface immediately in the dialog.
  const projectsQuery = useQuery({
    queryKey: ["projects"],
    queryFn: listProjects,
  });

  // When the projects arrive, pick a default: last-used id if it's still
  // in the list, else the first project. This effect runs once per
  // projects-data change — if the user has already picked a project
  // manually, `projectId` is non-empty and the effect leaves it alone.
  useEffect(() => {
    if (!projectsQuery.data || projectsQuery.data.length === 0) return;
    if (projectId !== "") return;
    const last = readLastProject();
    const stillExists =
      last !== null && projectsQuery.data.some((p) => p.ProjectID === last);
    setProjectId(
      stillExists ? (last as string) : projectsQuery.data[0].ProjectID,
    );
  }, [projectsQuery.data, projectId, readLastProject]);

  // Imperatively open the dialog on mount. The parent only mounts this
  // component when the user clicks "New sandbox", so this fires exactly
  // once per dialog lifetime.
  useEffect(() => {
    const dialog = dialogRef.current;
    if (dialog && !dialog.open) {
      dialog.showModal();
    }
  }, []);

  const imageRef = useMemo(() => {
    if (imageSelection === CUSTOM_SENTINEL) return customImage.trim();
    return imageSelection;
  }, [imageSelection, customImage]);

  const canSubmit = useMemo(() => {
    if (pending) return false;
    if (name.trim() === "") return false;
    if (imageRef === "") return false;
    if (projectId === "") return false;
    return true;
  }, [pending, name, imageRef, projectId]);

  function handleCancelEvent(e: SyntheticEvent<HTMLDialogElement>) {
    // ESC key triggers the native `cancel` event on the dialog. We
    // preventDefault to keep the dialog element alive — React unmounts
    // it cleanly when the parent flips `open` to false via onClose.
    e.preventDefault();
    if (!pending) onClose();
  }

  function handleBackdropClick(e: SyntheticEvent<HTMLDialogElement>) {
    // When showModal()-opened, a click that lands on the dialog's own
    // box (rather than any child) is a backdrop click. Close unless a
    // submit is in flight.
    if (pending) return;
    if (e.target === e.currentTarget) onClose();
  }

  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!canSubmit) return;
    // Submit handler wired in Task 7.
  }

  const projects = projectsQuery.data ?? [];

  return (
    <dialog
      ref={dialogRef}
      onCancel={handleCancelEvent}
      onClick={handleBackdropClick}
      aria-labelledby="new-sandbox-title"
      className="bg-transparent p-0 backdrop:bg-black/60"
    >
      <form
        onSubmit={onSubmit}
        className="w-[440px] max-w-[90vw] border border-[var(--border-strong)] bg-[var(--bg-raised)] p-8"
      >
        <div className="mb-6">
          <h2
            id="new-sandbox-title"
            className="font-display text-[15px] font-semibold tracking-[0.02em] text-[var(--fg-primary)]"
          >
            New sandbox
          </h2>
          <p className="mt-1 font-mono text-[9px] tracking-[0.08em] text-[var(--fg-muted)]">
            create and start a sandbox
          </p>
        </div>

        <label
          htmlFor="nsd-project"
          className="mb-1 block font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]"
        >
          Project
        </label>
        <select
          id="nsd-project"
          value={projectId}
          onChange={(e) => setProjectId(e.currentTarget.value)}
          disabled={projects.length === 0}
          className="mb-4 w-full border border-[var(--border-subtle)] bg-transparent px-3 py-2 text-sm text-[var(--fg-primary)] outline-none focus:border-[var(--fg-primary)]"
        >
          {projects.length === 0 ? (
            <option value="">No projects — create one via the CLI</option>
          ) : (
            projects.map((p) => (
              <option key={p.ProjectID} value={p.ProjectID}>
                {p.Name}
              </option>
            ))
          )}
        </select>

        <label
          htmlFor="nsd-name"
          className="mb-1 block font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]"
        >
          Name
        </label>
        <input
          id="nsd-name"
          type="text"
          autoFocus
          maxLength={64}
          value={name}
          onChange={(e) => setName(e.currentTarget.value)}
          className="mb-4 w-full border border-[var(--border-subtle)] bg-transparent px-3 py-2 text-sm text-[var(--fg-primary)] outline-none focus:border-[var(--fg-primary)]"
        />

        <fieldset className="mb-4">
          <legend className="mb-2 block font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">
            Image
          </legend>
          <div className="grid grid-cols-2 gap-2">
            {IMAGE_PRESETS.map((preset) => (
              <button
                type="button"
                key={preset.ref}
                onClick={() => setImageSelection(preset.ref)}
                className={[
                  "flex flex-col items-start border px-3 py-2 text-left transition-colors",
                  imageSelection === preset.ref
                    ? "border-[var(--fg-primary)] text-[var(--fg-primary)]"
                    : "border-[var(--border-subtle)] text-[var(--fg-secondary)]",
                ].join(" ")}
              >
                <span className="text-[12px]">{preset.label}</span>
                <span className="font-mono text-[9px] text-[var(--fg-muted)]">
                  {preset.backend}
                </span>
              </button>
            ))}
            <button
              type="button"
              onClick={() => setImageSelection(CUSTOM_SENTINEL)}
              className={[
                "col-span-2 flex flex-col items-start border px-3 py-2 text-left transition-colors",
                imageSelection === CUSTOM_SENTINEL
                  ? "border-[var(--fg-primary)] text-[var(--fg-primary)]"
                  : "border-[var(--border-subtle)] text-[var(--fg-secondary)]",
              ].join(" ")}
            >
              <span className="text-[12px]">Custom…</span>
              <span className="font-mono text-[9px] text-[var(--fg-muted)]">
                manual image ref
              </span>
            </button>
          </div>
          {imageSelection === CUSTOM_SENTINEL && (
            <input
              type="text"
              aria-label="Custom image ref"
              placeholder="e.g. images:ubuntu/24.04"
              value={customImage}
              onChange={(e) => setCustomImage(e.currentTarget.value)}
              className="mt-2 w-full border border-[var(--border-subtle)] bg-transparent px-3 py-2 text-sm font-mono text-[var(--fg-primary)] outline-none focus:border-[var(--fg-primary)]"
            />
          )}
        </fieldset>

        <div className="mb-4 grid grid-cols-2 gap-3">
          <div>
            <label
              htmlFor="nsd-cpu"
              className="mb-1 block font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]"
            >
              CPU limit
            </label>
            <input
              id="nsd-cpu"
              type="number"
              min={1}
              value={cpuLimit}
              onChange={(e) => setCpuLimit(e.currentTarget.value)}
              className="w-full border border-[var(--border-subtle)] bg-transparent px-3 py-2 text-sm text-[var(--fg-primary)] outline-none focus:border-[var(--fg-primary)]"
            />
          </div>
          <div>
            <label
              htmlFor="nsd-memory"
              className="mb-1 block font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]"
            >
              Memory (MB)
            </label>
            <input
              id="nsd-memory"
              type="number"
              min={64}
              value={memoryLimitMB}
              onChange={(e) => setMemoryLimitMB(e.currentTarget.value)}
              className="w-full border border-[var(--border-subtle)] bg-transparent px-3 py-2 text-sm text-[var(--fg-primary)] outline-none focus:border-[var(--fg-primary)]"
            />
          </div>
        </div>

        <fieldset className="mb-6">
          <legend className="mb-2 block font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]">
            Network
          </legend>
          <div className="flex gap-4 text-sm text-[var(--fg-primary)]">
            <label className="flex items-center gap-2">
              <input
                type="radio"
                name="nsd-network"
                value="isolated"
                checked={networkMode === "isolated"}
                onChange={() => setNetworkMode("isolated")}
              />
              <span>isolated</span>
            </label>
            <label className="flex items-center gap-2">
              <input
                type="radio"
                name="nsd-network"
                value="published"
                checked={networkMode === "published"}
                onChange={() => setNetworkMode("published")}
              />
              <span>published</span>
            </label>
          </div>
        </fieldset>

        {error !== null && (
          <p
            role="alert"
            className="mb-4 text-xs text-[var(--status-failed)]"
          >
            {error}
          </p>
        )}

        <div className="flex justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            disabled={pending}
            className="border border-[var(--border-subtle)] bg-transparent px-4 py-2 text-xs font-medium tracking-[0.02em] text-[var(--fg-secondary)] transition-opacity disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={!canSubmit}
            className="border border-[var(--invert-bg)] bg-[var(--invert-bg)] px-4 py-2 text-xs font-medium tracking-[0.02em] text-[var(--fg-on-invert)] transition-opacity disabled:opacity-50"
          >
            {pending ? "Creating…" : "Create"}
          </button>
        </div>
      </form>
    </dialog>
  );
}
