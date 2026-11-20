import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ChatPage } from "../src/chat/ChatPage";
import { ThreadSidebar } from "../src/chat/ThreadSidebar";
import { RailFrame } from "../src/app/App";
import { Window } from "happy-dom";
import { ConfigPage } from "../src/settings/ConfigPage";
import { TraceBrowser, Waterfall } from "../src/traces/TracesPage";
import { Button } from "../src/components/ui/button";
import { Input } from "../src/components/ui/input";
import { Select } from "../src/components/ui/select";
import { Switch } from "../src/components/ui/switch";
import type { TraceSpan, TraceSummary } from "../src/lib/api";

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
    }),
  );

  expect(html).toContain('aria-label="Mobile settings navigation"');
  expect(html).toContain("md:hidden");
  expect(html).toContain("hidden min-h-0 w-[238px] shrink-0 flex-col bg-neutral-100 md:flex");
});

// The phone sidebar must stay inside the viewport with its account footer
// on screen. That is a chain: the frame is pinned to the viewport's height,
// the inner wrapper hands that height to the sidebar, and between the header
// and the footer one region shrinks and scrolls while the footer does not.
test("the phone sidebar is bounded to the viewport so its account menu stays reachable", () => {
  const html = renderToStaticMarkup(
    createElement(
      RailFrame,
      { open: true },
      createElement(ThreadSidebar, {
        activeThreadId: null,
        onSelect: () => {},
        onStartNew: () => {},
        onDeleted: () => {},
        onActiveTitleChange: () => {},
        onCollapse: () => {},
        reloadKey: 0,
        draftOpen: false,
        view: "chat",
        onNavigate: () => {},
        features: ["finance"],
        onOpenExtension: () => {},
        onOpenSettings: () => {},
        onLogout: () => {},
      }),
    ),
  );
  const window = new Window();
  window.document.body.innerHTML = html;
  const doc = window.document;
  const classes = (selector: string) => (doc.querySelector(selector)?.getAttribute("class") ?? "").split(/\s+/);

  const frame = doc.querySelector("[data-rail-frame]")!;
  const inner = doc.querySelector("[data-rail-inner]")!;
  expect(inner.parentElement).toBe(frame);
  expect(classes("[data-rail-frame]")).toEqual(expect.arrayContaining(["absolute", "inset-y-0"]));
  expect(classes("[data-rail-inner]")).toContain("h-full");
  const sidebar = inner.firstElementChild!;
  expect(sidebar.getAttribute("class")!.split(/\s+/)).toEqual(expect.arrayContaining(["flex", "flex-col", "h-full"]));

  // The nav and the chat list scroll together on a phone; the footer is the
  // scroll region's sibling, so it never scrolls away.
  const scroll = doc.querySelector("[data-rail-scroll]")!;
  expect(scroll.parentElement).toBe(sidebar);
  expect(classes("[data-rail-scroll]")).toEqual(expect.arrayContaining(["min-h-0", "flex-1", "max-lg:overflow-y-auto"]));
  expect(scroll.querySelector('nav[aria-label="Main navigation"]')).not.toBeNull();
  expect(scroll.querySelector("[data-chat-list]")).not.toBeNull();
  const footer = doc.querySelector("[data-rail-footer]")!;
  expect(footer.parentElement).toBe(sidebar);
  expect(classes("[data-rail-footer]")).toContain("shrink-0");
  expect(scroll.contains(footer)).toBe(false);
});

test("trace list presents labelled turn controls and duration without a wide table", () => {
  const html = renderToStaticMarkup(
    createElement(TraceBrowser, {
      traces: [trace],
      onSessionExpired: () => {},
    }),
  );

  expect(html).toContain('aria-label="Inspect turn question"');
  expect(html).toContain("7.5s");
  expect(html).toContain("1 step");
  expect(html).not.toContain("<table");
});

test("waterfall steps are keyboard selectable", () => {
  const html = renderToStaticMarkup(createElement(Waterfall, { trace, spans }));

  expect(html).toContain('aria-label="Inspect step 1: deepseek-v4-pro"');
  expect(html).toContain('aria-pressed="false"');
  expect(html).toContain("trace-step");
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

test("chat header carries the menu control on a phone only while the rail is closed", () => {
  const closed = renderToStaticMarkup(
    createElement(ChatPage, {
      threadId: null,
      title: "New chat",
      onSessionExpired: () => {},
      onOpenSidebar: () => {},
    }),
  );
  expect(closed).toContain('aria-label="Open sidebar"');
  expect(closed).toContain("lg:hidden");

  const open = renderToStaticMarkup(
    createElement(ChatPage, { threadId: null, title: "New chat", onSessionExpired: () => {} }),
  );
  expect(open).not.toContain('aria-label="Open sidebar"');
});
