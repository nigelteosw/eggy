import { useState } from "react";
import { SessionExpiredError, type Theme, applyTheme, setTheme } from "./api";
import { cn, errorMessage } from "./lib/utils";
import { CardHeader } from "./components/ui/card-header";
import { SelectedCheckIcon } from "./components/ui/icons";
import { ErrorBanner } from "./components/ui/error-banner";

const OPTIONS: { value: Theme; label: string; description: string; swatch: string }[] = [
  { value: "dark", label: "Charcoal", description: "Neutral dark. The default.", swatch: "bg-[hsl(0_0%_11%)]" },
  { value: "light", label: "Paper", description: "Warm off-white.", swatch: "bg-[hsl(105_24%_98%)]" },
];

export function AppearanceCard({
  theme,
  onThemeChange,
  onSessionExpired,
}: {
  theme: Theme;
  onThemeChange: (theme: Theme) => void;
  onSessionExpired: () => void;
}) {
  const [error, setError] = useState<string | null>(null);

  async function choose(next: Theme) {
    if (next === theme) return;
    setError(null);
    // Applied before the write lands, then rolled back if it fails: this is
    // the one setting whose result the owner is looking directly at, so
    // waiting on a round-trip to repaint would read as an unresponsive toggle.
    onThemeChange(next);
    applyTheme(next);
    try {
      await setTheme(next);
    } catch (err) {
      onThemeChange(theme);
      applyTheme(theme);
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(errorMessage(err, "Could not save theme"));
    }
  }

  return (
    <div className="mb-2">
      <CardHeader title="Appearance" className="mb-3" description="Saved to your config, so it follows you to any browser you log in from." />
      <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
        {OPTIONS.map((option) => {
          const selected = theme === option.value;
          return (
            <button
              key={option.value}
              type="button"
              onClick={() => choose(option.value)}
              aria-pressed={selected}
              className={cn(
                "flex items-center gap-3 rounded-2xl p-3.5 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-600/30",
                selected ? "bg-accent-100 shadow-[inset_0_0_0_1px_hsl(var(--accent-300))]" : "bg-neutral-100 hover:bg-neutral-200",
              )}
            >
              <span
                aria-hidden
                className={cn("h-[38px] w-[38px] shrink-0 rounded-xl shadow-[inset_0_0_0_1px_hsl(var(--neutral-300))]", option.swatch)}
              />
              <span className="min-w-0 flex-1">
                <span className="block text-sm font-medium">{option.label}</span>
                <span className="mt-0.5 block text-xs text-neutral-700">{option.description}</span>
              </span>
              <SelectedCheckIcon selected={selected} />
            </button>
          );
        })}
      </div>
      {error && (
        <ErrorBanner className="mt-3">
          {error}
        </ErrorBanner>
      )}
    </div>
  );
}
