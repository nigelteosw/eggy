// The Finance list's search, filter and sort state, and the request it becomes.
//
// The matching and ordering run on the server, over the amounts in minor units:
// comparing money here would break the rule that the browser never adds,
// compares or converts it. This file only decides what the owner has asked
// for and says so in the request. Every request names its sort and order, so
// the server's defaults never have to be guessed at here.

export type SortKey = "date" | "amount" | "merchant" | "category";
export type SortOrder = "asc" | "desc";

export type EntryView = {
  query: string;
  kind: string;
  category: string;
  sort: SortKey;
  order: SortOrder;
};

export const DEFAULT_VIEW: EntryView = { query: "", kind: "", category: "", sort: "date", order: "desc" };

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

// The query arguments for one month's entries. Blank search and filter are left
// out rather than sent empty.
export function listRequest(range: { from: string; to: string }, view: EntryView) {
  const request: { from: string; to: string; q?: string; kind?: string; category?: string; sort: SortKey; order: SortOrder } = {
    from: range.from,
    to: range.to,
    sort: view.sort,
    order: view.order,
  };
  const query = view.query.trim();
  if (query !== "") request.q = query;
  if (view.kind !== "") request.kind = view.kind;
  if (view.category !== "") request.category = view.category;
  return request;
}

// How long typing waits before the list is asked again.
export const SEARCH_DEBOUNCE_MS = 250;
