import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

// A finger needs about 44 px to hit a control reliably. The rule only applies
// where the primary pointer is a finger, so desktop density is unchanged.
test("controls are at least 44 px tall where the pointer is a finger", () => {
  const css = readFileSync(join(import.meta.dir, "../src/app/index.css"), "utf8");
  expect(css).toContain("@media (pointer: coarse)");
  const rule = css.slice(css.indexOf("@media (pointer: coarse)"));
  expect(rule).toContain("min-height: 2.75rem");
  expect(rule).toContain("button");
});
