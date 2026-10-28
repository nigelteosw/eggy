import type { AgentRun } from "../../lib/api";

// Pure rules for the agent runner's panel: the wording the spec fixes, what a
// restart is needed for, and how the form's text maps to config. The components
// render these; they do not decide them.

// The start copy the spec requires, verbatim: an owner must read it before a run
// pushes anything.
export const AGENT_START_COPY =
  "This run will push a feature branch and open a pull request on your behalf. Eggy will not approve or merge it. Your repository's CI will run on the branch.";

// The Extensions card's line, verbatim.
export const EXTENSION_COPY = "Runs commands as a sandboxed user that can reach the internet but not Eggy's data.";

// The most checks a repository may list. It matches the config validation.
export const MAX_CHECKS = 8;

// The skills the runner bundles. The list mirrors config.AgentRunnerSkills.
export const AGENT_SKILLS = ["brainstorming", "writing-plans", "verification-before-completion"] as const;

const STATE_LABELS: Record<string, string> = {
  queued: "Queued",
  running: "Running",
  waiting_for_user: "Waiting for you",
  publishing: "Publishing",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
  interrupted: "Interrupted",
};

export function stateLabel(state: string): string {
  return STATE_LABELS[state] ?? state;
}

// A run the owner can still stop: queued, running, waiting, or publishing.
export function canCancel(state: string): boolean {
  return ["queued", "running", "waiting_for_user", "publishing"].includes(state);
}

// A restart is needed exactly when what the file says differs from what the
// process is running. An unsaved switch position is neither, so it claims nothing.
export function needsRestart(savedEnabled: boolean, running: boolean): boolean {
  return savedEnabled !== running;
}

// The notice shown when bash is withheld. Nothing is shown when it is offered.
export function sandboxNotice(status: { sandbox_ok: boolean; sandbox_reason: string }): string | null {
  if (status.sandbox_ok) {
    return null;
  }
  return `Sandbox unavailable: ${status.sandbox_reason}. Runs can edit and run checks but have no bash.`;
}

// The form's skills field is "name=true,name=false", in a fixed order.
export function skillsToField(skills: Record<string, boolean>): string {
  return AGENT_SKILLS.map((name) => `${name}=${skills[name] ?? true}`).join(",");
}

export function skillsFromField(field: string): Record<string, boolean> {
  const out: Record<string, boolean> = {};
  for (const pair of field.split(",")) {
    const [name, value] = pair.split("=");
    if (name && value !== undefined) {
      out[name.trim()] = value.trim() === "true";
    }
  }
  return out;
}

// One check per line, split on whitespace. A blank line is nothing, not an
// empty command; config validation refuses an empty program separately.
export function parseChecks(text: string): string[][] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
    .map((line) => line.split(/\s+/));
}

const STAGE_LABELS: Record<string, string> = {
  "": "Not started publishing",
  committed: "Committed",
  push_intended: "Pushing",
  pushed: "Pushed",
  pr_intended: "Opening pull request",
  pr_opened: "Pull request opened",
  delivered: "Delivered",
};

// The publish stage as words. It says how far publication got, which is the
// first thing to read when a run stops after its commit.
export function stageLabel(stage: string): string {
  return STAGE_LABELS[stage] ?? stage;
}

// The one line a run's card leads with: the issue, if there is one, in the
// owner's words. The full text is the detail page's job.
export function runHeadline(run: AgentRun): string {
  switch (run.state) {
    case "failed":
    case "cancelled":
    case "interrupted":
      return run.failure || (run.state === "failed" ? "Failed without a reason recorded." : stateLabel(run.state));
    case "waiting_for_user":
      return `Asks you: ${run.question ?? ""}`.trim();
    case "completed":
      if (run.pr_url) {
        return "Pull request opened.";
      }
      return (run.output ?? "").split("\n")[0] || "Completed.";
    default:
      return run.stage ? `${stateLabel(run.state)} · ${stageLabel(run.stage)}` : stateLabel(run.state);
  }
}

// The badge tone: a finished run that worked is good, one that failed is a
// warning, and everything still moving is plain.
export function runTone(state: string): "plain" | "warn" | "good" {
  if (state === "completed") return "good";
  if (state === "failed") return "warn";
  return "plain";
}

// A run is waiting for the owner, still moving, or stopped. The runs page
// filters on these, and the page's badge counts the first.
export function isWaiting(state: string): boolean {
  return state === "waiting_for_user";
}

export function isActive(state: string): boolean {
  return ["queued", "running", "publishing"].includes(state);
}

// Only a finished run can be deleted: the same states the server accepts.
export function canDelete(state: string): boolean {
  return ["completed", "failed", "cancelled", "interrupted"].includes(state);
}

export function isStopped(state: string): boolean {
  return ["failed", "cancelled", "interrupted"].includes(state);
}

export type RunFilter = "all" | "needs_you" | "active" | "completed" | "stopped";

export const RUN_FILTERS: { id: RunFilter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "needs_you", label: "Needs you" },
  { id: "active", label: "In progress" },
  { id: "completed", label: "Completed" },
  { id: "stopped", label: "Stopped" },
];

export function matchesFilter(state: string, filter: RunFilter): boolean {
  switch (filter) {
    case "all":
      return true;
    case "needs_you":
      return isWaiting(state);
    case "active":
      return isActive(state);
    case "completed":
      return state === "completed";
    case "stopped":
      return isStopped(state);
  }
}

export function filterRuns(runs: AgentRun[], filter: RunFilter): AgentRun[] {
  return runs.filter((run) => matchesFilter(run.state, filter));
}

// Runs the owner has to act on: the agent is waiting for an answer.
export function waitingRuns(runs: AgentRun[]): AgentRun[] {
  return runs.filter((run) => isWaiting(run.state));
}

// The checkpoints a publication passes, in order. A run's stage says how many
// it has reached; a run that stopped partway shows the ones it got to.
export const PUBLISH_CHECKPOINTS = ["Committed", "Pushed", "Pull request opened", "Delivered"] as const;

const STAGE_REACHED: Record<string, number> = {
  "": 0,
  committed: 1,
  push_intended: 1,
  pushed: 2,
  pr_intended: 2,
  pr_opened: 3,
  delivered: 4,
};

// The publication checkpoints as done or not. A run that never reached
// publication has none done.
export function publishProgress(stage: string): { label: string; done: boolean }[] {
  const reached = STAGE_REACHED[stage] ?? 0;
  return PUBLISH_CHECKPOINTS.map((label, index) => ({ label, done: index < reached }));
}

// The model's calls are the noisiest steps in a run's record. They are hidden
// by default, so the timeline reads as what the agent did.
export function isModelStep(kind: string): boolean {
  return kind === "model";
}

// The task as one line for a row: the brief's first line, trimmed. The whole
// brief is shown on the run's own page.
export function briefTitle(brief: string | undefined): string {
  return (brief ?? "").split("\n")[0].trim();
}

// Seconds as a short duration: "45s", "3m 12s", "2h 5m".
export function formatDuration(seconds: number): string {
  const total = Math.max(0, Math.round(seconds));
  if (total < 60) return `${total}s`;
  const minutes = Math.floor(total / 60);
  if (minutes < 60) return `${minutes}m ${total % 60}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

// Tokens in a compact form the owner can compare at a glance: "950", "42K", "1.2M".
export function formatTokens(tokens: number): string {
  return new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(tokens);
}

// How long ago an ISO time was, relative to now. Older than a week it is a date.
export function timeAgo(iso: string, now: number = Date.now()): string {
  const seconds = Math.round((now - new Date(iso).getTime()) / 1000);
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
  const days = Math.floor(hours / 24);
  if (days < 7) return `${days} day${days === 1 ? "" : "s"} ago`;
  return new Date(iso).toLocaleDateString();
}
