import { expect, test } from "bun:test";
import { createElement, type ComponentType } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { GoogleCard } from "../src/customize/GoogleCard";
import { HeartbeatCard } from "../src/settings/HeartbeatCard";
import { McpCard } from "../src/customize/McpCard";
import { ModelsCard } from "../src/settings/ModelsCard";
import { ProvidersCard } from "../src/settings/ProvidersCard";
import { TracingCard } from "../src/settings/TracingCard";
import { AdvancedCard } from "../src/settings/AdvancedCard";

function render(Card: ComponentType<{ onSessionExpired: () => void }>) {
  return renderToStaticMarkup(createElement(Card, { onSessionExpired: () => {} }));
}

test("connection and model creation forms start behind explicit actions", () => {
  const cases: Array<[ComponentType<{ onSessionExpired: () => void }>, string]> = [
    [ProvidersCard, "Add provider"],
    [ModelsCard, "Add model alias"],
    [McpCard, "Add MCP server"],
    [GoogleCard, "Configure Google Workspace"],
  ];

  for (const [Card, action] of cases) {
    const html = render(Card);
    expect(html).toContain(`<summary`);
    expect(html).toContain(action);
    expect(html).not.toContain("<details open");
  }
});

test("technical model and connection fields use advanced disclosure", () => {
  expect(render(ProvidersCard)).toContain("Advanced options");
  expect(render(ModelsCard)).toContain("Advanced options");
  expect(render(McpCard)).toContain("Advanced options");
  expect(render(GoogleCard)).toContain("Advanced options");
});

test("automation and diagnostics expose configuration on demand", () => {
  expect(render(HeartbeatCard)).toContain("Configure heartbeat");
  expect(render(HeartbeatCard)).toContain("Advanced options");
  expect(render(TracingCard)).toContain("Configure tracing");
  expect(render(TracingCard)).toContain("Advanced options");
  expect(render(AdvancedCard)).toContain("Edit config.yaml");
});
