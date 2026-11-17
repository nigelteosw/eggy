import type { WeatherOutlook, WeatherPeriod, WeatherReport } from "../lib/api";

// The weather is Singapore's, so it is read in Singapore time wherever the
// person opening Eggy happens to be.
const ZONE = "Asia/Singapore";

export type Glyph = "sun" | "moon" | "partly-day" | "partly-night" | "cloud" | "rain" | "storm" | "haze" | "wind";
export type Sky = "day" | "night" | "overcast" | "rain" | "storm";

// glyphFor reads NEA's forecast words ("Partly Cloudy (Night)", "Heavy Thundery
// Showers with Gusty Winds") into one picture. Order matters: thunder beats
// rain beats wind, because "showers with gusty winds" is a wet day first.
export function glyphFor(forecast: string | undefined, night: boolean): Glyph {
  const text = (forecast ?? "").toLowerCase();
  const isNight = text.includes("(night)") || (night && !text.includes("(day)"));
  if (text.includes("thunder")) return "storm";
  if (/rain|shower|drizzle/.test(text)) return "rain";
  if (/haz|mist|fog|smok/.test(text)) return "haze";
  if (text.includes("wind")) return "wind";
  if (text.includes("partly")) return isNight ? "partly-night" : "partly-day";
  if (/cloud|overcast/.test(text)) return "cloud";
  if (/fair|sun|warm|clear/.test(text)) return isNight ? "moon" : "sun";
  return isNight ? "partly-night" : "partly-day";
}

// shown drops NEA's "(Day)" and "(Night)": the picture already says which,
// and "Partly Cloudy (Day)" in the middle of the afternoon reads as noise.
export function shown(forecast: string | undefined): string {
  return (forecast ?? "").replace(/\s*\((?:day|night)\)\s*$/i, "");
}

export function skyFor(glyph: Glyph, night: boolean): Sky {
  if (glyph === "storm") return "storm";
  if (glyph === "rain") return "rain";
  if (night) return "night";
  if (glyph === "cloud" || glyph === "haze") return "overcast";
  return "day";
}

export function isWet(forecast: string | undefined): boolean {
  const glyph = glyphFor(forecast, false);
  return glyph === "rain" || glyph === "storm";
}

function hourIn(date: Date): number {
  return Number(new Intl.DateTimeFormat("en-GB", { hour: "numeric", hourCycle: "h23", timeZone: ZONE }).format(date));
}

// Night is after seven and before seven, about when it is dark on the equator
// all year round.
export function isNightAt(date: Date): boolean {
  const hour = hourIn(date);
  return hour < 7 || hour >= 19;
}

export function periodIsNight(period: WeatherPeriod): boolean {
  const hour = hourIn(new Date(period.start));
  return /night/i.test(period.label) || hour >= 18 || hour < 6;
}

export function dayLine(date: Date): string {
  return new Intl.DateTimeFormat("en-GB", { weekday: "long", day: "numeric", month: "short", timeZone: ZONE }).format(date);
}

export function clockTime(date: Date): string {
  return new Intl.DateTimeFormat("en-GB", { hour: "numeric", minute: "2-digit", hour12: true, timeZone: ZONE })
    .format(date)
    .replace(" ", " ");
}

function dateKey(date: Date): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: ZONE }).format(date);
}

// outlookLabel says "Tomorrow" for tomorrow and the weekday otherwise.
export function outlookLabel(day: WeatherOutlook, now: Date): string {
  const tomorrow = dateKey(new Date(now.getTime() + 24 * 3600 * 1000));
  return day.date === tomorrow ? "Tomorrow" : day.day;
}

// umbrellaNote is the one line Home says when rain is coming: now, if it is
// wet now, else the first wet block ahead today or tonight. Nothing when dry,
// so the note means something when it appears.
export function umbrellaNote(report: WeatherReport): string | null {
  const now = report.now;
  if (now?.forecast && isWet(now.forecast)) {
    return `${shown(now.forecast)} in ${report.area} until ${untilOf(now.valid_text)}.`.replace(" until .", ".");
  }
  const wet = report.periods?.find((period) => isWet(period.forecast));
  if (!wet) return null;
  return `${shown(wet.forecast)} ${wet.label === "Now" ? "now" : wet.label.toLowerCase()}.`;
}

// "10.30 pm to 12.30 am" -> "12.30 am".
function untilOf(validText: string | undefined): string {
  const parts = (validText ?? "").split(/\s+to\s+/);
  return parts.length === 2 ? parts[1] : "";
}

// rangeBar places one day's low-high on the scale the whole outlook spans, as
// fractions of its width, so the days can be compared at a glance.
export function rangeBar(day: { low: number; high: number }, days: { low: number; high: number }[]): { left: number; width: number } {
  const floor = Math.min(...days.map((d) => d.low));
  const ceiling = Math.max(...days.map((d) => d.high));
  const span = Math.max(ceiling - floor, 1);
  const left = (day.low - floor) / span;
  return { left, width: Math.max((day.high - day.low) / span, 0.06) };
}

export function windText(low: number, high: number, direction?: string): string {
  return `${low}–${high} km/h${direction && direction !== "VARIABLE" ? ` ${direction}` : ""}`;
}
