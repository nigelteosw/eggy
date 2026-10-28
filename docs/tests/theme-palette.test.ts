import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const root = join(import.meta.dir, "..");

// The docs and the panel share one palette; a drift here would make the docs
// look like a different product from the app they describe.
test("the docs use the Eggshell and Cast iron ground colours, not the old green", () => {
  const css = readFileSync(join(root, "src/styles/global.css"), "utf8");
  expect(css).toContain("--page: #151515");
  expect(css).toContain("--page: #F6F1E7");
  expect(css).not.toContain("#b9f7a8");
});

test("the first paint follows the device until the reader chooses", () => {
  const layout = readFileSync(join(root, "src/layouts/DocsLayout.astro"), "utf8");
  expect(layout).toContain("prefers-color-scheme: dark");
  expect(layout).toContain('theme-color" media="(prefers-color-scheme: light)"');
});
