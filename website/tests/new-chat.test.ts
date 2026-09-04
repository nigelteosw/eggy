import { expect, test } from "bun:test";
import { canStartNewChat } from "../src/ThreadSidebar";
import type { Thread } from "../src/api";

function thread(id: string, title: string): Thread {
  return { id, title, updatedAt: "2026-09-05T12:00:00Z" };
}

test("refuses a new chat while the open one is still empty", () => {
  const threads = [thread("fresh", ""), thread("older", "Yesterday's chat")];

  expect(canStartNewChat(threads, "fresh")).toBe(false);
});

test("allows a new chat once the open one has been written in", () => {
  const threads = [thread("fresh", ""), thread("used", "Deploy the daemon")];

  expect(canStartNewChat(threads, "used")).toBe(true);
});

// The list arrives a moment after the first render, and a button that starts
// out disabled for that moment is a button that looks broken.
test("allows a new chat before the thread list has loaded", () => {
  expect(canStartNewChat([], null)).toBe(true);
  expect(canStartNewChat([], "not-loaded-yet")).toBe(true);
});
