# Create Sandbox UI Design

**Date**: 2026-04-08
**Status**: Approved

## Summary

Add a "New sandbox" action to the Sandboxes list page in the web UI that opens a modal dialog, collects a minimal set of fields (name, image, optional CPU/memory limits, network mode), submits a `POST /v1/sandboxes`, then closes the dialog and navigates to the new sandbox's detail page. This closes the last major gap in the web UI: today the list view is read-plus-lifecycle only, and creating a sandbox requires the CLI or direct API calls.

## Context

The web UI currently exposes four sandbox actions (start, stop, destroy, terminal attach) but no create flow. Users who want a new sandbox from the browser have no path. The REST handler for creation already exists at `internal/api/sandbox.go:53` (`createSandbox`) and is fully backend-agnostic — it takes a `project_id`, `name`, an `image_id`, and an optional set of limits, and the service layer picks the provider based on the image reference (`internal/service/sandbox.go` `resolveBackend`: an image ref containing `/` routes to Incus, otherwise Firecracker). Because the backend already auto-selects a provider, the UI does not need a backend toggle — picking the image is sufficient to select Incus vs. Firecracker.

Two constraints shape the design:

1. **No image inventory endpoint.** There is no `GET /v1/images` that enumerates the Incus aliases and Firecracker rootfs files a given server actually has available. The all-in-one image ships two known-good images per provider (`alpine/3.21` and `debian/12` for Incus; `alpine-3.21` and `debian-12` for Firecracker — see `docker-compose.yml:23` for the Incus preload list and `Dockerfile.navarisd-firecracker` for the Firecracker rootfs build). The dialog curates these four as presets and falls back to a "custom" text input for anything else.
2. **Project context.** The list view already fans out across every project via `fetchAllSandboxes` in `web/src/routes/Sandboxes.tsx:27`, so the user does not have an "active project" when they click New. The dialog picks a default project (last-used via `localStorage`, else the first project returned by `listProjects`) and exposes a dropdown so the user can override it.

## Design

### 1. Architecture and data flow

The dialog is a self-contained component rendered from `Sandboxes.tsx`. It is mounted only when opened, has its own React state for form fields, and talks to the backend through a new `createSandbox` function in `web/src/api/sandboxes.ts`.

```
┌─────────────────┐
│ Sandboxes.tsx   │  (list page)
│  - header adds  │
│    "New" button │
│  - useState     │
│    open/close   │
└────────┬────────┘
         │ open=true
         ▼
┌─────────────────────────────┐
│ NewSandboxDialog.tsx         │  (new)
│  - native <dialog>            │
│  - form fields + local state  │
│  - listProjects (for dropdown)│
│  - useLastProject hook        │
│  - useNavigate                │
└────────┬────────────────────┘
         │ onSubmit
         ▼
┌─────────────────┐     POST /v1/sandboxes     ┌──────────────────┐
│ createSandbox() │──────────────────────────▶│ navarisd          │
│ in api/sandboxes│◀──────202 Operation────────│ createSandbox     │
└────────┬────────┘                            │ handler           │
         │ op.ResourceID                       └──────────────────┘
         ▼
┌─────────────────────────────┐
│ 1. queryClient.invalidate    │
│    (["sandboxes"])           │
│ 2. writeLastProject(id)      │
│ 3. onClose()                 │
│ 4. navigate(/sandboxes/{id}) │
└─────────────────────────────┘
```

**Why a native `<dialog>`**: The project has no existing modal primitive. The browser's `<dialog>` element provides focus trapping, ESC-to-close, backdrop styling via `::backdrop`, and top-layer rendering for free in every target browser (Chrome 110+, Firefox 109+, Safari 16.4+ — same matrix the UI already supports, see `2026-04-07-webui-design.md:12`). It is strictly simpler than a Radix/headlessui dependency and avoids adding a library for a single screen.

**Why mount-on-open**: The dialog fetches the project list (`listProjects`) so it can populate the project dropdown. Mounting only when `open=true` means the list request fires when the user clicks New, not on every render of the Sandboxes page. The dialog unmounts on close, so each opening starts with a fresh form.

### 2. Component surface

**New files**

| File | Purpose |
|---|---|
| `web/src/components/NewSandboxDialog.tsx` | Modal dialog component. Owns form state, talks to createSandbox, invalidates the sandbox query, navigates on success. |
| `web/src/components/NewSandboxDialog.test.tsx` | Vitest + React Testing Library unit tests. Mocks `createSandbox`, `listProjects`, and `useNavigate`. |
| `web/src/hooks/useLastProject.ts` | Tiny hook: `{ lastProjectId, setLastProjectId }` backed by `localStorage["navaris.lastProjectId"]`. Safe-read (try/catch around `localStorage` access) so it tolerates private-mode / disabled storage. |

**Modified files**

| File | Change |
|---|---|
| `web/src/api/sandboxes.ts` | Add `createSandbox(req)` function and `CreateSandboxRequest` input type. |
| `web/src/types/navaris.ts` | Add `Operation` interface mirroring `internal/domain/operation.go` (PascalCase fields, same pattern as existing `Sandbox`/`Project` types). |
| `web/src/routes/Sandboxes.tsx` | Add "New sandbox" button to the header, `const [open, setOpen] = useState(false)` state, and `<NewSandboxDialog open={open} onClose={() => setOpen(false)} />` mount at the bottom of the page. |

No other files change. The router, auth flow, sidebar, and event stream all stay identical.

### 3. Form fields and defaults

The form deliberately exposes only the fields a typical developer needs to spin up a sandbox. Everything else (expires-at, metadata, snapshot sources, host pinning) is out of scope for v1.

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| Project | `<select>` | yes | last-used id if still in the list, else first project | Populated from `listProjects()`. If there are no projects, the dialog shows an empty-state message and a disabled Create button (see "Error handling"). |
| Name | `<input type="text">` | yes | `""` | `autoFocus`, `maxLength={64}`, trimmed before submit. Empty after trim disables Create. |
| Image | segmented control + `<input type="text">` | yes | `alpine/3.21` (Incus) | Four preset chips plus a "Custom…" option that reveals a text input. See "Image picker" below. |
| CPU limit | `<input type="number" min="1">` | no | blank | If blank, omitted from the request. If set, sent as a number. |
| Memory (MB) | `<input type="number" min="64">` | no | blank | If blank, omitted. If set, sent as a number. |
| Network | radio group | yes | `isolated` | Two options: `isolated` and `published`. |

**Image picker**: a small segmented control with four hard-coded presets:

- `alpine/3.21` — Incus, Alpine Linux 3.21
- `debian/12` — Incus, Debian 12
- `alpine-3.21` — Firecracker, Alpine Linux 3.21
- `debian-12` — Firecracker, Debian 12

Plus a fifth chip labeled `Custom…` that reveals a free-form text input. The free-form value is sent verbatim as `image_id`; the backend's `resolveBackend` handles provider auto-detection. The segmented control renders the provider name as a subtle caption under each preset label (e.g. "alpine/3.21" / "incus" stacked) so users can see at a glance which backend they'll hit. Selecting a preset clears and hides the custom input.

**No snapshot support** in v1. The create form always uses an image, not a snapshot. Creating from a snapshot remains CLI-only until we design a snapshot-picker UX (listed under Out of Scope).

### 4. Project handling

```ts
// web/src/hooks/useLastProject.ts
const KEY = "navaris.lastProjectId";

export function useLastProject() {
  const read = useCallback((): string | null => {
    try { return localStorage.getItem(KEY); }
    catch { return null; }
  }, []);
  const write = useCallback((id: string) => {
    try { localStorage.setItem(KEY, id); }
    catch { /* swallow — private mode */ }
  }, []);
  return { readLastProject: read, writeLastProject: write };
}
```

The dialog uses this hook on mount:

```ts
const { data: projects } = useQuery({ queryKey: ["projects"], queryFn: listProjects });
const { readLastProject, writeLastProject } = useLastProject();

useEffect(() => {
  if (!projects || projects.length === 0) return;
  const last = readLastProject();
  const candidate = last && projects.some((p) => p.ProjectID === last) ? last : projects[0].ProjectID;
  setProjectId(candidate);
}, [projects, readLastProject]);
```

The "still in the list" check matters: a user may have deleted the project their last-used id points at. If the stale id is not in the fetched list, fall back to the first project.

After a successful create, `writeLastProject(projectId)` runs before navigation so the next open of the dialog starts with the same project.

### 5. Submit flow

```ts
async function onSubmit(e: FormEvent) {
  e.preventDefault();
  setError(null);
  setPending(true);
  try {
    const op = await createSandbox({
      project_id: projectId,
      name: name.trim(),
      image_id: imageRef,
      cpu_limit: cpuLimit || undefined,
      memory_limit_mb: memoryLimitMB || undefined,
      network_mode: networkMode,
    });
    writeLastProject(projectId);
    await queryClient.invalidateQueries({ queryKey: ["sandboxes"] });
    onClose();
    navigate(`/sandboxes/${op.ResourceID}`);
  } catch (err) {
    if (err instanceof ApiError) {
      setError(messageForCreateError(err));
    } else {
      setError("Unable to create sandbox. Try again.");
    }
  } finally {
    setPending(false);
  }
}
```

The `createSandbox` function in `web/src/api/sandboxes.ts`:

```ts
export interface CreateSandboxRequest {
  project_id: string;
  name: string;
  image_id: string;
  cpu_limit?: number;
  memory_limit_mb?: number;
  network_mode: NetworkMode;
}

export async function createSandbox(req: CreateSandboxRequest): Promise<Operation> {
  return apiFetch<Operation>("/v1/sandboxes", { method: "POST", json: req });
}
```

**Why `op.ResourceID`, not `op.SandboxID`**: the handler calls `respondOperation(w, op)` which emits the domain Operation struct (`internal/domain/operation.go:27`). The struct has no `json` tags, so field names wire as `ResourceID` / `ResourceType` in PascalCase — same convention as every other type in `web/src/types/navaris.ts`. The `Type` field will be `"create_sandbox"`, `ResourceType` will be `"sandbox"`, and `ResourceID` will be the new sandbox's UUID — exactly what `/sandboxes/:id` expects.

**Cache invalidation**: awaiting `invalidateQueries` before navigating means the Sandboxes list (and any other observer of `["sandboxes"]`) re-fetches before the user returns to it. Even though we navigate away, the list is still the back-button target. The event stream will also fire a `sandbox_state_changed` event once the create operation runs, which independently triggers invalidation via `useEventStream` — but we invalidate synchronously here so the list refreshes immediately even if the websocket connection is slow.

### 6. Error handling and edge cases

| Situation | Handling |
|---|---|
| No projects exist yet | Dialog opens, the project dropdown renders with a disabled "No projects — create one via the CLI" placeholder, Create button is disabled. The dialog does not try to auto-create a project. |
| `listProjects` fails | Dialog renders the form with an inline error above the field: "Unable to load projects. Try closing and reopening the dialog." Create button stays disabled. |
| Name is empty after trim | Create button disabled; no request fired. |
| Duplicate name in project (409 Conflict) | Server returns `errors.go:ErrConflict` → 409. Dialog shows "A sandbox with that name already exists in this project." Form stays open with values intact. |
| Invalid state (422 InvalidState) e.g. project missing | Show the server's message verbatim (it's user-safe). |
| 5xx | Show "Server error. Try again." — the `response.go` error mapper already redacts the real message at 500. |
| Network failure | `apiFetch` throws a non-`ApiError`; catch block shows "Unable to create sandbox. Try again." |
| User hits ESC mid-submit | Native `<dialog>` would close on ESC. We intercept `onCancel` and preventDefault when `pending=true`, so the dialog stays open until the request resolves or errors. |
| User clicks backdrop mid-submit | Backdrop click is treated the same as ESC — we use an `onClick` on the dialog that calls `onClose` only if the click target is the dialog itself (the backdrop is the dialog's own box model when `showModal()`-opened). We skip this close when `pending=true`. |
| Backend auto-picks Firecracker but KVM is disabled on the host | The Create request succeeds but the sandbox will fail to start. The Sandboxes list (which we invalidated) will show it in `failed`. Out of scope to pre-validate host capabilities — surface the failure on the detail page as today. |

### 7. Testing

All tests are unit tests under Vitest + React Testing Library. jsdom/undici's `AbortSignal.any` incompatibility means we mock the React Router hooks (`useNavigate`) and the network (`createSandbox`, `listProjects`) rather than booting a real router or fetch layer — the existing UI tests in the repo already follow this pattern.

**`NewSandboxDialog.test.tsx`** covers:

1. **Renders fields and defaults** — form mounts with "Name" focused, `isolated` network selected, Create button disabled.
2. **Name validation** — typing and then clearing the name field re-disables Create.
3. **Project default** — when `localStorage["navaris.lastProjectId"]` points at an existing project, that project is selected; when it points at a deleted project, the first project is selected.
4. **Successful create** — submitting the form calls `createSandbox` with the exact expected payload, invalidates `["sandboxes"]`, writes `navaris.lastProjectId`, calls `onClose`, and calls `navigate("/sandboxes/abc123")` with the `ResourceID` from the mocked Operation.
5. **Conflict error** — when `createSandbox` rejects with `ApiError(409, "conflict", "...")`, the dialog shows "A sandbox with that name already exists in this project." and stays open.
6. **Custom image** — selecting "Custom…" reveals the text input; the typed value is sent as `image_id`.

**`Sandboxes.test.tsx`** (adding to the existing file if present, otherwise creating one alongside the existing test layout) covers:

1. **New button renders and opens dialog** — click toggles `open` state, the dialog becomes visible.
2. **Does not open dialog on initial render** — guards against accidental always-open regression.

The test-file co-location matches the `web/src/index.css.test.ts` pattern already in the repo.

### 8. Styling and accessibility

- The dialog uses the existing theme variables (`--bg-raised`, `--border-strong`, `--fg-primary`, `--fg-muted`, `--status-failed`, `--invert-bg`). No new CSS custom properties.
- Layout matches the Login form: an inner panel with `border border-[var(--border-strong)] bg-[var(--bg-raised)] p-8`, form labels in uppercase mono `text-[9px] tracking-[0.1em]`, inputs with `border border-[var(--border-subtle)]` turning to `focus:border-[var(--fg-primary)]`. Create and Cancel buttons at the bottom, Create using the same inverted style as Login's Sign in button.
- `aria-labelledby="new-sandbox-title"` on the form, `role="alert"` on the error line (matches Login), `aria-invalid` on the name input when empty-after-interaction.
- The native `<dialog>` handles focus trapping and restores focus to the New button on close — no custom focus ring logic needed.

## Files Modified

| File | Change |
|---|---|
| `web/src/components/NewSandboxDialog.tsx` | NEW — dialog component |
| `web/src/components/NewSandboxDialog.test.tsx` | NEW — dialog tests |
| `web/src/hooks/useLastProject.ts` | NEW — localStorage hook |
| `web/src/api/sandboxes.ts` | Add `CreateSandboxRequest` type and `createSandbox()` function |
| `web/src/types/navaris.ts` | Add `Operation` interface |
| `web/src/routes/Sandboxes.tsx` | Add "New sandbox" header button and dialog mount |
| `web/src/routes/Sandboxes.test.tsx` | Add tests for header button + dialog open/close (file may not exist yet — create if absent) |

No Go, server, or Docker changes. No new npm dependencies.

## What Does NOT Change

- No new REST endpoints — the `POST /v1/sandboxes` handler and `createSandbox` service method are used as-is.
- No changes to auth, cookies, or middleware.
- No changes to the event stream or cache invalidation strategy beyond the synchronous `invalidateQueries` call in the submit handler.
- No changes to the CLI, the integration tests, or the all-in-one Docker image.
- No new theme tokens or Tailwind config changes. (The `:root` short-name aliases added in `fc9c5c2` are reused for the error line and any status accents the dialog may show.)
- No changes to `docs/superpowers/specs/2026-04-07-webui-design.md` — this spec is an additive v2 feature that slots into the existing UI architecture.

## Out of Scope (YAGNI)

- **Snapshot source picker.** Creating from a snapshot is possible via the CLI; a UI picker needs a `/v1/snapshots?project_id=…` list view we do not have yet. Deferred until we build a snapshots page.
- **Expires-at / TTL input.** Valuable but adds a date-time picker we do not currently have and is not part of the typical local-dev flow. Deferable to a v2 polish pass.
- **Metadata / labels editor.** Arbitrary key/value pairs on create would need a repeated-input UI and server-side validation surface. Not worth it for the first version — users who need metadata can set it via the CLI.
- **Host pinning.** `host_id` is not in `createSandboxRequest` today; skipped.
- **Backend toggle override.** The backend auto-resolves from the image ref; a manual override would only create footguns.
- **Image inventory endpoint.** A real `GET /v1/images` is a separate design problem (the Incus provider has an `ImageCache`, Firecracker has a filesystem scan — unifying them is out of scope here). The curated presets cover the default all-in-one image, and the custom text input is the escape hatch.
- **Live validation of image existence.** The form does not pre-flight the image ref against the server. An invalid image ref will cause the create to fail and the sandbox to land in `failed`; the user sees this on the list.
- **Optimistic cache update.** We invalidate and re-fetch instead of building a placeholder row. The round-trip is fast enough on localhost, and it avoids the race with the event stream's own `sandbox_state_changed` invalidations.
- **End-to-end browser test.** The UI test plan is unit-level (per `2026-04-07-webui-design.md:31` non-goal on Playwright). The puppeteer probe script used for diagnosing the delete-button regression is a one-off dev tool, not a committed test.
- **Multi-sandbox batch create.** One sandbox per dialog submission. A "create 5 sandboxes" bulk path is speculative and adds complexity.
