import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ConfigPage } from "../src/ConfigPage";
import { pathForView, viewForPath } from "../src/routing";

test("maps settings and traces to browser paths", () => {
  expect(pathForView("chat")).toBe("/");
  expect(pathForView("config")).toBe("/settings");
  expect(pathForView("traces")).toBe("/traces");
});

test("maps browser paths back to app views", () => {
  expect(viewForPath("/")).toBe("chat");
  expect(viewForPath("/settings")).toBe("config");
  expect(viewForPath("/settings/")).toBe("config");
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
    "Appearance",
    "Advanced",
  ]);
  const groups = [...nav.matchAll(/<optgroup label="([^"]+)"/g)].map((match) => match[1]);
  expect(groups).toEqual(["My settings", "People", "Shared deployment"]);
});

import { AppNavigation } from "../src/App";

for (const view of ["chat", "traces", "config"] as const) {
  test(`global navigation keeps all destinations reachable from ${view}`, () => {
    const html = renderToStaticMarkup(createElement(AppNavigation, { view, onNavigate: () => {} }));
    expect(html).toContain('aria-label="Main navigation"');
    expect(html).toContain('href="/"');
    expect(html).toContain('href="/traces"');
    expect(html).toContain('href="/settings"');
    expect(html.match(/aria-current="page"/g)).toHaveLength(1);
  });
}
