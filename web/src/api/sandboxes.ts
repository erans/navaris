import { apiFetch } from "./client";
import type { ListResponse, NetworkMode, Operation, Sandbox } from "@/types/navaris";

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
