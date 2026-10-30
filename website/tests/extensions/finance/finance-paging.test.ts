import { describe, expect, test } from "bun:test";
import { DEFAULT_VIEW, changeView, listRequest, pageCount, shownRange } from "../../../src/extensions/finance/finance-list";

const range = { from: "2026-10-01", to: "2026-10-31" };

describe("finance paging", () => {
  test("asks the server for one page at the view's size", () => {
    const view = { ...DEFAULT_VIEW, pageSize: 10 as const, page: 2 };
    const request = listRequest(range, view);
    expect(request.limit).toBe(10);
    expect(request.offset).toBe(20);
  });

  test("any change but paging returns to the first page", () => {
    const onPage3 = { ...DEFAULT_VIEW, page: 3 };
    expect(changeView(onPage3, { query: "grab" }).page).toBe(0);
    expect(changeView(onPage3, { pageSize: 50 }).page).toBe(0);
    expect(changeView(onPage3, { page: 4 }).page).toBe(4);
  });

  test("counts pages and the entries on screen", () => {
    expect(pageCount(0, 20)).toBe(1);
    expect(pageCount(41, 20)).toBe(3);
    expect(shownRange({ ...DEFAULT_VIEW, page: 2 }, 1)).toEqual({ first: 41, last: 41 });
  });
});
