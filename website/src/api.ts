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

// --- Home directory files -------------------------------------------------
//
// "edit" files are served as raw text and written back as-is; "read" files
// (logs) are served but never written; "secret" files (.env, auth.json) are
// listed so you can see they exist, but their contents never leave the host.
export type FileAccess = "edit" | "read" | "secret";

export type HomeFile = {
  path: string;
  access: FileAccess;
  size: number;
  modified?: string;
  language?: string;
  missing?: boolean;
  note?: string;
};

export type HomeFileListing = { root: string; files: HomeFile[] };

export type HomeFileContent = { path: string; access: FileAccess; content: string };

export function listFiles(): Promise<HomeFileListing> {
  return request<HomeFileListing>("/api/files");
}

export function readFile(path: string): Promise<HomeFileContent> {
  return request<HomeFileContent>(`/api/files/${encodePath(path)}`);
}

export function writeFile(path: string, content: string): Promise<CommandResult> {
  return request(`/api/files/${encodePath(path)}`, { method: "PUT", body: JSON.stringify({ content }) });
}

// Each segment is escaped separately so the slashes stay real path
// separators for the {path...} route rather than becoming %2F.
function encodePath(path: string): string {
  return path.split("/").map(encodeURIComponent).join("/");
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
