import { afterEach, expect, test } from "bun:test";
import { request, writes } from "../src/lib/api";

// The restart bar rechecks after a write, because a settings save is what puts
// config.yaml out of step with the running process. Chat traffic never
// changes config, so it does not cost a session check per message.
const realFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = realFetch;
});

async function writesAnnounced(path: string, method: string, status = 200): Promise<number> {
  globalThis.fetch = (async () => new Response("{}", { status })) as unknown as typeof fetch;
  let seen = 0;
  const listener = () => seen++;
  writes.addEventListener("write", listener);
  try {
    await request(path, { method }).catch(() => {});
  } finally {
    writes.removeEventListener("write", listener);
  }
  return seen;
}

test("a successful settings write is announced", async () => {
  expect(await writesAnnounced("/api/config/models", "POST")).toBe(1);
  expect(await writesAnnounced("/api/config/mcp/x", "DELETE")).toBe(1);
});

test("reads, failed writes and chat traffic are not", async () => {
  expect(await writesAnnounced("/api/config/models", "GET")).toBe(0);
  expect(await writesAnnounced("/api/config/models", "POST", 400)).toBe(0);
  expect(await writesAnnounced("/api/chat/threads/a/send", "POST")).toBe(0);
});
