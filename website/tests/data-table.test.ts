import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { DataTable } from "../src/components/ui/data-table";

const props = {
  headers: ["Provider", "Base URL"],
  rows: [["deepseek", "https://api.deepseek.com/v1/chat/completions"]],
  empty: "None yet.",
};

test("a wide screen keeps the table, and a phone gets each row as a stacked card", () => {
  const html = renderToStaticMarkup(createElement(DataTable, props));
  // The table only appears from the small breakpoint up; below it the stacked
  // form takes over, so no row is cut off by sideways scrolling.
  expect(html).toContain("hidden overflow-x-auto sm:block");
  expect(html).toContain("sm:hidden");
  expect(html).toContain("<table");
});

test("each stacked value carries its column's label, so the card reads on its own", () => {
  const html = renderToStaticMarkup(createElement(DataTable, props));
  expect(html).toContain(">Base URL<");
  expect(html).toContain("https://api.deepseek.com/v1/chat/completions");
});

test("the empty state is unchanged", () => {
  const html = renderToStaticMarkup(createElement(DataTable, { ...props, rows: [], empty: "None yet." }));
  expect(html).toContain("None yet.");
  expect(html).not.toContain("<table");
});
