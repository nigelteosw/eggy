import { describe, expect, test } from "bun:test";
import { orderedCommands, spaceNote, type TerminalInfo, type TerminalSpace } from "../src/extensions/terminal/terminal";

const term = { address: "a", space: "s", name: "shell", title: "", live: true, changed: "2026-11-20T00:00:00Z" } as TerminalInfo;
const space = (s: Partial<TerminalSpace>): TerminalSpace => ({ name: "s", terminals: [], ...s });

describe("spaceNote", () => {
  test("a copy in progress or failed says so, ahead of anything else", () => {
    expect(spaceNote(space({ repo: true, preparing: true }))?.text).toContain("Copying");
    const failed = spaceNote(space({ repo: true, error: "no token" }));
    expect(failed?.problem).toBe(true);
    expect(failed?.text).toContain("no token");
  });
  test("terminals are shown instead of a note", () => {
    expect(spaceNote(space({ terminals: [term] }))).toBeNull();
  });
  test("an empty space says how to start, a repository in its own words", () => {
    expect(spaceNote(space({ repo: true }))?.text).toContain("repository");
    expect(spaceNote(space({}))?.text).toContain("Nothing running");
  });
});

test("orderedCommands puts Claude Code first and keeps the rest", () => {
  expect(orderedCommands(["shell", "claude", "codex"])[0]).toBe("claude");
  expect(orderedCommands(["shell", "codex"])).toEqual(["shell", "codex"]);
});
