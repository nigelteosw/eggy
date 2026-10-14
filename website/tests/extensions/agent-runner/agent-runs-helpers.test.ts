import { expect, test } from "bun:test";
import type { AgentRun } from "../../../src/api";
import { briefTitle, filterRuns, formatDuration, formatTokens, publishProgress, timeAgo, waitingRuns } from "../../../src/extensions/agent-runner/agent-runner";

const run = (id: string, state: string): AgentRun => ({
  id,
  repository: "eggy",
  base_branch: "main",
  state,
  draft: false,
  used_tokens: 0,
  active_seconds: 0,
  created_at: "2026-10-13T12:00:00Z",
  updated_at: "2026-10-13T12:00:00Z",
});

test("the filters split runs into what needs the owner, what is moving, and what ended", () => {
  const runs = [run("a", "waiting_for_user"), run("b", "running"), run("c", "queued"), run("d", "completed"), run("e", "failed"), run("f", "interrupted")];
  expect(filterRuns(runs, "all")).toHaveLength(6);
  expect(filterRuns(runs, "needs_you").map((r) => r.id)).toEqual(["a"]);
  expect(filterRuns(runs, "active").map((r) => r.id)).toEqual(["b", "c"]);
  expect(filterRuns(runs, "completed").map((r) => r.id)).toEqual(["d"]);
  expect(filterRuns(runs, "stopped").map((r) => r.id)).toEqual(["e", "f"]);
  expect(waitingRuns(runs).map((r) => r.id)).toEqual(["a"]);
});

test("publish progress reads the stage as the checkpoints it has passed", () => {
  expect(publishProgress("").map((c) => c.done)).toEqual([false, false, false, false]);
  expect(publishProgress("committed").map((c) => c.done)).toEqual([true, false, false, false]);
  expect(publishProgress("push_intended").map((c) => c.done)).toEqual([true, false, false, false]);
  expect(publishProgress("pushed").map((c) => c.done)).toEqual([true, true, false, false]);
  expect(publishProgress("delivered").map((c) => c.done)).toEqual([true, true, true, true]);
});

test("durations and token counts read at a glance", () => {
  expect(formatDuration(45)).toBe("45s");
  expect(formatDuration(192)).toBe("3m 12s");
  expect(formatDuration(7500)).toBe("2h 5m");
  expect(formatTokens(950)).toBe("950");
  expect(formatTokens(42000)).toMatch(/^42/);
});

test("a run's age is relative until it is a week old", () => {
  const now = Date.parse("2026-10-13T12:10:00Z");
  expect(timeAgo("2026-10-13T12:09:30Z", now)).toBe("just now");
  expect(timeAgo("2026-10-13T12:00:00Z", now)).toBe("10 min ago");
  expect(timeAgo("2026-10-13T09:10:00Z", now)).toBe("3 h ago");
  expect(timeAgo("2026-10-11T12:10:00Z", now)).toBe("2 days ago");
});

test("a row's task is the brief's first line, trimmed", () => {
  expect(briefTitle("  Fix the docs \nmore detail")).toBe("Fix the docs");
  expect(briefTitle(undefined)).toBe("");
});
