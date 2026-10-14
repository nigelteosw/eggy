import { expect, test } from "bun:test";
import { activeCurrency, categoryHex, forCurrency, money, movement, monthShort, percentOf, signedAmount, isCashIn } from "../../../src/extensions/finance/finance";
import type { FinanceSummary } from "../../../src/api";

test("money reads with its currency, and a negative sign goes before the code", () => {
  expect(money("SGD", "654.80")).toBe("SGD 654.80");
  expect(money("JPY", "-1200")).toBe("-JPY 1200");
});

test("cash in is plus and cash out is minus, so investing reads as money going out", () => {
  expect(isCashIn("income")).toBe(true);
  expect(isCashIn("divest")).toBe(true);
  expect(isCashIn("expense")).toBe(false);
  expect(isCashIn("invest")).toBe(false);
  expect(signedAmount("expense", "SGD", "15.20")).toBe("−SGD 15.20");
  expect(signedAmount("income", "SGD", "900.00")).toBe("+SGD 900.00");
  expect(signedAmount("invest", "SGD", "200.00")).toBe("−SGD 200.00");
});

test("a rise in spending is bad and a rise in money in is good", () => {
  expect(movement(50.7, false)).toEqual({ arrow: "up", text: "50.7%", tone: "bad" });
  expect(movement(-12, false)).toEqual({ arrow: "down", text: "12%", tone: "good" });
  expect(movement(8, true)).toEqual({ arrow: "up", text: "8%", tone: "good" });
  expect(movement(8, null)).toEqual({ arrow: "up", text: "8%", tone: "neutral" });
  expect(movement(0, true)).toEqual({ arrow: "flat", text: "0%", tone: "neutral" });
});

test("a figure with nothing to compare against has no movement to show", () => {
  expect(movement(null, true)).toBeNull();
});

test("shares become CSS percentages without rounding them further", () => {
  expect(percentOf(0.6637)).toBe("66.37%");
  expect(percentOf(1)).toBe("100.00%");
  expect(percentOf(0)).toBe("0.00%");
});

test("an unknown colour name falls back to a neutral rather than drawing nothing", () => {
  expect(categoryHex("rose")).toBe("#f43f5e");
  expect(categoryHex("not-a-colour")).toBe(categoryHex("slate"));
});

test("months read in short form for the trend", () => {
  expect(monthShort("2026-09")).toBe("Sep");
  expect(monthShort("2027-01")).toBe("Jan");
});

const base = {
  month: "2026-10",
  from: "2026-10-01",
  to: "2026-10-31",
  currencies: ["SGD", "JPY"],
  palette: [],
  flows: [],
  trend: [],
  spending: [],
  days: [],
  categories: [],
  insights: [],
} satisfies Partial<FinanceSummary>;

test("the page draws the requested currency when it has activity, else the default, else the first", () => {
  const flows = [
    { currency: "JPY", inflow: "0", outflow: "1200", invested: "0", net: "-1200", net_invested: "0", count: 1, average_day: "40", average_share: 1, savings_rate: null, change: { inflow: null, outflow: null, invested: null, net: null } },
    { currency: "SGD", inflow: "0", outflow: "5", invested: "0", net: "-5", net_invested: "0", count: 1, average_day: "1", average_share: 1, savings_rate: null, change: { inflow: null, outflow: null, invested: null, net: null } },
  ];
  const summary = { ...base, default_currency: "SGD", flows } as FinanceSummary;
  expect(activeCurrency(summary, "JPY")).toBe("JPY");
  expect(activeCurrency(summary, "USD")).toBe("SGD");
  expect(activeCurrency({ ...summary, flows: [flows[0]] }, null)).toBe("JPY");
  expect(activeCurrency({ ...summary, flows: [] }, null)).toBe("SGD");
});

test("rows are kept to the currency they belong to, in the order given", () => {
  const rows = [
    { currency: "SGD", n: 1 },
    { currency: "JPY", n: 2 },
    { currency: "SGD", n: 3 },
  ];
  expect(forCurrency(rows, "SGD").map((r) => r.n)).toEqual([1, 3]);
});
