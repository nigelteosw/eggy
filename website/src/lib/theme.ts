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
