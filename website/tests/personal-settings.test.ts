import { afterEach, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { clearSession, setAgentThinking } from "../src/api";
import { PersonalSettingsCard } from "../src/PersonalSettingsCard";
import { ConfigPage, GROUPS, SECTIONS } from "../src/ConfigPage";

const realFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = realFetch;
  clearSession();
});

test("settings are grouped into my settings, people, and shared deployment, with the ownership copy", () => {
  expect(GROUPS.map((group) => group.heading)).toEqual(["My settings", "People", "Shared deployment"]);
  expect(GROUPS[0].note).toContain("Telegram and web sessions");
  expect(GROUPS[1].note).toContain("Trusted users");
  expect(GROUPS[2].note).toContain("affect everyone");
  const personal = SECTIONS.filter((section) => section.group === "personal").map((section) => section.id);
  expect(personal).toEqual(["personal", "automation", "permissions"]);
  const shared = SECTIONS.filter((section) => section.group === "shared").map((section) => section.id);
  expect(shared).toContain("models");
  expect(shared).toContain("connections");
  expect(shared).toContain("advanced");
  // Shared controls are labelled as such where they are rendered.
  for (const section of SECTIONS.filter((section) => section.group === "shared")) {
    expect(section.title).toContain("Shared deployment");
  }
  expect(SECTIONS.find((section) => section.id === "accounts")!.title).toContain("People — trusted users who can administer this deployment");
});

test("the settings page opens on my settings and lists every group in the mobile navigation", () => {
  const html = renderToStaticMarkup(createElement(ConfigPage, { theme: "dark", onThemeChange: () => {}, onSessionExpired: () => {} }));
  expect(html).toContain("My settings — applies to your Telegram and web sessions");
  expect(html).toContain('label="My settings"');
  expect(html).toContain('label="People"');
  expect(html).toContain('label="Shared deployment"');
  expect(html).toContain("Changes here affect everyone");
});

test("the personal settings card explains its scope and offers the four personal controls", async () => {
  globalThis.fetch = (async () =>
    new Response(JSON.stringify({ models: ["shared-default", "other"], model: "shared-default", efforts: ["low", "high"], effort: "high", show_thinking: true, approval_mode: "normal" }), { status: 200 })) as typeof fetch;
  const html = renderToStaticMarkup(createElement(PersonalSettingsCard, { onSessionExpired: () => {} }));
  expect(html).toContain("My settings — applies to your Telegram and web sessions");
  expect(html).toContain("yours alone");
  expect(html).not.toContain("api_key");
  expect(html).not.toContain("Provider");
});

test("thinking visibility posts the structured body to its own route", async () => {
  const calls: { path: string; method: string; body: string }[] = [];
  globalThis.fetch = (async (input, init) => {
    calls.push({ path: String(input), method: init?.method ?? "GET", body: String(init?.body ?? "") });
    return new Response(JSON.stringify({ models: [], model: "", efforts: [], effort: "", show_thinking: false }), { status: 200 });
  }) as typeof fetch;
  const selection = await setAgentThinking(false);
  expect(calls).toEqual([{ path: "/api/agent/thinking", method: "POST", body: JSON.stringify({ show: false }) }]);
  expect(selection.show_thinking).toBe(false);
});
