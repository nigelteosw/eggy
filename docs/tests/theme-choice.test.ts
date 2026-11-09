import { expect, test } from "bun:test";
import { nextPreference, readPreference, resolveTheme } from "../src/scripts/theme";

// The toggle cycles Match system -> Light -> Dark -> Match system, so a reader
// who picked a theme can always hand the choice back to their device.
test("the toggle cycles back to matching the system", () => {
  expect(nextPreference("system")).toBe("light");
  expect(nextPreference("light")).toBe("dark");
  expect(nextPreference("dark")).toBe("system");
});

test("matching the system follows the device, and a choice overrides it", () => {
  expect(resolveTheme("system", true)).toBe("dark");
  expect(resolveTheme("system", false)).toBe("light");
  expect(resolveTheme("light", true)).toBe("light");
  expect(resolveTheme("dark", false)).toBe("dark");
});

test("anything stored that is not a choice reads as matching the system", () => {
  expect(readPreference(null)).toBe("system");
  expect(readPreference("sepia")).toBe("system");
  expect(readPreference("dark")).toBe("dark");
});
