# Quiet Utility UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Eggy's chat, settings, and traces interfaces feel like one restrained utility console, remain usable at 320px, and add a desktop-resizable conversation sidebar.

**Architecture:** Keep the existing React pages, hash routing, API contracts, and CSS/Tailwind stack. Consolidate visual rules in `index.css`, keep page-specific behavior with its owning component, and expose only small pure functions for sidebar sizing and trace grouping so behavior can be tested without adding a UI framework.

**Tech Stack:** React 18, TypeScript, Tailwind CSS, Bun tests, Vite, Go embedded web UI

**Spec:** `docs/superpowers/specs/2026-09-05-quiet-utility-ui-design.md`

## Global constraints

- Production UI baseline is 4,919 lines across `website/src/**/*.{ts,tsx,css}`; the completed redesign must not exceed it.
- Add no dependencies, routes, config keys, tools, durable records, background loops, or API endpoints.
- Keep touch targets at least 44px and preserve a usable 320px viewport.
- Persist only the desktop conversation-sidebar width in `localStorage`.
- Preserve the current route behavior for `/`, `/settings`, and `/traces`.

### Task 1: Establish the quiet visual baseline and settings information architecture

**Files:**
- Modify: `website/src/ConfigPage.tsx`
- Modify: `website/src/index.css`
- Test: `website/tests/navigation.test.ts`
- Test: `website/tests/mobile-layout.test.ts`

- [ ] Add a failing navigation test that expects the seven user-facing settings groups: Models, Connections, Capabilities, Automation, Permissions, Appearance, and Advanced.
- [ ] Run `cd website && bun test tests/navigation.test.ts` and confirm it fails because the old MCP, Google, Tools, Tracing, and Approvals sections are still exposed as top-level navigation.
- [ ] Replace the nine-section settings navigation with seven groups and map the existing cards without changing their API behavior:

```tsx
const sections = [
  { id: "models", label: "Models", icon: ModelIcon },
  { id: "connections", label: "Connections", icon: ConnectionIcon },
  { id: "capabilities", label: "Capabilities", icon: ToolIcon },
  { id: "automation", label: "Automation", icon: AutomationIcon },
  { id: "permissions", label: "Permissions", icon: ShieldIcon },
  { id: "appearance", label: "Appearance", icon: AppearanceIcon },
  { id: "advanced", label: "Advanced", icon: AdvancedIcon },
] as const;
```

- [ ] Keep the desktop settings navigation labeled and visible; keep a sticky compact selector/header on mobile.
- [ ] Replace the radial canvas, floating panels, oversized rounding, and repeated eyebrow treatment with flat borders, neutral surfaces, and restrained elevation in `index.css`.
- [ ] Run the two focused tests and confirm they pass.

### Task 2: Add a desktop-resizable conversation sidebar

**Files:**
- Modify: `website/src/ThreadSidebar.tsx`
- Modify: `website/src/App.tsx`
- Test: `website/tests/sidebar-resize.test.ts`
- Test: `website/tests/mobile-layout.test.ts`

- [ ] Add failing tests for clamping widths below 240px and above 420px, retaining widths inside that range, and handling ArrowLeft, ArrowRight, Home, and End keyboard input.
- [ ] Run `cd website && bun test tests/sidebar-resize.test.ts` and confirm it fails because the sizing helpers do not exist.
- [ ] Export the minimal pure sizing behavior from `ThreadSidebar.tsx`:

```ts
export const SIDEBAR_MIN_WIDTH = 240;
export const SIDEBAR_DEFAULT_WIDTH = 288;
export const SIDEBAR_MAX_WIDTH = 420;

export function clampSidebarWidth(width: number): number {
  return Math.min(SIDEBAR_MAX_WIDTH, Math.max(SIDEBAR_MIN_WIDTH, width));
}
```

- [ ] Read a valid saved width once, update the desktop width during pointer drag, save on release, and reset to 288px on double-click.
- [ ] Add a keyboard-operable separator with `role="separator"`, `aria-orientation="vertical"`, `aria-valuemin`, `aria-valuemax`, and `aria-valuenow`; use 8px Arrow increments and Home/End bounds.
- [ ] Keep mobile as a fixed-width overlay drawer and hide the resize handle below the desktop breakpoint.
- [ ] Give New chat a clear labeled action while retaining the existing guard against creating duplicate empty chats.
- [ ] Run the focused tests and confirm they pass.

### Task 3: Simplify chat hierarchy and composer controls

**Files:**
- Modify: `website/src/ChatPage.tsx`
- Modify: `website/src/Composer.tsx`
- Modify: `website/src/index.css`
- Test: `website/tests/mobile-layout.test.ts`
- Test: `website/tests/new-chat.test.ts`

- [ ] Add failing render assertions for the direct page title, plain empty-state copy, semantic run-setting labels, and a composer that can fit at 320px without fixed-width children.
- [ ] Run the focused tests and confirm they fail on the old Conversation eyebrow, egg illustration, icon-only run settings, or fixed desktop arrangement.
- [ ] Remove the redundant Conversation eyebrow and assistant avatar; use readable message alignment, modest borders, and no decorative bubble shadows.
- [ ] Replace the egg empty state with a direct prompt explaining how to begin.
- [ ] Replace icon-led model, effort, and approval chips with compact labeled controls under a single Run settings row, preserving every existing option and request field.
- [ ] Stack or wrap composer controls on narrow screens, include safe-area bottom padding, and keep Send and menu controls at least 44px.
- [ ] Run the focused tests and confirm they pass.

### Task 4: Add progressive disclosure to configuration forms

**Files:**
- Modify: `website/src/ProvidersCard.tsx`
- Modify: `website/src/ModelsCard.tsx`
- Modify: `website/src/McpCard.tsx`
- Modify: `website/src/GoogleCard.tsx`
- Modify: `website/src/HeartbeatCard.tsx`
- Modify: `website/src/TracingCard.tsx`
- Modify: `website/src/AdvancedCard.tsx`
- Test: `website/tests/settings-disclosure.test.ts`

- [ ] Add failing server-rendered tests proving provider, model, MCP, and Google creation forms are hidden until their Add or Configure control is expanded and that technical details use native disclosure semantics.
- [ ] Run `cd website && bun test tests/settings-disclosure.test.ts` and confirm it fails against the always-open forms.
- [ ] Use native `<details>`/`<summary>` disclosure, avoiding a second state abstraction. Give each summary a clear action label and preserve form labels, validation, and submit behavior.
- [ ] Keep the current configured-item summaries visible at all times; move base URLs, environment variable names, timeouts, payload limits, tracing export details, and raw/diagnostic fields behind Advanced options.
- [ ] Keep destructive actions separate and explicit rather than hiding them in generic disclosure.
- [ ] Run the focused test and confirm it passes.

### Task 5: Make traces the sole developer-dashboard surface

**Files:**
- Modify: `website/src/TracesPage.tsx`
- Modify: `website/src/index.css`
- Test: `website/tests/traces-layout.test.ts`
- Test: `website/tests/trace-grouping.test.ts`

- [ ] Add failing tests that require real expansion buttons with `aria-expanded`, collapsed payload sections, and mobile labels for stacked trace fields.
- [ ] Run the focused tests and confirm they fail because clickable table rows are not keyboard controls and payload bodies open with the row.
- [ ] Remove the Observability eyebrow and present one title, compact summary metrics, filters, and a full-width flat trace table.
- [ ] Replace click-only `<tr>` behavior with buttons inside the primary cells for both conversation and turn expansion, preserving group behavior.
- [ ] Keep request, response, tool arguments, tool output, and raw JSON collapsed behind labeled disclosure controls.
- [ ] At narrow widths, render each row as a readable stacked record with field labels and no horizontal page overflow.
- [ ] Run the focused tests and confirm they pass.

### Task 6: Verify the deletion budget and repository

**Files:**
- Modify as needed: files changed above

- [ ] Run `cd website && bun test`.
- [ ] Run `cd website && bun run build`.
- [ ] Run `find website/src -type f \( -name '*.ts' -o -name '*.tsx' -o -name '*.css' \) -print0 | xargs -0 wc -l | tail -1` and confirm the result is no more than 4,919 lines.
- [ ] Run `GOCACHE=/tmp/eggy-go-cache GOTMPDIR=/tmp GOMODCACHE=/tmp/eggy-go-mod-cache make fmt vet test race build`.
- [ ] Run `git diff --check` and inspect `git diff --stat` plus `git status --short` for unintended files.
- [ ] Check Docker availability with `docker info`; run `make smoke` only when the daemon is available, otherwise report it as an environment blocker.
- [ ] Re-read the design spec and this plan, then verify each requirement against the final diff before reporting completion.
