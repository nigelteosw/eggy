import { expect, test } from "bun:test";
import { enterSendsMessage } from "../src/lib/composer";

test("Enter sends on a keyboard, and Shift+Enter starts a new line", () => {
  expect(enterSendsMessage("Enter", false, false)).toBe(true);
  expect(enterSendsMessage("Enter", true, false)).toBe(false);
});

test("on a touch keyboard Enter starts a new line, so a phone can write several lines", () => {
  expect(enterSendsMessage("Enter", false, true)).toBe(false);
  expect(enterSendsMessage("a", false, false)).toBe(false);
});
