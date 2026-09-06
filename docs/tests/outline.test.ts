import { describe, expect, test } from "bun:test";
import { activeHeadingId, type HeadingPosition } from "../src/scripts/outline";

// Positions as the browser reports them: `top` is viewport-relative, and
// `scrollMargin` is the heading's own scroll-margin-top, which is exactly the
// offset the browser leaves above a heading it has jumped to.
function at(id: string, top: number): HeadingPosition {
  return { id, top, scrollMargin: 90 };
}

describe("outline highlighting", () => {
  test("defaults to the first heading before anything is scrolled past", () => {
    expect(
      activeHeadingId([at("get-started", 400), at("build", 900)]),
    ).toBe("get-started");
  });

  // The regression this function exists for. Clicking an outline link scrolls
  // its heading to exactly its scroll-margin, so a rule that only counts
  // headings further up the page highlights the *next* one instead.
  test("a heading jumped to by its own anchor is the active one", () => {
    expect(
      activeHeadingId([
        at("build", -600),
        at("configure", 90),
        at("start", 700),
      ]),
    ).toBe("configure");
  });

  test("sub-pixel scroll positions still count as reached", () => {
    expect(activeHeadingId([at("build", -300), at("configure", 90.4)])).toBe(
      "configure",
    );
  });

  test("the last heading scrolled past wins, not the first one visible", () => {
    expect(
      activeHeadingId([
        at("build", -900),
        at("configure", -400),
        at("start", -20),
        at("what", 300),
      ]),
    ).toBe("start");
  });

  test("an empty outline has no active heading", () => {
    expect(activeHeadingId([])).toBeUndefined();
  });
});
