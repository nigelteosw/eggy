# Compact Documentation Sidebar Design

## Goal

Refine Eggy's desktop documentation navigation to match the compact behavior
of the Coral reference: every existing navigation group remains visible in a
typical desktop viewport, the sidebar stays sticky beneath the header, and the
sidebar has no independent scrollbar.

The Eggy logo's inner dot must remain Eggy yellow in both light and dark mode.
It must not inherit the mint documentation accent.

## Desktop Sidebar

Keep the existing navigation groups, labels, ordering, links, active state,
and semantic markup. Change only presentation:

- reduce the sidebar width and typography to a compact documentation scale;
- tighten item, heading, and group spacing so the complete navigation fits;
- keep the sidebar sticky below the site header;
- remove vertical overflow and the sidebar scrollbar;
- allow the document page to own normal vertical scrolling.

The active-page mint chevron and text treatment remain unchanged in meaning.

## Responsive Behavior

The desktop rule applies above the existing mobile breakpoint. The mobile
drawer keeps its independent vertical scrolling because its available height
and navigation access pattern are different. The right-side page outline and
article behavior are unchanged.

## Brand Mark

Introduce a dedicated brand-yellow color token and use it for the inner dot of
the Eggy mark in both themes. Documentation accent tokens remain mint and
continue to control active links, focus treatment, and article accents.

## Verification

- Add a focused regression test that checks the desktop sidebar is
  non-scrollable and the brand mark uses the dedicated yellow token.
- Run the documentation test suite, Astro type checking, and production build.
- Inspect representative desktop and mobile layouts, confirming that:
  - the complete desktop navigation is visible without a sidebar scrollbar;
  - the sidebar remains sticky while the article scrolls;
  - the mobile drawer still scrolls when necessary;
  - the logo center remains yellow in light and dark mode.

## Scope

This change does not alter navigation content, routes, article layout,
documentation copy, mobile drawer behavior, or Eggy runtime code.
