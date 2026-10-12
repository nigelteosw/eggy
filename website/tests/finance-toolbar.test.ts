import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { FinanceSummary } from "../src/api";
import { FinanceReport } from "../src/FinancePage";
import { FinanceToolbar } from "../src/FinanceToolbar";
import { DEFAULT_VIEW } from "../src/lib/finance-list";

const toolbar = (categories: string[] = ["food", "transport"]) =>
  renderToStaticMarkup(createElement(FinanceToolbar, { view: DEFAULT_VIEW, categories, onChange: () => {}, onAdd: () => {} }));

test("the toolbar reads search, filter, sort, then Add", () => {
  const markup = toolbar();
  const at = (text: string) => markup.indexOf(text);
  expect(at('aria-label="Search entries"')).toBeGreaterThan(-1);
  expect(at('aria-label="Filter entries"')).toBeGreaterThan(at('aria-label="Search entries"'));
  expect(at('aria-label="Sort entries"')).toBeGreaterThan(at('aria-label="Filter entries"'));
  expect(at(">Add")).toBeGreaterThan(at('aria-label="Sort entries"'));
});

test("the filter and sort panels stay closed until opened", () => {
  const markup = toolbar();
  expect(markup).not.toContain('aria-label="Category"');
  expect(markup).not.toContain('aria-label="Sort by"');
});

const summary: FinanceSummary = {
  month: "2026-10",
  from: "2026-10-01",
  to: "2026-10-31",
  currencies: ["SGD"],
  default_currency: "SGD",
  totals: [{ currency: "SGD", amount: "6.50", count: 1 }],
  by_category: [{ currency: "SGD", category: "food", amount: "6.50", count: 1, share: 1 }],
  by_day: [{ currency: "SGD", day: "2026-10-02", amount: "6.50", share: 1 }],
};

test("a search that matches nothing still shows the entries section and says so", () => {
  const markup = renderToStaticMarkup(
    createElement(FinanceReport, {
      summary,
      entries: [],
      total: 0,
      editingId: null,
      busy: false,
      onEdit: () => {},
      onSave: async () => {},
      onDelete: () => {},
      filtered: true,
      listControls: createElement("div", { "data-testid": "controls" }),
    }),
  );
  expect(markup).toContain("No entries match.");
  expect(markup).toContain('data-testid="controls"');
});

test("with no search or filter and no entries, the section is left out", () => {
  const markup = renderToStaticMarkup(
    createElement(FinanceReport, {
      summary,
      entries: [],
      total: 0,
      editingId: null,
      busy: false,
      onEdit: () => {},
      onSave: async () => {},
      onDelete: () => {},
    }),
  );
  expect(markup).not.toContain("No entries match.");
  expect(markup).not.toContain("Entries");
});
