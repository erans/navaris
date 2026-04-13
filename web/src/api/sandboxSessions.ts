import { apiFetch } from "./client";
import type { ListResponse, Session } from "@/types/navaris";

export async function listSessions(sandboxId: string): Promise<Session[]> {
  const res = await apiFetch<ListResponse<Session>>(
    `/v1/sandboxes/${encodeURIComponent(sandboxId)}/sessions`,
  );
  return res.data;
}

export async function createSession(
  sandboxId: string,
  shell?: string,
): Promise<Session> {
  return apiFetch<Session>(
    `/v1/sandboxes/${encodeURIComponent(sandboxId)}/sessions`,
    {
      method: "POST",
      json: { backing: "tmux", shell: shell ?? "" },
    },
  );
}

export async function destroySession(sessionId: string): Promise<void> {
  await apiFetch<unknown>(`/v1/sessions/${encodeURIComponent(sessionId)}`, {
    method: "DELETE",
  });
}
