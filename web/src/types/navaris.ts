// Navaris domain types mirror the Go structs in internal/api/dto.go. Keep
// these in sync by hand — the project intentionally does not auto-generate
// types yet. Optional fields match the `omitempty` tags on the server side.

export type SandboxState = "pending" | "running" | "stopped" | "failed";

export type Backend = "incus" | "firecracker";

export interface Project {
  id: string;
  name: string;
  createdAt: number;
}

export interface Sandbox {
  id: string;
  projectId: string;
  name: string;
  backend: Backend;
  state: SandboxState;
  image: string;
  cpu: number;
  memoryMB: number;
  createdAt: number;
  startedAt?: number;
  stoppedAt?: number;
  reason?: string;
}

export interface ListProjectsResponse {
  projects: Project[];
}

export interface ListSandboxesResponse {
  sandboxes: Sandbox[];
}

export type EventType =
  | "project_created"
  | "project_deleted"
  | "sandbox_created"
  | "sandbox_started"
  | "sandbox_stopped"
  | "sandbox_failed"
  | "sandbox_deleted";

export interface Event {
  id: string;
  type: EventType;
  timestamp: number;
  projectId?: string;
  sandboxId?: string;
  message?: string;
}
