// Calendar helpers for the Finance page. Dates are YYYY-MM-DD and months are
// YYYY-MM, handled as plain text and UTC arithmetic: a month has no timezone,
// and the browser's clock is not the owner's (the server decides "this month").
// Nothing here touches money.

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

// Offered as suggestions, never enforced: a category is free text.
export const CATEGORY_SUGGESTIONS = ["food", "groceries", "transport", "shopping", "bills", "entertainment", "health", "travel", "other"];
