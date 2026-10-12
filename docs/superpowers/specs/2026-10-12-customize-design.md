# Customize: skills and MCP

Status: proposed; not approved for implementation.
Date: 2026-10-12.

## Outcome

One Customize page where a user manages skills and MCP connections, instead of
editing files on disk and digging through Settings. A user can create, edit,
delete, enable, and disable a skill, and manage MCP and Google connections, from
the panel.

## Scope

In:

- A `/customize` page in the rail, below Scheduled, with Skills and Connectors
  tabs. The tab is kept in the URL query string.
- Skill create, edit, delete, enable, and disable in the panel.
- The existing `McpCard` and `GoogleCard` moved from Settings into the
  Connectors tab, unchanged. Settings no longer shows them, so there is one home.
- Bounded skill reads.
- Correcting the MCP docs' stale claim that remote calls get no per-call
  approvals.

Out (not planned; revisit only on real demand):

- Plugins or bundles. A skill in `skills/` plus an `mcp.servers` entry already
  gives everything a bundle would; bundling adds digests, ownership rules, and
  activation state for convenience only.
- Directory skills (`skills/<name>/SKILL.md`), references, and a `resource`
  argument on `skill_read`.
- An advanced MCP editor (filters, failure policy, `require_approval`), live
  runtime status, and test-connection.
- Revision tokens and conflict detection beyond the existing locked atomic write.

## Design

### Skills

Skills stay flat Markdown files in `skills/`, handled by
`internal/skills/store.go`.

- **Disable** is a `disabled: true` frontmatter key in the skill file itself.
  This means no new store, no SQLite table, and no migration. It survives backup
  as part of the file. Rolling back to an older binary is not safe for this
  flag: that binary ignores `disabled`, so every disabled skill becomes active
  again. Before a rollback, re-enable the skills you want kept off, or accept
  that they come back.
- `services.Skills.Enabled` filters out disabled skills before the 16 KiB index
  budget is applied, so disabled skills never crowd active ones out.
  `skill_read` refuses a disabled skill.
- **Admin listing vs. model view.** The panel lists every skill, including
  disabled and invalid ones, with a status of enabled, disabled, or invalid. The
  prompt index is not that list.
- **Bounded reads.** Enforce the 32 KiB limit while reading (`io.LimitReader`
  with limit+1), not after reading the whole file.
- **Writes** go through the existing `Write`/`Delete`, with locking, atomic
  replacement, and active-secret filtering. A rename is a delete plus a create.
- Skills are shared by the whole deployment: one account's disable applies to
  everyone, as with SOUL.md. The editor says so next to Save and Disable.

Agent Runner's superpowers skills will need a second skill source, an `fs.FS`
owned by the extension. That seam is not built here: it has no caller until
Agent Runner exists, and `AGENTS.md` forbids speculative lifecycle structure.
When Agent Runner lands, it adds the source then.

Endpoints, all requiring an authenticated session and passing CSRF checks:

- `GET /api/skills` lists skills with name, description, and status.
- `POST /api/skills` creates a skill.
- `GET /api/skills/{name}` reads one skill.
- `PUT /api/skills/{name}` replaces a skill, including its `disabled` flag.
- `DELETE /api/skills/{name}` deletes a skill and returns a result body (200),
  not 204, because the SPA's request helper always parses JSON.

Invalid files (oversized, malformed, or whose frontmatter name differs from the
file name) are listed with the reason, can be deleted, and cannot be opened or
toggled.

Names are validated by the store's existing rules after URL decoding. Request
bodies are capped.

### Connectors

Move the `McpCard` and `GoogleCard` components from `ConfigPage.tsx` into the
Connectors tab. There are no backend changes: config writes continue through
`internal/config`, and OAuth goes through the existing routes.

## Delivery

1. **Skills backend:** the frontmatter flag, filtering in `Enabled` and
   `skill_read`, bounded reads, and `internal/panel/skills.go` wired in
   bootstrap. Write the tests first, covering:
   - a disabled skill missing from the index and refused by `skill_read`;
   - a disabled skill using no index budget;
   - the model index sorted by name before the budget is applied;
   - an oversize file rejected without being fully read;
   - an invalid file listed as invalid without breaking the others;
   - mutations refused without a session.
2. **SPA:**
   - `CustomizePage.tsx` with a Skills tab (list, editor, enable toggle,
     delete confirmation) and a Connectors tab (the moved cards).
   - The route and the rail entry.
   - `ConfigPage.tsx` without the moved cards.
   - The MCP docs fix.

Run `make fmt vet test race build`, `bun test`, and `bun run build`, then check
both tabs in a browser at phone and desktop widths.

## Budget

Zero new config keys, tools, SQLite tables, durable forms, or background loops.
One new frontmatter key. A few hundred production Go lines plus one SPA page.
The cards are moved, not duplicated, so the deletion is their old place in
Settings.
