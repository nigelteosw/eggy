import { afterAll, beforeAll, expect, test } from "bun:test";
import { readdir, readFile, rm } from "node:fs/promises";
import { join } from "node:path";

const output = join(import.meta.dir, "../.test-dist");
let css = "";

beforeAll(async () => {
  const build = Bun.spawn(["bun", "x", "vite", "build", "--outDir", output, "--emptyOutDir"], {
    cwd: join(import.meta.dir, ".."),
    stdout: "pipe",
    stderr: "pipe",
  });
  const exitCode = await build.exited;
  if (exitCode !== 0) {
    throw new Error(await new Response(build.stderr).text());
  }
  const asset = (await readdir(join(output, "assets"))).find((name) => name.endsWith(".css"));
  if (!asset) throw new Error("Vite emitted no stylesheet");
  css = await readFile(join(output, "assets", asset), "utf8");
});

afterAll(async () => {
  await rm(output, { recursive: true, force: true });
});

// Regression: Tailwind Typography supplies fixed slate prose colours. On the
// dark theme those defaults made list text nearly disappear even though the
// surrounding message card had the correct foreground colour. The shipped
// CSS must override every prose colour at the component boundary.
function ruleFor(selector: string): string {
  const match = css.match(new RegExp(`\\.${selector}\\{([^}]+)\\}`));
  expect(match).not.toBeNull();
  return match?.[1] ?? "";
}

test("assistant markdown inherits Eggy's readable foreground palette", () => {
  const rule = ruleFor("assistant-message");
  const required = [
    /--tw-prose-body:\s*hsl\(var\(--foreground\)\)/,
    /--tw-prose-bullets:\s*hsl\(var\(--muted-foreground\)\)/,
    /--tw-prose-quotes:\s*hsl\(var\(--foreground\)\)/,
    /--tw-prose-code:\s*hsl\(var\(--foreground\)\)/,
  ];
  for (const declaration of required) expect(declaration.test(rule)).toBeTrue();
});

test("user markdown keeps every text treatment readable on the neutral bubble", () => {
  const rule = ruleFor("user-message");
  for (const property of ["body", "headings", "lead", "links", "bold", "quotes", "kbd", "code", "pre-code"]) {
    expect(rule).toContain(`--tw-prose-${property}: hsl(var(--foreground))`);
  }
});

// The Eggshell (light) and Cast iron (dark) palettes are read from the built
// stylesheet, so a token edit that breaks text legibility in either mode fails
// here rather than on someone's screen. Pairs are text on its surface.
function tokens(selector: string): Map<string, string> {
  const body = css.match(new RegExp(`${selector.replace(/\./g, "\\.")}\\{([^}]+)\\}`))?.[1] ?? "";
  const found = new Map<string, string>();
  for (const [, name, value] of body.matchAll(/--([\w-]+):\s*([^;]+);/g)) found.set(name, value.trim());
  return found;
}

function hslToRgb(value: string): [number, number, number] {
  const [h, s, l] = value.match(/[\d.]+/g)!.map(Number);
  const saturation = s / 100;
  const lightness = l / 100;
  const chroma = (1 - Math.abs(2 * lightness - 1)) * saturation;
  const hue = (h % 360) / 60;
  const x = chroma * (1 - Math.abs((hue % 2) - 1));
  const [r1, g1, b1] = hue < 1 ? [chroma, x, 0] : hue < 2 ? [x, chroma, 0] : hue < 3 ? [0, chroma, x] : hue < 4 ? [0, x, chroma] : hue < 5 ? [x, 0, chroma] : [chroma, 0, x];
  const m = lightness - chroma / 2;
  return [r1 + m, g1 + m, b1 + m];
}

function luminance(value: string): number {
  const [r, g, b] = hslToRgb(value).map((channel) => (channel <= 0.03928 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(foreground: string, background: string): number {
  const [light, dark] = [luminance(foreground), luminance(background)].sort((a, b) => b - a);
  return (light + 0.05) / (dark + 0.05);
}

const TEXT_PAIRS: [string, string][] = [
  ["foreground", "background"],
  ["foreground", "card"],
  ["muted-foreground", "background"],
  ["muted-foreground", "card"],
  ["muted-foreground", "muted"],
  ["accent-700", "background"],
  ["primary-foreground", "primary"],
  ["destructive", "background"],
  ["eg-red-ink", "eg-red-tint"],
  ["eg-ask-ink", "eg-ask"],
];

for (const [mode, selector] of [
  ["light", ":root"],
  ["dark", ".dark"],
] as const) {
  test(`the ${mode} palette keeps every text pair at WCAG AA (4.5:1)`, () => {
    const palette = tokens(selector);
    for (const [text, surface] of TEXT_PAIRS) {
      const ratio = contrast(palette.get(text)!, palette.get(surface)!);
      expect(ratio, `${text} on ${surface} in ${mode}`).toBeGreaterThanOrEqual(4.5);
    }
  });
}
