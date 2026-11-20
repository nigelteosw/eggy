import { afterEach, expect, test } from "bun:test";
import { CACHE, forgetAll, recall, remember } from "../src/lib/cache";
import { clearSession, request } from "../src/lib/api";

const realFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = realFetch;
  forgetAll();
});

function answer(body: unknown) {
  globalThis.fetch = (async () => new Response(JSON.stringify(body), { status: 200 })) as unknown as typeof fetch;
}

// A page opened a second time draws what it showed before, then refreshes.
test("a remembered answer is recalled until something forgets it", () => {
  expect(recall(CACHE.watch)).toBeUndefined();
  expect(remember(CACHE.watch, "- the BTO ballot")).toBe("- the BTO ballot");
  expect(recall<string>(CACHE.watch)).toBe("- the BTO ballot");
  expect(CACHE.history("t1")).not.toBe(CACHE.history("t2"));
});

// What is kept is one account's private data: the next person on this browser
// must not open to it.
test("ending the session forgets everything", () => {
  remember(CACHE.approvals, { table_rows: [["a1"]] });
  clearSession();
  expect(recall(CACHE.approvals)).toBeUndefined();
});

// A change made on one page is never contradicted by another page's older copy.
test("any write forgets everything, and a read forgets nothing", async () => {
  remember(CACHE.schedules, { table_rows: [["s1"]] });
  answer({ table_rows: [] });
  await request("/api/schedules");
  expect(recall(CACHE.schedules)).toBeDefined();

  answer({ state: "success" });
  await request("/api/schedules/s1", { method: "DELETE" });
  expect(recall(CACHE.schedules)).toBeUndefined();

  remember(CACHE.history("t1"), []);
  await request("/api/chat/t1/messages", { method: "POST" });
  expect(recall(CACHE.history("t1"))).toBeUndefined();
});
