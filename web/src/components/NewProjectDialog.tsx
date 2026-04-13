import { useEffect, useRef, useState, type FormEvent, type SyntheticEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ApiError } from "@/api/client";
import { createProject } from "@/api/projects";

// NewProjectDialog is a stripped-down sibling of NewSandboxDialog: the
// backend's POST /v1/projects only needs a name (see
// internal/api/project.go createProject — metadata is optional and not
// exposed here), so the form is a single input plus the usual pending /
// error plumbing. The parent mounts this with `{open && <... />}` so each
// open gets a fresh form; no manual reset needed.
export interface NewProjectDialogProps {
  onClose: () => void;
}

export default function NewProjectDialog({ onClose }: NewProjectDialogProps) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const queryClient = useQueryClient();

  const [name, setName] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Imperatively open the dialog on mount. Mirrors NewSandboxDialog — the
  // parent only mounts this when the user clicks "New project", so this
  // fires exactly once per dialog lifetime.
  useEffect(() => {
    const dialog = dialogRef.current;
    if (dialog && !dialog.open) {
      dialog.showModal();
    }
  }, []);

  const canSubmit = !pending && name.trim() !== "";

  function handleCancelEvent(e: SyntheticEvent<HTMLDialogElement>) {
    // ESC triggers the native `cancel` event. preventDefault to keep the
    // dialog element alive — React unmounts it cleanly when the parent flips
    // open to false via onClose.
    e.preventDefault();
    if (!pending) onClose();
  }

  function handleBackdropClick(e: SyntheticEvent<HTMLDialogElement>) {
    if (pending) return;
    if (e.target === e.currentTarget) onClose();
  }

  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!canSubmit) return;
    setError(null);
    setPending(true);
    try {
      await createProject(name.trim());
      await queryClient.invalidateQueries({ queryKey: ["projects"] });
      onClose();
    } catch (err) {
      setError(
        err instanceof ApiError
          ? messageForCreateError(err)
          : "Unable to create project. Try again.",
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <dialog
      ref={dialogRef}
      onCancel={handleCancelEvent}
      onClick={handleBackdropClick}
      aria-labelledby="new-project-title"
      className="m-auto bg-transparent p-0 backdrop:bg-black/60"
    >
      <form
        onSubmit={onSubmit}
        className="w-[420px] max-w-[90vw] border border-[var(--border-strong)] bg-[var(--bg-raised)] p-8"
      >
        <div className="mb-6">
          <h2
            id="new-project-title"
            className="font-display text-[15px] font-semibold tracking-[0.02em] text-[var(--fg-primary)]"
          >
            New project
          </h2>
          <p className="mt-1 font-mono text-[9px] tracking-[0.08em] text-[var(--fg-muted)]">
            create a project to group sandboxes
          </p>
        </div>

        <label
          htmlFor="npd-name"
          className="mb-1 block font-mono text-[9px] uppercase tracking-[0.1em] text-[var(--fg-muted)]"
        >
          Name
        </label>
        <input
          id="npd-name"
          type="text"
          autoFocus
          maxLength={64}
          value={name}
          onChange={(e) => setName(e.currentTarget.value)}
          className="mb-6 w-full border border-[var(--border-subtle)] bg-transparent px-3 py-2 text-sm text-[var(--fg-primary)] outline-none focus:border-[var(--fg-primary)]"
        />

        {error !== null && (
          <p role="alert" className="mb-4 text-xs text-[var(--status-failed)]">
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

// messageForCreateError mirrors the sandbox dialog's helper. The backend's
// project handler is lighter (no nested op), but we still get the usual
// 409 on duplicate name, 400 on missing name (which canSubmit already
// prevents), and 5xx from the server. apiFetch surfaces err.status
// reliably — err.message/err.code fall back to HTTP status strings for
// the same reasons documented in NewSandboxDialog.
function messageForCreateError(err: ApiError): string {
  if (err.status === 409) {
    return "A project with that name already exists.";
  }
  if (err.status === 400) {
    return "Invalid project name. Try again.";
  }
  if (err.status >= 500) {
    return "Server error. Try again.";
  }
  return err.message || "Unable to create project. Try again.";
}
