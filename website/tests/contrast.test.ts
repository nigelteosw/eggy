import { afterAll, beforeAll, expect, test } from "bun:test";
import { readdir, readFile, rm } from "node:fs/promises";
import { Glob } from "bun";
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

test("markdown inherits Eggy's readable foreground palette", () => {
  const rule = ruleFor("markdown");
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

// Palette contrast is checked in Go (internal/core/services/look_test.go),
// which pins its copy of both palettes to index.css and applies the same
// rule to every look an account can choose, so the list of text pairs lives
// in one place.

// The skill preview shipped bare `prose` and drew its bold labels in the
// plugin's near-black slate on the dark ground. Any element that turns on
// Typography must also take Eggy's palette.
test("every prose block takes Eggy's palette", async () => {
  const offenders: string[] = [];
  for await (const file of new Glob("src/**/*.tsx").scan(join(import.meta.dir, ".."))) {
    const text = await readFile(join(import.meta.dir, "..", file), "utf8");
    // A className is one string or a cn(...) call over several lines; read up
    // to the tag's end so a palette class in another argument counts.
    for (const match of text.matchAll(/className=(?:"[^"]*"|\{cn\([\s\S]*?\)\})/g)) {
      const classes = match[0];
      if (!/(?<![\w:-])prose(?![\w:-])/.test(classes)) continue;
      if (!/(?<![\w-])(markdown|user-message)(?![\w-])/.test(classes)) offenders.push(`${file}: ${classes}`);
    }
  }
  expect(offenders).toEqual([]);
});
