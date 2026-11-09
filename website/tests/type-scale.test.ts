import { expect, test } from "bun:test";
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import { cn } from "../src/lib/utils";
import config from "../tailwind.config.js";

// The panel has five text sizes and a sixth for form fields, which must stay at
// 16 px on a phone or iOS zooms on focus. Tailwind's own scale is not
// generated, so a stray text-sm would render unstyled; this test catches it,
// and any arbitrary size, before that happens.
const SIZES = ["caption", "small", "body", "title", "display", "field"];

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

test("the type scale is exactly the six named sizes", () => {
  expect(Object.keys(config.theme.fontSize).sort()).toEqual([...SIZES].sort());
});

test("no source uses Tailwind's default sizes or an arbitrary text size", async () => {
  // An em size (inline code at 0.9em) scales with the token around it, so it
  // is not a second scale; px and rem are.
  const banned = /(?<![\w-])(?:[a-z]+:)*text-(?:xs|sm|base|lg|[2-9]?xl|\[[\d.]+(?:px|rem)\])(?![\w-])/g;
  const offenders: string[] = [];
  for (const file of await sources(join(import.meta.dir, "../src"))) {
    const text = await readFile(file, "utf8");
    for (const match of text.matchAll(banned)) offenders.push(`${file.split("/src/")[1]}: ${match[0]}`);
  }
  expect(offenders).toEqual([]);
});

test("cn treats the size names as sizes, so a colour beside one does not drop it", () => {
  expect(cn("text-small text-foreground")).toBe("text-small text-foreground");
  expect(cn("text-small", "text-body")).toBe("text-body");
});
