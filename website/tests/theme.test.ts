import { expect, test } from "bun:test";
import { THEME_CACHE_KEY, isTheme, resolveTheme } from "../src/lib/theme";

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
