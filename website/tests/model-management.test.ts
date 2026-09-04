import { afterEach, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { removeModelAlias } from "../src/api";
import { ConfigPage } from "../src/ConfigPage";
import { ModelRowActions, modelDraftForRow } from "../src/ModelsCard";
import { DataTable } from "../src/components/ui/data-table";

const realFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = realFetch;
});

test("model rows map back into the editable fields", () => {
  expect(modelDraftForRow(["fast", "deepseek", "deepseek-v4-flash", "low, high"])).toEqual({
    alias: "fast",
    provider: "deepseek",
    model: "deepseek-v4-flash",
    reasoningEfforts: "low, high",
  });
});

test("model rows expose edit and remove actions", () => {
  const html = renderToStaticMarkup(
    createElement(ModelRowActions, { alias: "fast", onEdit: () => {}, onRemove: () => {} }),
  );

  expect(html).toContain("Edit fast");
  expect(html).toContain("Remove fast");
});

test("table actions remain visible while wide model rows scroll", () => {
  const html = renderToStaticMarkup(
    createElement(DataTable, {
      headers: ["Alias", "Provider", "Model"],
      rows: [["fast", "deepseek", "deepseek-v4-flash"]],
      empty: "No models",
      renderRowAction: () => createElement("button", null, "Edit"),
    }),
  );

  expect((html.match(/sticky right-0/g) ?? []).length).toBeGreaterThanOrEqual(2);
});

test("model removal calls the alias-specific authenticated route", async () => {
  let path = "";
  let method = "";
  globalThis.fetch = (async (input, init) => {
    path = String(input);
    method = init?.method ?? "";
    return new Response(JSON.stringify({ state: "success" }), { status: 200 });
  }) as typeof fetch;

  await removeModelAlias("fast/model");

  expect(path).toBe("/api/config/models/fast%2Fmodel");
  expect(method).toBe("DELETE");
});

test("the Models section keeps its restart action nearby", () => {
  const html = renderToStaticMarkup(
    createElement(ConfigPage, {
      theme: "dark",
      onThemeChange: () => {},
      onSessionExpired: () => {},
      onBackToChat: () => {},
    }),
  );

  expect(html).toContain("Restart Eggy");
  expect(html).toContain("New model choices appear in chat after restart");
});
