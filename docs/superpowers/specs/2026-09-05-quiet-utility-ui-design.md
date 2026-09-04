# Quiet utility web UI

## Context

Eggy's authenticated web UI is functional and responsive, but its presentation
still reads like a generated dashboard. Repeated eyebrow labels, page headings,
card headings, descriptions, rounded panels, shadows, pills, and decorative
empty states compete instead of establishing a clear hierarchy. Settings show
technical setup detail before the owner's current state or intended action, and
several important controls rely on icon recognition or hover tooltips.

The product should feel like a quiet, polished utility. Chat and Settings should
be calm and task-oriented. Traces is the only developer-dashboard surface, and
it should use the restrained density associated with Stripe and OpenAI rather
than decorative analytics chrome. Mobile is a first-class layout, not a
compressed desktop view.

## Goals

- Make the primary action and current state obvious on every screen.
- Remove decorative and explanatory repetition that makes the UI feel
  generated.
- Give Chat, Settings, and Traces distinct interaction models while keeping one
  visual language.
- Use progressive disclosure for technical configuration.
- Make every screen usable at 320 CSS pixels without page-level horizontal
  overflow.
- Make the desktop chat sidebar resizable and remember its width per device.
- Preserve all existing backend behavior, APIs, capabilities, and safety
  invariants.

## Non-goals

- No new capability, configuration field, API endpoint, backend route, tool, or
  durable backend record.
- No UI framework, state-management library, router, analytics package, or
  visualization dependency.
- No change to authentication, approvals, chat persistence, model selection,
  trace recording, or configuration validation.
- No attempt to turn Settings into an operational dashboard.
- No repository mutation or shipping capability.

## Product character

The visual system is restrained and conventional:

- Neutral flat surfaces and one-pixel separators establish structure.
- Green is reserved for primary actions, focus, active state, and success.
- Red is reserved for destructive actions and failures.
- Shadows are reserved for overlays, menus, and the focused composer.
- Corner radii are modest; nested rounded panels are removed.
- Monospace is reserved for identifiers, model names, timings, token values,
  config, and payloads.
- Hierarchy comes from type scale, weight, spacing, and alignment rather than
  eyebrow labels, gradients, pills, or ornamental icons.

The radial page glow, decorative egg empty states, repeated uppercase eyebrow
labels, and redundant card headings are removed. Each page has one title, one
short purpose statement where needed, and one visually dominant action.

## Shared shell

Chat remains at `/`, Settings at `/settings`, and Traces at `/traces`. Existing
History API navigation and browser Back/Forward behavior remain authoritative.

Desktop uses quiet left-side navigation. Mobile uses compact sticky headers
with visible text labels and touch targets of at least 44 by 44 CSS pixels.
Back, New chat, Settings, and Traces must be understandable without hover
tooltips. The authenticated surfaces continue to use the existing theme and
session lifecycle.

Shared page-header, section, status, and disclosure patterns replace repeated
one-off card markup. A shared primitive is justified only when it removes more
markup than it adds and keeps screen-specific behavior visible at the call site.

## Chat

### Conversation navigation

The sidebar's primary action is an explicit **New chat** control. Conversation
rows show a title and useful recency; rename and delete remain in the row action
menu. The current conversation has one restrained active treatment.

On desktop, the sidebar is draggable horizontally. Its width is clamped between
240 and 420 CSS pixels, defaults to 288 pixels, and is stored in `localStorage`
as an ephemeral per-device preference. The resize separator:

- uses `role="separator"` and `aria-orientation="vertical"`;
- exposes the current, minimum, and maximum widths;
- supports Left and Right Arrow keys in eight-pixel steps;
- resets to 288 pixels on double-click; and
- installs pointer-move and pointer-up listeners only during an active drag.

The existing collapse control remains independent of width. Mobile uses a
fixed-width full-height drawer and does not expose resizing, because horizontal
dragging conflicts with touch navigation.

### Conversation content

The chat header shows the conversation title once. The `Conversation` eyebrow
is removed. A fresh empty chat focuses attention on the composer and uses one
brief prompt instead of a decorative egg illustration.

Assistant responses remain readable Markdown. User messages remain visually
distinct without oversized bubbles or ornamental avatars. Typing, errors, and
pending state stay close to the conversation they describe.

Approval requests remain inline. Their consequence is the first text in the
panel, followed by consistently ordered Approve and Reject actions. They use
semantic warning styling rather than a decorative callout treatment.

### Composer

The composer is the dominant Chat action: a plain text area, a clear send
button, and restrained focus treatment. Model, reasoning effort, and approval
mode appear in a subdued **Run settings** row below the input. Controls use
short text labels and values instead of icon-led pills. Their existing native
select behavior is preserved for mobile.

The composer remains pinned to the bottom of the viewport, includes safe-area
padding on mobile, wraps controls without clipping, and never creates
page-level horizontal overflow.

## Settings

Settings behaves like a native preferences screen, not a dashboard. Its
navigation is reduced to seven owner-oriented groups:

1. **Models** — providers, available-model discovery, and configured aliases.
2. **Connections** — Google Workspace and MCP servers.
3. **Capabilities** — the tools Eggy can call.
4. **Automation** — schedules, heartbeat, and the watch list.
5. **Permissions** — approval mode and pending approvals.
6. **Appearance** — theme selection.
7. **Advanced** — tracing configuration, raw YAML, and restart.

Each Settings destination has one page heading. Current configuration appears
first as plain rows with status and relevant actions. Add and edit forms are
hidden until the owner chooses **Add**, **Edit**, or **Configure**. Technical
fields such as endpoints, environment variable names, scopes, timeouts, and
filters live in an **Advanced options** disclosure within their owning flow.

Progressive disclosure changes presentation only. Every currently editable
field remains reachable, and all writes continue through the existing
`internal/config` web endpoints and validation.

Routine actions and destructive actions are visually separated. Save buttons
name the object they affect where ambiguity is possible. Success and error
messages appear beside the action that produced them. A recoverable API failure
does not clear entered form values.

Desktop keeps grouped left navigation and a readable content column. Mobile
uses a compact sticky header and section picker, one-column forms, full-width
primary actions, and no data table that requires horizontal page scrolling.

## Traces

Traces is Eggy's only developer-dashboard surface. It keeps high information
density while using flat surfaces, quiet separators, restrained type, and
semantic color.

The page header contains **Traces**, one short purpose statement, and Refresh.
Conversations remain the top-level grouping. Each conversation group summarizes
turn count, total duration, total tokens, and failures.

Desktop uses a full-width table. Mobile uses stacked summary rows with explicit
labels and no horizontal scrolling. Group and turn expansion controls are real
keyboard-operable buttons rather than click handlers on table rows.

Expanded turn information appears in this order:

1. outcome and summary metrics;
2. timing waterfall;
3. model and tool steps; and
4. request and response payloads.

Raw JSON and long payloads remain collapsed until requested. The waterfall
keeps the existing model/tool distinction, but color is semantic and secondary
to labels. Success and active state use green; failures use red; all other
states are neutral.

Loading, empty, disabled, incomplete, and failed states explain what happened
and provide the next available action. Payload panels retain bounded height,
wrapping, and internal scrolling.

## Responsive behavior

- The supported minimum viewport is 320 CSS pixels.
- No authenticated page may introduce page-level horizontal overflow.
- Desktop-only navigation is replaced, not merely squeezed, on mobile.
- Dense trace tables become labeled stacked rows below the existing small
  breakpoint.
- Forms render as one column below the small breakpoint.
- Fixed and sticky regions account for mobile safe-area insets.
- Interactive controls have at least a 44-pixel touch target unless they are
  inside a larger 44-pixel interactive row.
- Text and controls may wrap; primary actions must remain visible without
  horizontal panning.

## Accessibility

- Every icon-only control has an accessible name, but primary navigation does
  not depend on icon recognition.
- Keyboard focus remains visible against both themes.
- Sidebar resizing supports pointer and keyboard interaction.
- Disclosures expose expanded state with `aria-expanded` and control a named
  region.
- Trace group and turn expansion uses buttons with explicit expanded state.
- Status and error messages retain appropriate live-region semantics.
- Color is never the only indication of status or selection.

## Error handling and state

Existing API calls and server responses remain unchanged. Presentation state
such as open forms, disclosures, and selected Settings destination stays local
to the UI. Sidebar width is the only new persisted browser preference.

Session expiry continues to return the owner to login. Failed configuration
writes retain draft values and show the server's validation result beside the
relevant form. Trace-detail failures remain scoped to the expanded turn.

## Verification

Implementation uses test-first changes for behavior and interaction logic.
Verification includes:

- sidebar width clamping, keyboard adjustment, reset, and persisted fallback;
- Settings navigation grouping and disclosure semantics;
- Chat and Traces accessibility semantics;
- mobile markup contracts and overflow-resistant structure;
- existing routing, chat, trace, and contrast regression tests;
- `bun test` and the production website build;
- `GOCACHE=/tmp/eggy-go-cache GOTMPDIR=/tmp make fmt vet test race build`;
  `GOMODCACHE=/tmp/eggy-go-mod-cache` may be added when the host module cache is
  not writable; and
- `make smoke` when Docker is available.

A live desktop and mobile browser review is required when a browser connection
is available. If it remains unavailable, that limitation is reported rather
than represented as a visual pass.

## Deletion budget

- Production lines: net increase capped at zero. Resize and disclosure logic
  must be paid for by deleting repeated card chrome, decorative markup, verbose
  copy, obsolete comments, and superseded components.
- Config keys: zero.
- Registered tools: zero.
- Backend durable records: zero.
- Background loops or goroutines: zero.
- Backend API endpoints and routes: zero.
- Production dependencies: zero.
- One `localStorage` value may be added for the per-device sidebar width.

The change fails its scope constraint if it adds a second way to perform an
existing configuration write, changes backend behavior, or leaves production
UI code larger overall.
