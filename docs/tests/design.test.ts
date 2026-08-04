import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

// The two properties the compact sidebar design exists to protect are both one
// declaration away from silently regressing, and neither is visible to a route
// or content test. See docs/superpowers/specs/2026-07-31-compact-docs-sidebar-design.md.
const stylesheet = readFileSync(
  join(import.meta.dir, "../src/styles/global.css"),
  "utf8",
);

/**
 * Returns the declaration block of a rule whose selector list contains the
 * given selector exactly. Matching the selector as a whole word is what keeps
 * `.sidebar` from also picking up `.sidebar-nav`.
 */
function declarations(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const rule = new RegExp(
    `(^|[,{}])\\s*${escaped}\\s*(,[^{]*)?\\{([^}]*)\\}`,
    "m",
  );
  const match = stylesheet.match(rule);
  expect(match, `no rule for ${selector} in global.css`).not.toBeNull();
  return match![3];
}

describe("compact sidebar design", () => {
  // The whole point of tightening the navigation scale was that the complete
  // desktop navigation fits without its own scrollbar. A later spacing change
  // that overflowed would most likely be repaired by turning scrolling back on,
  // which reverts the design without touching the spec.
  test("the desktop sidebar does not scroll independently", () => {
    const sidebar = declarations(".sidebar");
    expect(sidebar).toMatch(/overflow-y:\s*visible/);
    expect(sidebar).not.toMatch(/overflow(-[xy])?:\s*(auto|scroll|hidden)/);
    // Sticky beneath the header is the other half of the behavior: without it
    // a non-scrolling sidebar simply leaves the viewport.
    expect(sidebar).toMatch(/position:\s*sticky/);
    expect(sidebar).toMatch(/top:\s*var\(--header-height\)/);
  });

  // The mobile drawer keeps its own scrolling on purpose, because its available
  // height and access pattern are different. Pinned so the rule above is never
  // "fixed" by applying it to both.
  test("the mobile drawer still scrolls", () => {
    expect(declarations(".mobile-drawer")).toMatch(/overflow-y:\s*auto/);
  });
});

describe("brand mark", () => {
  // The inner dot is Eggy yellow in both themes and must not inherit the mint
  // documentation accent. Swapping one variable here is exactly the failure the
  // spec was written to prevent, and it looks deliberate in a diff.
  test("the mark uses the dedicated yellow token, not the accent", () => {
    const mark = declarations(".brand-mark span");
    expect(mark).toMatch(/background:\s*var\(--brand-yellow\)/);
    expect(mark).not.toMatch(/--accent/);
  });

  test("brand yellow is defined once and is not redefined per theme", () => {
    const definitions = stylesheet.match(/--brand-yellow:\s*[^;]+;/g) ?? [];
    expect(definitions).toHaveLength(1);
    // A theme override would be the other way the mark drifts: defined once in
    // :root means light and dark cannot disagree.
    expect(declarations(":root")).toMatch(/--brand-yellow:/);
  });
});
