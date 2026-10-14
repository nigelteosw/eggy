// Calendar, category and presentation helpers for the Finance page. Dates are
// YYYY-MM-DD and months are YYYY-MM, handled as plain text and UTC arithmetic: a
// month has no timezone, and the browser's clock is not the owner's (the server
// decides "this month"). Nothing here adds, compares or converts money: amounts
// arrive formatted, and shares and percentages arrive computed.

import type { FinanceEntry, FinanceSummary } from "../../api";

export function shiftMonth(month: string, delta: number): string {
  const [year, number] = month.split("-").map(Number);
  const index = year * 12 + (number - 1) + delta;
  const shiftedYear = Math.floor(index / 12);
  const shiftedMonth = (index % 12) + 1;
  return `${String(shiftedYear).padStart(4, "0")}-${String(shiftedMonth).padStart(2, "0")}`;
}

export function monthLabel(month: string): string {
  const [year, number] = month.split("-").map(Number);
  return new Intl.DateTimeFormat("en-US", { month: "long", year: "numeric", timeZone: "UTC" }).format(new Date(Date.UTC(year, number - 1, 1)));
}

// The short form the trend chart labels its columns with.
export function monthShort(month: string): string {
  const [year, number] = month.split("-").map(Number);
  return new Intl.DateTimeFormat("en-US", { month: "short", timeZone: "UTC" }).format(new Date(Date.UTC(year, number - 1, 1)));
}

export function dayLabel(day: string): string {
  const [year, month, date] = day.split("-").map(Number);
  return new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", timeZone: "UTC" }).format(new Date(Date.UTC(year, month - 1, date)));
}

// Every day from..to inclusive, so a strip shows the quiet days too.
export function daysOfRange(from: string, to: string): string[] {
  const [fy, fm, fd] = from.split("-").map(Number);
  const end = Date.parse(`${to}T00:00:00Z`);
  const days: string[] = [];
  for (let at = Date.UTC(fy, fm - 1, fd); at <= end; at += 86_400_000) {
    days.push(new Date(at).toISOString().slice(0, 10));
  }
  return days;
}

// The kinds an entry can take, and what each is called on the page. The stored
// word stays the same; the page says what the owner did with the money.
export const KINDS = ["expense", "income", "invest", "divest"] as const;
export type Kind = (typeof KINDS)[number];

export const KIND_LABEL: Record<Kind, string> = {
  expense: "Spent",
  income: "Received",
  invest: "Invested",
  divest: "Withdrawn",
};

// Money leaving the owner's pocket reads as minus, money arriving as plus.
// Investing is cash out, withdrawing is cash in, so the sign follows the cash.
export function isCashIn(kind: string): boolean {
  return kind === "income" || kind === "divest";
}

// The colours a category can take, by the name the server gives them. Each is a
// mid-tone that reads on both the light and the dark card, so one value serves
// both themes. An unknown name falls back to a neutral slate rather than failing.
const CATEGORY_HEX: Record<string, string> = {
  sky: "#0ea5e9",
  emerald: "#10b981",
  amber: "#f59e0b",
  rose: "#f43f5e",
  violet: "#8b5cf6",
  orange: "#f97316",
  teal: "#14b8a6",
  indigo: "#6366f1",
  lime: "#84cc16",
  slate: "#64748b",
};

export function categoryHex(color: string): string {
  return CATEGORY_HEX[color] ?? CATEGORY_HEX.slate;
}

// A money amount with its currency, the minus sign placed before the code so a
// net outflow reads "-SGD 45.20" rather than "SGD -45.20".
export function money(currency: string, amount: string): string {
  return amount.startsWith("-") ? `-${currency} ${amount.slice(1)}` : `${currency} ${amount}`;
}

// The sign an entry's amount carries on the page: plus for cash in, minus for cash out.
export function signedAmount(kind: string, currency: string, amount: string): string {
  return `${isCashIn(kind) ? "+" : "−"}${currency} ${amount}`;
}

export type Movement = { arrow: "up" | "down" | "flat"; text: string; tone: "good" | "bad" | "neutral" };

// How far a figure moved against last month, as the page words it. goodWhenUp
// says whether a rise is welcome (money in, savings) or not (spending). A figure
// with no earlier month to compare with has no movement, and says nothing.
export function movement(percent: number | null, goodWhenUp: boolean | null): Movement | null {
  if (percent === null) return null;
  const size = Math.abs(percent);
  const text = `${Number.isInteger(size) ? size : size.toFixed(1)}%`;
  if (percent === 0) return { arrow: "flat", text: "0%", tone: "neutral" };
  const up = percent > 0;
  const tone = goodWhenUp === null ? "neutral" : up === goodWhenUp ? "good" : "bad";
  return { arrow: up ? "up" : "down", text, tone };
}

// A share as a CSS percentage, for a bar's width or height. The server sends
// shares to four places, so two places here keep the arithmetic to presentation.
export function percentOf(share: number): string {
  return `${(share * 100).toFixed(2)}%`;
}

// Which currency the page draws its figures in. The owner's pick stands while it
// has entries this month; otherwise the default, then whichever has any.
export function activeCurrency(summary: FinanceSummary, requested: string | null): string {
  const codes = summary.flows.map((flow) => flow.currency);
  if (requested && codes.includes(requested)) return requested;
  if (codes.includes(summary.default_currency)) return summary.default_currency;
  return codes[0] ?? summary.default_currency;
}

// The totals and charts the page shows are in one currency at a time. These
// keep each list to that currency, in the order the server gave.
export const forCurrency = <T extends { currency: string }>(rows: T[], currency: string): T[] => rows.filter((row) => row.currency === currency);

// The words under a count of entries.
export const entriesWord = (count: number) => `${count} ${count === 1 ? "entry" : "entries"}`;

// Offered as suggestions, never enforced: a category is free text.
export const CATEGORY_SUGGESTIONS = ["food", "groceries", "transport", "shopping", "bills", "entertainment", "health", "travel", "other"];

// The row's category as a name, merchant first when there is one.
export const entryTitle = (entry: FinanceEntry) => entry.merchant || entry.category;
