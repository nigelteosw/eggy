import { expect, test } from "bun:test";
import { rewriteWatch, watchItems } from "../src/chat/watch-edit";

const doc = "# Watch\n\n- Emails from real people\n* Mooncakes: 6 Oct deadline\nAunt camera reminder: done\n";

// Headings and blank lines are structure, so Home lists only the items, with
// the bullet taken off.
test("items are the non-blank, non-heading lines without their bullets", () => {
  expect(watchItems(doc)).toEqual([
    { line: 2, text: "Emails from real people" },
    { line: 3, text: "Mooncakes: 6 Oct deadline" },
    { line: 4, text: "Aunt camera reminder: done" },
  ]);
});

// Editing on Home rewrites only the lines it touched, so the headings and the
// notes Eggy leaves in WATCH.md survive an edit made from the list.
test("an edit keeps the line's own bullet, a delete drops the line, and the rest is untouched", () => {
  const items = watchItems(doc);
  const edited = [{ ...items[0], text: "Emails from real people older than a day" }, items[1]];
  expect(rewriteWatch(doc, edited)).toBe("# Watch\n\n- Emails from real people older than a day\n* Mooncakes: 6 Oct deadline\n");
});

test("an item cleared to nothing is a delete", () => {
  const items = watchItems(doc).map((item) => (item.line === 3 ? { ...item, text: "  " } : item));
  expect(rewriteWatch(doc, items)).toBe("# Watch\n\n- Emails from real people\nAunt camera reminder: done\n");
});

test("a new item is appended as a bullet, before the trailing newline", () => {
  const items = [...watchItems(doc), { line: null, text: "Flight check-in opens" }, { line: null, text: "" }];
  expect(rewriteWatch(doc, items)).toBe(doc + "- Flight check-in opens\n");
  expect(rewriteWatch("", [{ line: null, text: "First thing" }])).toBe("- First thing\n");
});
