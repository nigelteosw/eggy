import { expect, test } from "bun:test";
import { isEdgeSwipe, visibleHeight } from "../src/lib/viewport";

// A swipe from the left edge is how a phone user opens a sidebar. It must start
// at the edge, move mostly sideways, and go far enough to be intended.
test("a rightward swipe from the left edge opens the sidebar; anything else does not", () => {
  expect(isEdgeSwipe({ x: 6, y: 300 }, { x: 150, y: 310 })).toBe(true);
  expect(isEdgeSwipe({ x: 120, y: 300 }, { x: 300, y: 300 })).toBe(false);
  expect(isEdgeSwipe({ x: 6, y: 300 }, { x: 40, y: 302 })).toBe(false);
  expect(isEdgeSwipe({ x: 6, y: 300 }, { x: 150, y: 480 })).toBe(false);
});

// When the keyboard opens, the visual viewport shrinks while the layout does
// not. The app is sized to what is visible, so the composer stays above it.
test("the app is sized to the visible viewport, falling back to the layout", () => {
  expect(visibleHeight({ height: 520 }, 844)).toBe(520);
  expect(visibleHeight(undefined, 844)).toBe(844);
});
