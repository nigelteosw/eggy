// timeAgo says how long ago a past instant was, for a message's time under it.
// Older than a week it gives the date, since "3 weeks ago" reads worse than it.
export function timeAgo(iso: string, now: number): string {
  const at = Date.parse(iso);
  if (Number.isNaN(at)) return iso;
  const minutes = Math.floor((now - at) / 60000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} ${hours === 1 ? "hour" : "hours"} ago`;
  const days = Math.floor(hours / 24);
  if (days === 1) return "yesterday";
  if (days < 7) return `${days} days ago`;
  return new Date(at).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

// relativeTime says how far away an instant is, in words an owner reads at a
// glance. Anything it cannot read is returned unchanged, so a value that is not
// a time is shown rather than hidden.
export function relativeTime(iso: string, now: number): string {
  const at = Date.parse(iso);
  if (Number.isNaN(at)) return iso;
  const seconds = Math.round((at - now) / 1000);
  if (seconds < 0) return "overdue";
  if (seconds < 60) return "now";
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `in ${minutes} ${minutes === 1 ? "minute" : "minutes"}`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `in ${hours} ${hours === 1 ? "hour" : "hours"}`;
  const days = Math.round(hours / 24);
  if (days === 1) return "tomorrow";
  return `in ${days} days`;
}
