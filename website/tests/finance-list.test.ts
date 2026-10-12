import { expect, test } from "bun:test";
import { DEFAULT_VIEW, SORT_KEYS, chooseSort, isFiltered, listRequest, type EntryView } from "../src/lib/finance-list";

const range = { from: "2026-10-01", to: "2026-10-31" };

test("choosing a new sort key takes that key's natural order", () => {
  expect(chooseSort(DEFAULT_VIEW, "amount")).toMatchObject({ sort: "amount", order: "desc" });
  expect(chooseSort(DEFAULT_VIEW, "merchant")).toMatchObject({ sort: "merchant", order: "asc" });
  expect(chooseSort(DEFAULT_VIEW, "category")).toMatchObject({ sort: "category", order: "asc" });
});

test("choosing the key already in use flips its direction", () => {
  const ascending: EntryView = { ...DEFAULT_VIEW, sort: "amount", order: "asc" };
  expect(chooseSort(ascending, "amount").order).toBe("desc");
  expect(chooseSort(chooseSort(ascending, "amount"), "amount").order).toBe("asc");
});

test("a sort change keeps the search and the category filter", () => {
  const filtered: EntryView = { query: "coffee", category: "food", sort: "date", order: "desc" };
  expect(chooseSort(filtered, "merchant")).toEqual({ query: "coffee", category: "food", sort: "merchant", order: "asc" });
});

test("every sort key is offered once, in a fixed order", () => {
  expect(SORT_KEYS).toEqual(["date", "amount", "merchant", "category"]);
});

test("the request always names its sort and order, and leaves blank search and filter out", () => {
  expect(listRequest(range, DEFAULT_VIEW)).toEqual({ from: "2026-10-01", to: "2026-10-31", sort: "date", order: "desc" });
  expect(listRequest(range, { query: "  coffee  ", category: "food", sort: "amount", order: "asc" })).toEqual({
    from: "2026-10-01",
    to: "2026-10-31",
    q: "coffee",
    category: "food",
    sort: "amount",
    order: "asc",
  });
  expect(listRequest(range, { ...DEFAULT_VIEW, query: "   " })).not.toHaveProperty("q");
});

test("a view is filtered only by a search or a category, not by its sort", () => {
  expect(isFiltered(DEFAULT_VIEW)).toBe(false);
  expect(isFiltered({ ...DEFAULT_VIEW, sort: "amount", order: "asc" })).toBe(false);
  expect(isFiltered({ ...DEFAULT_VIEW, query: "tea" })).toBe(true);
  expect(isFiltered({ ...DEFAULT_VIEW, category: "food" })).toBe(true);
  expect(isFiltered({ ...DEFAULT_VIEW, query: "   " })).toBe(false);
});
