import { expect, test } from "bun:test";
import { createElement, type ComponentType } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { GoogleCard } from "../src/GoogleCard";
import { HeartbeatCard } from "../src/HeartbeatCard";
import { McpCard } from "../src/McpCard";
import { ModelsCard } from "../src/ModelsCard";
import { ProvidersCard } from "../src/ProvidersCard";
import { TracingCard } from "../src/TracingCard";
import { AdvancedCard } from "../src/AdvancedCard";

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
