import { afterEach, expect, test } from "bun:test";
import { RequestError, getAgentPAT } from "../src/lib/api";

const realFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = realFetch;
});

function stubFetch(body: string, init: ResponseInit) {
  globalThis.fetch = (async () => new Response(body, init)) as typeof fetch;
}

test("a plain-text 404 is a RequestError with the status, not a JSON parse error", async () => {
  // The mux answers an unknown route with the Go text "404 page not found".
  // Parsing that as JSON used to throw first, so callers that treat a 404 as
  // "this feature is off" never saw the status.
  stubFetch("404 page not found\n", { status: 404, headers: { "Content-Type": "text/plain" } });
  const failure = await getAgentPAT().catch((err: unknown) => err);
  expect(failure).toBeInstanceOf(RequestError);
  expect((failure as RequestError).status).toBe(404);
  expect((failure as Error).message).not.toContain("Unexpected non-whitespace");
  expect((failure as Error).message).toBe("Not found");
});

test("a non-JSON failure never shows the raw body as its message", async () => {
  stubFetch("<html>upstream exploded</html>", { status: 502, headers: { "Content-Type": "text/html" } });
  const failure = await getAgentPAT().catch((err: unknown) => err);
  expect(failure).toBeInstanceOf(RequestError);
  expect((failure as RequestError).status).toBe(502);
  expect((failure as Error).message).toBe("Request failed");
});
