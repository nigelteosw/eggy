import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { Window } from "happy-dom";
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { TraceBrowser } from "../src/TracesPage";
import type { TraceDetail, TraceSummary } from "../src/api";

const dom = new Window({ url: "http://localhost" });
const previous = { window: globalThis.window, document: globalThis.document, fetch: globalThis.fetch };
Object.assign(globalThis, { window: dom, document: dom.document, IS_REACT_ACT_ENVIRONMENT: true });
let root: Root;
let container: HTMLDivElement;
const noop = () => {};
const traces: TraceSummary[] = ["first", "second", "other"].map((id, i) => ({
  id,
  conversation_id: i === 2 ? "b" : "a",
  channel: "web",
  source: "web",
  kind: "owner",
  model: "model",
  input: `Question ${id}`,
  output: `Reply ${id}`,
  started_at: "2026-09-06T00:00:00Z",
  duration_ms: 1000,
  spans: 2,
  complete: true,
  total_tokens: 100,
  prompt_tokens: 80,
  completion_tokens: 20,
}));
function detail(id: string): TraceDetail {
  return {
    trace: traces.find((trace) => trace.id === id)!,
    spans: [1, 2].map((sequence) => ({
      sequence,
      kind: "tool_call",
      name: `tool-${sequence}`,
      started_at: "2026-09-06T00:00:00Z",
      duration_ms: 400,
      request: `arguments-${id}-${sequence}`,
      response: `result-${id}-${sequence}`,
    })),
  };
}
async function click(label: string) {
  const button = Array.from(container.querySelectorAll("button")).find(
    (button) => button.getAttribute("aria-label") === label || button.textContent === label,
  );
  expect(button).toBeDefined();
  await act(async () => button!.click());
}
async function render(rows = traces) {
  await act(async () =>
    root.render(
      createElement(TraceBrowser, { traces: rows, titles: { a: "Alpha", b: "Beta" }, onSessionExpired: noop }),
    ),
  );
}
beforeEach(() => {
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  globalThis.fetch = (async (url: string | URL | Request) =>
    Response.json(detail(String(url).split("/").at(-1)!))) as typeof fetch;
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  globalThis.fetch = previous.fetch;
});
afterAll(() => {
  Object.assign(globalThis, previous);
  delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  dom.happyDOM.abort();
});

test("selecting a turn and a step reveals only that payload, and switching conversations clears it", async () => {
  await render();
  expect(container.textContent).not.toContain("Question other");
  await click("Inspect turn Question first");
  expect(container.querySelector('[aria-label="Turn inspector"]')?.textContent).toContain("Question first");
  await click("Inspect step 1: tool-1");
  expect(container.textContent).toContain("result-first-1");
  await click("Inspect step 2: tool-2");
  expect(container.textContent).toContain("result-first-2");
  expect(container.textContent).not.toContain("result-first-1");
  await click("Select conversation Beta");
  expect(container.textContent).toContain("Question other");
  expect(container.textContent).not.toContain("result-first-2");
});

test("a late response from the previous turn cannot replace the selected turn", async () => {
  let resolve!: (value: Response) => void;
  globalThis.fetch = (async (url: string | URL | Request) =>
    String(url).endsWith("first")
      ? new Promise<Response>((done) => {
          resolve = done;
        })
      : Response.json(detail("second"))) as typeof fetch;
  await render();
  await click("Inspect turn Question first");
  await click("Inspect turn Question second");
  await act(async () => resolve(Response.json(detail("first"))));
  expect(container.querySelector('[aria-label="Turn inspector"]')?.textContent).toContain("Question second");
  expect(container.querySelector('[aria-label="Turn inspector"]')?.textContent).not.toContain("Question first");
});

test("failed detail requests can be retried and mobile back returns to the turn list", async () => {
  globalThis.fetch = (async () => Response.json({ title: "Unavailable" }, { status: 503 })) as typeof fetch;
  await render();
  await click("Inspect turn Question first");
  expect(container.querySelector('[role="alert"]')).not.toBeNull();
  globalThis.fetch = (async () => Response.json(detail("first"))) as typeof fetch;
  await click("Try again");
  expect(container.querySelector('[aria-label="Execution timeline"]')).not.toBeNull();
  await click("Back to turns");
  expect(container.querySelector('[aria-label="Turns"]')?.className).not.toContain("hidden");
});

test("refreshing recent turns keeps the chosen conversation when a new conversation arrives", async () => {
  await render();
  await click("Inspect turn Question first");
  await render([traces[2], traces[0], traces[1]]);
  expect(container.querySelector('[aria-label="Inspect turn Question first"]')?.getAttribute("aria-pressed")).toBe(
    "true",
  );
  expect(container.querySelector('[aria-label="Turn inspector"]')?.textContent).toContain("Question first");
});

test("opening a turn moves keyboard focus into its inspector and Back restores the turn control", async () => {
  await render();
  await click("Inspect turn Question first");
  expect(document.activeElement?.getAttribute("aria-label")).toBe("Turn inspector");
  await click("Back to turns");
  expect(document.activeElement?.getAttribute("aria-label")).toBe("Inspect turn Question first");
});
