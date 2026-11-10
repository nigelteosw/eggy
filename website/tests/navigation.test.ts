import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { AccountMenu } from "../src/app/AccountMenu";
import { tabsFor } from "../src/customize/CustomizePage";
import { FinanceCard } from "../src/extensions/finance/FinanceCard";
import { ConfigPage, SECTIONS } from "../src/settings/ConfigPage";
import { runningExtensions } from "../src/extensions";
import { ThreadSidebar, groupThreads } from "../src/chat/ThreadSidebar";
import type { Thread } from "../src/lib/api";
import { pathForView, viewForPath, type View } from "../src/app/routing";

test("maps each destination to its browser path", () => {
  expect(pathForView("chat")).toBe("/");
  expect(pathForView("scheduled")).toBe("/scheduled");
  expect(pathForView("traces")).toBe("/traces");
});

test("maps browser paths back to app views", () => {
  expect(viewForPath("/")).toBe("chat");
  expect(viewForPath("/scheduled")).toBe("scheduled");
  expect(viewForPath("/scheduled/")).toBe("scheduled");
  expect(viewForPath("/traces")).toBe("traces");
  expect(viewForPath("/unknown-page")).toBe("chat");
});

test("settings is a page of its own, with or without a trailing slash", () => {
  expect(pathForView("settings")).toBe("/settings");
  expect(viewForPath("/settings")).toBe("settings");
  expect(viewForPath("/settings/")).toBe("settings");
});

test("retired pages fall back to chat rather than an empty screen", () => {
  // The old Plugins page is gone and Finance is now a popup, so their old addresses land
  // on chat instead of a route that no longer renders anything.
  expect(viewForPath("/plugins")).toBe("chat");
  expect(viewForPath("/finance")).toBe("chat");
});

test("settings navigation groups configuration by user intent", () => {
  const html = renderToStaticMarkup(
    createElement(ConfigPage, {
      theme: "dark",
      onThemeChange: () => {},
      onSessionExpired: () => {},
    }),
  );
  // Scoped to the phone's section list: the settings cards have controls of
  // their own that must not be counted as navigation.
  const nav = html.match(/<nav[^>]*aria-label="Mobile settings navigation"[^>]*>(.*?)<\/nav>/)?.[1] ?? "";
  const labels = [...nav.matchAll(/<span class="min-w-0 flex-1">([^<]+)<\/span>/g)].map((match) => match[1]);

  expect(labels).toEqual([
    "Model &amp; when Eggy asks",
    "Check-ins",
    "Pending approvals",
    "People",
    "Models",
    "Tools",
    "Soul",
    "Advanced",
  ]);
  const groups = [...nav.matchAll(/<p[^>]*>(My settings|People|Shared deployment)<\/p>/g)].map((match) => match[1]);
  expect(groups).toEqual(["My settings", "People", "Shared deployment"]);
});

function sidebar(props: { view: View; features?: string[] }) {
  return renderToStaticMarkup(
    createElement(ThreadSidebar, {
      activeThreadId: null,
      onSelect: () => {},
      onStartNew: () => {},
      onDeleted: () => {},
      onActiveTitleChange: () => {},
      onCollapse: () => {},
      reloadKey: 0,
      draftOpen: false,
      view: props.view,
      onNavigate: () => {},
      features: props.features ?? [],
      onOpenExtension: () => {},
      onOpenSettings: () => {},
      onLogout: () => {},
    }),
  );
}

for (const view of ["chat", "scheduled", "traces"] as const) {
  test(`the rail keeps every destination reachable from ${view}`, () => {
    const html = sidebar({ view });
    expect(html).toContain('aria-label="Main navigation"');
    expect(html).toContain(">Scheduled<");
    expect(html).toContain(">New chat<");
    // The destinations that used to be a top bar now sit behind the account menu.
    expect(html).not.toContain('href="/traces"');
  });
}

test("Scheduled is the current destination only on the Scheduled view", () => {
  expect(sidebar({ view: "scheduled" }).match(/aria-current="page"/g)).toHaveLength(1);
  expect(sidebar({ view: "chat" })).not.toContain('aria-current="page"');
});

test("Extensions appear in the rail only when an extension is running, and Finance sits inside their menu", () => {
  expect(sidebar({ view: "chat", features: [] })).not.toContain(">Extensions<");
  const enabled = sidebar({ view: "chat", features: ["finance"] });
  expect(enabled).toContain(">Extensions<");
  // The menu is closed until opened, so Finance is not in the markup yet.
  expect(enabled).not.toContain(">Finance<");
  expect(enabled).not.toContain('href="/finance"');
});

test("the account menu is closed until opened, so the rail shows only the owner's name", () => {
  const html = renderToStaticMarkup(
    createElement(AccountMenu, {
      account: { id: "owner", username: "nigel" },
      onNavigate: () => {},
      onOpenSettings: () => {},
      onLogout: () => {},
    }),
  );
  expect(html).toContain("nigel");
  expect(html).toContain('aria-expanded="false"');
  expect(html).not.toContain("Sign out");
  expect(html).not.toContain('role="menu"');
});

test("an extension is listed only while its feature is running", () => {
  expect(runningExtensions([]).map((extension) => extension.id)).toEqual([]);
  expect(runningExtensions(["finance"]).map((extension) => extension.id)).toEqual(["finance"]);
  expect(runningExtensions(["other"]).map((extension) => extension.id)).toEqual([]);
});

function thread(id: string, updatedAt: string): Thread {
  return { id, title: id, updatedAt } as Thread;
}

test("chats are grouped by the day they were last written in, newest first", () => {
  // Local time, so the day boundaries do not depend on the machine running tests.
  const now = new Date(2026, 9, 11, 15, 0);
  const at = (day: number, hour = 12) => new Date(2026, 9, day, hour).toISOString();
  const groups = groupThreads(
    [
      thread("old", at(1)),
      thread("today-early", at(11, 1)),
      thread("yesterday", at(10)),
      thread("today", at(11, 14)),
      thread("week", at(6)),
    ],
    now,
  );

  expect(groups.map((group) => group.label)).toEqual(["Today", "Yesterday", "Previous 7 days", "Older"]);
  expect(groups.map((group) => group.threads.map((row) => row.id))).toEqual([
    ["today", "today-early"],
    ["yesterday"],
    ["week"],
    ["old"],
  ]);
});

test("an empty group is not shown", () => {
  const now = new Date(2026, 9, 11, 15, 0);
  const groups = groupThreads([thread("today", new Date(2026, 9, 11, 9).toISOString())], now);
  expect(groups.map((group) => group.label)).toEqual(["Today"]);
});

// Integrations are grouped by what a person is doing, on one page; Settings
// no longer has a Connections or Capabilities section.
test("Customize groups integrations as channels, connectors, skills and extensions", () => {
  expect(tabsFor([]).map((tab) => tab.label)).toEqual(["Channels", "Connectors", "Skills", "Extensions"]);
  expect(tabsFor(["marketplace"]).map((tab) => tab.label)).toEqual(["Channels", "Connectors", "Skills", "Extensions", "Plugins"]);
  for (const id of ["connections", "capabilities"]) expect(SECTIONS.some((section) => section.id === id)).toBe(false);
});

test("finance's card, under Customize → Extensions, shows its running state", () => {
  const render = (features: string[]) =>
    renderToStaticMarkup(createElement(FinanceCard, { onSessionExpired: () => {}, running: features.includes("finance") }));

  const financeStatus = (html: string) => html.match(/aria-label="Finance extension status"[^>]*>([^<]*)</)?.[1];

  const disabled = render([]);
  expect(financeStatus(disabled)).toBe("Not running");
  expect(disabled).toContain("Enable finance");

  const enabled = render(["finance"]);
  expect(financeStatus(enabled)).toBe("Running");
});

test("the rail header drags the window and keeps its collapse control clickable", () => {
  const html = sidebar({ view: "chat" });
  expect(html).toContain("[-webkit-app-region:drag]");
  expect(html).toContain('aria-label="Collapse sidebar"');
  expect(html).toContain("[-webkit-app-region:no-drag]");
});
