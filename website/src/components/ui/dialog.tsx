import { useEffect, type ReactNode } from "react";
import { CloseIcon } from "./icons";

// A centred popup for a feature that is not a destination of its own. It is
// written here rather than pulled from a dialog library for the same reason
// as sidebar.tsx: a modal with Escape, a backdrop, and a focus start is all
// this needs. Focus is not trapped; the popup is short-lived and the close
// button is the first control in the tab order.
export function Dialog({
  open,
  title,
  onClose,
  children,
}: {
  open: boolean;
  title: string;
  onClose: () => void;
  children: ReactNode;
}) {
  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-foreground/25 p-3 backdrop-blur-[2px] sm:p-6"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onClick={(event) => event.stopPropagation()}
        className="flex h-[min(88dvh,52rem)] w-full max-w-5xl flex-col overflow-hidden rounded-2xl bg-background shadow-lift"
      >
        <div className="flex h-14 shrink-0 items-center gap-2 pl-5 pr-2 shadow-[inset_0_-1px_0_hsl(var(--border))]">
          <h2 className="flex-1 truncate text-[14.5px] font-semibold tracking-tight">{title}</h2>
          <button
            type="button"
            onClick={onClose}
            aria-label={`Close ${title}`}
            title="Close"
            className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl text-neutral-700 transition-colors hover:bg-neutral-100 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            <CloseIcon />
          </button>
        </div>
        <div className="scrollbar-slim min-h-0 flex-1 overflow-y-auto">{children}</div>
      </div>
    </div>
  );
}
