# Compact Documentation Sidebar Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the complete desktop documentation navigation visible without an internal scrollbar and keep the Eggy logo center yellow in both themes.

**Architecture:** Preserve the existing Astro components and navigation data. Prove the current rendered layout fails the requested behavior, then make a small token and desktop shell adjustment in the shared stylesheet and repeat the browser checks; mobile drawer behavior remains independent.

**Tech Stack:** Astro, CSS, Bun test

## Global Constraints

- Keep every existing navigation group, label, order, and route.
- The desktop sidebar stays sticky beneath the header and has no independent scrollbar.
- The page owns desktop vertical scrolling.
- The mobile drawer remains vertically scrollable.
- The logo inner dot uses dedicated Eggy yellow in light and dark themes.
- Do not change Eggy runtime code.

---

### Task 1: Compact Sidebar and Yellow Brand Mark

**Files:**
- Modify: `docs/src/styles/global.css`

**Interfaces:**
- Consumes: existing `.sidebar`, `.sidebar-nav`, `.nav-group`, and `.brand-mark span` selectors
- Produces: `--brand-yellow` CSS token and non-scrollable desktop `.sidebar` styling

- [ ] **Step 1: Verify the current rendered behavior fails**

Serve the current docs and inspect computed browser behavior at a desktop
viewport. Record the sidebar's `scrollHeight`, `clientHeight`, and computed
`overflowY`, plus the brand dot's computed color in dark mode.

Expected: the sidebar is independently scrollable and the brand dot is mint,
proving the current page fails the requested behavior.

- [ ] **Step 2: Implement the minimal CSS refinement**

In `docs/src/styles/global.css`:

- define `--brand-yellow: #dba400` in the root theme, matching Eggy's favicon;
- change `.brand-mark span` to `background: var(--brand-yellow)`;
- reduce `--sidebar-width` from `280px` to `240px`;
- change desktop `.sidebar` overflow to visible and compact its vertical
  padding;
- reduce desktop navigation group gaps, heading size/margin, item gaps, link
  padding, and link type size while preserving the active chevron;
- leave `.mobile-drawer` overflow unchanged.

- [ ] **Step 3: Run docs verification**

Run:

```bash
cd docs
bun test
bunx astro check
bun run build
```

Expected: all tests pass, Astro reports no errors, and the static build exits
successfully.

- [ ] **Step 4: Inspect the rendered result**

Serve the built docs and inspect desktop dark/light themes plus the mobile
drawer. Confirm all desktop groups fit without a sidebar scrollbar and the
logo center remains yellow.

- [ ] **Step 5: Run repository verification**

Run: `GOCACHE=/tmp/eggy-go-cache GOTMPDIR=/tmp make fmt vet test race build`

Run `make smoke` only if Docker is available; report an unavailable daemon as
an environment blocker.
