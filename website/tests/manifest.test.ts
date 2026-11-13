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

// Android offers the install prompt only with PNG icons at 192 and 512, and
// crops a home-screen icon to its own mask shape: without a maskable icon the
// rounded egg tile is shrunk onto a white disc.
test("the manifest carries the PNG and maskable icons Android installs from", () => {
  const manifest = JSON.parse(readFileSync(join(root, "public/manifest.webmanifest"), "utf8"));
  const png = (size: string, purpose: string) =>
    manifest.icons.some((icon: { type: string; sizes: string; purpose: string }) => icon.type === "image/png" && icon.sizes === size && icon.purpose === purpose);
  expect(png("192x192", "any")).toBe(true);
  expect(png("512x512", "any")).toBe(true);
  expect(png("512x512", "maskable")).toBe(true);
});

test("index.html links the manifest and the icon it describes", () => {
  const html = readFileSync(join(root, "index.html"), "utf8");
  expect(html).toContain('href="/manifest.webmanifest"');
  expect(html).toContain('href="/egg.svg"');
  expect(html).toContain('href="/apple-touch-icon.png"');
  expect(existsSync(join(root, "public/apple-touch-icon.png"))).toBe(true);
});
