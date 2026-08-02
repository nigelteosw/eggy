# Eggy Documentation Site Design

Date: 2026-07-30
Status: shipped

One deviation from what is written below. This spec lists "Google Calendar" as
a Configure page. Native Calendar was removed on 2026-07-31, the day after this
was written, in favour of a configured MCP calendar server. The page shipped as
`configure/google-workspace.md` and is broader than planned: one Google grant
covering Gmail, Calendar, Drive, Docs, Sheets, and Contacts. The shipped docs
are correct; this document is not, and is kept as the record of the original
design rather than edited to match.

## Goal

Replace the bare Astro scaffold under `docs/` with a comprehensive static
documentation site for Eggy. The first release serves both operators and
contributors, while prioritizing the path from discovering Eggy to deploying
and operating a working single-owner instance.

The site documents only behavior shipped on the current branch. Planned work
from `TODO.md` must not appear as available functionality.

## Deployment

The documentation is a fully static Astro site deployed with GitHub Pages at:

`https://nigelteosw.github.io/eggy/`

Astro uses `/eggy/` as its production base path. Assets, navigation,
search results, canonical URLs, and internal links must work from that nested
path. A GitHub Actions workflow builds the site from `docs/` and deploys the
generated `docs/dist/` artifact. The site has no server runtime, database, CMS,
or dependency on a running Eggy instance.

## Information Architecture

The navigation is organized into five groups:

### Get started

- Introduction
- Quickstart
- Deploy on Railway

### Use Eggy

- Web chat
- Telegram
- Models and reasoning effort
- Approvals and protected actions

### Configure

- Configuration overview
- Model providers
- Google Calendar
- MCP servers
- Repository inspection

### Operate

- Persistence and memory
- Health checks
- Security model
- Troubleshooting

### Project

- Architecture
- Adding an adapter
- Local development
- Testing and releases

The introduction follows the useful structural pattern of the Coral
documentation landing article: a concise product explanation, a three-step
getting-started sequence, an overview of how the system works, and direct links
to common tasks. Eggy's content, terminology, examples, and identity remain
original.

## Content Sources and Accuracy

Documentation is derived from the current implementation, including:

- `README.md` and `config.example.yaml`;
- configuration types and validation in `internal/config`;
- Telegram commands in `internal/commands`;
- HTTP and authenticated web routes in `internal/web`;
- tool registration and runtime composition in `internal/bootstrap`;
- provider-neutral contracts in `internal/ports`;
- kernel service behavior and approval boundaries;
- compiled adapters under `plugins`;
- `Dockerfile`, `railway.toml`, `Makefile`, and current CI behavior.

The docs describe configuration fields, environment variables, commands,
tools, routes, persistence locations, extension boundaries, trust boundaries,
and known operational failure modes. Examples must not contain real
credentials. Optional capabilities must be clearly identified as optional and
their disabled behavior must be explained.

## Visual Design

The design takes structural inspiration from Coral's documentation without
copying its brand:

- near-black dark mode by default, with a persistent light-mode toggle;
- soft white typography, muted gray secondary text, and a mint-green accent;
- compact top bar with Eggy identity, docs label, search, GitHub link, and
  theme control;
- fixed grouped navigation on the left with generously spaced, sentence-case
  labels and a mint chevron for the current page;
- a central article column around 720 to 760 pixels wide;
- a sticky, unboxed "On this page" outline with indented nested headings and a
  mint chevron for the active section on large screens;
- a mobile navigation drawer and no right outline on narrow screens;
- crisp borders, restrained radii, and minimal shadows;
- high-contrast code blocks with copy controls;
- distinct note, warning, security, and optional-feature callouts;
- visible focus states, reduced-motion support, and accessible contrast.

The result should feel technical, quiet, and typography-led. It must avoid
decorative gradients, oversized marketing sections, excessive rounding, and
card-heavy layouts.

## Site Architecture

Documentation pages are Markdown content entries rendered through shared Astro
layouts. A single navigation definition is the source of truth for sidebar
groups, previous/next links, search metadata, and route validation.

Shared components provide:

- top navigation;
- desktop sidebar and mobile drawer;
- breadcrumbs;
- generated page outline;
- article layout;
- callouts;
- code-block copy controls;
- previous/next navigation;
- client-side search.

The search index is generated at build time from page titles, descriptions,
headings, and plain-text content. Search runs entirely in the browser and
returns base-path-safe links.

## Responsive and Accessible Behavior

Desktop uses the three-column documentation layout: navigation, article, and
page outline. Medium widths remove the outline. Mobile widths collapse the
sidebar into an accessible modal drawer.

Navigation and search must be usable by keyboard. The current page is exposed
semantically, focus is managed when the mobile drawer opens and closes, icon
buttons have accessible names, heading hierarchy is valid, and animation is
disabled or reduced when the user requests reduced motion.

## Error Handling and Resilience

- Unknown routes render a useful static 404 page with links back to the
  introduction and search.
- An empty search query shows guidance instead of an empty failure state.
- Search continues to allow navigation when JavaScript is unavailable because
  the sidebar and article links remain ordinary HTML links.
- External links are visually distinguished and internal links remain within
  `/eggy/`.
- Optional Eggy subsystems are documented with explicit prerequisites and
  disabled-state behavior to prevent misleading setup instructions.

## Verification

Before completion:

1. Build the docs from `docs/` with Bun.
2. Run Astro's static and type checks.
3. Validate that every navigation entry resolves to a generated page.
4. Validate internal links, unique routes, and unique navigation entries.
5. Check documented Telegram commands against the implementation.
6. Serve the production build from `/eggy/` and inspect
   representative desktop and mobile pages.
7. Confirm search, mobile navigation, page-outline links, copy buttons, 404
   behavior, and previous/next navigation.
8. Run the repository-required `make fmt vet test race build`.
9. Run `make smoke` when Docker is available; otherwise report the Docker
   environment blocker separately.

## Scope Boundaries

This work changes the documentation application and its GitHub Pages deployment
workflow. It does not change Eggy runtime behavior, introduce a documentation
backend, document roadmap items as shipped, or redesign the separate embedded
web application under `website/`.
