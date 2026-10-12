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
