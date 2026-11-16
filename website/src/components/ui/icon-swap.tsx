import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

// IconSwap shows one of two icons in the same cell and blurs between them when
// `on` changes (index.css, .icon-swap). Both stay mounted so the leaving icon
// can animate out; the hidden one is aria-hidden like every icon here.
export function IconSwap({ on, onIcon, offIcon, className }: { on: boolean; onIcon: ReactNode; offIcon: ReactNode; className?: string }) {
  return (
    <span className={cn("icon-swap", className)} aria-hidden="true">
      <span data-off={on ? undefined : ""}>{onIcon}</span>
      <span data-off={on ? "" : undefined}>{offIcon}</span>
    </span>
  );
}
