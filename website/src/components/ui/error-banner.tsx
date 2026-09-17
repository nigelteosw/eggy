import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

// The red strip a card shows when a request fails. `preformatted` keeps line
// breaks, for a config rejection that quotes the file back with its line.
export function ErrorBanner({
  children,
  className,
  preformatted = false,
}: {
  children: ReactNode;
  className?: string;
  preformatted?: boolean;
}) {
  const base = "rounded-2xl bg-eg-red-tint px-3.5 py-2.5 text-sm text-eg-red-ink";
  if (preformatted) {
    return (
      <pre role="alert" className={cn("overflow-x-auto whitespace-pre-wrap", base, className)}>
        {children}
      </pre>
    );
  }
  return (
    <p role="alert" className={cn(base, className)}>
      {children}
    </p>
  );
}
