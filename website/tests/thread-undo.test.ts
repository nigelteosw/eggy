import { expect, test } from "bun:test";
import { reinsertThread } from "../src/chat/ThreadSidebar";
import type { Thread } from "../src/lib/api";

const row = (id: string): Thread => ({ id, title: id } as Thread);

// Undo puts the chat back where it was, so the list does not reorder under
// the person who only meant to take one thing back.
test("undo restores a deleted chat at its old row", () => {
  const list = [row("a"), row("c")];
  expect(reinsertThread(list, row("b"), 1).map((thread) => thread.id)).toEqual(["a", "b", "c"]);
});

test("undo appends when the list shrank while the chat was held", () => {
  expect(reinsertThread([row("a")], row("z"), 9).map((thread) => thread.id)).toEqual(["a", "z"]);
});
