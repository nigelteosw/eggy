import { useEffect, useState } from "react";

// isEdgeSwipe recognises the gesture that opens the sidebar on a phone: it
// starts within a finger's width of the left edge, moves right well past a tap,
// and stays mostly horizontal so a scroll is not taken for it.
export function isEdgeSwipe(start: { x: number; y: number }, end: { x: number; y: number }): boolean {
  const dx = end.x - start.x;
  const dy = Math.abs(end.y - start.y);
  return start.x <= 24 && dx >= 60 && dy <= dx / 2;
}

// visibleHeight is the height the app should fill: what the visual viewport
// shows (less when the keyboard is up), or the layout height when the browser
// reports no visual viewport.
export function visibleHeight(visual: { height: number } | undefined, layout: number): number {
  return visual ? visual.height : layout;
}

// useVisibleHeight tracks visibleHeight as the keyboard opens and closes. The
// viewport units (dvh) do not follow the iOS keyboard, so the root is sized in
// pixels from here instead.
export function useVisibleHeight(): number | undefined {
  const [height, setHeight] = useState<number | undefined>(undefined);
  useEffect(() => {
    const visual = window.visualViewport;
    if (!visual) return;
    const update = () => setHeight(visibleHeight({ height: visual.height }, window.innerHeight));
    update();
    visual.addEventListener("resize", update);
    return () => visual.removeEventListener("resize", update);
  }, []);
  return height;
}
