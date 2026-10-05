import { afterEach, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { AppNavigation } from "../src/App";
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
} from "../src/api";
import { AddEntryForm, FinanceReport } from "../src/FinancePage";
import { FinanceSettingsView } from "../src/FinanceCard";
import { daysOfRange, monthLabel, shiftMonth } from "../src/lib/finance";
import { pathForView, viewForPath } from "../src/routing";

test("the finance view has its own path, with or without a trailing slash", () => {
  expect(pathForView("finance")).toBe("/finance");
  expect(viewForPath("/finance")).toBe("finance");
  expect(viewForPath("/finance/")).toBe("finance");
  expect(viewForPath("/settings")).toBe("config");
});

test("the Finance tab appears only when the server reports the feature", () => {
  const without = renderToStaticMarkup(createElement(AppNavigation, { view: "chat", onNavigate: () => {} }));
  expect(without).not.toContain("Finance");
  expect(without).not.toContain('href="/finance"');

  const empty = renderToStaticMarkup(createElement(AppNavigation, { view: "chat", onNavigate: () => {}, features: [] }));
  expect(empty).not.toContain('href="/finance"');

  const withFinance = renderToStaticMarkup(
    createElement(AppNavigation, { view: "chat", onNavigate: () => {}, features: ["finance"] }),
  );
  expect(withFinance).toContain('href="/finance"');
  expect(withFinance).toContain("Finance");
  // The other destinations are still there, and exactly one is current.
  for (const href of ["/", "/traces", "/settings"]) expect(withFinance).toContain(`href="${href}"`);
  expect(withFinance.match(/aria-current="page"/g)).toHaveLength(1);

  const onFinance = renderToStaticMarkup(
    createElement(AppNavigation, { view: "finance", onNavigate: () => {}, features: ["finance"] }),
  );
  expect(onFinance.match(/aria-current="page"/g)).toHaveLength(1);
  expect(onFinance).toMatch(/aria-current="page"[^>]*>Finance</);
});

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

const summary: FinanceSummary = {
  month: "2026-10",
  from: "2026-10-01",
  to: "2026-10-31",
  currencies: ["USD", "EUR", "JPY", "GBP", "CNY", "AUD", "CAD", "CHF", "HKD", "SGD"],
  default_currency: "SGD",
  totals: [
    { currency: "SGD", amount: "45.20", count: 3 },
    { currency: "JPY", amount: "1200", count: 1 },
  ],
  by_category: [
    { currency: "SGD", category: "food", amount: "30.00", count: 2, share: 1 },
    { currency: "SGD", category: "transport", amount: "15.20", count: 1, share: 0.5067 },
    { currency: "JPY", category: "travel", amount: "1200", count: 1, share: 1 },
  ],
  by_day: [
    { currency: "SGD", day: "2026-10-01", amount: "30.00", share: 1 },
    { currency: "SGD", day: "2026-10-02", amount: "15.20", share: 0.5067 },
    { currency: "JPY", day: "2026-10-02", amount: "1200", share: 1 },
  ],
};

const entries: FinanceEntry[] = [
  { id: "e1", date: "2026-10-02", amount: "15.20", currency: "SGD", category: "transport", merchant: "Grab", note: "", source: "chat" },
  { id: "e2", date: "2026-10-01", amount: "30.00", currency: "SGD", category: "food", merchant: "Ya Kun", note: "with tip", source: "photo" },
];

function report(over: Partial<Parameters<typeof FinanceReport>[0]> = {}) {
  return renderToStaticMarkup(
    createElement(FinanceReport, {
      summary,
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

test("the report shows one total per currency, the default first", () => {
  const html = report();
  expect(html).toContain("45.20");
  expect(html).toContain("3 entries");
  expect(html).toContain("1200");
  expect(html).toContain("1 entry");
  expect(html.indexOf("45.20")).toBeLessThan(html.indexOf("1200"));
});

test("category bars are sized by the server's share, with no arithmetic on money", () => {
  const html = report();
  expect(html).toContain('style="width:100.00%"');
  expect(html).toContain('style="width:50.67%"');
  for (const category of ["food", "transport", "travel"]) expect(html).toContain(category);
});

test("the daily strip draws every day of the month in the default currency", () => {
  const html = report();
  const strip = html.match(/data-testid="day-strip"[^>]*>(.*?)<\/ol>/s)?.[1] ?? "";
  expect(strip.match(/<li/g)).toHaveLength(31);
  // The JPY row is another currency's day, not part of this strip.
  expect(strip).not.toContain("1200");
});

test("entries list their details and offer edit and delete", () => {
  const html = report();
  for (const text of ["Grab", "Ya Kun", "2026-10-02", "with tip"]) expect(html).toContain(text);
  expect(html).toContain("photo");
  expect(html.match(/>Edit</g)).toHaveLength(2);
  expect(html.match(/>Delete</g)).toHaveLength(2);
});

test("an entry being edited shows its fields instead of its row", () => {
  const html = report({ editingId: "e2" });
  expect(html).toContain('value="30.00"');
  expect(html).toContain("Save");
  expect(html).toContain("Cancel");
  // The other row is untouched.
  expect(html.match(/>Edit</g)).toHaveLength(1);
});

test("an empty month says so and says how to start", () => {
  const html = report({
    summary: { ...summary, totals: [], by_category: [], by_day: [] },
    entries: [],
    total: 0,
  });
  expect(html).toContain("No spending logged in October 2026");
  expect(html).toContain("Telegram");
});

test("a list that was cut short says how much is not shown", () => {
  expect(report({ total: 250 })).toContain("248 more");
  expect(report()).not.toContain("more not shown");
});

test("the add-entry form offers the supported currencies with the default selected", () => {
  const html = renderToStaticMarkup(
    createElement(AddEntryForm, { currencies: summary.currencies, defaultCurrency: "SGD", busy: false, onCreate: async () => true }),
  );
  const options = [...html.matchAll(/<option[^>]*value="([A-Z]{3})"/g)].map((m) => m[1]);
  expect(options).toEqual(summary.currencies);
  expect(html).toMatch(/<option[^>]*value="SGD"[^>]*selected|<option[^>]*selected[^>]*value="SGD"/);
  expect(html).toContain("Add entry");
  // Categories are suggestions, not an enum: the field is free text.
  expect(html).toContain('<datalist id="finance-categories">');
  expect(html).toContain('value="groceries"');
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
