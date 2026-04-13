import { apiFetch } from "./client";
import type { ListResponse, Project } from "@/types/navaris";

// listProjects fetches every project the caller can see. The server wraps
// list responses in a {data, pagination} envelope (see
// internal/api/response.go), so we unwrap once here and hand the array back
// to callers.
export async function listProjects(): Promise<Project[]> {
  const res = await apiFetch<ListResponse<Project>>("/v1/projects");
  return res.data;
}

// createProject posts to /v1/projects — see internal/api/project.go
// createProject. The backend requires a non-empty name (400 otherwise) and
// returns 201 with the new Project directly (no list envelope). Metadata is
// optional and we don't expose it from the UI yet; callers pass just the
// name and we omit metadata so JSON.stringify drops the key.
export async function createProject(name: string): Promise<Project> {
  return apiFetch<Project>("/v1/projects", {
    method: "POST",
    json: { name },
  });
}
