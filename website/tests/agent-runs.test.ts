import { expect, test } from "bun:test";
import { runHeadline, runTone, stageLabel } from "../src/lib/agent-runner";
import type { AgentRun } from "../src/api";

const base: AgentRun = { id: "r1", repository: "eggy", state: "running", draft: false, created_at: "2026-10-13T12:59:40Z" };

test("a failed run's card says why it failed, not just that it did", () => {
  const run = { ...base, state: "failed", failure: ".github/workflows/ci.yml: workflow and CI files are not changed by Eggy" };
  expect(runHeadline(run)).toBe(run.failure);
  expect(runTone(run.state)).toBe("warn");
});

test("a failed run with no recorded reason says so instead of going blank", () => {
  expect(runHeadline({ ...base, state: "failed" })).toBe("Failed without a reason recorded.");
});

test("a run waiting on the owner leads with the question it is asking", () => {
  expect(runHeadline({ ...base, state: "waiting_for_user", question: "Which branch should I base on?" })).toBe(
    "Asks you: Which branch should I base on?",
  );
});

test("a completed run leads with its pull request, then its summary", () => {
  expect(runHeadline({ ...base, state: "completed", pr_url: "https://github.com/o/r/pull/9", output: "Added a line." })).toBe(
    "Pull request opened.",
  );
  expect(runHeadline({ ...base, state: "completed", output: "Added a line.\nTests pass." })).toBe("Added a line.");
  expect(runTone("completed")).toBe("good");
});

test("an active run shows what it is doing, not an empty card", () => {
  expect(runHeadline({ ...base, state: "publishing", stage: "pushed" })).toBe("Publishing · Pushed");
  expect(runHeadline(base)).toBe("Running");
  expect(runTone("running")).toBe("plain");
});

test("publish stages read as words", () => {
  expect(stageLabel("pushed")).toBe("Pushed");
  expect(stageLabel("push_intended")).toBe("Pushing");
  expect(stageLabel("pr_opened")).toBe("Pull request opened");
  expect(stageLabel("")).toBe("Not started publishing");
});
