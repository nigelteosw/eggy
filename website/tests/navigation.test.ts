import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ConfigPage } from "../src/ConfigPage";
import { pathForView, viewForPath } from "../src/routing";

test("maps settings and traces to browser paths", () => {
  expect(pathForView("chat")).toBe("/");
  expect(pathForView("config")).toBe("/settings");
  expect(pathForView("plugins")).toBe("/plugins");
  expect(pathForView("traces")).toBe("/traces");
});

test("maps browser paths back to app views", () => {
  expect(viewForPath("/")).toBe("chat");
  expect(viewForPath("/settings")).toBe("config");
  expect(viewForPath("/settings/")).toBe("config");
  expect(viewForPath("/plugins")).toBe("plugins");
  expect(viewForPath("/plugins/")).toBe("plugins");
  expect(viewForPath("/traces")).toBe("traces");
  expect(viewForPath("/unknown-page")).toBe("chat");
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

import { AppNavigation } from "../src/App";
import { PluginsPage } from "../src/PluginsPage";

for (const view of ["chat", "plugins", "traces", "config"] as const) {
  test(`global navigation keeps all destinations reachable from ${view}`, () => {
    const html = renderToStaticMarkup(createElement(AppNavigation, { view, onNavigate: () => {} }));
    expect(html).toContain('aria-label="Main navigation"');
    expect(html).toContain('href="/"');
    expect(html).toContain('href="/plugins"');
    expect(html).toContain('href="/traces"');
    expect(html).toContain('href="/settings"');
    expect(html.match(/aria-current="page"/g)).toHaveLength(1);
  });
}

test("Plugins stays reachable when Finance is disabled", () => {
  const html = renderToStaticMarkup(createElement(AppNavigation, { view: "chat", onNavigate: () => {}, features: [] }));
  expect(html).toContain('href="/plugins"');
  expect(html).not.toContain('href="/finance"');
});

test("Finance settings live on Plugins instead of Capabilities", () => {
  const html = renderToStaticMarkup(
    createElement(ConfigPage, {
      theme: "dark",
      onThemeChange: () => {},
      onSessionExpired: () => {},
      initialSection: "capabilities",
    }),
  );
  expect(html).toContain("Tools");
  expect(html).not.toContain("Enable finance");
});

test("Plugins reports Finance runtime state and links to its ledger only when running", () => {
  const common = { onSessionExpired: () => {}, onNavigate: () => {} };
  const disabled = renderToStaticMarkup(createElement(PluginsPage, { ...common, features: [] }));
  expect(disabled).toContain("Plugins");
  expect(disabled).toContain("Not running");
  expect(disabled).toContain("Enable finance");
  expect(disabled).not.toContain('href="/finance"');

  const enabled = renderToStaticMarkup(createElement(PluginsPage, { ...common, features: ["finance"] }));
  expect(enabled).toContain("Running");
  expect(enabled).toContain('href="/finance"');
});
