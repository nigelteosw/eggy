import { useEffect } from "react";
import type { Theme } from "./api";
import { ConfigPage } from "./ConfigPage";
import { CloseIcon } from "./components/ui/icons";

// Settings as a popup over the current view. The sections and their two-pane
// layout are ConfigPage's; this only frames them, so the popup and any direct
// use of ConfigPage cannot drift apart. On a phone the popup takes the whole
// screen and loses its rounded frame.
export function SettingsDialog({
  open,
  onClose,
  theme,
  onThemeChange,
  features,
  onSessionExpired,
}: {
  open: boolean;
  onClose: () => void;
  theme: Theme;
  onThemeChange: (theme: Theme) => void;
  features: string[];
  onSessionExpired: () => void;
}) {
  useEffect(() => {
    if (!open) return;
    const onKey = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-stretch justify-center bg-foreground/25 backdrop-blur-[2px] sm:items-center sm:p-6"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Settings"
        onClick={(event) => event.stopPropagation()}
        className="flex h-dvh w-full max-w-[1000px] flex-col overflow-hidden bg-background sm:h-[min(88dvh,50rem)] sm:rounded-2xl sm:shadow-lift"
      >
        <div className="flex h-12 shrink-0 items-center justify-end pr-2">
          <button
            type="button"
            onClick={onClose}
            aria-label="Close settings"
            title="Close"
            className="flex h-11 w-11 items-center justify-center rounded-xl text-neutral-700 transition-colors hover:bg-neutral-100 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            <CloseIcon />
          </button>
        </div>
        <div className="flex min-h-0 flex-1 flex-col">
          <ConfigPage
            theme={theme}
            onThemeChange={onThemeChange}
            onSessionExpired={onSessionExpired}
            features={features}
          />
        </div>
      </div>
    </div>
  );
}
