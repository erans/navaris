# Create Sandbox UI Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "New sandbox" button to the web UI Sandboxes list that opens a modal dialog, collects minimal fields (name, image, optional CPU/memory, network), submits `POST /v1/sandboxes`, invalidates the list cache, and navigates to the new sandbox's detail page.

**Architecture:** The dialog is a self-contained React component using the browser-native `<dialog>` element. It is conditionally mounted (only when `open=true`), fetches the project list via `listProjects` for the project dropdown, remembers the last-used project in `localStorage`, and on successful submit calls a new `createSandbox` API wrapper. No new backend code — the `POST /v1/sandboxes` handler already exists and auto-detects the provider from the image reference.

**Tech Stack:** React 18, TypeScript, `@tanstack/react-query`, React Router v7, native HTML `<dialog>`, Tailwind v4, Vitest + React Testing Library + MSW (msw/node).

**Spec:** `docs/superpowers/specs/2026-04-08-create-sandbox-ui-design.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `web/src/types/navaris.ts` | Modify | Add `Operation` interface mirroring `internal/domain/operation.go` |
| `web/src/api/sandboxes.ts` | Modify | Add `CreateSandboxRequest` type and `createSandbox()` function |
| `web/src/api/sandboxes.test.ts` | Create | Unit test for `createSandbox` using MSW |
| `web/src/hooks/useLastProject.ts` | Create | Tiny hook wrapping `localStorage["navaris.lastProjectId"]` with try/catch |
| `web/src/hooks/useLastProject.test.ts` | Create | Unit test for read/write + private-mode safety |
| `web/src/test/setup.ts` | Modify | Shim `HTMLDialogElement.showModal/close` so jsdom tracks the `open` attribute |
| `web/src/components/NewSandboxDialog.tsx` | Create | Modal dialog: form state, submit, error handling, navigation |
| `web/src/components/NewSandboxDialog.test.tsx` | Create | Dialog unit tests (rendering, validation, project defaults, image picker, submit success, conflict error, pending guard) |
| `web/src/routes/Sandboxes.tsx` | Modify | Add "New sandbox" header button, `open` state, dialog mount |
| `web/src/routes/Sandboxes.test.tsx` | Modify | Add tests for the New button + dialog open/close |

No Go, server, or Docker changes. No new npm dependencies.

---

## Reference Patterns (read before implementing)

Before starting, skim these files — they are the authoritative style references for anything not spelled out below:

- `web/src/routes/Login.tsx` — the inverted-button styling, uppercase mono label convention, `ApiError` branching, `role="alert"` error line. **The dialog's visual style mirrors this form.**
- `web/src/routes/Login.test.tsx` — MSW + MemoryRouter + multi-`<Route>` pattern for asserting navigation without mocking `useNavigate`. **The dialog's test file mirrors this pattern.**
- `web/src/routes/Sandboxes.test.tsx` — uses MSW with realistic Go-wire-shape fixtures (PascalCase fields). **Reuse the same fixture shapes.**
- `web/src/components/StateBadge.tsx` — `bg-[var(--status-*)]` arbitrary-utility convention for status colors.
- `web/src/routes/SandboxDetail.tsx` — existing sandbox lifecycle UI; note the `["sandbox", id]` and `["sandboxes"]` invalidation pattern.
- `web/src/api/client.ts` — `apiFetch<T>(path, { method, json })` stringifies + sets content-type automatically; throws `ApiError` on non-2xx.

**Important:** the codebase uses MSW (`msw/node`) for test mocking, not `vi.mock`. Any test that needs to stub HTTP must create an MSW handler. Do not use `vi.mock("@/api/sandboxes", ...)` — it breaks the existing mocking discipline.

---

### Task 1: Add `Operation` type + `createSandbox` API wrapper

**Files:**
- Modify: `web/src/types/navaris.ts` (append `Operation` interface)
- Modify: `web/src/api/sandboxes.ts` (append `CreateSandboxRequest` type and `createSandbox()` function)
- Create: `web/src/api/sandboxes.test.ts` (new test file)

- [ ] **Step 1: Write the failing test**

Create `web/src/api/sandboxes.test.ts`:

```ts
import { describe, it, expect, beforeAll, afterEach, afterAll } from "vitest";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { createSandbox } from "./sandboxes";

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

describe("createSandbox", () => {
  it("POSTs to /v1/sandboxes with the request body and returns the Operation", async () => {
    let seenBody: unknown = null;
    server.use(
      http.post("/v1/sandboxes", async ({ request }) => {
        seenBody = await request.json();
        return HttpResponse.json(
          {
            OperationID: "op_1",
            ResourceType: "sandbox",
            ResourceID: "sbx_new",
            SandboxID: "sbx_new",
            SnapshotID: "",
            Type: "create_sandbox",
            State: "pending",
            StartedAt: "2026-04-08T12:00:00Z",
            FinishedAt: null,
            ErrorText: "",
            Metadata: null,
          },
          { status: 202 },
        );
      }),
    );

    const op = await createSandbox({
      project_id: "prj_1",
      name: "test-1",
      image_id: "alpine/3.21",
      cpu_limit: 2,
      memory_limit_mb: 512,
      network_mode: "isolated",
    });

    expect(op.ResourceID).toBe("sbx_new");
    expect(op.Type).toBe("create_sandbox");
    expect(seenBody).toEqual({
      project_id: "prj_1",
      name: "test-1",
      image_id: "alpine/3.21",
      cpu_limit: 2,
      memory_limit_mb: 512,
      network_mode: "isolated",
    });
  });

  it("omits cpu_limit and memory_limit_mb when not provided", async () => {
    let seenBody: Record<string, unknown> = {};
    server.use(
      http.post("/v1/sandboxes", async ({ request }) => {
        seenBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(
          {
            OperationID: "op_2",
            ResourceType: "sandbox",
            ResourceID: "sbx_2",
            SandboxID: "sbx_2",
            SnapshotID: "",
            Type: "create_sandbox",
            State: "pending",
            StartedAt: "2026-04-08T12:00:00Z",
            FinishedAt: null,
            ErrorText: "",
            Metadata: null,
          },
          { status: 202 },
        );
      }),
    );

    await createSandbox({
      project_id: "prj_1",
      name: "test-2",
      image_id: "debian-12",
      network_mode: "published",
    });

    expect(seenBody).not.toHaveProperty("cpu_limit");
    expect(seenBody).not.toHaveProperty("memory_limit_mb");
    expect(seenBody.image_id).toBe("debian-12");
    expect(seenBody.network_mode).toBe("published");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd web && npx vitest run src/api/sandboxes.test.ts
```

Expected: FAIL with `createSandbox is not exported from './sandboxes'` (or similar module-resolution error).

- [ ] **Step 3: Add `Operation` interface to types**

Append to `web/src/types/navaris.ts` (after the existing `Event` interface):

```ts
// Operation mirrors domain.Operation in internal/domain/operation.go. The Go
// struct has no json tags, so wire field names are PascalCase. Lifecycle
// handlers (create, start, stop, destroy) all return this envelope with
// ResourceType="sandbox" and ResourceID set to the sandbox's UUID.
export type OperationState =
  | "pending"
  | "running"
  | "succeeded"
  | "failed"
  | "cancelled";

export interface Operation {
  OperationID: string;
  ResourceType: string;
  ResourceID: string;
  SandboxID: string;
  SnapshotID: string;
  Type: string;
  State: OperationState;
  StartedAt: string;
  FinishedAt: string | null;
  ErrorText: string;
  Metadata: Record<string, unknown> | null;
}
```

- [ ] **Step 4: Add `createSandbox` function**

Modify `web/src/api/sandboxes.ts`:

1. Update the top import to pull in `NetworkMode` and `Operation`:

```ts
import { apiFetch } from "./client";
import type { ListResponse, NetworkMode, Operation, Sandbox } from "@/types/navaris";
```

2. Append at the bottom of the file (after `destroySandbox`):

```ts
// CreateSandboxRequest is the JSON body shape accepted by
// POST /v1/sandboxes — see internal/api/sandbox.go createSandboxRequest.
// project_id and name are required; the backend auto-selects a provider
// from image_id (a "/" in the ref routes to Incus, anything else to
// Firecracker — see internal/service/sandbox.go resolveBackend).
//
// Optional numeric fields are omitted rather than sent as null so the
// backend treats them as "use the provider default". JSON.stringify drops
// keys whose values are `undefined`, so setting `cpu_limit: undefined` is
// equivalent to not sending the key at all.
export interface CreateSandboxRequest {
  project_id: string;
  name: string;
  image_id: string;
  cpu_limit?: number;
  memory_limit_mb?: number;
  network_mode: NetworkMode;
}

export async function createSandbox(
  req: CreateSandboxRequest,
): Promise<Operation> {
  return apiFetch<Operation>("/v1/sandboxes", {
    method: "POST",
    json: req,
  });
}
```

- [ ] **Step 5: Run test to verify it passes**

```bash
cd web && npx vitest run src/api/sandboxes.test.ts
```

Expected: PASS with 2 tests.

- [ ] **Step 6: Commit**

```bash
git add web/src/types/navaris.ts web/src/api/sandboxes.ts web/src/api/sandboxes.test.ts
git commit -m "feat(web): add createSandbox API wrapper and Operation type

Adds the client-side plumbing for POST /v1/sandboxes. The Operation type
mirrors domain.Operation (PascalCase wire shape, no json tags on the Go
struct), and CreateSandboxRequest is the strictly-typed JSON body the UI
will submit from the new-sandbox dialog. Numeric optional fields are
left undefined when absent so JSON.stringify drops them rather than
sending nulls.

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

### Task 2: Add `useLastProject` hook

**Files:**
- Create: `web/src/hooks/useLastProject.ts`
- Create: `web/src/hooks/useLastProject.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/hooks/useLastProject.test.ts`:

```ts
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useLastProject } from "./useLastProject";

describe("useLastProject", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("returns null when no id has been written", () => {
    const { result } = renderHook(() => useLastProject());
    expect(result.current.readLastProject()).toBeNull();
  });

  it("round-trips a project id through localStorage", () => {
    const { result } = renderHook(() => useLastProject());
    act(() => result.current.writeLastProject("prj_42"));
    expect(result.current.readLastProject()).toBe("prj_42");
  });

  it("uses the storage key 'navaris.lastProjectId'", () => {
    const { result } = renderHook(() => useLastProject());
    act(() => result.current.writeLastProject("prj_99"));
    expect(localStorage.getItem("navaris.lastProjectId")).toBe("prj_99");
  });

  it("swallows thrown errors from localStorage.getItem (private mode)", () => {
    const spy = vi
      .spyOn(Storage.prototype, "getItem")
      .mockImplementation(() => {
        throw new Error("SecurityError: private mode");
      });
    const { result } = renderHook(() => useLastProject());
    expect(result.current.readLastProject()).toBeNull();
    expect(spy).toHaveBeenCalled();
  });

  it("swallows thrown errors from localStorage.setItem (private mode)", () => {
    const spy = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });
    const { result } = renderHook(() => useLastProject());
    expect(() => {
      act(() => result.current.writeLastProject("prj_1"));
    }).not.toThrow();
    expect(spy).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd web && npx vitest run src/hooks/useLastProject.test.ts
```

Expected: FAIL with `useLastProject is not exported` (module not found).

- [ ] **Step 3: Create the hook**

Create `web/src/hooks/useLastProject.ts`:

```ts
import { useCallback } from "react";

// KEY is the single localStorage key used by the web UI to remember the
// project the user most recently created a sandbox in. Writing it lives
// here so there's exactly one place to grep if the name ever needs to
// change.
const KEY = "navaris.lastProjectId";

// useLastProject exposes read/write helpers for the "last-used project id"
// preference. Both helpers are stable-reference (useCallback with no deps)
// so consumers can put them in effect dependency arrays without causing
// re-runs. Every localStorage access is wrapped in try/catch so a browser
// with storage disabled (private mode, security settings, quota exceeded)
// silently falls through to "no preference" rather than crashing the app.
export function useLastProject() {
  const readLastProject = useCallback((): string | null => {
    try {
      return localStorage.getItem(KEY);
    } catch {
      return null;
    }
  }, []);

  const writeLastProject = useCallback((id: string): void => {
    try {
      localStorage.setItem(KEY, id);
    } catch {
      // Storage disabled or quota exceeded — no-op; the user will just
      // see the default project next time they open the dialog.
    }
  }, []);

  return { readLastProject, writeLastProject };
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd web && npx vitest run src/hooks/useLastProject.test.ts
```

Expected: PASS with 5 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/hooks/useLastProject.ts web/src/hooks/useLastProject.test.ts
git commit -m "feat(web): add useLastProject hook for last-used project id

Tiny hook that wraps localStorage['navaris.lastProjectId'] with try/catch
so private-mode browsers silently fall through. Used by the incoming
NewSandboxDialog to default the project dropdown to the user's last
choice, with fallback to the first project when the stored id is stale.

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

### Task 3: Shim `HTMLDialogElement` in the test setup

**Files:**
- Modify: `web/src/test/setup.ts`

**Why:** jsdom 25 has incomplete support for the native `<dialog>` element — `showModal()` and `close()` are present in some versions but not all, and neither toggles the `open` attribute. Our dialog tests rely on the `open` attribute being observable after calling `showModal()`, so we stub both methods to track it. The shim is a no-op in real browsers (only applies when the native method is missing).

- [ ] **Step 1: Replace the test setup with the shim**

Replace the contents of `web/src/test/setup.ts` with:

```ts
import "@testing-library/jest-dom/vitest";

// jsdom's support for <dialog> is incomplete — showModal()/close() may be
// missing or may not toggle the `open` attribute. The NewSandboxDialog
// relies on the attribute being observable (tests assert visibility via
// DOM queries, and showModal is called imperatively on mount). Stub both
// methods so the element behaves close enough to the browser for tests.
// In a real browser these prototype properties already exist, so the
// `if (!...)` guards make the shim a no-op at runtime.
if (typeof HTMLDialogElement !== "undefined") {
  if (!HTMLDialogElement.prototype.showModal) {
    HTMLDialogElement.prototype.showModal = function showModal() {
      this.setAttribute("open", "");
    };
  }
  if (!HTMLDialogElement.prototype.close) {
    HTMLDialogElement.prototype.close = function close() {
      this.removeAttribute("open");
      this.dispatchEvent(new Event("close"));
    };
  }
}
```

- [ ] **Step 2: Verify the existing test suite still passes**

```bash
cd web && npm test -- --run
```

Expected: all existing tests still pass (the shim only adds new functionality; it cannot break existing behavior).

- [ ] **Step 3: Commit**

```bash
git add web/src/test/setup.ts
git commit -m "test(web): shim HTMLDialogElement.showModal/close for jsdom

jsdom 25 has incomplete native <dialog> support — showModal/close may be
missing or may not toggle the open attribute. Stub both to track the
attribute so the incoming NewSandboxDialog tests can assert visibility
via ordinary DOM queries. In real browsers the prototype properties
already exist, so the guards make the shim a no-op.

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

### Task 4: `NewSandboxDialog` — scaffold (form fields, open/close, validation)

**Files:**
- Create: `web/src/components/NewSandboxDialog.tsx`
- Create: `web/src/components/NewSandboxDialog.test.tsx`

This task creates the dialog with everything except the submit flow and error messages. Subsequent tasks layer on submit, errors, and page wiring. The scaffold is committed in a working state: it renders, validates the name field, and has a disabled Create button when the name is empty.

- [ ] **Step 1: Write the scaffold test file**

Create `web/src/components/NewSandboxDialog.test.tsx`:

```tsx
import { describe, it, expect, beforeAll, afterEach, afterAll, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useParams } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import NewSandboxDialog from "./NewSandboxDialog";

const sampleProject = {
  ProjectID: "prj_1",
  Name: "default",
  CreatedAt: "2026-01-01T00:00:00Z",
  UpdatedAt: "2026-01-01T00:00:00Z",
  Metadata: null,
};

const secondProject = {
  ProjectID: "prj_2",
  Name: "other",
  CreatedAt: "2026-01-01T00:00:00Z",
  UpdatedAt: "2026-01-01T00:00:00Z",
  Metadata: null,
};

const server = setupServer(
  http.get("/v1/projects", () =>
    HttpResponse.json({ data: [sampleProject, secondProject], pagination: null }),
  ),
);

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  server.resetHandlers();
  localStorage.clear();
});
afterAll(() => server.close());

function DetailStub() {
  const { id } = useParams<{ id: string }>();
  return <div>Detail for {id}</div>;
}

function renderDialog(onClose = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/sandboxes"]}>
        <Routes>
          <Route
            path="/sandboxes"
            element={<NewSandboxDialog onClose={onClose} />}
          />
          <Route path="/sandboxes/:id" element={<DetailStub />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...utils, onClose };
}

describe("NewSandboxDialog — scaffold", () => {
  it("renders a title and the core form fields", async () => {
    renderDialog();
    expect(await screen.findByText(/new sandbox/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/name/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/cpu/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/memory/i)).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /isolated/i })).toBeChecked();
    expect(
      screen.getByRole("radio", { name: /published/i }),
    ).not.toBeChecked();
  });

  it("disables Create when the name is empty and enables it once filled", async () => {
    renderDialog();
    const create = await screen.findByRole("button", { name: /^create$/i });
    expect(create).toBeDisabled();
    await userEvent.type(screen.getByLabelText(/name/i), "my-sandbox");
    expect(create).toBeEnabled();
    await userEvent.clear(screen.getByLabelText(/name/i));
    expect(create).toBeDisabled();
  });

  it("trims whitespace-only names and keeps Create disabled", async () => {
    renderDialog();
    const create = await screen.findByRole("button", { name: /^create$/i });
    await userEvent.type(screen.getByLabelText(/name/i), "   ");
    expect(create).toBeDisabled();
  });

  it("calls onClose when the Cancel button is clicked", async () => {
    const { onClose } = renderDialog();
    await screen.findByRole("button", { name: /^create$/i });
    await userEvent.click(screen.getByRole("button", { name: /^cancel$/i }));
    expect(onClose).toHaveBeenCalled();
  });

  it("shows the dialog element with the open attribute after mount", async () => {
    renderDialog();
    await screen.findByText(/new sandbox/i);
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("open");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd web && npx vitest run src/components/NewSandboxDialog.test.tsx
```

Expected: FAIL with `Cannot find module './NewSandboxDialog'`.

- [ ] **Step 3: Create the scaffold component**

Create `web/src/components/NewSandboxDialog.tsx`:

```tsx
import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { useNavigate } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "@/api/client";
import { listProjects } from "@/api/projects";
import { createSandbox } from "@/api/sandboxes";
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
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { readLastProject, writeLastProject } = useLastProject();

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
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

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

  function handleCancelEvent(e: React.SyntheticEvent<HTMLDialogElement>) {
    // ESC key triggers the native `cancel` event on the dialog. We
    // preventDefault to keep the dialog element alive — React unmounts
    // it cleanly when the parent flips `open` to false via onClose.
    e.preventDefault();
    if (!pending) onClose();
  }

  function handleBackdropClick(e: React.MouseEvent<HTMLDialogElement>) {
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
            create and start a new sandbox
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
```

- [ ] **Step 4: Run the scaffold tests to verify they pass**

```bash
cd web && npx vitest run src/components/NewSandboxDialog.test.tsx
```

Expected: PASS with 5 tests in "NewSandboxDialog — scaffold".

- [ ] **Step 5: Commit**

```bash
git add web/src/components/NewSandboxDialog.tsx web/src/components/NewSandboxDialog.test.tsx
git commit -m "feat(web): scaffold NewSandboxDialog form and validation

Creates the dialog component with name, project, image (presets +
custom), CPU/memory, and network fields. Uses the native <dialog>
element with imperative showModal() on mount. The Create button is
disabled until a trimmed name is present and a project is selected.
The onSubmit handler is stubbed — Task 7 wires it to createSandbox,
invalidation, and navigation.

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

### Task 5: `NewSandboxDialog` — project defaulting from `useLastProject`

**Files:**
- Modify: `web/src/components/NewSandboxDialog.test.tsx` (add describe block)

The component already reads `useLastProject` in Task 4's effect. This task just verifies the behavior under the three scenarios: no stored id, stored id that's still valid, and stored id that points at a deleted project.

- [ ] **Step 1: Append the project-default tests**

Add this describe block to `web/src/components/NewSandboxDialog.test.tsx` (after the existing "scaffold" block):

```tsx
describe("NewSandboxDialog — project defaulting", () => {
  it("defaults to the first project when no id is stored", async () => {
    renderDialog();
    await screen.findByText(/new sandbox/i);
    const select = screen.getByLabelText(/project/i) as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe("prj_1"));
  });

  it("honors a stored project id when it still exists", async () => {
    localStorage.setItem("navaris.lastProjectId", "prj_2");
    renderDialog();
    await screen.findByText(/new sandbox/i);
    const select = screen.getByLabelText(/project/i) as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe("prj_2"));
  });

  it("falls back to the first project when the stored id is stale", async () => {
    localStorage.setItem("navaris.lastProjectId", "prj_gone");
    renderDialog();
    await screen.findByText(/new sandbox/i);
    const select = screen.getByLabelText(/project/i) as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe("prj_1"));
  });

  it("disables Create and shows a helper when there are no projects", async () => {
    server.use(
      http.get("/v1/projects", () =>
        HttpResponse.json({ data: [], pagination: null }),
      ),
    );
    renderDialog();
    await screen.findByText(/new sandbox/i);
    expect(
      await screen.findByText(/no projects — create one via the cli/i),
    ).toBeInTheDocument();
    const create = screen.getByRole("button", { name: /^create$/i });
    await userEvent.type(screen.getByLabelText(/name/i), "test");
    expect(create).toBeDisabled();
  });
});
```

- [ ] **Step 2: Run the new tests**

```bash
cd web && npx vitest run src/components/NewSandboxDialog.test.tsx -t "project defaulting"
```

Expected: PASS with 4 tests. (The logic already exists — this task only adds assertions.)

- [ ] **Step 3: Commit**

```bash
git add web/src/components/NewSandboxDialog.test.tsx
git commit -m "test(web): verify NewSandboxDialog project defaulting

Covers the three project-selection scenarios: no stored id, stored id
still present, stored id stale (deleted project). Also verifies the
no-projects state disables Create and shows the CLI helper text.

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

### Task 6: `NewSandboxDialog` — image picker tests (presets + custom)

**Files:**
- Modify: `web/src/components/NewSandboxDialog.test.tsx`

The image picker is already implemented in the scaffold (Task 4). This task adds tests that verify the preset buttons switch selection and the "Custom…" option reveals a text input whose value is used as the image ref.

- [ ] **Step 1: Append the image picker tests**

Add this describe block to `web/src/components/NewSandboxDialog.test.tsx`:

```tsx
describe("NewSandboxDialog — image picker", () => {
  it("defaults to alpine/3.21 preset", async () => {
    renderDialog();
    const alpine = await screen.findByRole("button", { name: /alpine\/3\.21/i });
    // Selected preset uses the --fg-primary color class; non-selected uses
    // --fg-secondary. We assert via the class list to avoid coupling to
    // the specific hex, which lives in index.css.
    expect(alpine.className).toContain("border-[var(--fg-primary)]");
  });

  it("switches selection when a different preset is clicked", async () => {
    renderDialog();
    const debian = await screen.findByRole("button", { name: /debian-12/i });
    await userEvent.click(debian);
    expect(debian.className).toContain("border-[var(--fg-primary)]");
    const alpine = screen.getByRole("button", { name: /alpine\/3\.21/i });
    expect(alpine.className).not.toContain("border-[var(--fg-primary)]");
  });

  it("reveals a text input when Custom… is selected", async () => {
    renderDialog();
    const custom = await screen.findByRole("button", { name: /custom…/i });
    expect(screen.queryByLabelText(/custom image ref/i)).not.toBeInTheDocument();
    await userEvent.click(custom);
    expect(screen.getByLabelText(/custom image ref/i)).toBeInTheDocument();
  });

  it("disables Create when Custom… is selected but the text input is empty", async () => {
    renderDialog();
    const custom = await screen.findByRole("button", { name: /custom…/i });
    await userEvent.click(custom);
    await userEvent.type(screen.getByLabelText(/name/i), "my-sandbox");
    // Wait for the project dropdown to default to prj_1 before asserting
    // the Create button state — otherwise canSubmit is gated by the empty
    // projectId and not by the empty custom ref.
    const select = screen.getByLabelText(/project/i) as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe("prj_1"));
    expect(screen.getByRole("button", { name: /^create$/i })).toBeDisabled();
  });
});
```

- [ ] **Step 2: Run the new tests**

```bash
cd web && npx vitest run src/components/NewSandboxDialog.test.tsx -t "image picker"
```

Expected: PASS with 4 tests.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/NewSandboxDialog.test.tsx
git commit -m "test(web): verify NewSandboxDialog image picker behavior

Asserts that preset selection switches on click, that Custom… reveals a
text input, and that an empty custom ref keeps Create disabled.

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

### Task 7: `NewSandboxDialog` — submit flow + navigation

**Files:**
- Modify: `web/src/components/NewSandboxDialog.tsx` (fill in `onSubmit`)
- Modify: `web/src/components/NewSandboxDialog.test.tsx` (add submit test)

- [ ] **Step 1: Write the failing submit test**

Append to `web/src/components/NewSandboxDialog.test.tsx`:

```tsx
describe("NewSandboxDialog — submit", () => {
  it("POSTs the form, invalidates sandboxes, writes last project, and navigates", async () => {
    let seenBody: Record<string, unknown> = {};
    server.use(
      http.post("/v1/sandboxes", async ({ request }) => {
        seenBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(
          {
            OperationID: "op_1",
            ResourceType: "sandbox",
            ResourceID: "sbx_created",
            SandboxID: "sbx_created",
            SnapshotID: "",
            Type: "create_sandbox",
            State: "pending",
            StartedAt: "2026-04-08T12:00:00Z",
            FinishedAt: null,
            ErrorText: "",
            Metadata: null,
          },
          { status: 202 },
        );
      }),
    );
    const { onClose } = renderDialog();
    await screen.findByText(/new sandbox/i);
    await userEvent.type(screen.getByLabelText(/name/i), "integration-test");
    await userEvent.type(screen.getByLabelText(/cpu/i), "2");
    await userEvent.type(screen.getByLabelText(/memory/i), "512");
    await userEvent.click(screen.getByRole("button", { name: /^create$/i }));
    // Navigating to /sandboxes/:id renders the DetailStub.
    expect(await screen.findByText("Detail for sbx_created")).toBeInTheDocument();
    expect(onClose).toHaveBeenCalled();
    expect(localStorage.getItem("navaris.lastProjectId")).toBe("prj_1");
    expect(seenBody).toEqual({
      project_id: "prj_1",
      name: "integration-test",
      image_id: "alpine/3.21",
      cpu_limit: 2,
      memory_limit_mb: 512,
      network_mode: "isolated",
    });
  });

  it("omits cpu_limit and memory_limit_mb when the inputs are empty", async () => {
    let seenBody: Record<string, unknown> = {};
    server.use(
      http.post("/v1/sandboxes", async ({ request }) => {
        seenBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(
          {
            OperationID: "op_2",
            ResourceType: "sandbox",
            ResourceID: "sbx_nolimits",
            SandboxID: "sbx_nolimits",
            SnapshotID: "",
            Type: "create_sandbox",
            State: "pending",
            StartedAt: "2026-04-08T12:00:00Z",
            FinishedAt: null,
            ErrorText: "",
            Metadata: null,
          },
          { status: 202 },
        );
      }),
    );
    renderDialog();
    await screen.findByText(/new sandbox/i);
    await userEvent.type(screen.getByLabelText(/name/i), "no-limits");
    await userEvent.click(screen.getByRole("button", { name: /^create$/i }));
    await screen.findByText("Detail for sbx_nolimits");
    expect(seenBody).not.toHaveProperty("cpu_limit");
    expect(seenBody).not.toHaveProperty("memory_limit_mb");
  });

  it("sends a custom image ref when Custom… is picked", async () => {
    let seenBody: Record<string, unknown> = {};
    server.use(
      http.post("/v1/sandboxes", async ({ request }) => {
        seenBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(
          {
            OperationID: "op_3",
            ResourceType: "sandbox",
            ResourceID: "sbx_custom",
            SandboxID: "sbx_custom",
            SnapshotID: "",
            Type: "create_sandbox",
            State: "pending",
            StartedAt: "2026-04-08T12:00:00Z",
            FinishedAt: null,
            ErrorText: "",
            Metadata: null,
          },
          { status: 202 },
        );
      }),
    );
    renderDialog();
    await screen.findByText(/new sandbox/i);
    await userEvent.click(screen.getByRole("button", { name: /custom…/i }));
    await userEvent.type(
      screen.getByLabelText(/custom image ref/i),
      "images:ubuntu/24.04",
    );
    await userEvent.type(screen.getByLabelText(/name/i), "custom-image");
    await userEvent.click(screen.getByRole("button", { name: /^create$/i }));
    await screen.findByText("Detail for sbx_custom");
    expect(seenBody.image_id).toBe("images:ubuntu/24.04");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd web && npx vitest run src/components/NewSandboxDialog.test.tsx -t "submit"
```

Expected: FAIL — submit handler is stubbed, nothing happens on click.

- [ ] **Step 3: Fill in the `onSubmit` implementation**

Replace the stubbed `onSubmit` in `web/src/components/NewSandboxDialog.tsx` with:

```tsx
  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!canSubmit) return;
    setError(null);
    setPending(true);
    try {
      const req = {
        project_id: projectId,
        name: name.trim(),
        image_id: imageRef,
        network_mode: networkMode,
        // Empty strings from type="number" inputs become NaN via Number,
        // which we filter out here. The CreateSandboxRequest type treats
        // these as optional so undefined is dropped by JSON.stringify.
        cpu_limit: cpuLimit === "" ? undefined : Number(cpuLimit),
        memory_limit_mb:
          memoryLimitMB === "" ? undefined : Number(memoryLimitMB),
      };
      const op = await createSandbox(req);
      writeLastProject(projectId);
      await queryClient.invalidateQueries({ queryKey: ["sandboxes"] });
      onClose();
      navigate(`/sandboxes/${op.ResourceID}`);
    } catch (err) {
      setError(
        err instanceof ApiError
          ? messageForCreateError(err)
          : "Unable to create sandbox. Try again.",
      );
    } finally {
      setPending(false);
    }
  }
```

Add this helper at the bottom of the file (after the component export):

```tsx
// messageForCreateError maps common HTTP statuses to friendlier copy.
// We key off err.status rather than err.code because the navarisd
// errorResponse shape in internal/api/response.go is
// `{"error": {"code": <int>, "message": "..."}}`, and apiFetch reads
// body.code at the top level — so err.code falls back to `http_<N>`
// and isn't useful for branching. Status is the reliable signal.
// For 5xx, the server has already redacted the message before it
// reaches the wire, so any fallback copy we use here is fine.
function messageForCreateError(err: ApiError): string {
  if (err.status === 409) {
    return "A sandbox with that name already exists in this project.";
  }
  if (err.status === 422) {
    return err.message || "Invalid request.";
  }
  if (err.status >= 500) {
    return "Server error. Try again.";
  }
  return err.message || "Unable to create sandbox. Try again.";
}
```

- [ ] **Step 4: Run the submit tests again**

```bash
cd web && npx vitest run src/components/NewSandboxDialog.test.tsx -t "submit"
```

Expected: PASS with 3 tests.

- [ ] **Step 5: Run the full dialog test file to catch regressions**

```bash
cd web && npx vitest run src/components/NewSandboxDialog.test.tsx
```

Expected: PASS with all tests from Tasks 4-7 (14+ tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/components/NewSandboxDialog.tsx web/src/components/NewSandboxDialog.test.tsx
git commit -m "feat(web): wire NewSandboxDialog submit to createSandbox

Submit now POSTs to /v1/sandboxes, writes the last-used project id,
awaits invalidation of the [sandboxes] query key, closes the dialog,
and navigates to the new sandbox's detail page. Numeric limits are
omitted from the body when their inputs are empty so JSON.stringify
drops them rather than sending NaN or null.

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

### Task 8: `NewSandboxDialog` — error handling and pending guard

**Files:**
- Modify: `web/src/components/NewSandboxDialog.test.tsx`

The error helper is already implemented in Task 7. This task adds the tests that verify it (409 copy, generic fallback) and the pending-guard behavior (ESC / Cancel do nothing while a submit is in flight).

- [ ] **Step 1: Write the failing error tests**

Append to `web/src/components/NewSandboxDialog.test.tsx`:

```tsx
describe("NewSandboxDialog — errors", () => {
  it("shows a specific message on 409 conflict and stays open", async () => {
    server.use(
      http.post("/v1/sandboxes", () =>
        HttpResponse.json(
          { error: { code: 409, message: "conflict" } },
          { status: 409 },
        ),
      ),
    );
    const { onClose } = renderDialog();
    await screen.findByText(/new sandbox/i);
    await userEvent.type(screen.getByLabelText(/name/i), "dup-name");
    await userEvent.click(screen.getByRole("button", { name: /^create$/i }));
    expect(
      await screen.findByText(/already exists in this project/i),
    ).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
    // Create button re-enables so the user can correct and retry.
    expect(screen.getByRole("button", { name: /^create$/i })).toBeEnabled();
  });

  it("shows a fallback on 500 without leaking the server body", async () => {
    server.use(
      http.post("/v1/sandboxes", () =>
        HttpResponse.json(
          { error: { code: 500, message: "internal server error" } },
          { status: 500 },
        ),
      ),
    );
    renderDialog();
    await screen.findByText(/new sandbox/i);
    await userEvent.type(screen.getByLabelText(/name/i), "boom");
    await userEvent.click(screen.getByRole("button", { name: /^create$/i }));
    expect(
      await screen.findByText(/server error\. try again/i),
    ).toBeInTheDocument();
  });

  it("shows a generic message on network failure", async () => {
    server.use(
      http.post("/v1/sandboxes", () => HttpResponse.error()),
    );
    renderDialog();
    await screen.findByText(/new sandbox/i);
    await userEvent.type(screen.getByLabelText(/name/i), "offline");
    await userEvent.click(screen.getByRole("button", { name: /^create$/i }));
    expect(
      await screen.findByText(/unable to create sandbox/i),
    ).toBeInTheDocument();
  });
});

describe("NewSandboxDialog — pending guard", () => {
  it("ignores ESC while pending", async () => {
    // Hold the POST open so the dialog stays in the pending state for the
    // duration of the test. We resolve it manually at the end.
    let resolveCreate: (() => void) | null = null;
    const createPromise = new Promise<void>((res) => {
      resolveCreate = res;
    });
    server.use(
      http.post("/v1/sandboxes", async () => {
        await createPromise;
        return HttpResponse.json(
          {
            OperationID: "op_pending",
            ResourceType: "sandbox",
            ResourceID: "sbx_pending",
            SandboxID: "sbx_pending",
            SnapshotID: "",
            Type: "create_sandbox",
            State: "pending",
            StartedAt: "2026-04-08T12:00:00Z",
            FinishedAt: null,
            ErrorText: "",
            Metadata: null,
          },
          { status: 202 },
        );
      }),
    );
    const { onClose } = renderDialog();
    await screen.findByText(/new sandbox/i);
    await userEvent.type(screen.getByLabelText(/name/i), "pending");
    await userEvent.click(screen.getByRole("button", { name: /^create$/i }));
    // Now the mutation is in flight: Create button becomes "Creating…".
    expect(
      await screen.findByRole("button", { name: /creating/i }),
    ).toBeInTheDocument();
    // Firing a cancel event on the dialog must NOT invoke onClose.
    const dialog = screen.getByRole("dialog");
    dialog.dispatchEvent(
      new Event("cancel", { bubbles: true, cancelable: true }),
    );
    expect(onClose).not.toHaveBeenCalled();
    // Clicking Cancel is also a no-op while pending (disabled button).
    expect(screen.getByRole("button", { name: /^cancel$/i })).toBeDisabled();
    // Release the hanging mutation so the test can unwind cleanly.
    resolveCreate?.();
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });
});
```

- [ ] **Step 2: Run the new tests**

```bash
cd web && npx vitest run src/components/NewSandboxDialog.test.tsx -t "errors|pending guard"
```

Expected: PASS. The `messageForCreateError` helper already keys off `err.status` (set up in Task 7 Step 3) so the 409 conflict test matches the friendly copy, the 500 test matches "Server error. Try again.", and the non-ApiError network-failure test hits the generic fallback.

- [ ] **Step 3: Verify all error tests pass**

```bash
cd web && npx vitest run src/components/NewSandboxDialog.test.tsx
```

Expected: PASS across all NewSandboxDialog describe blocks (17+ tests total).

- [ ] **Step 4: Commit**

```bash
git add web/src/components/NewSandboxDialog.tsx web/src/components/NewSandboxDialog.test.tsx
git commit -m "feat(web): NewSandboxDialog error handling and pending guard

Covers the 409/5xx/network-failure branches with tests over the
status-based messageForCreateError helper, and asserts the pending
guard: ESC and Cancel must not close the dialog while a submit is
in flight.

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

### Task 9: Wire the dialog into the Sandboxes page

**Files:**
- Modify: `web/src/routes/Sandboxes.tsx`
- Modify: `web/src/routes/Sandboxes.test.tsx`

- [ ] **Step 1: Write the failing Sandboxes-page tests**

Append to the `describe("Sandboxes list", ...)` block in `web/src/routes/Sandboxes.test.tsx`:

```tsx
  it("shows a New sandbox button in the header", async () => {
    renderPage();
    await screen.findByText("fedora-test-01");
    expect(
      screen.getByRole("button", { name: /new sandbox/i }),
    ).toBeInTheDocument();
  });

  it("does not mount the dialog on initial render", async () => {
    renderPage();
    await screen.findByText("fedora-test-01");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("opens the dialog when New sandbox is clicked", async () => {
    renderPage();
    await screen.findByText("fedora-test-01");
    await userEvent.click(
      screen.getByRole("button", { name: /new sandbox/i }),
    );
    expect(
      await screen.findByRole("dialog"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: /new sandbox/i }),
    ).toBeInTheDocument();
  });
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd web && npx vitest run src/routes/Sandboxes.test.tsx
```

Expected: FAIL — the button doesn't exist yet.

- [ ] **Step 3: Add the button, state, and dialog mount to Sandboxes.tsx**

Modify `web/src/routes/Sandboxes.tsx`:

1. Append the `NewSandboxDialog` import. The existing `useMemo, useState` React import is already present and does not need to change — only the new component import is added:

```tsx
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { listProjects } from "@/api/projects";
import { listSandboxes } from "@/api/sandboxes";
import type { Sandbox, SandboxState } from "@/types/navaris";
import { StateBadge } from "@/components/StateBadge";
import NewSandboxDialog from "@/components/NewSandboxDialog";
```

2. Inside the `Sandboxes` component, add the `open` state near the existing `stateFilter` state:

```tsx
  const [stateFilter, setStateFilter] = useState<StateFilter>("all");
  const [backendFilter, setBackendFilter] = useState<BackendFilter>("all");
  const [newDialogOpen, setNewDialogOpen] = useState(false);
```

3. Update the header in the JSX to include the button. Replace the existing `<header>...</header>` block with:

```tsx
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
```

4. At the very end of the component's JSX (just before the closing `</div>` of the outermost element), add the conditional dialog mount:

```tsx
      {newDialogOpen && (
        <NewSandboxDialog onClose={() => setNewDialogOpen(false)} />
      )}
```

- [ ] **Step 4: Run the Sandboxes tests again**

```bash
cd web && npx vitest run src/routes/Sandboxes.test.tsx
```

Expected: PASS with the three new tests (plus all existing Sandboxes tests still passing).

- [ ] **Step 5: Commit**

```bash
git add web/src/routes/Sandboxes.tsx web/src/routes/Sandboxes.test.tsx
git commit -m "feat(web): mount NewSandboxDialog from the Sandboxes page

Adds a 'New sandbox' button to the list-page header, conditional-mounts
the dialog when clicked, and unmounts it via the parent's open state
(so each opening starts with a fresh form). The dialog is NOT mounted
on initial render — it only appears after the button is clicked.

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

### Task 10: Full test suite + type-check + production build

**Files:** none modified

Final verification that the feature compiles, type-checks cleanly, and all tests pass together. The existing suites have not been modified other than for the Sandboxes and setup files, so they should all still pass.

- [ ] **Step 1: Run the full web test suite**

```bash
cd /home/eran/work/navaris && make web-test
```

Expected: PASS. If any unrelated test fails, inspect it — the shim in `setup.ts` and the new Sandboxes tests are the only cross-cutting changes.

- [ ] **Step 2: Type-check the web source**

```bash
cd /home/eran/work/navaris/web && npm run lint
```

(This runs `tsc --noEmit`.) Expected: no errors.

- [ ] **Step 3: Run the production build**

```bash
cd /home/eran/work/navaris && make web-build
```

Expected: Vite build completes with no errors. The `dist/` output is copied to `internal/webui/dist/` by the make target.

- [ ] **Step 4: Manual smoke test (optional but recommended)**

If a local navarisd is already running via the all-in-one Docker image:

```bash
docker compose --profile default up -d --build navaris
```

Then in a browser:

1. Log in with the UI password.
2. Navigate to `/sandboxes`.
3. Click **New sandbox**.
4. Verify the dialog appears with the Alpine preset selected.
5. Fill in a name, leave CPU/memory blank, click **Create**.
6. Verify the browser lands on `/sandboxes/<new-id>` and the sandbox shows up in `pending` → `starting` → `running`.
7. Refresh the list — the sandbox is present with the expected fields.

- [ ] **Step 5: Final commit (only if anything had to be fixed)**

If Steps 1-3 caught any lint or type errors, fix them and commit:

```bash
git add <fixed files>
git commit -m "fix(web): resolve lint/type issues in Create Sandbox UI

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

If everything passed cleanly, no additional commit is needed for this task.

---

## Success Criteria

The feature is complete when:

1. A user on the Sandboxes list can click **New sandbox** and see the modal dialog.
2. The dialog pre-selects their last-used project (or the first project if there's no history).
3. Filling in a name, choosing a preset image, and clicking Create submits `POST /v1/sandboxes`, invalidates the list query, closes the dialog, and lands on the new sandbox's detail page.
4. A duplicate name shows the "already exists in this project" message without closing the dialog.
5. The full Vitest suite passes (including the new tests for `createSandbox`, `useLastProject`, `NewSandboxDialog`, and `Sandboxes`).
6. `tsc --noEmit` passes with no type errors.
7. `make web-build` produces a clean production bundle.

## Out of Scope (DO NOT implement)

These are called out in the spec's Out-of-Scope section and should stay out:

- Snapshot source picker
- Expires-at / TTL input
- Metadata / labels editor
- Host pinning
- Explicit backend toggle
- Live image inventory endpoint
- Optimistic cache updates
- Playwright end-to-end tests
- Bulk / multi-sandbox create

If the implementer subagent starts building any of these, the spec reviewer will reject the work.
