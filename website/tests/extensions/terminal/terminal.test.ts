import { expect, test } from "bun:test";
import { age, applyModifier, attachURL, composeBytes, inbox, needsYouCount, sectionOf, terminalLabel, type TerminalInfo } from "../../../src/extensions/terminal/terminal";
import { runningExtensions } from "../../../src/extensions";

function term(over: Partial<TerminalInfo>): TerminalInfo {
  return { address: "t1", space: "web", name: "claude", title: "", live: true, changed: "2026-11-19T10:00:00Z", ...over };
}

test("the inbox puts what needs you first, then done, working, idle", () => {
  const spaces = [
    {
      name: "web",
      terminals: [
        term({ address: "t1", status: "working" }),
        term({ address: "t2", status: "blocked" }),
        term({ address: "t3", live: false }),
        term({ address: "t4" }),
        term({ address: "t5", status: "made-up-word" }),
      ],
    },
  ];
  const groups = inbox(spaces);
  expect(groups.map((g) => g.section)).toEqual(["needs_you", "done", "working", "idle"]);
  expect(groups[3].terminals.map((t) => t.address).sort()).toEqual(["t4", "t5"]);
  expect(needsYouCount(spaces)).toBe(1);
});

test("a finished program is done whatever it last said", () => {
  expect(sectionOf(term({ live: false, status: "blocked" }))).toBe("done");
});

test("newest change first within a section", () => {
  const groups = inbox([{ name: "a", terminals: [term({ address: "old", status: "blocked", changed: "2026-11-19T09:00:00Z" }), term({ address: "new", status: "blocked", changed: "2026-11-19T11:00:00Z" })] }]);
  expect(groups[0].terminals.map((t) => t.address)).toEqual(["new", "old"]);
});

test("ctrl makes a letter its control code and alt prefixes escape", () => {
  expect(applyModifier("c", "ctrl")).toBe("\x03");
  expect(applyModifier("C", "ctrl")).toBe("\x03");
  expect(applyModifier("[", "ctrl")).toBe("\x1b");
  expect(applyModifier("b", "alt")).toBe("\x1bb");
  expect(applyModifier("x", null)).toBe("x");
});

test("compose sends its text then Enter, with line breaks as carriage returns", () => {
  expect(composeBytes("fix the test")).toBe("fix the test\r");
  expect(composeBytes("a\nb")).toBe("a\rb\r");
});

test("the attach URL follows the page's scheme and host", () => {
  expect(attachURL("t3", 80, 24, { protocol: "https:", host: "eggy.example" } as Location)).toBe("wss://eggy.example/api/terminal/terminals/t3/attach?cols=80&rows=24");
  expect(attachURL("t3", 80, 24, { protocol: "http:", host: "localhost:8080" } as Location)).toBe("ws://localhost:8080/api/terminal/terminals/t3/attach?cols=80&rows=24");
});

test("a terminal is labelled by its title, else its command", () => {
  expect(terminalLabel(term({ title: "Fix retry test" }))).toBe("Fix retry test");
  expect(terminalLabel(term({ title: "  " }))).toBe("claude");
});

test("age is compact", () => {
  const now = Date.parse("2026-11-19T12:00:00Z");
  expect(age("2026-11-19T11:59:30Z", now)).toBe("now");
  expect(age("2026-11-19T11:56:00Z", now)).toBe("4m");
  expect(age("2026-11-19T09:00:00Z", now)).toBe("3h");
});

test("the terminal is listed only while its feature is running", () => {
  expect(runningExtensions([]).some((e) => e.id === "terminal")).toBe(false);
  expect(runningExtensions(["terminal"]).some((e) => e.id === "terminal")).toBe(true);
});
