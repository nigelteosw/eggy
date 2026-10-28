import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { HomePage, watchLines } from "../src/HomePage";
import { RestartBar } from "../src/RestartBar";
import { pathForView, viewForPath } from "../src/routing";

test("home is a destination with its own address", () => {
  expect(pathForView("home")).toBe("/home");
  expect(viewForPath("/home")).toBe("home");
});

test("the watch list counts its items, not its headings or blank lines", () => {
  expect(watchLines("# Watching\n\n- PR #18 open since Aug 20\n- Invoice due Friday\n")).toEqual([
    "- PR #18 open since Aug 20",
    "- Invoice due Friday",
  ]);
  expect(watchLines("")).toEqual([]);
});

test("home says what it is doing while it loads, and offers its three sections", () => {
  const html = renderToStaticMarkup(
    createElement(HomePage, {
      onSessionExpired: () => {},
      onNavigate: () => {},
      onOpenSettings: () => {},
      onNewChat: () => {},
    }),
  );
  expect(html).toContain("Loading…");
  expect(html).toContain('aria-labelledby="home-watching"');
  expect(html).toContain('aria-labelledby="home-coming-up"');
});

test("the restart bar names the change and offers one action that applies it", () => {
  const html = renderToStaticMarkup(createElement(RestartBar, { onSessionExpired: () => {} }));
  expect(html).toContain("take effect after a restart");
  expect(html).toContain("Restart now");
  expect(html).toContain('role="status"');
});
