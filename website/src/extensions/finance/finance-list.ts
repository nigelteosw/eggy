// The Finance list's search, filter and sort state, and the request it becomes.
//
// The matching and ordering run on the server, over the amounts in minor units:
// comparing money here would break the rule that the browser never adds,
// compares or converts it. This file only decides what the owner has asked
// for and says so in the request. Every request names its sort and order, so
// the server's defaults never have to be guessed at here.

export type SortKey = "date" | "amount" | "merchant" | "category";
export type SortOrder = "asc" | "desc";

// How many entries a page shows. The server returns one page at a time, so a
// long month loads quickly and its oldest entries are a page away, not cut off.
export const PAGE_SIZES = [10, 20, 50] as const;
export type PageSize = (typeof PAGE_SIZES)[number];

export type EntryView = {
  query: string;
  kind: string;
  category: string;
  sort: SortKey;
  order: SortOrder;
  pageSize: PageSize;
  // Zero-based.
  page: number;
};

export const DEFAULT_VIEW: EntryView = { query: "", kind: "", category: "", sort: "date", order: "desc", pageSize: 20, page: 0 };

// changeView applies a change to the view. Anything other than paging starts
// again from the first page, since page 3 of a different search is not a place
// anyone asked to be.
export function changeView(view: EntryView, change: Partial<EntryView>): EntryView {
  return { ...view, ...change, page: change.page ?? 0 };
}

export function pageCount(total: number, pageSize: number): number {
  return Math.max(1, Math.ceil(total / pageSize));
}

// The 1-based positions of the entries on screen, for "21-40 of 87".
export function shownRange(view: EntryView, shown: number): { first: number; last: number } {
  const first = view.page * view.pageSize + 1;
  return { first, last: first + shown - 1 };
}

export function isPageSize(value: number): value is PageSize {
  return (PAGE_SIZES as readonly number[]).includes(value);
}

// The label each key shows, and the order its first choice takes: newest and
// largest first for date and amount, A to Z for the words.
export const SORTS: Record<SortKey, { label: string; firstOrder: SortOrder }> = {
  date: { label: "Date", firstOrder: "desc" },
  amount: { label: "Amount", firstOrder: "desc" },
  merchant: { label: "Merchant", firstOrder: "asc" },
  category: { label: "Category", firstOrder: "asc" },
};

export const SORT_KEYS = Object.keys(SORTS) as SortKey[];

// Choosing a key sorts by it in its natural order. Choosing the key already in
// use flips the direction, so the second click on a column reverses it.
export function chooseSort(view: EntryView, key: SortKey): EntryView {
  if (view.sort !== key) return { ...view, sort: key, order: SORTS[key].firstOrder };
  return { ...view, order: view.order === "asc" ? "desc" : "asc" };
}

// Whether a search, kind or category is narrowing the list. A sort alone does
// not hide anything, so it does not count.
export function isFiltered(view: EntryView): boolean {
  return view.query.trim() !== "" || view.kind !== "" || view.category !== "";
}

// How many of the filter panel's choices are in use, for the badge on its button.
export function filterCount(view: EntryView): number {
  return (view.kind !== "" ? 1 : 0) + (view.category !== "" ? 1 : 0);
}

// The query arguments for one page of a month's entries. Blank search and
// filter are left out rather than sent empty.
export function listRequest(range: { from: string; to: string }, view: EntryView) {
  const request: {
    from: string;
    to: string;
    q?: string;
    kind?: string;
    category?: string;
    sort: SortKey;
    order: SortOrder;
    limit: number;
    offset: number;
  } = {
    from: range.from,
    to: range.to,
    sort: view.sort,
    order: view.order,
    limit: view.pageSize,
    offset: view.page * view.pageSize,
  };
  const query = view.query.trim();
  if (query !== "") request.q = query;
  if (view.kind !== "") request.kind = view.kind;
  if (view.category !== "") request.category = view.category;
  return request;
}

// How long typing waits before the list is asked again.
export const SEARCH_DEBOUNCE_MS = 250;
