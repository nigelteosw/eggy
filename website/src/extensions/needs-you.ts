import type { AgentRun } from "../lib/api";
import { briefTitle, isWaiting } from "./agent-runner/agent-runner";
import { commandLabel, type TerminalInfo } from "./terminal/terminal";

// One thing waiting on the owner, from whichever extension it belongs to:
// a terminal whose program said it is blocked, or an agent run waiting for an
// answer. extension is the one to open to deal with it.
export type NeedsYouItem = {
  key: string;
  extension: "terminal" | "agent_runner";
  title: string;
  detail: string;
  changed: string;
};

// needsYouItems is the one list, newest first. Either source may be empty: an
// extension that is off contributes nothing.
export function needsYouItems(terminals: TerminalInfo[], runs: AgentRun[]): NeedsYouItem[] {
  const items: NeedsYouItem[] = [
    ...terminals.map((t) => ({
      key: `terminal:${t.address}`,
      extension: "terminal" as const,
      title: t.title.trim() || commandLabel(t.name),
      detail: [t.space, t.message || "Waiting for you"].filter(Boolean).join(" · "),
      changed: t.changed,
    })),
    ...runs
      .filter((r) => isWaiting(r.state))
      .map((r) => ({
        key: `run:${r.id}`,
        extension: "agent_runner" as const,
        title: briefTitle(r.brief) || `Run on ${r.repository}`,
        detail: [r.repository, r.question || "Waiting for you"].join(" · "),
        changed: r.updated_at,
      })),
  ];
  return items.sort((a, b) => Date.parse(b.changed) - Date.parse(a.changed));
}
