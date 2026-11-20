// The last answer to each read the panel makes, kept in memory so a page
// opened a second time draws what it showed before at once and refreshes it
// in the background, instead of "Loading…" on every visit. A page reads it for
// its first render (recall) and writes it when an answer lands (remember); the
// page's own fetch still runs every time, so what is shown is never older than
// the moment it took to arrive.
//
// Memory only, never browser storage: what is kept here is one account's
// conversations, watch list and approvals, and a shared iPad or a browser
// someone else signs into later must not open with the last person's data on
// screen. lib/api.ts forgets everything when the session ends, and after every
// write, so a change made on one page is never contradicted by another page's
// older copy. Theme and look are cached separately, in lib/theme.ts, because
// they must paint before the app runs.

const entries = new Map<string, unknown>();

export function recall<T>(key: string): T | undefined {
  return entries.get(key) as T | undefined;
}

export function remember<T>(key: string, value: T): T {
  entries.set(key, value);
  return value;
}

export function forgetAll(): void {
  entries.clear();
}

// The keys the panel uses, in one place so two pages reading the same data
// share one entry rather than each keeping its own.
export const CACHE = {
  threads: "threads",
  watch: "watch",
  schedules: "schedules",
  agent: "agent",
  approvals: "approvals",
  weather: "weather",
  history: (threadId: string) => `history:${threadId}`,
} as const;
