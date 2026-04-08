import { apiFetch } from "./client";
import type { ListResponse, Sandbox } from "@/types/navaris";

// listSandboxes is scoped to a single project. The /v1/sandboxes endpoint
// requires the project_id query parameter — see internal/api/sandbox.go
// listSandboxes — and returns 400 without it.
export async function listSandboxes(projectId: string): Promise<Sandbox[]> {
  const qs = `?project_id=${encodeURIComponent(projectId)}`;
  const res = await apiFetch<ListResponse<Sandbox>>(`/v1/sandboxes${qs}`);
  return res.data;
}

export async function getSandbox(id: string): Promise<Sandbox> {
  return apiFetch<Sandbox>(`/v1/sandboxes/${encodeURIComponent(id)}`);
}

// startSandbox / stopSandbox / destroySandbox each enqueue a long-running
// operation; the backend responds 202 with the Operation object. The UI
// fans state changes through the existing event stream rather than tracking
// the returned Operation, so we discard the body here. Destroy is a POST
// to /destroy — there is no DELETE route on the sandbox resource.
export async function startSandbox(id: string): Promise<void> {
  await apiFetch<unknown>(`/v1/sandboxes/${encodeURIComponent(id)}/start`, {
    method: "POST",
  });
}

export async function stopSandbox(id: string): Promise<void> {
  await apiFetch<unknown>(`/v1/sandboxes/${encodeURIComponent(id)}/stop`, {
    method: "POST",
  });
}

export async function destroySandbox(id: string): Promise<void> {
  await apiFetch<unknown>(`/v1/sandboxes/${encodeURIComponent(id)}/destroy`, {
    method: "POST",
  });
}
