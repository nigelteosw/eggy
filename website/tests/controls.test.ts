import { expect, test } from "bun:test";
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";

async function sources(dir: string): Promise<string[]> {
  const entries = await readdir(dir, { withFileTypes: true });
  const files = await Promise.all(entries.map((entry) => (entry.isDirectory() ? sources(join(dir, entry.name)) : [join(dir, entry.name)])));
  return files.flat().filter((file) => file.endsWith(".tsx"));
}

// One select: components/ui/select.tsx. Raw <select>s each styled their own
// way, so the Model picker in Settings had the browser's chevron and a
// different height from the Area picker in Customize.
test("every select is the shared Select", async () => {
  const raw: string[] = [];
  for (const file of await sources(join(import.meta.dir, "../src"))) {
    const name = file.split("/src/")[1];
    if (name === "components/ui/select.tsx") continue;
    if (/<select[\s>]/.test(await readFile(file, "utf8"))) raw.push(name);
  }
  expect(raw).toEqual([]);
});
