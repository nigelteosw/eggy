import { cn } from "../../lib/utils";

// SkeletonRows holds a list's place while it loads: rows about the height of
// the real ones, so nothing below jumps when the data lands, and never an
// empty state ("No chats yet") said before anything was actually looked at.
// The shimmer stops under reduced motion; the label is what a screen reader
// hears instead.
export function SkeletonRows({ rows = 3, className, rowClassName, label = "Loading" }: { rows?: number; className?: string; rowClassName?: string; label?: string }) {
  return (
    <div role="status" aria-label={label} className={cn("flex flex-col gap-2", className)}>
      {Array.from({ length: rows }, (_, index) => (
        <span
          key={index}
          aria-hidden="true"
          className={cn("block h-9 rounded-control bg-neutral-200/70 motion-safe:animate-pulse", rowClassName)}
          // Each row a little shorter, so the block reads as text, not bars.
          style={{ width: `${100 - index * 12}%` }}
        />
      ))}
    </div>
  );
}
