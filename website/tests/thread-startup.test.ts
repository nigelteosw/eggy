import { expect, test } from "bun:test";
import { initialThreadSelection } from "../src/ThreadSidebar";
import type { Thread } from "../src/api";

const now = Date.parse("2026-09-05T12:00:00Z");

function thread(id: string, updatedAt: string): Thread {
  return { id, title: "", updatedAt };
}

test("selects the newest chat when it was updated within five minutes", () => {
  const selection = initialThreadSelection(
    [
      thread("older", "2026-09-05T11:59:00Z"),
      thread("newest", "2026-09-05T11:58:00Z"),
    ],
    now,
  );

  expect(selection).toEqual("older");
});

test("leaves chat creation to the first message when no existing chat is recent", () => {
  const selection = initialThreadSelection([thread("stale", "2026-09-05T11:54:59Z")], now);

  expect(selection).toBeNull();
});

test("does not create a server chat when there are no existing chats", () => {
  expect(initialThreadSelection([], now)).toBeNull();
});
