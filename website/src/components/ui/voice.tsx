import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

// Eggy's voice: a soft serif for the few moments the product speaks rather
// than labels -- the home greeting, the sign-in line, and an empty state.
// Never for a control; tests/voice.test.ts holds the list of places.
export function EmptyNote({ children, className }: { children: ReactNode; className?: string }) {
  return <p className={cn("rounded-card bg-neutral-100 px-4 py-5 font-voice text-body text-neutral-700", className)}>{children}</p>;
}

// greeting is the home page's first line, by the hour where the reader is.
export function greeting(hour: number, name?: string): string {
  const part = hour < 5 ? "Good evening" : hour < 12 ? "Good morning" : hour < 18 ? "Good afternoon" : "Good evening";
  const who = name?.trim();
  return who ? `${part}, ${who}.` : `${part}.`;
}
