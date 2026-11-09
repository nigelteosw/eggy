import { expect, test } from "bun:test";
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";

// Status colour is three kitchen colours -- tomato for destructive and failed,
// chive for done, yolk-soft and yolk-ink for what needs the owner -- read from
// tokens that change with the theme. A raw palette class (text-red-300,
// bg-amber-950/30) is fixed to one mode and drifts from the rest; the retired
// eg-* and destructive names were the second and third reds.
async function sources(dir: string): Promise<string[]> {
  const entries = await readdir(dir, { withFileTypes: true });
  const files = await Promise.all(
    entries.map((entry) => {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) return sources(path);
      return /\.(tsx?|css)$/.test(entry.name) ? [path] : [];
    }),
  );
  return files.flat();
}

test("no source uses a raw palette colour or a retired status colour name", async () => {
  const palette = "slate|gray|zinc|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose";
  const banned = new RegExp(
    `(?<![\\w-])(?:[a-z-]+:)*(?:text|bg|border|ring|fill|stroke|from|to|via|outline|decoration|accent|caret|shadow)-(?:(?:${palette})-\\d{2,3}|eg-[\\w-]+|destructive(?:-foreground)?)(?:/\\d+)?(?![\\w-])`,
    "g",
  );
  const offenders: string[] = [];
  for (const file of await sources(join(import.meta.dir, "../src"))) {
    const text = await readFile(file, "utf8");
    for (const match of text.matchAll(banned)) offenders.push(`${file.split("/src/")[1]}: ${match[0]}`);
  }
  expect(offenders).toEqual([]);
});
