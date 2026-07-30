# Eggy Documentation Site Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build and publish a comprehensive, Coral-inspired static documentation site for Eggy's shipped operator and contributor behavior.

**Architecture:** Astro renders Markdown entries from one content collection through a shared three-column documentation shell. A typed navigation catalog drives routing, sidebar groups, previous/next links, search metadata, and validation; small framework-free browser scripts provide search, copy buttons, and the mobile drawer. GitHub Actions packages the static build one directory below the Pages root so the public site resolves at `/eggy/docs/`.

**Tech Stack:** Astro 7.1, TypeScript, Markdown content collections, Bun tests and package management, CSS, browser-native JavaScript, GitHub Pages Actions.

## Global Constraints

- Publish at exactly `https://nigelteosw.github.io/eggy/docs/`.
- Set Astro's production base to exactly `/eggy/docs/`.
- Document only behavior shipped on the current branch; never present `TODO.md` work as available.
- Keep the site fully static with no server runtime, database, CMS, or dependency on a running Eggy instance.
- Keep the documentation application entirely under `docs/`, except for its workflow under `.github/workflows/`.
- Do not change Eggy runtime behavior or the embedded application under `website/`.
- Preserve the warm off-white, charcoal, Eggy-yellow, typography-led visual direction with restrained radii and minimal shadows.
- Keep navigation and search keyboard accessible, provide visible focus states, and respect reduced-motion preferences.
- Derive commands, configuration, routes, capabilities, and security claims from current repository source.

---

## File Structure

### Build and data

- `docs/astro.config.mjs` — static output, production URL, nested base, and directory-style routes.
- `docs/package.json` — focused docs scripts and development dependencies.
- `docs/src/content.config.ts` — `docs` Markdown collection schema.
- `docs/src/data/navigation.ts` — single typed navigation catalog.
- `docs/src/lib/urls.ts` — base-safe documentation URL helpers.
- `docs/src/lib/search.ts` — Markdown-to-search-text normalization.
- `docs/src/pages/index.astro` — renders the introduction entry at the docs root.
- `docs/src/pages/[...slug].astro` — generates every non-index article route.
- `docs/src/pages/search-index.json.ts` — emits the static client-side search index.
- `docs/src/pages/404.astro` — static recovery page.

### Presentation

- `docs/src/layouts/DocsLayout.astro` — document metadata and three-column shell.
- `docs/src/components/Header.astro` — identity, GitHub link, search trigger, and mobile menu.
- `docs/src/components/Sidebar.astro` — grouped desktop and mobile navigation.
- `docs/src/components/PageOutline.astro` — second- and third-level heading links.
- `docs/src/components/SearchDialog.astro` — accessible browser-side search interface.
- `docs/src/components/ArticleFooter.astro` — previous and next navigation.
- `docs/src/styles/global.css` — complete visual system and responsive behavior.
- `docs/src/scripts/docs-ui.ts` — drawer, dialog, copy, outline, and keyboard interactions.

### Content

- `docs/src/content/docs/index.md` — introduction and three-step start.
- `docs/src/content/docs/get-started/quickstart.md`
- `docs/src/content/docs/get-started/deploy-railway.md`
- `docs/src/content/docs/use/web-chat.md`
- `docs/src/content/docs/use/telegram.md`
- `docs/src/content/docs/use/models.md`
- `docs/src/content/docs/use/approvals.md`
- `docs/src/content/docs/configure/configuration.md`
- `docs/src/content/docs/configure/model-providers.md`
- `docs/src/content/docs/configure/google-calendar.md`
- `docs/src/content/docs/configure/mcp-servers.md`
- `docs/src/content/docs/configure/repositories.md`
- `docs/src/content/docs/operate/persistence-memory.md`
- `docs/src/content/docs/operate/health-checks.md`
- `docs/src/content/docs/operate/security.md`
- `docs/src/content/docs/operate/troubleshooting.md`
- `docs/src/content/docs/project/architecture.md`
- `docs/src/content/docs/project/adding-adapter.md`
- `docs/src/content/docs/project/local-development.md`
- `docs/src/content/docs/project/testing-releases.md`

### Validation and deployment

- `docs/tests/navigation.test.ts` — catalog uniqueness, ordering, and page coverage.
- `docs/tests/search.test.ts` — deterministic search-text normalization.
- `docs/tests/source-consistency.test.ts` — documented Telegram commands match Go source.
- `docs/scripts/validate-built-site.ts` — generated routes and internal-link validation.
- `.github/workflows/docs-pages.yml` — build, package, upload, and deploy Pages artifact.

---

### Task 1: Establish the typed documentation content model

**Files:**
- Modify: `docs/package.json`
- Modify: `docs/astro.config.mjs`
- Create: `docs/src/content.config.ts`
- Create: `docs/src/data/navigation.ts`
- Create: `docs/src/lib/urls.ts`
- Create: `docs/tests/navigation.test.ts`
- Create: `docs/src/content/docs/index.md`
- Create: the other 19 Markdown files listed in the file structure with valid frontmatter and placeholder-free opening summaries

**Interfaces:**
- Produces: `DocNavItem`, `DocNavGroup`, `navigation`, `flatNavigation`, `findNavItem(pathname)`, and `docUrl(pathname)`.
- Produces: collection frontmatter `{ title: string; description: string; eyebrow?: string }`.
- Consumes: Astro's `glob()` loader and `z` schema helper.

- [ ] **Step 1: Write the failing navigation tests**

```ts
import { describe, expect, test } from "bun:test";
import { existsSync } from "node:fs";
import { join } from "node:path";
import { flatNavigation } from "../src/data/navigation";

describe("documentation navigation", () => {
  test("has unique routes and labels", () => {
    expect(new Set(flatNavigation.map((item) => item.path)).size).toBe(flatNavigation.length);
    expect(new Set(flatNavigation.map((item) => item.title)).size).toBe(flatNavigation.length);
  });

  test("starts at the introduction and covers every content entry", () => {
    expect(flatNavigation[0]?.path).toBe("/");
    for (const item of flatNavigation) {
      const file = item.path === "/"
        ? "index.md"
        : `${item.path.replace(/^\//, "")}.md`;
      expect(existsSync(join(import.meta.dir, "../src/content/docs", file))).toBeTrue();
    }
  });
});
```

- [ ] **Step 2: Run the focused test and verify the missing module failure**

Run: `cd docs && bun test tests/navigation.test.ts`

Expected: FAIL because `src/data/navigation.ts` does not exist.

- [ ] **Step 3: Add the navigation catalog and URL helper**

```ts
export type DocNavItem = {
  title: string;
  path: `/${string}` | "/";
  description: string;
};

export type DocNavGroup = {
  label: string;
  items: readonly DocNavItem[];
};

export const navigation = [
  { label: "Get started", items: [
    { title: "Introduction", path: "/", description: "Meet Eggy and understand its core workflow." },
    { title: "Quickstart", path: "/get-started/quickstart", description: "Run a local Eggy instance." },
    { title: "Deploy on Railway", path: "/get-started/deploy-railway", description: "Deploy Eggy with durable storage." },
  ] },
  { label: "Use Eggy", items: [
    { title: "Web chat", path: "/use/web-chat", description: "Chat with Eggy in the authenticated web UI." },
    { title: "Telegram", path: "/use/telegram", description: "Use Eggy's five Telegram commands and selections." },
    { title: "Models and reasoning effort", path: "/use/models", description: "Select configured model aliases." },
    { title: "Approvals and protected actions", path: "/use/approvals", description: "Understand payload-bound Calendar approvals." },
  ] },
  { label: "Configure", items: [
    { title: "Configuration overview", path: "/configure/configuration", description: "Configure the daemon without storing secrets in YAML." },
    { title: "Model providers", path: "/configure/model-providers", description: "Connect OpenAI-compatible model providers." },
    { title: "Google Calendar", path: "/configure/google-calendar", description: "Enable native Calendar reads and approved mutations." },
    { title: "MCP servers", path: "/configure/mcp-servers", description: "Connect trusted HTTP or stdio MCP servers." },
    { title: "Repository inspection", path: "/configure/repositories", description: "Configure trusted read-only repository access." },
  ] },
  { label: "Operate", items: [
    { title: "Persistence and memory", path: "/operate/persistence-memory", description: "Understand Eggy's files, SQLite database, and volume." },
    { title: "Health checks", path: "/operate/health-checks", description: "Monitor liveness and readiness." },
    { title: "Security model", path: "/operate/security", description: "Review trust boundaries and owner controls." },
    { title: "Troubleshooting", path: "/operate/troubleshooting", description: "Diagnose common startup and delivery failures." },
  ] },
  { label: "Project", items: [
    { title: "Architecture", path: "/project/architecture", description: "Understand the ports-and-adapters modular monolith." },
    { title: "Adding an adapter", path: "/project/adding-adapter", description: "Extend Eggy without changing its provider-neutral kernel." },
    { title: "Local development", path: "/project/local-development", description: "Set up and run the development environment." },
    { title: "Testing and releases", path: "/project/testing-releases", description: "Run required verification and build artifacts." },
  ] },
] as const satisfies readonly DocNavGroup[];

export const flatNavigation = navigation.flatMap((group) => group.items);
export const findNavItem = (pathname: string) =>
  flatNavigation.find((item) => item.path === pathname);
```

```ts
const base = import.meta.env.BASE_URL.replace(/\/$/, "");

export function docUrl(pathname: string): string {
  const normalized = pathname === "/" ? "/" : `/${pathname.replace(/^\/|\/$/g, "")}/`;
  return `${base}${normalized}`;
}
```

- [ ] **Step 4: Configure the collection and nested production base**

```ts
import { defineCollection, z } from "astro:content";
import { glob } from "astro/loaders";

const docs = defineCollection({
  loader: glob({ pattern: "**/*.md", base: "./src/content/docs" }),
  schema: z.object({
    title: z.string(),
    description: z.string(),
    eyebrow: z.string().optional(),
  }),
});

export const collections = { docs };
```

```js
import { defineConfig } from "astro/config";

export default defineConfig({
  site: "https://nigelteosw.github.io",
  base: "/eggy/docs",
  output: "static",
  trailingSlash: "always",
  build: { format: "directory" },
});
```

Add scripts `test`, `check`, `build`, and `validate` to `docs/package.json`, and add `@astrojs/check` plus `typescript` as development dependencies.

- [ ] **Step 5: Add all content files with final titles, descriptions, and one source-grounded opening paragraph**

Each file must contain valid frontmatter matching its navigation entry. Do not use filler such as "coming soon"; later content tasks expand these entries from current source.

- [ ] **Step 6: Run the focused tests and Astro content sync**

Run: `cd docs && bun test tests/navigation.test.ts && bun astro sync`

Expected: all navigation tests PASS and Astro generates collection types without schema errors.

- [ ] **Step 7: Commit the content model**

```bash
git add docs/package.json docs/bun.lock docs/astro.config.mjs docs/src/content.config.ts docs/src/data docs/src/lib/urls.ts docs/src/content/docs docs/tests/navigation.test.ts
git commit -m "docs: establish documentation content model"
```

---

### Task 2: Build the responsive documentation shell

**Files:**
- Create: `docs/src/layouts/DocsLayout.astro`
- Create: `docs/src/components/Header.astro`
- Create: `docs/src/components/Sidebar.astro`
- Create: `docs/src/components/PageOutline.astro`
- Create: `docs/src/components/ArticleFooter.astro`
- Create: `docs/src/styles/global.css`
- Replace: `docs/src/pages/index.astro`
- Create: `docs/src/pages/[...slug].astro`
- Create: `docs/src/pages/404.astro`

**Interfaces:**
- Consumes: `navigation`, `flatNavigation`, `findNavItem`, and `docUrl`.
- Consumes: rendered collection output `{ Content, headings }`.
- Produces: `DocsLayout` props `{ title, description, pathname, headings }`.

- [ ] **Step 1: Extend navigation tests with previous and next behavior**

Add exports `getAdjacentItems(pathname)` returning:

```ts
{
  previous?: DocNavItem;
  next?: DocNavItem;
}
```

Test that `/` has no previous item and points next to Quickstart, while the final page has no next item.

- [ ] **Step 2: Run the focused test and verify it fails**

Run: `cd docs && bun test tests/navigation.test.ts`

Expected: FAIL because `getAdjacentItems` is not exported.

- [ ] **Step 3: Implement adjacent navigation and article routing**

Implement `getAdjacentItems`, render `index.md` from `index.astro`, and generate all remaining entries from `[...slug].astro`:

```astro
---
import { getCollection, render } from "astro:content";
import DocsLayout from "../layouts/DocsLayout.astro";

export async function getStaticPaths() {
  const entries = await getCollection("docs", ({ id }) => id !== "index");
  return entries.map((entry) => ({
    params: { slug: entry.id },
    props: { entry },
  }));
}

const { entry } = Astro.props;
const { Content, headings } = await render(entry);
const pathname = `/${entry.id}`;
---
<DocsLayout title={entry.data.title} description={entry.data.description} {pathname} {headings}>
  <Content />
</DocsLayout>
```

- [ ] **Step 4: Implement the shared layout components**

The shell must expose:

```astro
<body data-docs-path={pathname}>
  <Header />
  <div class="docs-shell">
    <Sidebar currentPath={pathname} />
    <main id="main-content" class="article-column">
      <article class="prose"><slot /></article>
      <ArticleFooter currentPath={pathname} />
    </main>
    <PageOutline headings={headings} />
  </div>
</body>
```

Use semantic `nav`, `main`, `article`, and `aside` elements. Set canonical and Open Graph metadata from `Astro.site`, `Astro.url`, title, and description.

- [ ] **Step 5: Implement the complete CSS visual system**

Define color, type, spacing, border, radius, and layout tokens in `:root`; implement the three-column desktop grid, medium breakpoint without outline, mobile drawer state, article typography, code blocks, tables, blockquote callouts, footer links, and focus styles. Use system fonts only and include:

```css
:root {
  --page: #f7f5ef;
  --surface: #fffefa;
  --ink: #24231f;
  --muted: #6f6c63;
  --line: #dedbd1;
  --accent: #e5a900;
  --accent-soft: #fff3bf;
  --code: #1f211f;
}

@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    scroll-behavior: auto !important;
    transition-duration: 0.01ms !important;
  }
}
```

- [ ] **Step 6: Build and inspect generated routes**

Run: `cd docs && bun run check && bun run build`

Expected: Astro check and build succeed; `dist/index.html`, `dist/get-started/quickstart/index.html`, and `dist/404.html` exist.

- [ ] **Step 7: Commit the documentation shell**

```bash
git add docs/src/layouts docs/src/components docs/src/styles docs/src/pages docs/src/data/navigation.ts
git commit -m "docs: build responsive documentation shell"
```

---

### Task 3: Add accessible search and browser interactions

**Files:**
- Create: `docs/src/lib/search.ts`
- Create: `docs/tests/search.test.ts`
- Create: `docs/src/pages/search-index.json.ts`
- Create: `docs/src/components/SearchDialog.astro`
- Create: `docs/src/scripts/docs-ui.ts`
- Modify: `docs/src/components/Header.astro`
- Modify: `docs/src/layouts/DocsLayout.astro`
- Modify: `docs/src/styles/global.css`

**Interfaces:**
- Produces: `normalizeSearchText(markdown: string): string`.
- Produces: `/search-index.json` items `{ title, description, path, headings, text }`.
- Consumes: DOM elements identified by `data-search-*`, `data-drawer-*`, and `data-copy-code`.

- [ ] **Step 1: Write failing search normalization tests**

```ts
import { expect, test } from "bun:test";
import { normalizeSearchText } from "../src/lib/search";

test("removes frontmatter and Markdown punctuation", () => {
  const value = `---\ntitle: Hello\n---\n## Run Eggy\nUse \`make build\` and [open docs](/docs).`;
  expect(normalizeSearchText(value)).toBe("Run Eggy Use make build and open docs.");
});

test("collapses whitespace and strips fenced code markers", () => {
  expect(normalizeSearchText("```sh\\nmake build\\n```\\n\\nDone")).toBe("make build Done");
});
```

- [ ] **Step 2: Run the focused test and verify the missing module failure**

Run: `cd docs && bun test tests/search.test.ts`

Expected: FAIL because `src/lib/search.ts` does not exist.

- [ ] **Step 3: Implement deterministic search indexing**

`normalizeSearchText` removes frontmatter, HTML tags, fenced-code delimiters,
heading/list punctuation, Markdown links while retaining their labels, inline
code delimiters, and repeated whitespace.

`search-index.json.ts` loads the `docs` collection, renders headings, maps entry
IDs through the navigation catalog, and returns JSON with:

```ts
export const prerender = true;

export async function GET() {
  return new Response(JSON.stringify(items), {
    headers: { "Content-Type": "application/json; charset=utf-8" },
  });
}
```

- [ ] **Step 4: Implement accessible dialog, drawer, copy, and outline behavior**

Use the native `<dialog>` element for search. `Ctrl+K` and `Meta+K` open it,
Escape closes it, results are ordinary anchors, and an empty query explains
what can be searched. Fetch the index from
`${import.meta.env.BASE_URL}search-index.json`.

The mobile drawer traps focus while open, restores focus to its trigger, closes
on Escape and backdrop click, and toggles `aria-expanded`.

For every `pre > code`, append a labelled copy button; show "Copied" only after
`navigator.clipboard.writeText` resolves. Page-outline links update their
active state using `IntersectionObserver` without hiding content when the API
is unavailable.

- [ ] **Step 5: Run unit, check, and build verification**

Run: `cd docs && bun test && bun run check && bun run build`

Expected: tests, Astro check, and static build all PASS, and
`dist/search-index.json` contains all 20 navigation entries.

- [ ] **Step 6: Commit browser interactions**

```bash
git add docs/src/lib/search.ts docs/tests/search.test.ts docs/src/pages/search-index.json.ts docs/src/components/SearchDialog.astro docs/src/scripts/docs-ui.ts docs/src/components/Header.astro docs/src/layouts/DocsLayout.astro docs/src/styles/global.css
git commit -m "docs: add accessible documentation search"
```

---

### Task 4: Write operator documentation from shipped behavior

**Files:**
- Modify: `docs/src/content/docs/index.md`
- Modify: `docs/src/content/docs/get-started/*.md`
- Modify: `docs/src/content/docs/use/*.md`
- Modify: `docs/src/content/docs/configure/*.md`
- Modify: `docs/src/content/docs/operate/*.md`
- Create: `docs/tests/source-consistency.test.ts`

**Interfaces:**
- Consumes: current `README.md`, `config.example.yaml`, `internal/config`,
  `internal/commands`, `internal/web`, `internal/bootstrap`, and relevant
  `plugins`.
- Produces: complete operator-facing pages with exact commands and safe example values.

- [ ] **Step 1: Write the failing Telegram source-consistency test**

```ts
import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";

test("Telegram docs list every direct command exactly once", () => {
  const source = readFileSync("../internal/commands/commands.go", "utf8");
  const docs = readFileSync("src/content/docs/use/telegram.md", "utf8");
  const commands = [...source.matchAll(/\{Name: "([^"]+)"/g)].map((match) => `/${match[1]}`);
  expect(commands).toEqual(["/help", "/status", "/stop", "/clear", "/model"]);
  for (const command of commands) {
    const referenceRows = docs
      .split("\n")
      .filter((line) => line.startsWith(`| \`${command}`));
    expect(referenceRows).toHaveLength(1);
  }
});
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run: `cd docs && bun test tests/source-consistency.test.ts`

Expected: FAIL until the final Telegram reference has exactly one canonical
entry for each direct command.

- [ ] **Step 3: Write the introduction, quickstart, deployment, and usage pages**

Cover:

- Eggy as a single-owner Go daemon with Telegram and optional authenticated web chat;
- the read-only repository boundary;
- prerequisites, `cp` commands, `make build`, and the exact local run command;
- Railway Docker deployment, one replica, `/data` volume, healthcheck, injected `PORT`, and required environment variables;
- login, threads, streaming messages, approvals, providers/models/Calendar/MCP settings, and restart-required config changes in the web UI;
- all five Telegram commands, `telegram_select`, expiration, and non-approval semantics;
- model aliases, default restoration, and configured reasoning effort;
- direct Calendar reads versus payload-bound create/update/delete approvals.

Every claim must be verified against the current source before writing.

- [ ] **Step 4: Write the configuration pages**

Document every current top-level config group and distinguish YAML values from
environment-variable names. Include safe YAML fragments for:

- server and data directory;
- owner Telegram ID;
- agent model and timezone;
- providers and model aliases;
- repositories and read-only tools;
- runner restrictions;
- optional native Calendar;
- streamable HTTP and stdio MCP servers, filters, timeouts, failure cooldown,
  environment allowlist, and trust-at-configuration semantics.

- [ ] **Step 5: Write the operations pages**

Document:

- `/data/config.yaml`, context Markdown files, `eggy.db`, `state.json`,
  `auth.json`, `cron/`, `skills/`, `runs/`, and `logs/`;
- SQLite conversation persistence and the difference between clearing recent
  history and durable memory;
- `/healthz`, `/readyz`, Railway health behavior, and Telegram webhook acceptance;
- owner allowlisting, web session/login protection, encrypted OAuth records,
  provider secret isolation, trusted repositories, MCP trust, runner bounds,
  and Calendar approval isolation;
- symptom/cause/check/fix troubleshooting for startup config errors, unavailable
  optional tools, Telegram `204` without a reply, Calendar OAuth, MCP OAuth,
  nested base-path docs links, and Docker smoke availability.

- [ ] **Step 6: Run source consistency, all docs tests, check, and build**

Run: `cd docs && bun test && bun run check && bun run build`

Expected: all tests PASS and all operator pages build.

- [ ] **Step 7: Commit operator content**

```bash
git add docs/src/content/docs/index.md docs/src/content/docs/get-started docs/src/content/docs/use docs/src/content/docs/configure docs/src/content/docs/operate docs/tests/source-consistency.test.ts
git commit -m "docs: document Eggy operation and configuration"
```

---

### Task 5: Write contributor and architecture documentation

**Files:**
- Modify: `docs/src/content/docs/project/architecture.md`
- Modify: `docs/src/content/docs/project/adding-adapter.md`
- Modify: `docs/src/content/docs/project/local-development.md`
- Modify: `docs/src/content/docs/project/testing-releases.md`

**Interfaces:**
- Consumes: root `AGENTS.md`, `internal/ports/ports.go`,
  `internal/bootstrap`, package layout, `Makefile`, Docker smoke script, and CI.
- Produces: current contributor documentation without roadmap claims.

- [ ] **Step 1: Draft the architecture page from current packages**

Explain the one-way flow from entry points and web/commands through bootstrap,
kernel services, ports, and provider adapters. Include one maintainable Mermaid
overview showing Telegram/web input, composition root, agent loop, tools,
stores, external providers, and `/data`; do not create a package-by-package
dependency graph.

- [ ] **Step 2: Document extension rules with a concrete adapter walkthrough**

Use the current `AGENTS.md` rules:

1. select or add one narrow provider-neutral port;
2. create `plugins/<category>/<provider>/`;
3. keep credentials and wire types in the plugin;
4. wire only in `internal/bootstrap`;
5. put config parsing in `internal/config`;
6. add adapter tests and fake-adapter wiring where needed.

Explicitly document the `services` versus `services/repo` dependency direction,
the provider-neutral kernel boundary, the closed-for-modification intent, and
the independent Calendar approval executors.

- [ ] **Step 3: Document local development and required verification**

Include Go 1.26 and Bun prerequisites, safe local config setup, build/run
commands, focused tests, `make fmt vet test race build`, Docker-dependent
`make smoke`, CI job responsibilities, and the rule that `/data/state.json`
schema changes require compatibility or an explicit migration.

- [ ] **Step 4: Run all docs verification**

Run: `cd docs && bun test && bun run check && bun run build`

Expected: all tests and static build PASS with all contributor pages present.

- [ ] **Step 5: Commit contributor content**

```bash
git add docs/src/content/docs/project
git commit -m "docs: document Eggy architecture and development"
```

---

### Task 6: Validate links and package the nested GitHub Pages artifact

**Files:**
- Create: `docs/scripts/validate-built-site.ts`
- Modify: `docs/package.json`
- Create: `.github/workflows/docs-pages.yml`
- Modify: `.github/workflows/ci.yml`
- Modify: `docs/README.md`

**Interfaces:**
- Consumes: `flatNavigation`, `docs/dist`, and the `/eggy/docs/` base.
- Produces: a Pages artifact root containing `docs/index.html`,
  `docs/search-index.json`, route directories, assets, `docs/404.html`, and
  root `.nojekyll`.

- [ ] **Step 1: Extend navigation tests to reject non-base-safe authored links**

Scan Markdown links and fail internal absolute links that start with `/` but
not `/eggy/docs/`, while allowing relative links, fragments, and external
URLs.

- [ ] **Step 2: Run the test and verify it catches an injected bad-link fixture**

Temporarily add `[bad](/quickstart/)` to a local test fixture, confirm the test
fails with the bad URL, then remove the fixture before implementation.

- [ ] **Step 3: Implement generated-site validation**

`validate-built-site.ts` must:

- assert one generated `index.html` for every navigation path;
- parse local `href` values from generated HTML;
- ignore fragments, `mailto:`, `tel:`, and external URLs;
- strip `/eggy/docs/` before resolving a link within `docs/dist`;
- map directory links to `index.html`;
- fail with the source page and broken target;
- assert exactly 20 items in `search-index.json`;
- assert generated asset and canonical URLs contain `/eggy/docs/`.

- [ ] **Step 4: Add the build-and-package script**

Add `build:pages` to `docs/package.json`:

```json
{
  "scripts": {
    "build:pages": "astro build && bun run validate && rm -rf pages-root && mkdir -p pages-root/docs && cp -R dist/. pages-root/docs/ && touch pages-root/.nojekyll"
  }
}
```

The destructive target is the explicit `docs/pages-root` build artifact only.
Add `dist/` and `pages-root/` to `docs/.gitignore`.

- [ ] **Step 5: Add the Pages workflow and CI docs build**

The deployment workflow must:

- trigger on pushes to `main` that affect `docs/**` or itself, plus manual dispatch;
- grant `contents: read`, `pages: write`, and `id-token: write`;
- use `actions/checkout@v4` and `oven-sh/setup-bun@v2`;
- run `bun install --frozen-lockfile`, `bun test`, `bun run check`, and
  `bun run build:pages` from `docs/`;
- upload `docs/pages-root` using `actions/upload-pages-artifact@v3`;
- deploy with `actions/deploy-pages@v4`.

Add a non-deploying docs job to the existing CI workflow using the same install,
test, check, and normal build commands.

- [ ] **Step 6: Replace the starter README with exact docs commands**

Document:

```sh
cd docs
bun install
bun run dev
bun test
bun run check
bun run build
bun run build:pages
```

Explain that normal output is `docs/dist`, while `build:pages` packages the
site under `docs/pages-root/docs` so GitHub Pages serves it at
`/eggy/docs/`.

- [ ] **Step 7: Run focused Pages verification**

Run:

```sh
cd docs
bun test
bun run check
bun run build:pages
test -f pages-root/docs/index.html
test -f pages-root/docs/search-index.json
test -f pages-root/docs/get-started/quickstart/index.html
test -f pages-root/.nojekyll
```

Expected: every command exits 0.

- [ ] **Step 8: Commit deployment and validation**

```bash
git add docs/scripts docs/package.json docs/bun.lock docs/.gitignore docs/README.md .github/workflows/docs-pages.yml .github/workflows/ci.yml docs/tests/navigation.test.ts
git commit -m "ci: deploy Eggy docs to GitHub Pages"
```

---

### Task 7: Final visual and repository verification

**Files:**
- Modify as needed: files changed in Tasks 1–6

**Interfaces:**
- Consumes: complete docs build and repository verification commands.
- Produces: verified static documentation ready for review and publication.

- [ ] **Step 1: Serve the production build at its real nested path**

Run `cd docs && bun run preview`, then open:

- `/eggy/docs/`

Astro preview must serve the same `dist` files copied into the Pages artifact,
with `/eggy/docs/` preserved for assets and internal links.

- [ ] **Step 2: Inspect representative desktop pages**

Verify Introduction, MCP servers, Security model, Architecture, and 404 pages
at a desktop viewport. Confirm header, sidebar current state, readable line
length, code overflow, tables, callouts, right outline, previous/next links,
and no horizontal page overflow.

- [ ] **Step 3: Inspect representative mobile pages**

At a 390-pixel-wide viewport, confirm the drawer opens and closes by button,
Escape, and backdrop; focus returns to the trigger; the article and code blocks
fit; and the right outline is absent.

- [ ] **Step 4: Exercise browser interactions**

Confirm `Ctrl+K`/`Meta+K`, query matching, empty search guidance, result
navigation, code copying, outline anchors, and ordinary navigation with
JavaScript disabled.

- [ ] **Step 5: Run complete docs verification**

Run: `cd docs && bun test && bun run check && bun run build:pages`

Expected: all commands PASS and generated-site validation reports no broken
routes or links.

- [ ] **Step 6: Run repository-required verification**

Run: `make fmt vet test race build`

Expected: every target exits 0.

- [ ] **Step 7: Run Docker smoke when available**

Run: `docker info` and then `make smoke` only when the daemon is reachable.

Expected: smoke PASS, or record Docker unavailability as an environment blocker
without representing it as a passing test.

- [ ] **Step 8: Review the final diff for scope and stale content**

Run:

```sh
git diff --check
git status --short
git diff --stat HEAD~6..HEAD
rg -n "TODO|coming soon|planned|future feature" docs/src/content/docs
```

Expected: no whitespace errors, no unintended `website/` or Go runtime changes,
and no roadmap language presented as shipped behavior.

- [ ] **Step 9: Commit any verification fixes**

```bash
git add docs .github/workflows/docs-pages.yml .github/workflows/ci.yml
git commit -m "docs: finish Eggy documentation site"
```
