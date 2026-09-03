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

test("user markdown keeps every text treatment readable on the green bubble", () => {
  const rule = ruleFor("user-message");
  for (const property of ["body", "headings", "lead", "links", "bold", "quotes", "kbd", "code", "pre-code"]) {
    expect(rule).toContain(`--tw-prose-${property}: hsl(var(--primary-foreground))`);
  }
});
