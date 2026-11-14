import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { LOOK_CACHE_KEY, THEME_CACHE_KEY, fontHref, isTheme, lookProperties, resolveTheme } from "../src/lib/theme";

test("system follows the device's preference, and dark and light are fixed", () => {
  expect(resolveTheme("system", true)).toBe("dark");
  expect(resolveTheme("system", false)).toBe("light");
  expect(resolveTheme("dark", false)).toBe("dark");
  expect(resolveTheme("light", true)).toBe("light");
});

test("only the three stored names are themes; anything else falls back to system", () => {
  expect(isTheme("system")).toBe(true);
  expect(isTheme("solarized")).toBe(false);
  expect(isTheme(undefined)).toBe(false);
});

test("the cache key is one stable name the first-paint script can read", () => {
  // index.html repeats this name in a plain script that runs before the app,
  // so it cannot be imported. If it drifts, the page flashes the wrong theme.
  expect(THEME_CACHE_KEY).toBe("eggy.theme");
});

test("the first-paint script reads both cached keys", () => {
  const html = readFileSync(join(import.meta.dir, "../index.html"), "utf8");
  expect(html).toContain(`"${THEME_CACHE_KEY}"`);
  expect(html).toContain(`"${LOOK_CACHE_KEY}"`);
});

test("a look paints only its own palette's tokens and the faces it changes", () => {
  const look = { dark: { primary: "150 60% 45%" }, text_font: "Inter", code_font: "JetBrains Mono" };
  expect(lookProperties(look, "dark")).toEqual({ "--primary": "150 60% 45%", "--font-sans": expect.stringContaining('"Inter"') });
  // The light palette has no overrides here, and the default code face needs
  // no property: the stylesheet already sets it.
  expect(Object.keys(lookProperties(look, "light"))).toEqual(["--font-sans"]);
  expect(lookProperties({}, "light")).toEqual({});
});

test("a font request names one family from Google Fonts", () => {
  expect(fontHref("IBM Plex Sans")).toBe("https://fonts.googleapis.com/css2?family=IBM+Plex+Sans:wght@400;700&display=swap");
});
