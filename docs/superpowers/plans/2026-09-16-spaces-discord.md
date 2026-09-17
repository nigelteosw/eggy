# Personal Discord Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Use delegated execution only when the owner explicitly requests it. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let existing Eggy owners use private Discord DMs before implementing Telegram groups or Discord shared channels.

**Architecture:** Add one optional `plugins/channels/discord` adapter, composed in bootstrap, using the current owner Principal, dispatcher, turn service, account-scoped stores, tool gate, and owner panel. Generalise existing Telegram identity linking and extend explicit destination routing. No shared scope or guest execution is required for this milestone.

**Tech Stack:** Go 1.26, existing SQLite/Markdown/YAML adapters and panel, one pinned DiscordGo transport dependency confined to the Discord adapter. Verify the SDK release and current primary Discord documentation during implementation.

**Spec:** [Spaces design: delivery sequence and personal milestone](../specs/2026-09-16-spaces-design.md). **Status:** planning only; personal-first sequence approved on 2026-09-17.

## Delivery boundary

This plan has no dependency on the Spaces foundation. Telegram groups and Discord shared channels are deferred. The [Spaces foundation](2026-09-16-spaces.md) later supplies shared authorization and isolation; the [Discord shared-channel plan](2026-09-17-discord-shared-channels.md) extends this adapter after that foundation passes.

If Spaces already exists at execution time, use its personal scope. Otherwise preserve current account scope and let the later Spaces migration include Discord records. Never add a temporary second identity, context store, or agent loop.

## Global constraints

- Only currently configured, linked owners may start DM agent turns. Guild channels, threads, group DMs, bots, and webhooks cannot enter the harness, including messages from an owner.
- The only unauthenticated DM operation is redemption of an owner-created linking token. Linking text never reaches the model, history, traces, or logs.
- Personal memory, tools, and `/mode` retain existing account semantics. Discord conversation history is separate from Telegram and web; personal memory remains shared across the owner's personal surfaces.
- Approvals use the existing owner web panel and payload-bound executor. Discord sends a deterministic pending notice, with no decision buttons or text approval commands.
- Credentials remain operator-managed environment values. No bot installation, server mutation, deployment, production implementation, commit, or push is authorised by this planning document.
- Unconfigured Discord constructs no client, connection, handlers, channel-specific UI/API, or background work. Add no native tools or shared Space editor.
- Before each implementation task inspect the existing and staged diffs. Run focused failing tests before implementation and rerun them afterward. Suggested checkpoints require separate commit authorization.

## Task 1: Explicit personal destinations and channel routing

**Files:** modify `internal/kernel/destination/destination.go`, `internal/bootstrap/routed_channel.go`, `routed_channel_test.go`, `app_events.go`; extend destination tests and `internal/kernel/turns/turns_test.go`.

**Contract:** preserve `ports.Channel`. Add `destination.Discord` and opaque `ChannelID` to `Destination`; Discord's conversation key is `discord:dm:<channel-id>`. Owner identity continues to come from Principal, never from the destination. Keep existing Telegram/web record encodings and conversation IDs compatible. If the foundation's destination fields already exist, reuse them.

- [ ] Add destination and routing table tests:

```text
Discord DM C + owner A -> conversation discord:dm:C, Discord delivery only
Discord DM C + owner B -> independent account-scoped history
Discord with empty channel ID -> error before delivery
unknown destination kind -> error, never Telegram fallback
existing Telegram/web destination -> unchanged conversation and delivery
stored Discord approval -> retains exact DM destination across restart
```

- [ ] Run `go test ./internal/kernel/destination ./internal/bootstrap ./internal/kernel/turns -run 'Destination|Routed|Approval'`; confirm the new cases fail.
- [ ] Replace the router's web-versus-everything-else branch with explicit supported-kind dispatch. Require validated Discord destinations at ingress and delivery; a missing Discord target cannot become Telegram. Preserve legacy personal default decoding until the foundation migrates all producers.
- [ ] Keep proactive Telegram routing unchanged; do not redirect schedules or heartbeat based on the owner's most recent DM. Re-run focused tests.

## Task 2: Optional configuration and generalised owner linking

**Files:** create `internal/config/discord.go`, `discord_test.go`, `internal/bootstrap/identity_link.go`, `identity_link_test.go`, `internal/web/identity_link.go`, `identity_link_test.go`, `plugins/store/sqlite/identity_links.go`, `identity_links_test.go`; modify existing `internal/config/accounts.go`, `config.go`, `config_mutate.go`, Telegram pairing files in bootstrap/web/SQLite, `plugins/store/sqlite/machine.go`, `website/src/AccountsCard.tsx`, and `website/tests/accounts.test.ts`.

**Contract:** `DiscordConfig` contains `enabled` and `application_id`; connection ID is `discord`. Add `DISCORD_BOT_TOKEN` to `Secrets` and `Secrets.Values()`. Add unique opaque owner `discord_user_id`. Resolve it through the live account directory. Generalise the existing token coordinator/table with a connection discriminator; preserve existing Telegram routes and behaviour.

```go
CreateIdentityLink(context.Context, string, string, [32]byte, time.Time) error
// account ID, connection ID, token hash, expiry
ClaimIdentityLink(context.Context, string, [32]byte, time.Time) (string, [16]byte, bool, error)
// connection ID, token hash, now -> account ID, claim, found
FinishIdentityLink(context.Context, [16]byte, bool) error
DeleteIdentityLinks(context.Context, string, string) error
// account ID, connection ID
```

- [ ] Write failing tests for duplicate bindings, wrong-connection redemption, expired/replayed tokens, removed accounts, group redemption, disabled configuration, secret enumeration, and config-write failure followed by retry.
- [ ] Run `go test ./internal/config ./internal/bootstrap ./internal/web ./plugins/store/sqlite -run 'Discord|Pairing|IdentityLink|Secrets'`.
- [ ] Implement panel-authenticated token creation and private `/link <token>` redemption using existing claim/finalize/release semantics. Config writes use the existing lock and validation. Never span a config lock with a SQLite transaction.
- [ ] Migrate pending Telegram rows with `connection=telegram`; use the next unused `MachineStateVersion` at execution time. This is an identity-link migration, not a prerequisite Space migration. Test rollback/reopen and preservation of existing personal records.
- [ ] Add owner-panel linking/unlinking controls. Unlink cancels pending tokens and blocks future admission; recheck live identity before tool execution and delivery so in-flight work cannot retain revoked DM access. Work already committed externally cannot be undone.
- [ ] Re-run focused Go tests and `cd website && bun test tests/accounts.test.ts`.

## Task 3: Private DM adapter and existing approval flow

**Files:** create `plugins/channels/discord/client.go`, `handler.go`, `client_test.go`, `handler_test.go`; modify `go.mod`, `go.sum`; extend `internal/bootstrap/app_wiring.go`, `internal/kernel/turns/turns.go` and corresponding approval tests only where required for verified routing/revocation.

**Contract:** verified direct-message intake resolves the live owner and emits `events.Event{Owner, Source, Destination}` for the existing dispatcher. Provider types remain adapter-private. Use a narrow fakeable transport for sends/close and a pinned SDK for REST/Gateway lifecycle and rate limits. Use ordinary DM text and existing textual owner commands initially; `/link` is intercepted by the adapter. No application-command registration or interaction-token persistence is needed.

- [ ] Add fixtures:

```text
linked human + verified one-to-one DM -> existing owner turn
unknown human DM -> rate-limited deterministic denial; zero model/history/media work
valid private linking token -> coordinator only; zero agent work
owner in guild channel/thread/group DM -> ignored; zero agent work
bot/webhook/spoofed display name -> denied
owner A's DM -> no owner B memory, history, approval or mode
same numeric Telegram/Discord subject -> no automatic identity match
reply quote -> immediate same-DM reference only; no surrounding history fetch
```

- [ ] Run `go test ./plugins/channels/discord` and establish failing cases.
- [ ] Implement text-only DM intake, bounded same-DM quotes, explicit destinations, and transport-qualified event IDs such as `discord:message:<id>`. Reject unsupported attachments deterministically before download; attachment ingestion is outside this milestone.
- [ ] Do not retain rejected message bodies in SDK caches or logs. Verify SDK cache defaults. Use least required intents for personal DMs; no guild-content or member-list requirement for this milestone.
- [ ] Implement bounded text splitting and explicit disabled mention parsing on sends/edits. Report delivery errors; never invoke the model or tool again merely because output could not be sent.
- [ ] Implement `DeliverApproval` as a DM notice pointing to the existing authenticated owner panel. Preserve pending-record creation before notice delivery. Reject Discord `/approve` or `/reject` text and generic selections as decision paths; other supported personal commands reuse `internal/commands`.
- [ ] Add integration tests: owner B cannot decide owner A's approval; owner A's panel decision executes the existing payload-bound action and returns its result to the originating Discord DM; removal/unlink blocks pending execution/delivery; Telegram/web approval regressions remain green.
- [ ] Exercise reconnect replay against existing deduplication. Do not claim exactly-once external execution: inspect failures after committed mutations and ensure delivery errors cannot reopen them. If existing receipt semantics require repair, extend the current processed-event ledger with claim/outcome handling in place, with deterministic crash tests; do not pull in the whole Space migration or add another replay store.
- [ ] Re-run focused adapter, dispatcher, approval, and turn tests.

## Task 4: Bootstrap lifecycle, integration, and release verification

**Files:** create `internal/bootstrap/discord.go`, `discord_test.go`, `discord_integration_test.go`, `docs/src/content/docs/configure/discord.md`; modify `internal/bootstrap/app.go`, `app_events.go`, `routed_channel.go`, `internal/config/discord.go`, `docs/src/content/docs/configure/accounts.md`, `AGENTS.md`, `scripts/docker-smoke.sh`.

**Contract:** bootstrap constructs one optional Discord transport and wires it into the existing dispatcher and routed channel. Restart stops intake, drains admitted work under live authorization, then closes the old transport exactly once. No new agent loop, scheduler, or per-owner connection.

- [ ] Write deterministic barrier tests for disabled/unconfigured zero construction/network/registration, enabled single lifecycle, drain/restart, revoked owner during a turn, DM delivery failure, cross-owner isolation, and guild rejection.
- [ ] Run `go test ./internal/bootstrap ./internal/config -run 'Discord|Routed|Account|Approval|Restart'`, then implement wiring and rerun.
- [ ] Document operator bot setup, private linking, supported text commands, separate DM history/shared personal memory, panel approvals, and text-only scope. Keep Telegram private-only and identify both platforms' group support as deferred.
- [ ] Update AGENTS.md only for the requested personal Discord channel exception and transport boundary. Do not introduce shared ingress or alter personal `/mode` invariants in this milestone.
- [ ] Add fake Discord smoke coverage; run `make fmt vet test race build`, `cd website && bun test`, and `cd website && bun run build`. Run `make smoke` when Docker is available; report an unavailable daemon as blocked. Live Discord checks require operator-provided credentials and setup and are reported separately from fake tests.
- [ ] Inspect `git diff --check`, full task-owned changes, and staged filenames. Do not stage unrelated files.

## Footprint and completion

Estimated production addition: 600–1,100 lines, subject to implementation review, not an acceptance target. Delete the replaced router fallback branch and provider-specific duplication when generalising pairing. Add zero native tools, zero shared-Space config/records, one optional transport lifecycle, one Discord config block with two keys, one owner identity field, and one environment credential. Extend the existing pending-link table; any necessary receipt repair extends the existing ledger. No new durable storage form.

Complete means linked owners can converse privately, use their existing personal capabilities and panel approvals, and receive results in the correct DM; Telegram/web regressions pass; guild/group input does no agent work. No shared-Space migration, quotas, permissions editor, D&D, or image feature is part of this release. Execution defaults to inline checkpoints on a subsequent implementation instruction.

References to verify during implementation: [Discord Gateway](https://docs.discord.com/developers/events/gateway), [message resource](https://docs.discord.com/developers/resources/message), [DiscordGo](https://github.com/bwmarrin/discordgo).
