import { useEffect, useState } from "react";

// isEdgeSwipe recognises the gesture that opens the sidebar on a phone: it
// starts within a finger's width of the left edge, moves right well past a tap,
// and stays mostly horizontal so a scroll is not taken for it.
export function isEdgeSwipe(start: { x: number; y: number }, end: { x: number; y: number }): boolean {
  const dx = end.x - start.x;
  const dy = Math.abs(end.y - start.y);
  return start.x <= 24 && dx >= 60 && dy <= dx / 2;
}

export type Frame = { height: number; top: number; keyboard: boolean };

// A visual viewport this much shorter than the layout is the keyboard, not the
// browser's own toolbar, which is smaller and comes and goes while scrolling.
const KEYBOARD_MIN_HEIGHT = 150;

// visibleFrame is where the app should sit: as tall as the visual viewport
// shows (less when the keyboard is up) and as far down the page as it is.
// iOS opens the keyboard over a layout that keeps its full height and pans
// the visual viewport down to the focused field, so a root sized right but
// left at the top of the page is seen from partway down: the composer near
// the top of the screen and empty page below it. Without a visual viewport
// the layout height is the answer. keyboard says whether the keyboard has
// taken the bottom of the screen, when the page drops the home-indicator
// inset that the keyboard covers.
export function visibleFrame(visual: { height: number; pageTop: number } | undefined, layout: number): Frame {
  if (!visual) return { height: layout, top: 0, keyboard: false };
  return { height: visual.height, top: Math.max(0, visual.pageTop), keyboard: visual.height < layout - KEYBOARD_MIN_HEIGHT };
}

// useVisibleFrame tracks visibleFrame as the keyboard opens, closes and the
// visual viewport pans. Viewport units (dvh) follow none of that on iOS, so
// the root is placed in pixels from here instead.
export function useVisibleFrame(): Frame | undefined {
  const [frame, setFrame] = useState<Frame | undefined>(undefined);
  useEffect(() => {
    const visual = window.visualViewport;
    if (!visual) return;
    const update = () =>
      setFrame((current) => {
        const next = visibleFrame(visual, window.innerHeight);
        return current && current.height === next.height && current.top === next.top && current.keyboard === next.keyboard ? current : next;
      });
    update();
    visual.addEventListener("resize", update);
    visual.addEventListener("scroll", update);
    return () => {
      visual.removeEventListener("resize", update);
      visual.removeEventListener("scroll", update);
    };
  }, []);
  return frame;
}

// useWide reports whether the window is at Tailwind's md breakpoint or wider,
// where a popup has room around it. It follows the window as it is resized.
export function useWide(): boolean {
  const query = "(min-width: 768px)";
  const [wide, setWide] = useState(() => typeof window === "undefined" || window.matchMedia(query).matches);
  useEffect(() => {
    const list = window.matchMedia(query);
    const update = () => setWide(list.matches);
    update();
    list.addEventListener("change", update);
    return () => list.removeEventListener("change", update);
  }, []);
  return wide;
}
