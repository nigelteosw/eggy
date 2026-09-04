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
      onBackToChat: () => {},
    }),
  );
  const labels = [...html.matchAll(/<option[^>]*>([^<]+)<\/option>/g)].map((match) => match[1]);

  expect(labels).toEqual([
    "Models",
    "Connections",
    "Capabilities",
    "Automation",
    "Permissions",
    "Appearance",
    "Advanced",
  ]);
});
