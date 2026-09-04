import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { conversationLabel, groupTracesByConversation, TraceTable } from "../src/TracesPage";
import type { TraceSummary } from "../src/api";

function trace(id: string, conversation: string, startedAt: string, extra: Partial<TraceSummary> = {}): TraceSummary {
  return {
    id,
    conversation_id: conversation,
    channel: "web",
    source: "web",
    kind: "owner",
    model: "deepseek-v4-pro",
    input: `question ${id}`,
    output: "answer",
    spans: 2,
    started_at: startedAt,
    duration_ms: 1_000,
    complete: true,
    total_tokens: 100,
    prompt_tokens: 80,
    completion_tokens: 20,
    ...extra,
  };
}

const traces = [
  trace("t3", "thread-1", "2026-09-03T00:02:00Z"),
  trace("t2", "telegram", "2026-09-03T00:01:00Z", { channel: "telegram", source: "telegram", error: "boom" }),
  trace("t1", "thread-1", "2026-09-03T00:00:00Z"),
];

test("turns are grouped by conversation, ordered by their newest turn", () => {
  const groups = groupTracesByConversation(traces);
  expect(groups.map((group) => group.conversationId)).toEqual(["thread-1", "telegram"]);
  expect(groups[0].traces.map((row) => row.id)).toEqual(["t3", "t1"]);
});

test("a group totals the work its turns did", () => {
  const [thread, telegram] = groupTracesByConversation(traces);
  expect(thread.traces).toHaveLength(2);
  expect(thread.spans).toBe(4);
  expect(thread.durationMs).toBe(2_000);
  expect(thread.totalTokens).toBe(200);
  expect(thread.startedAt).toBe("2026-09-03T00:00:00Z");
  expect(thread.lastAt).toBe("2026-09-03T00:02:00Z");
  expect(thread.errors).toBe(0);
  expect(telegram.errors).toBe(1);
});

test("a conversation is named by its thread title, and by its surface without one", () => {
  const [thread, telegram] = groupTracesByConversation(traces);
  expect(conversationLabel(thread, { "thread-1": "Roof repairs" })).toBe("Roof repairs");
  expect(conversationLabel(thread, {})).toBe("Web conversation");
  expect(conversationLabel(telegram, {})).toBe("Telegram");
});

test("the table renders one header per conversation above its turns", () => {
  const html = renderToStaticMarkup(
    createElement(TraceTable, {
      traces,
      expanded: null,
      titles: { "thread-1": "Roof repairs" },
      onToggle: () => {},
      onSessionExpired: () => {},
    }),
  );

  expect(html).toContain("Roof repairs");
  expect(html).toContain("2 turns");
  expect(html).toContain("1 failed");
  expect(html.indexOf("Roof repairs")).toBeLessThan(html.indexOf("question t3"));
});

// Telegram is one conversation ID forever, so /clear is the only thing that
// can say where one line of work ended and the next began.
test("clearing a conversation starts a new group", () => {
  const groups = groupTracesByConversation([
    trace("after", "telegram", "2026-09-03T00:02:00Z", { channel: "telegram", session: "1757000000000000000" }),
    trace("before", "telegram", "2026-09-03T00:01:00Z", { channel: "telegram" }),
  ]);

  expect(groups.map((group) => group.traces.map((held) => held.id))).toEqual([["after"], ["before"]]);
  expect(groups.map((group) => group.conversationId)).toEqual(["telegram", "telegram"]);
  expect(new Set(groups.map((group) => group.key)).size).toBe(2);
});

test("turns from one uncleared stretch stay in one group", () => {
  const groups = groupTracesByConversation([
    trace("second", "telegram", "2026-09-03T00:02:00Z", { channel: "telegram", session: "1757000000000000000" }),
    trace("first", "telegram", "2026-09-03T00:01:00Z", { channel: "telegram", session: "1757000000000000000" }),
  ]);

  expect(groups.length).toBe(1);
  expect(groups[0].traces.map((held) => held.id)).toEqual(["second", "first"]);
});
