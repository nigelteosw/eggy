import { expect, test } from "bun:test";
import {
  AGENT_START_COPY,
  EXTENSION_COPY,
  canCancel,
  needsRestart,
  parseChecks,
  sandboxNotice,
  skillsFromField,
  skillsToField,
  stateLabel,
} from "../../../src/extensions/agent-runner/agent-runner";

test("the start and extension copy are the spec's wording, verbatim", () => {
  expect(AGENT_START_COPY).toBe(
    "This run will push a feature branch and open a pull request on your behalf. Eggy will not approve or merge it. Your repository's CI will run on the branch.",
  );
  expect(EXTENSION_COPY).toBe("Runs commands as a sandboxed user that can reach the internet but not Eggy's data.");
});

test("a restart is asked for exactly when the saved and running states differ", () => {
  expect(needsRestart(true, false)).toBe(true);
  expect(needsRestart(false, true)).toBe(true);
  expect(needsRestart(true, true)).toBe(false);
  expect(needsRestart(false, false)).toBe(false);
});

test("bash is reported unavailable with the reason, and not when it is available", () => {
  expect(sandboxNotice({ sandbox_ok: true, sandbox_reason: "" })).toBeNull();
  expect(sandboxNotice({ sandbox_ok: false, sandbox_reason: "not root" })).toBe(
    "Sandbox unavailable: not root. Runs can edit and run checks but have no bash.",
  );
});

test("only an active run can be cancelled, and the states read as words", () => {
  expect(canCancel("running")).toBe(true);
  expect(canCancel("waiting_for_user")).toBe(true);
  expect(canCancel("completed")).toBe(false);
  expect(stateLabel("waiting_for_user")).toBe("Waiting for you");
  expect(stateLabel("interrupted")).toBe("Interrupted");
});

test("skill toggles round-trip through the form's name=value field", () => {
  const field = skillsToField({ brainstorming: false });
  expect(field).toContain("brainstorming=false");
  expect(field).toContain("writing-plans=true");
  const back = skillsFromField(field);
  expect(back.brainstorming).toBe(false);
  expect(back["writing-plans"]).toBe(true);
});

test("checks parse one command per line, split on whitespace, and skip blanks", () => {
  expect(parseChecks("go vet ./...\n\n  go test ./...  \n")).toEqual([
    ["go", "vet", "./..."],
    ["go", "test", "./..."],
  ]);
  expect(parseChecks("   \n")).toEqual([]);
});
