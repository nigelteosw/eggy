import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { Body, timelineTicks, TraceTable, Waterfall } from "../src/TracesPage";
import type { TraceSpan, TraceSummary } from "../src/api";

test("timeline omits a tick that would collide with the duration label", () => {
  expect(timelineTicks(15_200)).toEqual([5_000, 10_000]);
});

test("timeline keeps ticks with enough room before the duration label", () => {
  expect(timelineTicks(20_000)).toEqual([5_000, 10_000, 15_000]);
});

test("timeline grid lines stay inside each step row", () => {
  const trace: TraceSummary = {
    id: "trace-1",
    conversation_id: "thread-1",
    channel: "web",
    source: "web",
    kind: "owner",
    model: "deepseek-v4-pro",
    input: "question",
    output: "answer",
    spans: 2,
    started_at: "2026-09-03T00:00:00Z",
    duration_ms: 7_500,
    complete: true,
    total_tokens: 100,
    prompt_tokens: 80,
    completion_tokens: 20,
  };
  const spans: TraceSpan[] = [
    { sequence: 1, kind: "model_call", name: "deepseek-v4-pro", request: "{}", response: "{}", started_at: "2026-09-03T00:00:00Z", duration_ms: 5_000 },
    { sequence: 2, kind: "tool_call", name: "status", request: "{}", response: "{}", started_at: "2026-09-03T00:00:05Z", duration_ms: 2_000 },
  ];

  const html = renderToStaticMarkup(createElement(Waterfall, { trace, spans }));
  const stepButtons = html.match(/<button[\s\S]*?<\/button>/g) ?? [];
  expect(stepButtons).toHaveLength(2);
  for (const button of stepButtons) expect(button).toContain("bg-border/70");
});

test("trace expansion is exposed through keyboard controls", () => {
  const trace: TraceSummary = {
    id: "trace-1",
    conversation_id: "thread-1",
    channel: "web",
    source: "web",
    kind: "owner",
    model: "deepseek-v4-pro",
    input: "question",
    output: "answer",
    spans: 1,
    started_at: "2026-09-03T00:00:00Z",
    duration_ms: 7_500,
    complete: true,
    total_tokens: 100,
    prompt_tokens: 80,
    completion_tokens: 20,
  };
  const html = renderToStaticMarkup(
    createElement(TraceTable, {
      traces: [trace],
      expanded: null,
      titles: { "thread-1": "Deploy Eggy" },
      onToggle: () => {},
      onSessionExpired: () => {},
    }),
  );

  expect(html).toContain('aria-label="Collapse conversation Deploy Eggy"');
  expect(html).toContain('aria-label="Expand turn question"');
  expect(html).toContain('aria-expanded="false"');
});

test("recorded payloads start collapsed", () => {
  const html = renderToStaticMarkup(createElement(Body, { label: "Arguments", text: '{"path":"/tmp"}' }));

  expect(html).toContain("<details");
  expect(html).not.toContain("<details open");
  expect(html).toContain("Arguments");
});
