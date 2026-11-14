// The panel's theme, resolved to the one palette a page is drawn in. "system"
// is the default and follows the device; "dark" and "light" are the owner's
// explicit choice. Kept free of the DOM so the rule is testable on its own.
export type Theme = "system" | "dark" | "light";
export type Palette = "dark" | "light";

// The last applied theme, kept in this browser so the page can take its palette
// before the first paint. index.html repeats this name in a plain script that
// runs before the app bundle loads, so the two must stay the same string.
export const THEME_CACHE_KEY = "eggy.theme";

export function isTheme(value: unknown): value is Theme {
  return value === "system" || value === "dark" || value === "light";
}

export function resolveTheme(theme: Theme, prefersDark: boolean): Palette {
  if (theme === "system") return prefersDark ? "dark" : "light";
  return theme;
}

// A look is the account's colours and fonts, resolved by the server to the
// tokens that differ from the stylesheet. The server holds the built-in
// looks and checks every custom one, so the panel only paints what it is sent.
export type PaletteOverrides = { light?: Record<string, string>; dark?: Record<string, string> };

export type LookView = {
  look: string;
  text_font: string;
  code_font: string;
  overrides: PaletteOverrides;
  has_custom: boolean;
  looks: string[];
  text_fonts: string[];
  code_fonts: string[];
};

// What this browser keeps of the look, so the first-paint script in
// index.html can paint it before the app loads. That script repeats this key.
export type PaintedLook = { light?: Record<string, string>; dark?: Record<string, string>; text_font?: string; code_font?: string };
export const LOOK_CACHE_KEY = "eggy.look";

// The faces the stylesheet loads itself. Any other offered face is fetched
// from Google Fonts when chosen.
export const DEFAULT_TEXT_FONT = "Geist";
export const DEFAULT_CODE_FONT = "JetBrains Mono";

const TEXT_FALLBACK = 'ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif';
const CODE_FALLBACK = '"SFMono-Regular", "SF Mono", Consolas, Menlo, monospace';

export function paintedLook(view: LookView): PaintedLook {
  return { ...view.overrides, text_font: view.text_font, code_font: view.code_font };
}

// lookProperties is every custom property a look sets for one palette: its
// colour overrides and the two font stacks. Properties it leaves out keep the
// stylesheet's value.
export function lookProperties(look: PaintedLook, palette: Palette): Record<string, string> {
  const properties: Record<string, string> = {};
  for (const [name, value] of Object.entries(look[palette] ?? {})) properties[`--${name}`] = value;
  if (look.text_font && look.text_font !== DEFAULT_TEXT_FONT) properties["--font-sans"] = `"${look.text_font}", ${TEXT_FALLBACK}`;
  if (look.code_font && look.code_font !== DEFAULT_CODE_FONT) properties["--font-mono"] = `"${look.code_font}", ${CODE_FALLBACK}`;
  return properties;
}

// fontHref is the Google Fonts stylesheet for one offered family. Every face
// offered has regular and bold, and the server refuses a family it does not
// offer, so this never builds a request for anything else.
export function fontHref(family: string): string {
  return `https://fonts.googleapis.com/css2?family=${family.trim().replace(/ /g, "+")}:wght@400;700&display=swap`;
}
