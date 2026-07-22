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

async function request(path: string, init?: RequestInit): Promise<CommandResult> {
  const response = await fetch(path, {
    ...init,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  const body = (await response.json()) as CommandResult;
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
