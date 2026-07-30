import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join, normalize } from "node:path";
import { flatNavigation } from "../src/data/navigation";

const dist = join(import.meta.dir, "../dist");
const base = "/eggy/";
const failures: string[] = [];

for (const item of flatNavigation) {
  const relative = item.path === "/" ? "index.html" : `${item.path.slice(1)}/index.html`;
  if (!existsSync(join(dist, relative))) {
    failures.push(`missing generated route: ${item.path} (${relative})`);
  }
}

const indexPath = join(dist, "search-index.json");
if (!existsSync(indexPath)) {
  failures.push("missing search-index.json");
} else {
  const items = JSON.parse(readFileSync(indexPath, "utf8"));
  if (items.length !== flatNavigation.length) {
    failures.push(`search index has ${items.length} items; expected ${flatNavigation.length}`);
  }
}

for (const file of htmlFiles(dist)) {
  const html = readFileSync(file, "utf8");
  const markup = html.replace(/<script\b[\s\S]*?<\/script>/gi, "");
  for (const match of markup.matchAll(/href="([^"]+)"/g)) {
    const href = match[1];
    if (
      href.startsWith("#") ||
      href.startsWith("mailto:") ||
      href.startsWith("tel:") ||
      href.startsWith("http://") ||
      href.startsWith("https://")
    ) {
      continue;
    }
    if (!href.startsWith(base)) {
      failures.push(`${relativeFile(file)}: internal link does not use ${base}: ${href}`);
      continue;
    }
    const pathOnly = href.split("#", 1)[0].split("?", 1)[0];
    const relative = pathOnly.slice(base.length);
    if (!relative) continue;
    const candidate = relative.endsWith("/")
      ? join(dist, relative, "index.html")
      : join(dist, relative);
    if (!existsSync(normalize(candidate))) {
      failures.push(`${relativeFile(file)}: broken internal link ${href}`);
    }
  }
}

const home = readFileSync(join(dist, "index.html"), "utf8");
if (!home.includes(`${base}_astro/`) || !home.includes(`https://nigelteosw.github.io${base}`)) {
  failures.push("home page is missing nested asset or canonical URLs");
}

if (failures.length) {
  console.error(failures.join("\n"));
  process.exit(1);
}

console.log(`Validated ${flatNavigation.length} routes and ${htmlFiles(dist).length} HTML files.`);

function htmlFiles(directory: string): string[] {
  if (!existsSync(directory)) return [];
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? htmlFiles(path) : entry.name.endsWith(".html") ? [path] : [];
  });
}

function relativeFile(file: string): string {
  return file.slice(dist.length + 1);
}
