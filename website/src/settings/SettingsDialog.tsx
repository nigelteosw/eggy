import { useEffect, useRef } from "react";
import type { Theme } from "../lib/api";
import type { SectionId } from "./ConfigPage";
import { ConfigPage } from "./ConfigPage";
import { RestartBar } from "../app/RestartBar";
import { CloseIcon } from "../components/ui/icons";

// Settings as a popup over whatever the owner was looking at, at md and up.
// A phone has no room for a frame around a two-pane page, so there Settings is
// the /settings page itself; App decides which, and this is only the frame.
// The sections and their layout are ConfigPage's, so the popup and the page
// cannot drift apart.
//
// Settings was once a popup and became a page because the popup covered the
// restart bar while the owner made the changes it reports. The bar is drawn
// inside the popup instead, above the sections, so it stays in view.
export function SettingsDialog({
  section,
  sectionKey,
  onClose,
  theme,
  onThemeChange,
  restartPending,
  onSessionExpired,
}: {
  section: SectionId;
  sectionKey: number;
  onClose: () => void;
  theme: Theme;
  onThemeChange: (theme: Theme) => void;
  restartPending: boolean;
  onSessionExpired: () => void;
}) {
  const frame = useRef<HTMLDivElement>(null);
  // The parent hands a new onClose on every render; reading it through a ref
  // keeps the effect to one run, so focus is moved in once, on open, and never
  // pulled back out of a field the owner is typing in.
  const close = useRef(onClose);
  close.current = onClose;

  useEffect(() => {
    const onKey = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") close.current();
    };
    window.addEventListener("keydown", onKey);
    // Focus moves into the popup, so the keyboard starts where the eye does.
    frame.current?.focus();
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <div
      className="fixed inset-0 z-50 flex animate-in fade-in items-center justify-center bg-foreground/25 p-6 backdrop-blur-[2px] duration-200 motion-reduce:animate-none"
      onClick={onClose}
    >
      <div
        ref={frame}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label="Settings"
        onClick={(event) => event.stopPropagation()}
        className="relative flex h-[min(88dvh,50rem)] w-full max-w-[1000px] animate-in zoom-in-95 flex-col overflow-hidden rounded-card bg-background shadow-lift outline-none duration-200 motion-reduce:animate-none lg:h-[min(90dvh,60rem)] lg:max-w-[1200px] 2xl:h-[min(90dvh,68rem)] 2xl:max-w-[1400px]"
      >
        {restartPending && <RestartBar onSessionExpired={onSessionExpired} />}
        <div className="relative flex min-h-0 flex-1 flex-col">
          <button
            type="button"
            onClick={onClose}
            aria-label="Close settings"
            title="Close (Esc)"
            className="absolute right-2 top-2 z-10 flex h-10 w-10 items-center justify-center rounded-control bg-background/80 text-neutral-700 backdrop-blur transition-colors hover:bg-neutral-100 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            <CloseIcon />
          </button>
          <ConfigPage
            key={sectionKey}
            initialSection={section}
            theme={theme}
            onThemeChange={onThemeChange}
            onSessionExpired={onSessionExpired}
          />
        </div>
      </div>
    </div>
  );
}
