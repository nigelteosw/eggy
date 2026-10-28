import { expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

const root = join(import.meta.dir, "..");

// Saving Eggy to a home screen needs a manifest that opens standalone and an
// icon that exists in the build; a manifest pointing at a missing file installs
// with a blank tile.
test("the manifest opens standalone with the palette's colours and a shipped icon", () => {
  const manifest = JSON.parse(readFileSync(join(root, "public/manifest.webmanifest"), "utf8"));
  expect(manifest.display).toBe("standalone");
  expect(manifest.start_url).toBe("/");
  expect(manifest.background_color).toBe("#F6F1E7");
  for (const icon of manifest.icons) {
    expect(existsSync(join(root, "public", icon.src))).toBe(true);
  }
});

test("index.html links the manifest and the icon it describes", () => {
  const html = readFileSync(join(root, "index.html"), "utf8");
  expect(html).toContain('href="/manifest.webmanifest"');
  expect(html).toContain('href="/egg.svg"');
  expect(html).toContain('href="/apple-touch-icon.png"');
  expect(existsSync(join(root, "public/apple-touch-icon.png"))).toBe(true);
});
