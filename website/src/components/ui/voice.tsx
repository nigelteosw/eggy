import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

// Eggy's voice: a soft serif for the few moments the product speaks rather
// than labels -- the home greeting, the sign-in line, and an empty state.
// Never for a control; tests/voice.test.ts holds the list of places.
export function EmptyNote({ children, className }: { children: ReactNode; className?: string }) {
  return <p className={cn("rounded-card bg-neutral-100 px-4 py-5 font-voice text-body text-neutral-700", className)}>{children}</p>;
}

// greeting is the home page's first line, by the hour where the reader is. An
// account first made from a Telegram owner is named by its numeric ID, which is
// not a name to greet anyone by, so the greeting goes without it.
export function greeting(hour: number, name?: string): string {
  const part = hour < 5 ? "Good evening" : hour < 12 ? "Good morning" : hour < 18 ? "Good afternoon" : "Good evening";
  const who = /^\d+$/.test(name?.trim() ?? "") ? "" : name?.trim();
  return who ? `${part}, ${who}.` : `${part}.`;
}
