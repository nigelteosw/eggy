import type { ReactNode } from "react";
import { cn } from "../../lib/utils";
import { MenuIcon } from "./icons";

// PageHeader is the heading of a work-area page. On a phone the button that
// opens the rail shares its row with the title and the page's one action, so
// the work starts at the top of the screen rather than under a bar of its own.
// At md and up the rail has its own reopen control, so the button is hidden.
export function PageHeader({
  title,
  description,
  action,
  onOpenSidebar,
  className,
}: {
  title: string;
  description?: ReactNode;
  action?: ReactNode;
  onOpenSidebar?: () => void;
  className?: string;
}) {
  return (
    <header className={cn("flex flex-col gap-1.5", className)}>
      <div className="flex min-h-11 items-center gap-2">
        {onOpenSidebar && (
          <button
            type="button"
            onClick={onOpenSidebar}
            aria-label="Open sidebar"
            title="Open sidebar"
            className="-ml-2 flex h-11 w-11 shrink-0 items-center justify-center rounded-xl text-neutral-700 transition-colors hover:bg-neutral-100 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 md:hidden"
          >
            <MenuIcon />
          </button>
        )}
        <h1 className="min-w-0 flex-1 truncate text-xl font-semibold tracking-tight">{title}</h1>
        {action}
      </div>
      {description && <p className="max-w-2xl text-sm leading-6 text-neutral-700">{description}</p>}
    </header>
  );
}
