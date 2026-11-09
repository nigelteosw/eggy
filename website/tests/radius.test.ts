import { expect, test } from "bun:test";
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import { cn } from "../src/lib/utils";
import config from "../tailwind.config.js";

// Controls are 10 px, cards and dialogs 16 px, pills full; mark is 4 px for
// glyph-sized things -- inline code, a legend swatch -- that a 10 px corner
// would turn into a circle. Tailwind's own radius scale is not generated, so a
// stray rounded-xl would render square; this test catches it first.
const RADII = ["none", "mark", "control", "card", "full"];

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

test("the radius scale is exactly the named radii", () => {
  expect(Object.keys(config.theme.borderRadius).sort()).toEqual([...RADII].sort());
});

test("every rounded class names one of the radii", async () => {
  const rounded = /(?<![\w-])rounded(?:-(?:t|b|l|r|s|e|tl|tr|bl|br|ss|se|es|ee))?(?:-([\w[\]%/._]+))?(?![\w-])/g;
  const offenders: string[] = [];
  for (const file of await sources(join(import.meta.dir, "../src"))) {
    // Comment lines are prose, where "rounded" is just a word.
    const text = (await readFile(file, "utf8"))
      .split("\n")
      .filter((line) => !/^\s*(\/\/|\*|\/\*|\{\/\*)/.test(line))
      .join("\n");
    for (const match of text.matchAll(rounded)) {
      const size = match[1];
      // The egg mark's outline is its own shape, not a corner radius.
      if (size?.startsWith("[") && file.endsWith("brand.tsx")) continue;
      if (size === undefined || !RADII.includes(size)) offenders.push(`${file.split("/src/")[1]}: ${match[0]}`);
    }
  }
  expect(offenders).toEqual([]);
});

test("cn treats the radius names as radii", () => {
  expect(cn("rounded-control", "rounded-card")).toBe("rounded-card");
});
