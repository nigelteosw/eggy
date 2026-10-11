import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { AccountMenu } from "../src/AccountMenu";
import { ConfigPage } from "../src/ConfigPage";
import { Dialog } from "../src/components/ui/dialog";
import { ThreadSidebar, groupThreads } from "../src/ThreadSidebar";
import type { Thread } from "../src/api";
import { pathForView, viewForPath, type View } from "../src/routing";

test("maps each destination to its browser path", () => {
  expect(pathForView("chat")).toBe("/");
  expect(pathForView("scheduled")).toBe("/scheduled");
  expect(pathForView("config")).toBe("/settings");
  expect(pathForView("traces")).toBe("/traces");
});

test("maps browser paths back to app views", () => {
  expect(viewForPath("/")).toBe("chat");
  expect(viewForPath("/scheduled")).toBe("scheduled");
  expect(viewForPath("/scheduled/")).toBe("scheduled");
  expect(viewForPath("/settings")).toBe("config");
  expect(viewForPath("/settings/")).toBe("config");
  expect(viewForPath("/traces")).toBe("traces");
  expect(viewForPath("/unknown-page")).toBe("chat");
});

test("retired pages fall back to chat rather than an empty screen", () => {
  // Plugins was a page and Finance is now a popup, so their old addresses land
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
  // Scoped to the navigation select: settings cards have selects of their own.
  const nav = html.match(/<select[^>]*aria-label="Mobile settings navigation"[^>]*>(.*?)<\/select>/)?.[1] ?? "";
  const labels = [...nav.matchAll(/<option[^>]*>([^<]+)<\/option>/g)].map((match) => match[1]);

  expect(labels).toEqual([
    "Model &amp; approvals",
    "Automation",
    "Pending approvals",
    "People",
    "Models",
    "Connections",
    "Capabilities",
    "Soul",
    "Appearance",
    "Advanced",
  ]);
  const groups = [...nav.matchAll(/<optgroup label="([^"]+)"/g)].map((match) => match[1]);
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
      onOpenFinance: () => {},
      onLogout: () => {},
    }),
  );
}

for (const view of ["chat", "scheduled", "traces", "config"] as const) {
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

test("Finance appears in the rail only when the capability is running", () => {
  expect(sidebar({ view: "chat", features: [] })).not.toContain(">Finance<");
  const enabled = sidebar({ view: "chat", features: ["finance"] });
  expect(enabled).toContain(">Finance<");
  // It opens a popup, so it is a button rather than a link to a page.
  expect(enabled).not.toContain('href="/finance"');
});

test("the account menu is closed until opened, so the rail shows only the owner's name", () => {
  const html = renderToStaticMarkup(
    createElement(AccountMenu, {
      account: { id: "owner", username: "nigel" },
      onNavigate: () => {},
      onLogout: () => {},
    }),
  );
  expect(html).toContain("nigel");
  expect(html).toContain('aria-expanded="false"');
  expect(html).not.toContain("Sign out");
  expect(html).not.toContain('role="menu"');
});

test("the finance popup renders nothing while closed and names itself when open", () => {
  const closed = renderToStaticMarkup(
    createElement(Dialog, { open: false, title: "Finance", onClose: () => {} }, "body"),
  );
  expect(closed).toBe("");

  const open = renderToStaticMarkup(
    createElement(Dialog, { open: true, title: "Finance", onClose: () => {} }, "body"),
  );
  expect(open).toContain('role="dialog"');
  expect(open).toContain('aria-label="Finance"');
  expect(open).toContain('aria-label="Close Finance"');
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

test("finance settings live under Capabilities, where its running state is shown", () => {
  const render = (features: string[]) =>
    renderToStaticMarkup(
      createElement(ConfigPage, {
        theme: "dark",
        onThemeChange: () => {},
        onSessionExpired: () => {},
        features,
        initialSection: "capabilities",
      }),
    );

  const disabled = render([]);
  expect(disabled).toContain("Not running");
  expect(disabled).toContain("Enable finance");

  const enabled = render(["finance"]);
  expect(enabled).toContain("Running");
  expect(enabled).not.toContain("Not running");
});
