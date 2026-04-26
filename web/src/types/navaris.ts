// Navaris domain types mirror the Go structs in internal/domain/. The domain
// structs do not declare json tags, so encoding/json serialises field names
// in their original Go casing (PascalCase). The list response wrapper in
// internal/api/response.go does declare json tags, so the envelope itself is
// lowercase. The Go API tests in internal/api/*_test.go assert on the
// PascalCase shape, so this is the intentional, tested wire contract.
//
// Keep these in sync by hand — the project intentionally does not auto-
// generate types yet.

export type SandboxState =
  | "pending"
  | "starting"
  | "running"
  | "stopping"
  | "stopped"
  | "failed"
  | "destroyed";

export type NetworkMode = "isolated" | "published";

export interface Project {
  ProjectID: string;
  Name: string;
  CreatedAt: string;
  UpdatedAt: string;
  Metadata: Record<string, unknown> | null;
}

// ActiveBoost mirrors the boostResponse Go struct in internal/api/boost.go.
// That struct declares json tags, so wire field names are snake_case.
export interface ActiveBoost {
  boost_id: string;
  sandbox_id: string;
  original_cpu_limit: number | null;
  original_memory_limit_mb: number | null;
  boosted_cpu_limit: number | null;
  boosted_memory_limit_mb: number | null;
  started_at: string;
  expires_at: string;
  state: "active" | "revert_failed";
  revert_attempts?: number;
  last_error?: string;
}

export interface Sandbox {
  SandboxID: string;
  ProjectID: string;
  Name: string;
  State: SandboxState;
  Backend: string;
  BackendRef: string;
  HostID: string;
  SourceImageID: string;
  ParentSnapshotID: string;
  CreatedAt: string;
  UpdatedAt: string;
  ExpiresAt: string | null;
  CPULimit: number | null;
  MemoryLimitMB: number | null;
  NetworkMode: NetworkMode | "";
  Metadata: Record<string, unknown> | null;
  // active_boost is snake_case because the backend serialises the embedded
  // boostResponse with a json:"active_boost" tag (unlike the base Sandbox
  // fields which have no tags and therefore serialise as PascalCase).
  active_boost?: ActiveBoost;
}

// ListResponse is the envelope returned by endpoints like GET /v1/projects and
// GET /v1/sandboxes. The envelope keys are lowercase because
// internal/api/response.go declares json tags on the wrapper struct itself.
export interface ListResponse<T> {
  data: T[];
  pagination: unknown;
}

export type SessionState = "active" | "detached" | "exited" | "destroyed";

export interface Session {
  SessionID: string;
  SandboxID: string;
  Backing: string;
  Shell: string;
  State: SessionState;
  CreatedAt: string;
  UpdatedAt: string;
  LastAttachedAt: string | null;
  IdleTimeout: number | null;
  Metadata: Record<string, unknown> | null;
}

// EventType mirrors domain.EventType. The set is fixed by the backend; new
// values must be added in lockstep with internal/domain/event.go.
export type EventType =
  | "sandbox_state_changed"
  | "snapshot_state_changed"
  | "image_state_changed"
  | "session_state_changed"
  | "operation_state_changed"
  | "exec_output"
  | "exec_completed"
  | "ui.login"
  | "ui.login_failed"
  | "ui.attach_opened"
  | "ui.attach_closed";

// Event matches domain.Event exactly. Data is an untyped map — consumers
// should narrow on Type before reading specific keys.
export interface Event {
  Type: EventType;
  Timestamp: string;
  Data: Record<string, unknown> | null;
}

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
