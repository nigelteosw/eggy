// How often Eggy checks in, as choices a person picks rather than a duration
// they type. The server stores a Go duration ("3h0m0s"); these helpers turn it
// into minutes and back, and name a cadence the way the picker shows it.

export const CADENCES: { minutes: number; label: string }[] = [
  { minutes: 0, label: "Off" },
  { minutes: 30, label: "Every 30 minutes" },
  { minutes: 60, label: "Every hour" },
  { minutes: 120, label: "Every 2 hours" },
  { minutes: 180, label: "Every 3 hours" },
  { minutes: 360, label: "Every 6 hours" },
  { minutes: 720, label: "Every 12 hours" },
  { minutes: 1440, label: "Once a day" },
];

// minutesOf reads the interval the server reports: a Go duration such as
// "1h30m0s", or "off". Anything it cannot read is null, and the picker then
// keeps the stored value rather than guessing.
export function minutesOf(interval: string): number | null {
  const value = interval.trim();
  if (value === "" || value === "off") return 0;
  const match = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+(?:\.\d+)?)s)?$/.exec(value);
  if (!match || match[0] === "") return null;
  return Number(match[1] ?? 0) * 60 + Number(match[2] ?? 0) + Math.round(Number(match[3] ?? 0) / 60);
}

// durationOf is what the server's save takes: blank for off, else minutes.
export function durationOf(minutes: number): string {
  return minutes > 0 ? `${minutes}m` : "";
}

export function cadenceLabel(minutes: number): string {
  const preset = CADENCES.find((cadence) => cadence.minutes === minutes);
  if (preset) return preset.label;
  if (minutes % 60 === 0) return `Every ${minutes / 60} hours`;
  if (minutes > 60) return `Every ${Math.floor(minutes / 60)} h ${minutes % 60} min`;
  return `Every ${minutes} minutes`;
}

// windowOf reads "08:00-22:00" or "any hour" into the two time fields.
export function windowOf(window: string): { start: string; end: string } | null {
  const match = /^(\d{1,2}:\d{2})-(\d{1,2}:\d{2})$/.exec(window.trim());
  if (!match) return null;
  const pad = (time: string) => time.padStart(5, "0");
  return { start: pad(match[1]), end: pad(match[2]) };
}
