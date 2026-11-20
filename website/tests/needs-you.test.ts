import { expect, test } from "bun:test";
import type { AgentRun } from "../src/lib/api";
import { needsYouItems } from "../src/extensions/needs-you";
import type { TerminalInfo } from "../src/extensions/terminal/terminal";

const blocked = {
  address: "t1",
  space: "app",
  name: "claude",
  title: "",
  live: true,
  status: "blocked",
  message: "Allow edit?",
  changed: "2026-11-20T10:00:00Z",
} as TerminalInfo;

const run = (id: string, state: string, updated: string) =>
  ({ id, repository: "app", brief: "Fix login\nmore", state, question: "Which branch?", updated_at: updated }) as AgentRun;

test("blocked terminals and waiting runs are one list, newest first", () => {
  const items = needsYouItems([blocked], [run("r1", "waiting_for_user", "2026-11-20T11:00:00Z"), run("r2", "running", "2026-11-20T12:00:00Z")]);
  expect(items.map((i) => i.key)).toEqual(["run:r1", "terminal:t1"]);
  expect(items[0]).toMatchObject({ extension: "agent_runner", title: "Fix login", detail: "app · Which branch?" });
  expect(items[1]).toMatchObject({ extension: "terminal", title: "Claude Code", detail: "app · Allow edit?" });
});

test("an extension that is off contributes nothing", () => {
  expect(needsYouItems([], [])).toEqual([]);
  expect(needsYouItems([blocked], [])).toHaveLength(1);
});
