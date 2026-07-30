export type ResultField = { label: string; value: string };

export type CommandResult = {
  state: "success" | "info" | "warning" | "error" | "help";
  title?: string;
  detail?: string;
  fields?: ResultField[];
  table_headers?: string[];
  table_rows?: string[][];
  lines?: string[];
  next?: string[];
};

export class SessionExpiredError extends Error {}

async function request<T = CommandResult>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  const body = (await response.json()) as T & Partial<CommandResult>;
  if (response.status === 401) {
    throw new SessionExpiredError(body.title ?? "Not authenticated");
  }
  if (!response.ok) {
    throw new Error(body.title ?? "Request failed");
  }
  return body;
}

export function checkSession(): Promise<CommandResult> {
  return request("/api/session");
}

// "safe" means eggyd could not start -- almost always a config.yaml it cannot
// load -- and is serving only the repair surface. Asked before anything else,
// because in safe mode every other route is absent or reporting the failure.
export type Mode = "normal" | "safe";

export function getMode(): Promise<Mode> {
  return request<{ mode: Mode }>("/api/mode").then((result) => result.mode);
}

export function getStartupFailure(): Promise<CommandResult> {
  return request("/api/safemode");
}

// The raw config is text, not JSON: it is the owner's file, comments and all,
// handed back exactly as stored so what they edit is what they wrote.
export async function getRawConfig(): Promise<string> {
  const response = await fetch("/api/config/raw", { credentials: "same-origin" });
  if (response.status === 401) {
    throw new SessionExpiredError("Not authenticated");
  }
  if (!response.ok) {
    throw new Error("Could not read config.yaml");
  }
  return response.text();
}

export async function saveRawConfig(body: string): Promise<CommandResult> {
  const response = await fetch("/api/config/raw", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "text/yaml" },
    body,
  });
  const result = (await response.json()) as CommandResult;
  if (response.status === 401) {
    throw new SessionExpiredError(result.title ?? "Not authenticated");
  }
  if (!response.ok) {
    // The rejection reason is the whole point here: it is why the config
    // would not have started, and it is what the owner edits against.
    throw new Error(result.title ?? "Eggy refused the config");
  }
  return result;
}

export function login(email: string, password: string): Promise<CommandResult> {
  return request("/api/login", { method: "POST", body: JSON.stringify({ email, password }) });
}

export function logout(): Promise<CommandResult> {
  return request("/api/logout", { method: "POST" });
}

export type ConfigSection = "providers" | "models" | "calendar";

export function getConfig(section: ConfigSection): Promise<CommandResult> {
  return request(`/api/config/${section}`);
}

export function setConfig(section: ConfigSection, values: Record<string, string>): Promise<CommandResult> {
  return request(`/api/config/${section}`, { method: "POST", body: JSON.stringify(values) });
}

export type MCPServerInput = {
  name: string;
  url: string;
  auth: "oauth" | "bearer-env" | "none";
  bearer_token_env: string;
  enabled: boolean;
};

export function listMCPServers(): Promise<CommandResult> {
  return request("/api/config/mcp");
}

export function setMCPServer(input: MCPServerInput): Promise<CommandResult> {
  return request("/api/config/mcp", { method: "POST", body: JSON.stringify(input) });
}

export function removeMCPServer(name: string): Promise<CommandResult> {
  return request(`/api/config/mcp/${encodeURIComponent(name)}`, { method: "DELETE" });
}

export type ChatEvent = {
  kind: "message" | "typing" | "edit" | "approval";
  id?: string;
  text?: string;
  approval?: { id: string; summary: string };
};

export type Thread = { id: string; title: string; updatedAt: string };

export function listThreads(): Promise<Thread[]> {
  return request("/api/chat/threads").then((result) =>
    (result.table_rows ?? []).map((row) => ({ id: row[0], title: row[1], updatedAt: row[2] })),
  );
}

export function createThread(): Promise<string> {
  return request<{ id: string }>("/api/chat/threads", { method: "POST" }).then((result) => result.id);
}

export function sendChatMessage(threadId: string, text: string): Promise<CommandResult> {
  return request(`/api/chat/threads/${encodeURIComponent(threadId)}/send`, { method: "POST", body: JSON.stringify({ text }) });
}

export function approveChatDecision(approvalId: string, approved: boolean): Promise<CommandResult> {
  return request("/api/chat/approve", { method: "POST", body: JSON.stringify({ approval_id: approvalId, approved }) });
}

export function getChatHistory(threadId: string): Promise<CommandResult> {
  return request(`/api/chat/threads/${encodeURIComponent(threadId)}/history`);
}
