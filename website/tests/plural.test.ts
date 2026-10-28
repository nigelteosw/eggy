import { expect, test } from "bun:test";
import { count } from "../src/lib/plural";

test("a count reads correctly for one and for many", () => {
  expect(count(1, "turn")).toBe("1 turn");
  expect(count(2, "turn")).toBe("2 turns");
  expect(count(0, "tool call")).toBe("0 tool calls");
});

test("an irregular plural is given explicitly", () => {
  expect(count(1, "run", "runs")).toBe("1 run");
  expect(count(3, "entry", "entries")).toBe("3 entries");
});
