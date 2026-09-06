export type HeadingPosition = {
  id: string;
  /** Viewport-relative top, as getBoundingClientRect reports it. */
  top: number;
  /** The heading's own scroll-margin-top: the gap the browser leaves above it
   * when it jumps to that anchor. */
  scrollMargin: number;
};

// A heading counts as reached once the page has scrolled to where clicking its
// own outline link would land -- its top at its own scroll-margin. Measuring
// against that offset rather than against a fixed band is what makes a clicked
// link highlight itself: the click leaves the heading exactly at the boundary,
// so anything stricter highlights the following heading instead.
//
// The epsilon absorbs fractional scroll positions, which are ordinary on
// trackpads and high-DPI displays.
const REACHED_EPSILON = 1;

/**
 * Returns the id of the heading the reader is currently under: the last one
 * scrolled past, or the first heading while still above all of them.
 */
export function activeHeadingId(
  positions: readonly HeadingPosition[],
): string | undefined {
  let active = positions[0]?.id;
  for (const position of positions) {
    if (position.top - position.scrollMargin > REACHED_EPSILON) break;
    active = position.id;
  }
  return active;
}
