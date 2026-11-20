import { expect, test } from "bun:test";
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";

// Every work-area page sits in PageShell, so its gutters and the height of its
// title row match every other page and the chat header. A page that built
// its own container is how the title came to jump between 28 and 62 px from
// the top as the owner moved around. Traces is the one exception: its browser
// fills the height and scrolls its own panes, so it lays the header row out
// itself, with the same offset.
const OWN_FRAME = ["traces/TracesPage.tsx"];

async function sources(dir: string): Promise<string[]> {
  const entries = await readdir(dir, { withFileTypes: true });
  const files = await Promise.all(entries.map((entry) => (entry.isDirectory() ? sources(join(dir, entry.name)) : [join(dir, entry.name)])));
  return files.flat().filter((file) => file.endsWith(".tsx"));
}

test("every page with a header sits in PageShell", async () => {
  const strays: string[] = [];
  for (const file of await sources(join(import.meta.dir, "../src"))) {
    const name = file.split("/src/")[1];
    if (name.startsWith("components/ui/") || OWN_FRAME.includes(name)) continue;
    const text = await readFile(file, "utf8");
    if (text.includes("<PageHeader") && !text.includes("<PageShell")) strays.push(name);
  }
  expect(strays).toEqual([]);
});

test("no page builds its own centred container", async () => {
  const strays: string[] = [];
  for (const file of await sources(join(import.meta.dir, "../src"))) {
    const name = file.split("/src/")[1];
    // Settings' section pane sits beside its list in a two-pane frame, inside
    // the dialog or the page, so it is a pane rather than a page.
    if (name === "components/ui/page-shell.tsx" || name === "settings/ConfigPage.tsx") continue;
    const text = await readFile(file, "utf8");
    if (/mx-auto flex[^"]*max-w-\[860px\]/.test(text)) strays.push(name);
  }
  expect(strays).toEqual([]);
});
