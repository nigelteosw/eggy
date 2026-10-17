import { RequestError, SessionExpiredError, request } from "../../api";
import { errorMessage } from "../../lib/utils";

// Types for the marketplace routes. They mirror the panel's JSON, not the Go
// types, so the SPA reads exactly what the panel writes.
export type MarketplaceSource = { name: string; kind: string; repo: string; path?: string; ref?: string; sha?: string; error?: string };
export type MarketplaceItem = { name: string; description: string; kind: string; unsupported?: string };
export type PlanRequest = { source: string; item: string; renames?: Record<string, string>; install_id?: string };
export type PlanSkill = { name: string; upstream_name: string; description: string; body: string; files: string[]; contents?: Record<string, string>; warnings?: string[] };
export type Plan = {
  source: string;
  repo: string;
  sha: string;
  item: string;
  digest: string;
  skills: PlanSkill[];
  mcp: { name: string; url: string }[];
  skipped: { path: string; reason: string }[];
  conflicts: string[];
  index_bytes: number;
};
export type Install = {
  id: string;
  source: string;
  repo: string;
  item: string;
  commit_sha: string;
  installed_by: string;
  installed_at: string;
  updated_at: string;
  disabled: boolean;
  files: { kind: string; target: string }[];
};
export type InstallStatus = { install: Install; status: string; edited: string[] };
export type SourceConfig = { name: string; kind: string; repo: string; path?: string; ref?: string };

// The approval a plan produces: the commit and digest the owner was shown.
export type Approval = PlanRequest & { sha: string; digest: string };

export const listSources = () => request<MarketplaceSource[]>("/api/marketplace/sources");
export const listItems = (source: string) => request<MarketplaceItem[]>(`/api/marketplace/items?source=${encodeURIComponent(source)}`);
export const planItem = (body: PlanRequest) => request<Plan>("/api/marketplace/plan", { method: "POST", body: JSON.stringify(body) });
export const installItem = (body: Approval) => request<Install>("/api/marketplace/installs", { method: "POST", body: JSON.stringify(body) });
export const listInstalls = () => request<InstallStatus[]>("/api/marketplace/installs");
export const checkInstall = (id: string) => request<{ status: InstallStatus; plan: Plan }>(`/api/marketplace/installs/${id}/check`, { method: "POST" });
export const updateInstall = (id: string, body: Approval & { replace_edited?: string[] }) =>
  request<Install>(`/api/marketplace/installs/${id}`, { method: "PUT", body: JSON.stringify(body) });
export const setInstallDisabled = (id: string, disabled: boolean) =>
  request(`/api/marketplace/installs/${id}/disabled`, { method: "POST", body: JSON.stringify({ disabled }) });
export const removeInstall = (id: string, deleteEdited: string[]) =>
  request(`/api/marketplace/installs/${id}`, { method: "DELETE", body: JSON.stringify({ delete_edited: deleteEdited }) });
export const getSourcesConfig = () => request<{ sources: SourceConfig[] }>("/api/config/marketplace");
export const saveSourcesConfig = (sources: SourceConfig[]) => request("/api/config/marketplace", { method: "PUT", body: JSON.stringify({ sources }) });

// The approval for a reviewed plan: exactly the commit and digest the owner saw.
export function approvalFor(plan: Plan, renames: Record<string, string>): Approval {
  return { source: plan.source, item: plan.item, renames, sha: plan.sha, digest: plan.digest };
}

// What a rename change asks the server for. A new rename is a new plan, so the
// request carries the renames the owner chose.
export function planRequestFor(source: string, item: string, renames: Record<string, string>, installId?: string): PlanRequest {
  const request: PlanRequest = { source, item, renames };
  if (installId) request.install_id = installId;
  return request;
}

// Only the ticked edited skills are deleted; the rest stay as hand-placed skills.
export function removePayload(edited: string[], ticked: Set<string>): string[] {
  return edited.filter((name) => ticked.has(name));
}

// A plan with conflicts cannot be installed until each one is renamed.
export function canInstall(plan: Plan): boolean {
  return plan.conflicts.length === 0;
}

// A 409 from the server is one of two refusals: the reviewed content changed,
// or a name collides. The message says what to do next.
export function refusalMessage(err: unknown): string | null {
  if (!(err instanceof RequestError) || err.status !== 409) return null;
  if (err.message === "already installed") return "This item is already installed.";
  if (Array.isArray(err.body.conflicts)) return "Some names already exist. Rename them and review again.";
  return "The content changed since you reviewed it. Review it again before installing.";
}

// The tabs the Plugins area offers. It is hidden entirely without the feature.
export const PLUGIN_TABS = ["discover", "installed", "sources"] as const;
export type PluginTab = (typeof PLUGIN_TABS)[number];

// The Plugins tab exists only when the deployment runs the marketplace.
export function pluginsEnabled(features: string[]): boolean {
  return features.includes("marketplace");
}

// One place for a failed request: an expired session goes back to sign-in,
// anything else is shown in the view that made the request.
export function handleFailure(err: unknown, onSessionExpired: () => void, setError: (message: string) => void, fallback: string): void {
  if (err instanceof SessionExpiredError) {
    onSessionExpired();
    return;
  }
  setError(errorMessage(err, fallback));
}
