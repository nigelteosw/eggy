// The reader's theme preference. "system" follows the device and is what a
// reader starts with; the head script in DocsLayout.astro applies the same rule
// before first paint, because an inline script cannot import this module.
export type Preference = "system" | "light" | "dark";
export type Theme = "light" | "dark";

export const STORAGE_KEY = "eggy-docs-theme";

export function readPreference(stored: string | null): Preference {
  return stored === "light" || stored === "dark" ? stored : "system";
}

export function nextPreference(current: Preference): Preference {
  return current === "system" ? "light" : current === "light" ? "dark" : "system";
}

export function resolveTheme(preference: Preference, deviceIsDark: boolean): Theme {
  if (preference === "system") return deviceIsDark ? "dark" : "light";
  return preference;
}

export const LABELS: Record<Preference, string> = {
  system: "Match system",
  light: "Light",
  dark: "Dark",
};
