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
