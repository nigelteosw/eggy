import { expect, test } from "bun:test";
import { filterThreads } from "../src/ThreadSidebar";
import type { Thread } from "../src/api";

const threads: Thread[] = [
  { id: "1", title: "Deploy the daemon", updatedAt: "2026-10-28T10:00:00Z" },
  { id: "2", title: "Invoice for Sam", updatedAt: "2026-10-27T10:00:00Z" },
  { id: "3", title: "", updatedAt: "2026-10-26T10:00:00Z" },
];

// Search finds a chat by what it was about, whatever the case of the query.
test("a search matches chat titles without regard to case", () => {
  expect(filterThreads(threads, "sam").map((thread) => thread.id)).toEqual(["2"]);
  expect(filterThreads(threads, "DEPLOY").map((thread) => thread.id)).toEqual(["1"]);
});

test("an empty search keeps every chat, and an untitled chat is never matched by a title", () => {
  expect(filterThreads(threads, "  ")).toHaveLength(3);
  expect(filterThreads(threads, "new")).toEqual([]);
});
