import { expect, test } from "bun:test";
import { isEdgeSwipe, visibleFrame } from "../src/lib/viewport";

// A swipe from the left edge is how a phone user opens a sidebar. It must start
// at the edge, move mostly sideways, and go far enough to be intended.
test("a rightward swipe from the left edge opens the sidebar; anything else does not", () => {
  expect(isEdgeSwipe({ x: 6, y: 300 }, { x: 150, y: 310 })).toBe(true);
  expect(isEdgeSwipe({ x: 120, y: 300 }, { x: 300, y: 300 })).toBe(false);
  expect(isEdgeSwipe({ x: 6, y: 300 }, { x: 40, y: 302 })).toBe(false);
  expect(isEdgeSwipe({ x: 6, y: 300 }, { x: 150, y: 480 })).toBe(false);
});

// When the keyboard opens, the visual viewport shrinks while the layout does
// not, and iOS also pans it down the page to show the focused field. The app
// is sized to what is visible and moved to where it is, so the composer sits
// on the keyboard rather than near the top of the screen with empty page
// below it.
test("the app fills the visible viewport where it is, falling back to the layout", () => {
  expect(visibleFrame({ height: 520, pageTop: 0 }, 844)).toEqual({ height: 520, top: 0 });
  expect(visibleFrame({ height: 520, pageTop: 300 }, 844)).toEqual({ height: 520, top: 300 });
  // An overscroll bounce reports a negative offset for a moment; the app
  // does not follow it above the page.
  expect(visibleFrame({ height: 520, pageTop: -40 }, 844)).toEqual({ height: 520, top: 0 });
  expect(visibleFrame(undefined, 844)).toEqual({ height: 844, top: 0 });
});
