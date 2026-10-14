import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { FinanceSummary } from "../../../src/api";
import { FinanceReport } from "../../../src/extensions/finance/FinancePage";
import { FinanceToolbar } from "../../../src/extensions/finance/FinanceToolbar";
import { DEFAULT_VIEW } from "../../../src/extensions/finance/finance-list";

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
  palette: ["sky"],
  flows: [
    {
      currency: "SGD",
      inflow: "0",
      outflow: "6.50",
      invested: "0",
      net: "-6.50",
      net_invested: "0",
      count: 1,
      average_day: "6.50",
      average_share: 1,
      savings_rate: null,
      change: { inflow: null, outflow: null, invested: null, net: null },
    },
  ],
  trend: [],
  spending: [{ currency: "SGD", category: "food", color: "sky", amount: "6.50", count: 1, share: 1, previous: "0", change: null }],
  days: [{ currency: "SGD", day: "2026-10-02", amount: "6.50", share: 1 }],
  categories: [{ name: "food", count: 1, color: "sky" }],
  insights: [],
};

const reportWith = (over: Partial<Parameters<typeof FinanceReport>[0]>) =>
  renderToStaticMarkup(
    createElement(FinanceReport, {
      summary,
      currency: "SGD",
      onCurrency: () => {},
      entries: [],
      total: 0,
      editingId: null,
      busy: false,
      onEdit: () => {},
      onSave: async () => {},
      onDelete: () => {},
      ...over,
    }),
  );

test("a search that matches nothing still shows the entries section and says so", () => {
  const markup = reportWith({
    filtered: true,
    listControls: createElement("div", { "data-testid": "controls" }),
  });
  expect(markup).toContain("No entries match.");
  expect(markup).toContain('data-testid="controls"');
});

test("with no search or filter and no entries, the section is left out", () => {
  const markup = reportWith({ summary: { ...summary, flows: [], spending: [], days: [] } });
  expect(markup).not.toContain("No entries match.");
  expect(markup).not.toContain("Entries");
});
