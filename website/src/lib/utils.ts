import { type ClassValue, clsx } from "clsx";
import { extendTailwindMerge } from "tailwind-merge";

// The panel's type and radius scales replace Tailwind's (tailwind.config.js).
// Without naming the sizes here, tailwind-merge reads text-small as a colour
// and drops it beside text-foreground, and cannot tell two radii conflict.
const twMerge = extendTailwindMerge({
  override: { classGroups: { "font-size": [{ text: ["caption", "small", "body", "field", "title", "display"] }] } },
  extend: { theme: { radius: ["mark", "control", "card"] } },
});

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// The message a thrown value carries, or the fallback when it is not an Error.
// Every card's catch block reads an error this way; the fallback is the one
// part that differs.
export function errorMessage(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}
