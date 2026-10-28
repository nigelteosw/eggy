import { afterEach, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  checkSession,
  clearSession,
  createFinanceEntry,
  currentSession,
  deleteFinanceEntry,
  getFinanceEntries,
  getFinanceSummary,
  updateFinanceEntry,
  type FinanceEntry,
  type FinanceSummary,
} from "../../../src/lib/api";
import { CategoriesPanel } from "../../../src/extensions/finance/FinanceCategories";
import { AddEntryForm, FinanceReport } from "../../../src/extensions/finance/FinancePage";
import { FinanceSettingsView } from "../../../src/extensions/finance/FinanceCard";
import { daysOfRange, monthLabel, shiftMonth } from "../../../src/extensions/finance/finance";
test("months shift across year boundaries and read as words", () => {
  expect(shiftMonth("2026-10", -1)).toBe("2026-09");
  expect(shiftMonth("2026-10", 1)).toBe("2026-11");
  expect(shiftMonth("2026-01", -1)).toBe("2025-12");
  expect(shiftMonth("2026-12", 1)).toBe("2027-01");
  expect(shiftMonth("2026-03", 12)).toBe("2027-03");
  expect(monthLabel("2026-10")).toBe("October 2026");
  expect(monthLabel("2027-01")).toBe("January 2027");
});

test("a month's days are listed in full, including leap days", () => {
  const october = daysOfRange("2026-10-01", "2026-10-31");
  expect(october).toHaveLength(31);
  expect(october[0]).toBe("2026-10-01");
  expect(october[30]).toBe("2026-10-31");
  expect(daysOfRange("2026-02-01", "2026-02-28")).toHaveLength(28);
  expect(daysOfRange("2028-02-01", "2028-02-29")).toHaveLength(29);
});

// One month of SGD and JPY activity, as the server would answer it. SGD earned
// 900.00 and spent 45.20; JPY spent 1200 on a trip.
const summary: FinanceSummary = {
  month: "2026-10",
  from: "2026-10-01",
  to: "2026-10-31",
  currencies: ["USD", "EUR", "JPY", "GBP", "CNY", "AUD", "CAD", "CHF", "HKD", "SGD"],
  default_currency: "SGD",
  palette: ["sky", "emerald", "amber", "rose"],
  flows: [
    {
      currency: "SGD",
      inflow: "900.00",
      outflow: "45.20",
      invested: "200.00",
      net: "654.80",
      net_invested: "200.00",
      count: 4,
      average_day: "1.51",
      average_share: 0.0503,
      savings_rate: 72.8,
      change: { inflow: null, outflow: 50.7, invested: null, net: null },
    },
    {
      currency: "JPY",
      inflow: "0",
      outflow: "1200",
      invested: "0",
      net: "-1200",
      net_invested: "0",
      count: 1,
      average_day: "40",
      average_share: 0.2,
      savings_rate: null,
      change: { inflow: null, outflow: null, invested: null, net: null },
    },
  ],
  trend: [
    { month: "2026-09", currency: "SGD", inflow: "0", outflow: "30.00", invested: "0", net: "-30.00", inflow_share: 0, outflow_share: 0.0333 },
    { month: "2026-10", currency: "SGD", inflow: "900.00", outflow: "45.20", invested: "200.00", net: "654.80", inflow_share: 1, outflow_share: 0.0502 },
    { month: "2026-10", currency: "JPY", inflow: "0", outflow: "1200", invested: "0", net: "-1200", inflow_share: 0, outflow_share: 1 },
  ],
  spending: [
    { currency: "SGD", category: "food", color: "rose", amount: "30.00", count: 2, share: 0.6637, previous: "20.00", change: 50 },
    { currency: "SGD", category: "transport", color: "sky", amount: "15.20", count: 1, share: 0.3363, previous: "0", change: null },
    { currency: "JPY", category: "travel", color: "sky", amount: "1200", count: 1, share: 1, previous: "0", change: null },
  ],
  days: [
    { currency: "SGD", day: "2026-10-01", amount: "30.00", share: 1 },
    { currency: "SGD", day: "2026-10-02", amount: "15.20", share: 0.5067 },
    { currency: "JPY", day: "2026-10-02", amount: "1200", share: 1 },
  ],
  categories: [
    { name: "food", count: 2, color: "rose" },
    { name: "transport", count: 1, color: "sky" },
    { name: "travel", count: 1, color: "sky" },
  ],
  insights: ["You kept 72.8% of what came in", "Food is up 50% on last month"],
};

const entries: FinanceEntry[] = [
  { id: "e1", kind: "expense", date: "2026-10-02", amount: "15.20", currency: "SGD", category: "transport", merchant: "Grab", note: "", source: "chat" },
  { id: "e2", kind: "expense", date: "2026-10-01", amount: "30.00", currency: "SGD", category: "food", merchant: "Ya Kun", note: "with tip", source: "photo" },
];

function report(over: Partial<Parameters<typeof FinanceReport>[0]> = {}) {
  return renderToStaticMarkup(
    createElement(FinanceReport, {
      summary,
      currency: "SGD",
      onCurrency: () => {},
      entries,
      total: entries.length,
      editingId: null,
      busy: false,
      onEdit: () => {},
      onSave: async () => {},
      onDelete: () => {},
      ...over,
    }),
  );
}

test("the page offers a currency switch and draws one currency at a time", () => {
  const html = report();
  expect(html).toContain('aria-label="Currency"');
  expect(html).toContain("SGD 654.80");
  expect(html).toContain("SGD 45.20");
  // JPY's figures belong to the JPY view, not this one.
  expect(html).not.toContain("JPY 1200");
  const jpy = report({ currency: "JPY" });
  expect(jpy).toContain("JPY 1200");
  expect(jpy).not.toContain("SGD 45.20");
});

test("the overview says what was left over, how much was saved, and how spending moved", () => {
  const html = report();
  expect(html).toContain("Left over this month");
  expect(html).toContain("72.8% saved");
  expect(html).toContain("▲ 50.7%");
  expect(html).toContain("Money in");
  expect(html).toContain("Invested");
});

test("a negative figure is marked as negative", () => {
  const html = report({ currency: "JPY" });
  expect(html).toContain("-JPY 1200");
  expect(html).toContain("text-eg-red-ink");
});

test("insights show for the default currency only", () => {
  expect(report()).toContain("You kept 72.8% of what came in");
  expect(report({ currency: "JPY" })).not.toContain("You kept 72.8%");
});

test("the spending bar is split by category in its own colour, with each share and movement", () => {
  const html = report();
  expect(html).toContain('aria-label="Spending split by category"');
  expect(html).toContain('style="width:66.37%;background-color:#f43f5e"');
  expect(html).toContain('style="width:33.63%;background-color:#0ea5e9"');
  expect(html).toContain("66%");
  expect(html).toContain("▲ 50%");
});

test("the trend shows six months of paired bars with the month's net beneath", () => {
  const html = report();
  const trend = html.match(/aria-label="Money in and out by month"[^>]*>(.*?)<\/ol>/s)?.[1] ?? "";
  expect(trend.match(/<li/g)).toHaveLength(2);
  expect(html).toContain("Sep");
  expect(html).toContain("Oct");
  expect(html).toContain("654.80");
});

test("the daily strip draws every day of the month in the chosen currency, with the average line", () => {
  const html = report();
  const strip = html.match(/data-testid="day-strip"[^>]*>(.*?)<\/ol>/s)?.[1] ?? "";
  expect(strip.match(/<li/g)).toHaveLength(31);
  // The JPY row is another currency's day, not part of this strip.
  expect(strip).not.toContain("1200");
  expect(html).toContain("border-dashed");
  expect(html).toContain("bottom:5.03%");
});

test("entries show a coloured category, a signed amount, and the kind they were filed as", () => {
  const html = report({
    entries: [
      ...entries,
      { id: "e3", kind: "income", date: "2026-10-03", amount: "900.00", currency: "SGD", category: "salary", merchant: "", note: "", source: "panel" },
    ],
    total: 3,
  });
  for (const text of ["Grab", "Ya Kun", "2026-10-02", "with tip"]) expect(html).toContain(text);
  expect(html).toContain("via photo");
  expect(html).toContain("−SGD 15.20");
  expect(html).toContain("+SGD 900.00");
  expect(html).toContain("Received");
  expect(html.match(/>Edit</g)).toHaveLength(3);
});

test("an entry being edited shows its fields instead of its row, with its kind", () => {
  const html = report({ editingId: "e2" });
  expect(html).toContain('value="30.00"');
  expect(html).toContain("Save");
  expect(html).toContain("Cancel");
  expect(html).toContain('aria-label="Kind"');
  // The other row is untouched.
  expect(html.match(/>Edit</g)).toHaveLength(1);
});

test("an empty month says so and says how to start", () => {
  const html = report({
    summary: { ...summary, flows: [], trend: [], spending: [], days: [], insights: [] },
    entries: [],
    total: 0,
  });
  expect(html).toContain("No money logged in October 2026");
  expect(html).toContain("Tell Eggy");
});

test("a list that was cut short says how much is not shown", () => {
  expect(report({ total: 250 })).toContain("248 more not shown");
  expect(report()).not.toContain("more not shown");
});

test("the add-entry form offers the four kinds with spending first, and the supported currencies", () => {
  const html = renderToStaticMarkup(
    createElement(AddEntryForm, { currencies: summary.currencies, defaultCurrency: "SGD", categories: ["food"], busy: false, onCreate: async () => true }),
  );
  for (const kind of ["Spent", "Received", "Invested", "Withdrawn"]) expect(html).toContain(`>${kind}<`);
  expect(html).toMatch(/aria-checked="true"[^>]*>Spent</);
  const options = [...html.matchAll(/<option[^>]*value="([A-Z]{3})"/g)].map((m) => m[1]);
  expect(options).toEqual(summary.currencies);
  expect(html).toMatch(/<option[^>]*value="SGD"[^>]*selected|<option[^>]*selected[^>]*value="SGD"/);
  expect(html).toContain("Add entry");
  // Categories are suggestions, not an enum: the field is free text.
  expect(html).toContain('<datalist id="finance-categories">');
  expect(html).toContain('value="groceries"');
});

test("the categories panel shows each category's palette, with its own colour selected", () => {
  const html = renderToStaticMarkup(
    createElement(CategoriesPanel, { categories: summary.categories, palette: summary.palette, busy: false, onColour: () => {}, onRename: async () => true }),
  );
  expect(html).toContain("Renaming a category to the name of another merges");
  const rose = html.match(/aria-label="rose"[^>]*aria-checked="(true|false)"|aria-checked="(true|false)"[^>]*aria-label="rose"/);
  expect(rose).not.toBeNull();
  expect(rose?.[0]).toContain('aria-checked="true"');
});

// --- API calls, through a recorded fetch -------------------------------------

type Call = { url: string; method: string; body: string | undefined; csrf: string | null };
const realFetch = globalThis.fetch;
let calls: Call[] = [];

function stubFetch(respond: (url: string) => unknown) {
  calls = [];
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const headers = new Headers(init?.headers);
    calls.push({
      url: String(input),
      method: (init?.method ?? "GET").toUpperCase(),
      body: typeof init?.body === "string" ? init.body : undefined,
      csrf: headers.get("X-Eggy-CSRF"),
    });
    return new Response(JSON.stringify(respond(String(input))), { status: 200, headers: { "Content-Type": "application/json" } });
  }) as typeof fetch;
}

afterEach(() => {
  globalThis.fetch = realFetch;
  clearSession();
});

test("the session carries the server's feature list, always as a list", async () => {
  stubFetch(() => ({ state: "success", account: { id: "a", username: "a" }, csrf: "tok", features: ["finance"] }));
  await checkSession();
  expect(currentSession()?.features).toEqual(["finance"]);

  stubFetch(() => ({ state: "success", account: { id: "a", username: "a" }, csrf: "tok" }));
  await checkSession();
  expect(currentSession()?.features).toEqual([]);
});

test("finance calls hit the documented routes with the CSRF token on writes", async () => {
  stubFetch((url) => (url === "/api/session" ? { state: "success", csrf: "tok", features: ["finance"] } : { ok: true }));
  await checkSession();

  await getFinanceSummary("2026-10");
  await getFinanceEntries({ from: "2026-10-01", to: "2026-10-31", category: "food", limit: 50 });
  await createFinanceEntry({ amount: "14.50", category: "food", merchant: "Ya Kun" });
  await updateFinanceEntry("e 1", { amount: "15.40", note: "" });
  await deleteFinanceEntry("e1");

  const [, summaryCall, listCall, createCall, patchCall, deleteCall] = calls;
  expect(summaryCall.url).toBe("/api/finance/summary?month=2026-10");
  expect(summaryCall.method).toBe("GET");
  expect(summaryCall.csrf).toBeNull();

  const list = new URL(listCall.url, "https://x.test");
  expect(list.pathname).toBe("/api/finance/entries");
  expect(Object.fromEntries(list.searchParams)).toEqual({ from: "2026-10-01", to: "2026-10-31", category: "food", limit: "50" });

  expect(createCall.method).toBe("POST");
  expect(createCall.url).toBe("/api/finance/entries");
  expect(JSON.parse(createCall.body ?? "")).toEqual({ amount: "14.50", category: "food", merchant: "Ya Kun" });
  expect(createCall.csrf).toBe("tok");

  expect(patchCall.method).toBe("PATCH");
  expect(patchCall.url).toBe("/api/finance/entries/e%201");
  // An explicitly empty note is sent: it means clear it, not leave it.
  expect(JSON.parse(patchCall.body ?? "")).toEqual({ amount: "15.40", note: "" });
  expect(patchCall.csrf).toBe("tok");

  expect(deleteCall.method).toBe("DELETE");
  expect(deleteCall.url).toBe("/api/finance/entries/e1");
  expect(deleteCall.csrf).toBe("tok");
});

test("an unset month asks for the server's current one", async () => {
  stubFetch(() => ({}));
  await getFinanceSummary();
  expect(calls[0].url).toBe("/api/finance/summary");
});

// --- Settings card -------------------------------------------------------------

function settings(over: Partial<Parameters<typeof FinanceSettingsView>[0]> = {}) {
  return renderToStaticMarkup(
    createElement(FinanceSettingsView, {
      enabled: false,
      savedEnabled: false,
      currency: "SGD",
      currencies: summary.currencies,
      running: false,
      saving: false,
      error: null,
      onEnabledChange: () => {},
      onCurrencyChange: () => {},
      onSave: () => {},
      ...over,
    }),
  );
}

test("the settings card says to restart only when saved and running states differ", () => {
  expect(settings({ enabled: true, savedEnabled: true, running: false })).toContain("Restart to apply");
  expect(settings({ enabled: false, savedEnabled: false, running: true })).toContain("Restart to apply");
  expect(settings({ enabled: true, savedEnabled: true, running: true })).not.toContain("Restart to apply");
  expect(settings({ enabled: false, savedEnabled: false, running: false })).not.toContain("Restart to apply");
});

test("the settings card identifies whether Finance is running", () => {
  expect(settings({ running: true })).toContain("Running");
  expect(settings({ running: false })).toContain("Not running");
});

// Flipping the switch is not yet a change to config.yaml, so it cannot yet
// need a restart.
test("an unsaved switch does not claim a restart is needed", () => {
  expect(settings({ enabled: true, savedEnabled: false, running: false })).not.toContain("Restart to apply");
});

test("the settings card offers the supported currencies and reassures about data", () => {
  const html = settings({ currency: "JPY" });
  const options = [...html.matchAll(/<option[^>]*value="([A-Z]{3})"/g)].map((m) => m[1]);
  expect(options).toEqual(summary.currencies);
  expect(html).toMatch(/<option[^>]*value="JPY"[^>]*selected|<option[^>]*selected[^>]*value="JPY"/);
  expect(html).toContain("private");
  expect(html).toContain("kept");
});

test("a settings error is shown", () => {
  expect(settings({ error: "finance.currency must be one of" })).toContain("finance.currency must be one of");
});
