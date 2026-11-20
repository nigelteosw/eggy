import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

// PageShell is the one frame every work-area page sits in: its own scroller,
// the same side gutters as the chat, and a top offset that puts a
// PageHeader's 44 px title row on the same line as the chat header and the
// rail's logo (both 56 px rows), so moving between pages never makes the
// title jump. Two widths: reading, for forms and lists, and wide, for Home,
// which lays out side by side on a large screen.
//
// An extension window passes as="div": it already sits inside the app's main.
export function PageShell({
  children,
  width = "reading",
  as: Tag = "main",
  className,
}: {
  children: ReactNode;
  width?: "reading" | "wide";
  as?: "main" | "div";
  className?: string;
}) {
  return (
    <Tag className="app-canvas scrollbar-slim h-full min-h-0 min-w-0 overflow-y-auto">
      <div
        className={cn(
          "mx-auto flex w-full max-w-[860px] flex-col gap-6 px-4 pb-11 pt-1.5 sm:px-8",
          width === "wide" && "xl:max-w-[1240px]",
          className,
        )}
      >
        {children}
      </div>
    </Tag>
  );
}
