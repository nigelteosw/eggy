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
export type Theme = "dark" | "light";

// The probe carries the theme as well as the mode because it is the only
// response that lands before first paint. Reading the preference any later
// means the panel renders in one theme and then flips to the other.
export type Probe = { mode: Mode; theme: Theme };

export function getMode(): Promise<Probe> {
  return request<Probe>("/api/mode").then((result) => ({
    mode: result.mode ?? "normal",
    theme: result.theme === "light" ? "light" : "dark",
  }));
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

export type ConfigSection = "providers" | "models" | "google" | "heartbeat" | "tracing" | "appearance";

// The theme is owner config rather than browser state, so it survives logging
// in from a different machine. Applying it locally is separate (see
// applyTheme): the write persists the choice, the DOM change shows it.
export function setTheme(theme: Theme): Promise<CommandResult> {
  return setConfig("appearance", { theme });
}

export function applyTheme(theme: Theme): void {
  document.documentElement.classList.toggle("dark", theme === "dark");
}

export function getConfig(section: ConfigSection): Promise<CommandResult> {
  return request(`/api/config/${section}`);
}

export function setConfig(section: ConfigSection, values: Record<string, string>): Promise<CommandResult> {
  return request(`/api/config/${section}`, { method: "POST", body: JSON.stringify(values) });
}

// discoverModels browses one provider's own catalog. It is not a config
// section: nothing is written, and what comes back is a suggestion until the
// owner turns a row into an alias.
export function discoverModels(provider: string): Promise<CommandResult> {
  return request(`/api/config/models/available?provider=${encodeURIComponent(provider)}`);
}

export function removeModelAlias(alias: string): Promise<CommandResult> {
  return request(`/api/config/models/${encodeURIComponent(alias)}`, { method: "DELETE" });
}

export type MCPServerInput = {
  name: string;
  url: string;
  transport: string;
  auth: "oauth" | "bearer-env" | "none";
  bearer_token_env: string;
  // oauth_client_id is a client registered by hand, for an authorization
  // server without dynamic client registration. It is not a secret -- it
  // travels in the authorization URL -- so it is sent as a value. The secret
  // is only ever named: oauth_client_secret_env is an environment variable.
  oauth_client_id: string;
  oauth_client_secret_env: string;
  // Sent as "true"/"false" rather than a JSON boolean: every config route
  // takes a flat string map, and the one decoder in internal/config reads the
  // same words from a chat command's enabled=false.
  enabled: string;
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

// The merged tool catalog: every tool a turn can call, kernel and MCP alike,
// read live from the one registry the loop runs on.
export function listTools(): Promise<CommandResult> {
  return request("/api/tools");
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

export function renameThread(threadId: string, title: string): Promise<CommandResult> {
  return request(`/api/chat/threads/${encodeURIComponent(threadId)}`, { method: "PATCH", body: JSON.stringify({ title }) });
}

export function deleteThread(threadId: string): Promise<CommandResult> {
  return request(`/api/chat/threads/${encodeURIComponent(threadId)}`, { method: "DELETE" });
}

// Approvals waiting on the owner. Deciding one goes through
// approveChatDecision below -- the same route a chat tap uses -- so this is
// only the view that was missing.
export function listApprovals(): Promise<CommandResult> {
  return request("/api/approvals");
}

// The approval mode is strict, normal or auto. Setting names the mode it wants
// rather than advancing to the next one, the same gesture /mode is in chat: a
// panel and a phone starting from different states cannot then disagree about
// where they ended up.
export function getApprovalMode(): Promise<CommandResult> {
  return request("/api/approvals/mode");
}

export function setApprovalMode(mode: string): Promise<CommandResult> {
  return request("/api/approvals/mode", {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ mode }).toString(),
  });
}

// What the chat composer's controls render from: every alias the owner may
// pick, the one in force, the effort levels that model supports, and the
// approval mode. One read rather than three, so the row settles at once.
export type AgentSelection = {
  models: string[];
  model: string;
  efforts: string[];
  effort: string;
  approval_mode?: string;
};

export function getAgent(): Promise<AgentSelection> {
  return request<AgentSelection>("/api/agent");
}

function postAgent(path: string, body: Record<string, string>): Promise<AgentSelection> {
  return request<AgentSelection>(path, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams(body).toString(),
  });
}

// Both writes answer with the selection they produced rather than with an
// acknowledgement: switching models can invalidate the stored effort, and only
// the server knows whether it did.
export function setAgentModel(model: string): Promise<AgentSelection> {
  return postAgent("/api/agent/model", { model });
}

export function setAgentEffort(effort: string): Promise<AgentSelection> {
  return postAgent("/api/agent/effort", { effort });
}

// Restarting rebuilds the daemon around config.yaml as it now stands, which
// is what every "restart to take effect" notice in this panel is asking for.
// A config Eggy could not load comes back as a rejection with the reason and
// nothing restarts, so this is safe to offer as a button.
export function restartEggy(): Promise<CommandResult> {
  return request("/api/restart", { method: "POST" });
}

// Schedules come back as a table like every other list route, so the card
// renders them with DataTable rather than through a projected type.
export function listSchedules(): Promise<CommandResult> {
  return request("/api/schedules");
}

// The watch list is the heartbeat's checklist. It is a document rather than a
// config section, so it has its own routes instead of a ConfigSection: the
// heartbeat reads it live, and saving one needs no restart.
export function getWatchList(): Promise<CommandResult> {
  return request("/api/context/watch");
}

export function saveWatchList(content: string): Promise<CommandResult> {
  return request("/api/context/watch", { method: "POST", body: JSON.stringify({ content }) });
}

export function cancelSchedule(id: string): Promise<CommandResult> {
  return request(`/api/schedules/${encodeURIComponent(id)}`, { method: "DELETE" });
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

// A trace is one turn as it actually ran: every model call with the prompt
// that produced it, every tool call with its arguments and its output. It is
// nested rather than tabular, so unlike every list route above these answer
// with JSON documents instead of table_headers/table_rows.
export type TraceSummary = {
  id: string;
  conversation_id: string;
  // Empty until the conversation has been cleared once. Traces group on the
  // conversation and the session together, so /clear starts a new group.
  session?: string;
  channel: string;
  source: string;
  kind: string;
  model: string;
  effort?: string;
  input: string;
  output: string;
  error?: string;
  spans: number;
  started_at: string;
  duration_ms: number;
  complete: boolean;
  total_tokens: number;
  prompt_tokens: number;
  completion_tokens: number;
  cached_prompt_tokens?: number;
};

export type TraceSpan = {
  sequence: number;
  kind: "model_call" | "tool_call";
  name: string;
  call_id?: string;
  // Request and response are stored as text so a trace records what actually
  // crossed the boundary. They are JSON in practice, and the viewer pretty-
  // prints what parses and shows the rest verbatim.
  request: string;
  response: string;
  error?: string;
  started_at: string;
  duration_ms: number;
  total_tokens?: number;
  prompt_tokens?: number;
  completion_tokens?: number;
  cached_prompt_tokens?: number;
};

export type TraceDetail = { trace: TraceSummary; spans: TraceSpan[] };

// Tracing switched off leaves the routes unmounted, so the list 404s with the
// mux's own plain-text body rather than with a JSON result. That is a
// different fact from "no turns yet" and the panel says so, which means it
// cannot go through request(): the shared parser would fail on the body
// before the status could be read.
export class TracingDisabledError extends Error {}

export async function listTraces(limit = 50): Promise<TraceSummary[]> {
  const response = await fetch(`/api/traces?limit=${limit}`, { credentials: "same-origin" });
  if (response.status === 401) {
    throw new SessionExpiredError("Not authenticated");
  }
  if (response.status === 404) {
    throw new TracingDisabledError("Tracing is switched off");
  }
  if (!response.ok) {
    throw new Error("Could not load traces");
  }
  const body = (await response.json()) as { traces?: TraceSummary[] };
  return body.traces ?? [];
}

export function getTrace(id: string): Promise<TraceDetail> {
  return request<TraceDetail>(`/api/traces/${encodeURIComponent(id)}`);
}
