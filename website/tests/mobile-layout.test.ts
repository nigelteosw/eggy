import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ConfigPage } from "../src/ConfigPage";
import { TraceTable, Waterfall } from "../src/TracesPage";
import { Button } from "../src/components/ui/button";
import { Input } from "../src/components/ui/input";
import { Select } from "../src/components/ui/select";
import { Switch } from "../src/components/ui/switch";
import type { TraceSpan, TraceSummary } from "../src/api";

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

const spans: TraceSpan[] = [
  {
    sequence: 1,
    kind: "model_call",
    name: "deepseek-v4-pro",
    request: "{}",
    response: "{}",
    started_at: "2026-09-03T00:00:00Z",
    duration_ms: 5_000,
  },
];

test("settings expose a compact mobile section navigation", () => {
  const html = renderToStaticMarkup(
    createElement(ConfigPage, {
      theme: "dark",
      onThemeChange: () => {},
      onSessionExpired: () => {},
      onBackToChat: () => {},
    }),
  );

  expect(html).toContain('aria-label="Mobile settings navigation"');
  expect(html).toContain("md:hidden");
  expect(html).toContain("hidden md:flex");
});

test("trace rows expose their metrics as a stacked mobile card", () => {
  const html = renderToStaticMarkup(
    createElement(TraceTable, {
      traces: [trace],
      expanded: null,
      onToggle: () => {},
      onSessionExpired: () => {},
    }),
  );

  expect(html).toContain("min-w-0");
  expect(html).toContain("sm:table-row");
  expect(html).toContain("Steps");
  expect(html).toContain("Duration");
  expect(html).toContain("Tokens");
  expect(html).not.toContain("min-w-[58rem]");
});

test("waterfall span rows give the chart the full mobile width", () => {
  const html = renderToStaticMarkup(createElement(Waterfall, { trace, spans }));

  expect(html).toContain("min-w-0 w-full");
  expect(html).toContain("order-3");
  expect(html).toContain("sm:order-none");
});

test("form controls expose phone-sized touch targets", () => {
  const html = renderToStaticMarkup(
    createElement("div", null,
      createElement(Button, null, "Save"),
      createElement(Input, { "aria-label": "Name" }),
      createElement(Select, { "aria-label": "Mode" }, createElement("option", null, "Normal")),
      createElement(Switch, { checked: false, onCheckedChange: () => {}, label: "Enabled" }),
    ),
  );

  expect((html.match(/min-h-11|h-11/g) ?? []).length).toBeGreaterThanOrEqual(4);
});
