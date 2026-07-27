import * as React from "react";
import { cn } from "../../lib/utils";

/**
 * A checkbox rendered as a track-and-thumb switch. Backed by a real
 * <input type="checkbox"> so form semantics, labels and keyboard handling
 * come for free -- only the visuals are custom.
 */
export function Switch({
  checked,
  onCheckedChange,
  label,
  className,
  disabled,
}: {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  label: React.ReactNode;
  className?: string;
  disabled?: boolean;
}) {
  return (
    <label className={cn("group inline-flex cursor-pointer items-center gap-3 text-sm text-foreground", className)}>
      <span className="relative inline-flex h-5 w-9 shrink-0 items-center">
        <input
          type="checkbox"
          checked={checked}
          disabled={disabled}
          onChange={(event) => onCheckedChange(event.target.checked)}
          className="peer sr-only"
        />
        <span
          className={cn(
            "h-5 w-9 rounded-full bg-input transition-colors",
            "peer-checked:bg-primary peer-disabled:opacity-50",
            "peer-focus-visible:ring-2 peer-focus-visible:ring-ring/40 peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-background",
          )}
        />
        <span
          className={cn(
            "pointer-events-none absolute left-0.5 h-4 w-4 rounded-full bg-white shadow-subtle transition-transform",
            checked && "translate-x-4",
          )}
        />
      </span>
      <span className="select-none">{label}</span>
    </label>
  );
}
