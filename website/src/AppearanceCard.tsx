import { useState } from "react";
import { SessionExpiredError, type Theme, applyTheme, setTheme } from "./api";
import { cn } from "./lib/utils";

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
      setError(err instanceof Error ? err.message : "Could not save theme");
    }
  }

  return (
    <div className="mb-2">
      <div className="mx-0.5 mb-3">
        <h3 className="text-[15px] font-semibold tracking-tight">Appearance</h3>
        <p className="mt-1.5 max-w-[620px] text-[12.5px] leading-relaxed text-neutral-700">
          Saved to your config, so it follows you to any browser you log in from.
        </p>
      </div>
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
              <svg
                width="17"
                height="17"
                viewBox="0 0 20 20"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                className={cn("shrink-0 text-accent-600", selected ? "opacity-100" : "opacity-0")}
              >
                <path d="M4.5 10.5 8 14l7.5-8" />
              </svg>
            </button>
          );
        })}
      </div>
      {error && (
        <p className="mt-3 rounded-xl bg-eg-red-tint px-3 py-2 text-sm text-eg-red-ink" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
