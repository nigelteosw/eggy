import { useEffect, useState } from "react";
import { SessionExpiredError, type LookView, type Theme, applyLook, applyTheme, getAgent, setAgentLook, setAgentTheme } from "../lib/api";
import { Label } from "../components/ui/label";
import { FIELD_LABEL } from "../components/ui/form";
import { cn, errorMessage } from "../lib/utils";
import { CardHeader } from "../components/ui/card-header";
import { SelectedCheckIcon } from "../components/ui/icons";
import { ErrorBanner } from "../components/ui/error-banner";

// Three equal modes, named for what the person sees. "Match system" follows the
// device and is the default for an account that has not chosen.
const OPTIONS: { value: Theme; label: string; description: string; swatch: string }[] = [
  { value: "system", label: "Match system", description: "Light or dark, following this device.", swatch: "bg-gradient-to-br from-[hsl(40_30%_96%)] to-[hsl(30_6%_8%)]" },
  { value: "light", label: "Light", description: "For daylight, in your look's light colours.", swatch: "bg-[hsl(40_30%_96%)]" },
  { value: "dark", label: "Dark", description: "For evenings, in your look's dark colours.", swatch: "bg-[hsl(220_10%_6%)]" },
];

// The looks, named for what they change. Custom is offered once the person
// has asked Eggy for one; until then the hint says how.
const LOOKS: Record<string, { label: string; description: string; swatch: string }> = {
  eggshell: { label: "Eggshell", description: "Warm paper by day, near-black and amber at night.", swatch: "bg-gradient-to-br from-[hsl(40_45%_94%)] to-[hsl(220_10%_6%)]" },
  "cast-iron": { label: "Cast iron", description: "Eggshell by day, warm charcoal at night.", swatch: "bg-gradient-to-br from-[hsl(40_45%_94%)] to-[hsl(0_0%_8%)]" },
  custom: { label: "Custom", description: "The look you asked Eggy for.", swatch: "bg-gradient-to-br from-primary to-background" },
};

const selectClass =
  "h-[42px] w-full rounded-control border border-neutral-200 bg-background px-3.5 text-field text-foreground sm:text-small focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-600/30";

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
  const [look, setLook] = useState<LookView | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    getAgent()
      .then((selection) => setLook(selection.look ?? null))
      .catch((err) => {
        if (err instanceof SessionExpiredError) onSessionExpired();
      });
    // Read once when the card opens; every write answers with the new look.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Unlike the palette, a look is painted from the server's answer rather
  // than ahead of it: the server resolves a name to colours, so there is
  // nothing to paint until it has.
  async function changeLook(change: { look?: string; text_font?: string; code_font?: string }) {
    setError(null);
    setBusy(true);
    try {
      const selection = await setAgentLook(change);
      if (selection.look) {
        setLook(selection.look);
        applyLook(selection.look);
      }
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(errorMessage(err, "Could not save your look"));
    } finally {
      setBusy(false);
    }
  }

  async function choose(next: Theme) {
    if (next === theme) return;
    setError(null);
    // Applied before the write lands, then rolled back if it fails: this is
    // the one setting whose result the owner is looking directly at, so
    // waiting on a round-trip to repaint would read as an unresponsive toggle.
    onThemeChange(next);
    applyTheme(next);
    try {
      await setAgentTheme(next);
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
      <CardHeader title="Appearance" className="mb-3" description="Yours alone. It follows you to any browser you log in from." />
      <p className={cn(FIELD_LABEL, "mb-1.5")}>Light or dark</p>
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
                "flex items-center gap-3 rounded-card p-3.5 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-600/30",
                selected ? "bg-accent-100 shadow-[inset_0_0_0_1px_hsl(var(--accent-300))]" : "bg-neutral-100 hover:bg-neutral-200",
              )}
            >
              <span
                aria-hidden
                className={cn("h-[38px] w-[38px] shrink-0 rounded-control shadow-[inset_0_0_0_1px_hsl(var(--neutral-300))]", option.swatch)}
              />
              <span className="min-w-0 flex-1">
                <span className="block text-small font-medium">{option.label}</span>
                <span className="mt-0.5 block text-caption text-neutral-700">{option.description}</span>
              </span>
              <SelectedCheckIcon selected={selected} />
            </button>
          );
        })}
      </div>
      {look && (
        <>
          <p className={cn(FIELD_LABEL, "mb-1.5 mt-5")}>Look</p>
          <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
            {look.looks
              .filter((name) => LOOKS[name] && (name !== "custom" || look.has_custom))
              .map((name) => {
                const option = LOOKS[name];
                const selected = look.look === name;
                return (
                  <button
                    key={name}
                    type="button"
                    disabled={busy}
                    onClick={() => !selected && changeLook({ look: name })}
                    aria-pressed={selected}
                    className={cn(
                      "flex items-center gap-3 rounded-card p-3.5 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-600/30",
                      selected ? "bg-accent-100 shadow-[inset_0_0_0_1px_hsl(var(--accent-300))]" : "bg-neutral-100 hover:bg-neutral-200",
                    )}
                  >
                    <span aria-hidden className={cn("h-[38px] w-[38px] shrink-0 rounded-control shadow-[inset_0_0_0_1px_hsl(var(--neutral-300))]", option.swatch)} />
                    <span className="min-w-0 flex-1">
                      <span className="block text-small font-medium">{option.label}</span>
                      <span className="mt-0.5 block text-caption text-neutral-700">{option.description}</span>
                    </span>
                    <SelectedCheckIcon selected={selected} />
                  </button>
                );
              })}
          </div>
          {!look.has_custom && (
            <p className="mt-2.5 text-caption text-neutral-700">
              Want your own? Ask Eggy in chat, such as “give my panel a forest-green accent” or “make it feel like an old terminal”.
            </p>
          )}
          <div className="mt-5 grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="look-text-font" className={FIELD_LABEL}>Text font</Label>
              <select id="look-text-font" value={look.text_font} disabled={busy} onChange={(event) => changeLook({ text_font: event.target.value })} className={selectClass}>
                {look.text_fonts.map((font) => (
                  <option key={font} value={font}>{font}</option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="look-code-font" className={FIELD_LABEL}>Code font</Label>
              <select id="look-code-font" value={look.code_font} disabled={busy} onChange={(event) => changeLook({ code_font: event.target.value })} className={selectClass}>
                {look.code_fonts.map((font) => (
                  <option key={font} value={font}>{font}</option>
                ))}
              </select>
            </div>
          </div>
        </>
      )}
      {error && (
        <ErrorBanner className="mt-3">
          {error}
        </ErrorBanner>
      )}
    </div>
  );
}
