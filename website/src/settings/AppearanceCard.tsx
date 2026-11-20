import { Select } from "../components/ui/select";
import { type CSSProperties, type FormEvent, useEffect, useState } from "react";
import { SessionExpiredError, type LookView, type Theme, applyLook, applyTheme, deleteAgentTheme, getAgent, saveAgentTheme, setAgentLook, setAgentTheme } from "../lib/api";
import { Label } from "../components/ui/label";
import { Input } from "../components/ui/input";
import { Button } from "../components/ui/button";
import { FIELD_LABEL } from "../components/ui/form";
import { cn, errorMessage } from "../lib/utils";
import { CardHeader } from "../components/ui/card-header";
import { SelectedCheckIcon } from "../components/ui/icons";
import { ErrorBanner } from "../components/ui/error-banner";
import { type LookOption, gradientStop, swatchStyle } from "../lib/theme";

// Three equal modes, named for what the person sees. "Match system" follows the
// device and is the default for an account that has not chosen. Each swatch is
// drawn in the chosen look's own colours, which the server sends.
const OPTIONS: { value: Theme; label: string; description: string }[] = [
  { value: "system", label: "Match system", description: "Light or dark, following this device." },
  { value: "light", label: "Light", description: "For daylight, in your theme's light colours." },
  { value: "dark", label: "Dark", description: "For evenings, in your theme's dark colours." },
];

function modeSwatch(mode: Theme, look: LookOption | undefined): CSSProperties | undefined {
  if (!look) return undefined;
  if (mode === "light") return { background: `oklch(${look.light})` };
  if (mode === "dark") return { background: `oklch(${look.dark})` };
  const hue = look.accent.split(" ")[2];
  return { backgroundImage: `linear-gradient(135deg in oklch, ${gradientStop(look.light, hue)} 45%, ${gradientStop(look.dark, hue)} 55%)` };
}


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
  const [themeName, setThemeName] = useState("");
  const [themeDescription, setThemeDescription] = useState("");
  const current = look?.options.find((option) => option.name === look.look);

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
  // nothing to paint until it has. Every look write answers with the whole
  // selection, so one path applies them all.
  async function write(send: () => Promise<{ look?: LookView }>, failure: string): Promise<boolean> {
    setError(null);
    setBusy(true);
    try {
      const selection = await send();
      if (selection.look) {
        setLook(selection.look);
        applyLook(selection.look);
      }
      return true;
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return false;
      }
      setError(errorMessage(err, failure));
      return false;
    } finally {
      setBusy(false);
    }
  }

  const changeLook = (change: { look?: string; text_font?: string; code_font?: string }) => write(() => setAgentLook(change), "Could not save your theme");

  async function saveTheme(event: FormEvent) {
    event.preventDefault();
    if (await write(() => saveAgentTheme({ label: themeName, description: themeDescription }), "Could not save the theme")) {
      setThemeName("");
      setThemeDescription("");
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
                selected ? "bg-accent-100 shadow-[inset_0_0_0_1px_oklch(var(--accent-300))]" : "bg-neutral-100 hover:bg-neutral-200",
              )}
            >
              <span
                aria-hidden
                className="h-[38px] w-[38px] shrink-0 rounded-control bg-neutral-200 shadow-[inset_0_0_0_1px_var(--hairline-strong)]"
                style={modeSwatch(option.value, current)}
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
          <p className={cn(FIELD_LABEL, "mb-1.5 mt-5")}>Themes</p>
          <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
            {look.options.map((option) => {
                const selected = look.look === option.name;
                return (
                  <div
                    key={option.name}
                    className={cn(
                      "flex items-center rounded-card transition-colors",
                      selected ? "bg-accent-100 shadow-[inset_0_0_0_1px_oklch(var(--accent-300))]" : "bg-neutral-100 hover:bg-neutral-200",
                    )}
                  >
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => !selected && changeLook({ look: option.name })}
                      aria-pressed={selected}
                      className="flex min-w-0 flex-1 items-center gap-3 rounded-card p-3.5 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-600/30"
                    >
                      <span aria-hidden className="h-[38px] w-[38px] shrink-0 rounded-control shadow-[inset_0_0_0_1px_var(--hairline-strong)]" style={swatchStyle(option)} />
                      <span className="min-w-0 flex-1">
                        <span className="block text-small font-medium">{option.label}</span>
                        <span className="mt-0.5 block text-caption text-neutral-700">{option.description}</span>
                      </span>
                      <SelectedCheckIcon selected={selected} />
                    </button>
                    {option.mine && (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => void write(() => deleteAgentTheme(option.name), "Could not delete the theme")}
                        aria-label={`Delete the ${option.label} theme`}
                        className="mr-1.5 shrink-0 rounded-control px-2.5 py-1.5 text-caption text-neutral-700 transition-colors hover:bg-neutral-200 hover:text-tomato"
                      >
                        Delete
                      </button>
                    )}
                  </div>
                );
              })}
          </div>
          {look.look === "custom" && (
            <form onSubmit={saveTheme} className="mt-3 flex flex-col gap-2.5 rounded-card bg-neutral-100 p-3.5">
              <p className="text-small font-medium">Save this theme for everyone</p>
              <p className="text-caption text-neutral-700">Everyone on this Eggy can then choose it, and it stays through redeploys. Only you can delete it.</p>
              <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.6fr)_auto]">
                <Input aria-label="Theme name" placeholder="Name, such as Mint" maxLength={40} value={themeName} onChange={(event) => setThemeName(event.target.value)} />
                <Input aria-label="Theme description" placeholder="What it looks like (optional)" maxLength={120} value={themeDescription} onChange={(event) => setThemeDescription(event.target.value)} />
                <Button type="submit" disabled={busy || themeName.trim() === ""}>Save theme</Button>
              </div>
            </form>
          )}
          {!look.has_custom && (
            <p className="mt-2.5 text-caption text-neutral-700">
              Want your own? Ask Eggy in chat, such as “give my panel a forest-green accent” or “make it feel like an old terminal”.
            </p>
          )}
          <div className="mt-5 grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="look-text-font" className={FIELD_LABEL}>Text font</Label>
              <Select id="look-text-font" value={look.text_font} disabled={busy} onChange={(event) => changeLook({ text_font: event.target.value })}>
                {look.text_fonts.map((font) => (
                  <option key={font} value={font}>{font}</option>
                ))}
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="look-code-font" className={FIELD_LABEL}>Code font</Label>
              <Select id="look-code-font" value={look.code_font} disabled={busy} onChange={(event) => changeLook({ code_font: event.target.value })}>
                {look.code_fonts.map((font) => (
                  <option key={font} value={font}>{font}</option>
                ))}
              </Select>
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
