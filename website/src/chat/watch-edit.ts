// Editing WATCH.md one item at a time, for Home's Watching list. The document
// is the owner's and Eggy's both -- a check-in annotates what it has already
// reported -- so an edit rewrites only the item lines it touched and leaves
// headings, blank lines and everything else exactly where they were.

// An item is one line of the document. A new item, not yet saved, has no line.
export type WatchItem = { line: number | null; text: string };

const BULLET = /^\s*[-*]\s+/;

// watchItems lists the items: blank lines and headings are structure, not
// items. The bullet is taken off for display and put back on save.
export function watchItems(text: string): WatchItem[] {
  return text.split("\n").flatMap((raw, line) => {
    const trimmed = raw.trim();
    if (trimmed === "" || trimmed.startsWith("#")) return [];
    return [{ line, text: trimmed.replace(BULLET, "") }];
  });
}

// rewriteWatch applies an edited list back onto the document it came from. An
// item line missing from the list, or cleared to nothing, is deleted; an edited
// one keeps its own bullet; a new one is appended as "- text".
export function rewriteWatch(original: string, items: WatchItem[]): string {
  const edits = new Map(items.filter((item) => item.line !== null).map((item) => [item.line as number, item.text.trim()]));
  const itemLines = new Set(watchItems(original).map((item) => item.line));
  const lines = original === "" ? [] : original.split("\n");
  const kept = lines.flatMap((raw, line) => {
    if (!itemLines.has(line)) return [raw];
    const text = edits.get(line);
    if (!text) return [];
    return [(raw.match(BULLET)?.[0] ?? "") + text];
  });
  const added = items.filter((item) => item.line === null && item.text.trim() !== "").map((item) => `- ${item.text.trim()}`);
  if (added.length === 0) return kept.join("\n");
  // Keep the file's trailing newline after the new lines rather than before them.
  if (kept.length > 0 && kept[kept.length - 1] === "") kept.pop();
  return [...kept, ...added, ""].join("\n");
}
