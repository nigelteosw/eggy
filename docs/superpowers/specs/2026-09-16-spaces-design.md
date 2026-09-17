# Spaces: identity, context, and permissions

Date: 2026-09-16

Status: design agreed in conversation; implementation not started. Detailed defaults below are design recommendations for review, not claims about existing behaviour.

## Outcome

Reuse Eggy's existing harness for personal and shared Spaces. Every turn has an authenticated origin, a verified speaker, a Space, and a conversation. These select the context and enforce the available actions. A Space is not another agent, runtime, provider, or process.

Delivery order revised with the owner on 2026-09-17: personal Discord DMs first; Telegram groups and Discord shared channels later. The shared identity/Space/permission foundation and owner editor are deferred with group support. Only Discord shared channels depend on that foundation. D&D tools and image generation are separate feature implementations that consume this foundation; they are not implemented by this plan.

## Personal Discord milestone (first)

The [personal Discord plan](../plans/2026-09-16-spaces-discord.md) adds verified one-to-one DMs for existing linked accounts using current Principal, stores, turn service, tools, and personal `/mode`. It does not require shared-Space authorization or its storage migration. If Spaces is already available at execution time, reuse personal scope; otherwise migrate these records together with existing personal surfaces when Spaces arrives.

Discord DM history is separate from Telegram/web conversations; personal memory stays account-scoped across those surfaces. The first release uses ordinary text DMs and existing textual owner commands, with private `/link` interception and panel-only approval decisions. Discord sends pending notices and receives approved outcomes through its original verified DM destination. Attachments and Discord application-command registration are outside the first release. Reject guild messages, threads, group DMs, bots, and webhooks before model work, even when their author is an owner.

Generalise the existing identity-link coordinator once, preserving Telegram compatibility. Add explicit personal Discord routing without letting invalid destinations fall back to Telegram. Recheck live ownership/linking before execution and delivery; remove no shared-scope safeguards from the future design to make DMs work.

The sections below describe the deferred shared-Space target. Group behaviour is not a first-release requirement or an available capability. The [foundation plan](../plans/2026-09-16-spaces.md) and [Discord shared-channel plan](../plans/2026-09-17-discord-shared-channels.md) implement that later target.

## Decisions made with the owner

1. Anyone may address Eggy inside an approved group, without registration.
2. One Telegram group or Discord channel maps to one shared Space. No cross-group sharing or bridging.
3. Eggy responds only to a mention, reply to Eggy, or explicit command addressed to Eggy.
4. Only directed messages are retained and used. A directed reply may include its single referenced message, never a surrounding-history fetch.
5. Only existing Eggy owners may administer Spaces or access internals. Platform group administrators gain no Eggy privileges.
6. One permission policy applies to everyone in a Space, including owners. Resource ownership checks can restrict an action to the speaker's own resource.
7. Each shared Space has persistent memory, inspectable/editable/clearable by owners, separate from personal memory.
8. Presets with customisation configure permissions. New capabilities are denied until explicitly enabled.
9. Approval requirements are per action. Routine granted writes may run automatically. Reuse the existing approval mechanism.
10. Shared-Space approvals are reviewed only in the owner web panel. Group users cannot approve or reject them.
11. Only owners with verified, linked platform identities may start private agent conversations. Guests gain no DM access from group participation.
12. Owner-configured usage limits include an explicit Unlimited choice. Unlimited does not bypass authorization, concurrency safeguards, or timeouts.
13. Owner conversations resolve to personal Spaces, retaining private behaviour and data.

## Architecture and trust

Keep `ports.Principal{AccountID}` as proof of a configured owner. Do not turn guests into synthetic accounts or use a Space creator's account to run group turns. Add a separate provider-neutral `TurnScope` containing a verified actor, Space ID/kind, conversation ID, and policy revision. Provider IDs are opaque strings in the kernel; wire parsing stays in adapters.

A verified owner may be recognised as the group speaker, but shared execution does not carry an owner Principal. An owner web request may inspect/manage a Space after owner authorization; execution of an approved shared action deliberately drops the approver's personal principal and restores the original shared scope. Owner-only ports continue failing closed without a Principal.

Personal Space IDs are deterministic, using `personal_` plus the SHA-256 hex digest of the configured account ID. Shared IDs are generated `space_` plus 16 random bytes encoded as hex. Neither platform names nor external IDs become filesystem paths. A personal Space is inferred from a currently configured owner; it is not a second account registry.

```text
Verified adapter event
  -> resolve linked owner DM OR approved shared binding
  -> reject unaddressed input / inactive Space / exhausted quota
  -> same turn service, scoped history + context + tool definitions
  -> authorize action + resource ownership + approval requirement
  -> same tool executor / model loop
  -> scoped persistence and verified originating destination
```

Missing scope is an error on production turn paths. Remove the destination's implicit Telegram fallback after converting internal producers. Unknown Space, missing policy, invalid config, or unavailable quota storage cannot fall back to personal/unlimited execution.

## Identity and ingress

An actor key is `(connection ID, provider user ID)`. Connection ID identifies the configured bot installation, not a display name. Display names and tags are presentation only. Guest actor keys support attribution, per-person limits, and ownership without a user account, membership list, web session, or personal Space. Do not merge identities across providers automatically.

Existing Telegram owner pairing remains the proof-of-control flow. Generalise its coordinator when adding Discord, retaining expiring hashed tokens, private-only redemption, unique bindings, and claim/finalize/release around SQLite and config writes. OIDC sign-in identity remains separate. Removed owners and unlinked identities lose DM access on the next admission; pending work rechecks current authorization.

For an unknown DM, give a small deterministic access response (rate-limited), without model calls, media downloads, history, or memory. The sole allowed non-owner DM operation is redeeming an owner-created linking token; this is authentication, not an agent conversation. Bots, webhooks impersonating humans, and Telegram anonymous/send-as-channel actors are rejected for shared agent turns in this version.

An owner approves a Space in the authenticated web panel by entering its exact platform IDs. The adapter validates accessible chat/channel metadata and type before activation, and the panel shows the verified name and audience. No public discovery ledger, guest invitation process, or group command can approve a Space.

Binding keys are `(connection, group ID)` for Telegram and `(connection, guild ID, channel ID)` for Discord. A Discord server is not itself a Space. Telegram topics and ordinary Discord threads get separate conversation IDs within their parent Space. Private Discord threads are excluded: they have a different audience and must not share parent-channel memory. No unsupported channel type is silently treated as a public channel.

Ordinary chatter is discarded before downloads, model requests, durable records, and traces. A quoted message is bounded, marked as untrusted quoted data, and accepted only from the current conversation/audience. No recursive reply expansion or cross-channel fetch. Attachments follow existing size, secret-filtering, and transient-image rules. Edited messages do not automatically retrigger paid work.

Group commands are a small explicit surface: `/ask`, `/help`, and `/space` initially. The latter reports the public purpose, enabled capabilities, retention behaviour, and whether usage is limited; no owner identities, secrets, internal paths, or administrative details. Unknown commands never pass to the owner command executor. Domain commands can be added with their capability. Mentions and commands targeting another bot are ignored.

## Permissions and approvals

Each supported tool action has exactly one of `deny`, `allow`, or `ask`. Absent entries mean deny. An action must also have a locally declared shared-Space safety contract; `ReadOnlyTool()` alone is insufficient because reads can disclose private data. Use an optional interface beside the tool implementation for eligible actions and preset metadata; no global duplicate list of all tool actions.

The existing `RunOptions.AllowedTools` narrows advertised schemas. Execution checks the current policy again, including direct tool calls and approval replay. A wildcard for a tool's future actions is not a valid stored permission. Persist presets as an explicit action map at save time; upgrading a preset or adding a tool does not grant anything automatically. Removed/unavailable actions remain visibly unavailable and cannot execute.

Shared action order is: valid active scope -> capability supported in shared Spaces -> explicit grant -> resource ownership/data boundary -> usage reservation if paid -> approval decision -> execution. `deny` cannot be overridden by approval. `ask` reuses `ApprovalToolCall`, `ApprovalService`, and `ApprovalToolExecutor`. `allow` does not relabel writes as reads.

Personal `/mode strict|normal|auto` retains its existing semantics. Shared Spaces use their action map instead, within the same gate. An owner's personal auto mode has no effect in groups. Group `/mode`, `/model`, `/restart`, config, pairing, OAuth, and approval commands are unavailable.

Shared memory is observable by other participants. It must not claim `InternalTool()` under the old owner-private rationale. Keep the single memory implementation, but classify shared writes as mutations through its Space-aware definition and use the action map to allow or ask. Only `MEMORY.md` is agent-writable in shared Spaces; persona/purpose is owner-managed. Personal USER/MEMORY semantics are unchanged.

The first eligible capabilities are scoped memory/recall plus conversation. Repository/terminal/files, general skills, Google, MCP, schedules, heartbeat, and administration are not eligible in shared Spaces. The editor cannot enable them. A future capability must enforce its own data boundary before becoming eligible; do not expose arbitrary resource selectors that its adapter cannot validate. D&D and image actions appear only after those capabilities exist and are configured.

Shared approvals bind Space ID, policy revision, conversation/destination, actor key, tool/action, exact arguments, expiry, and any existing external-grant generation. All currently configured owners may inspect and decide shared approvals in the panel. Personal approvals remain private to their owner. Decisions and execution are single-claim operations; concurrent owners cannot execute twice. Preserve existing per-operation payload binding.

At execution, revalidate policy and origin. Any policy revision change invalidates pending shared approvals, including permissive changes; owners request a fresh action. Reconstruct the original execution scope without lending owner authority. Deliver the bounded action result to its originating conversation only; the panel records status. A delivery failure never reruns the mutation. Unknown external execution outcomes require owner reconciliation, not automatic retry. No general-purpose durable job queue is introduced.

## Context, memory, and state

Personal documents remain at their existing account paths. Shared documents live at `<home>/spaces/<generated-id>/memories/`, using the same Markdown adapter, locks, atomic writes, size bounds, and active-secret filter. Shared SOUL.md is initialised from a packaged neutral persona, never copied from the deployment's owner-editable SOUL.md. Shared MEMORY.md contains facts for that Space; USER.md and WATCH.md are not loaded. Owner edits use the same service and filtering as agent writes.

History is keyed by `(space_id, conversation_id)`. Shared memory spans conversations inside that Space and the editor explicitly states that these conversations share an audience. Stored messages preserve their actor attribution. Recall, FTS search, summaries, compaction, traces, resets, usage, event deduplication, and approval execution must use Space scope. Search SQL filters by scope before ranking/limiting results.

Provider session/cache-routing keys also include Space and conversation. Incoming events claim the existing deduplication ledger durably before executing work. Completed or uncertain events are not automatically replayed after reconnect or a delivery failure. Unknown external outcomes remain owner-visible for reconciliation; this is not a promise of exactly-once delivery or execution by external providers.

No cross-Space memory search, history import, automatic copying, or personal-to-group recall. System prompts receive a small authoritative Space description and capabilities, separate from guest-provided content. Persona and memory text never authorize actions. General owner skills and repository manifests are omitted from group prompts.

Extend the existing stores; do not build parallel group-history or group-memory implementations. Rename ownership columns to `space_id` for messages, threads, conversation resets, traces, machine state, approvals, processed events, and proactive-message state; migrate existing account values to their personal Space IDs. Sessions, login identities, owner schedules, pairing records, and sealed outbound grants retain their existing owner/deployment semantics. Do not expose owner schedules to shared Spaces just because their runtime state is scoped.

The schema migration runs transactionally, verifies counts and relationships, and raises `MachineStateVersion` from the inspected value 8 to 9 (choose the next unused version if another migration lands first). Preserve existing approval IDs/payload digests and legacy owner metadata for personal approvals. Existing Markdown paths need no migration. Back up `eggy.db` and owner documents before rollout; downgrade requires restoring the backup, not opening the new schema with an old binary.

## Policy storage and administration

YAML remains the policy/config authority, Markdown remains owner-facing context, SQLite remains machine-managed state. Add `spaces:` records to validated config; no parallel SQLite policy copy. All mutations go through `internal/config` under its existing file lock. Runtime reads validate fresh configuration and fail closed. SQLite stores counters, reservation/receipt records, approval state, and audit events, not a second authoritative policy.

Recommended configuration shape:

```yaml
spaces:
  - id: space_79c08ea88adc1674d544e5e1c43c0b177
    name: Friday campaign
    enabled: true
    revision: 1
    connection: telegram
    group_id: "-1001234567890"
    preset: chat
    model: default
    actions:
      memory:add: allow
      memory:replace: allow
      memory:remove: allow
      recall_conversation: allow
    limits:
      model_calls_per_day: 100
      requests_per_person_per_minute: 5
      images_per_day: 0
```

Action identifiers above use the existing tool/action names. Memory is already in context and has no read action; its three write actions accept only `file: memory` in shared Spaces. Discord records use `guild_id` and `channel_id` instead of `group_id`. IDs are strings to avoid numeric precision loss. `null` explicitly means Unlimited, `0` means no allowance, and omitted limits receive finite defaults. Saving a policy increments its revision server-side; conflicting edits return a conflict rather than overwrite. Runtime scope also binds a canonical hash of the effective policy, so direct YAML edits cannot evade invalidation by retaining an old revision number.

The Chat preset enables text conversation and scoped memory/recall. The D&D preset is unavailable until its required capabilities are installed/configured; selecting it must never pretend dice or images already work. Model choice is owner-managed per Space, fixed for guests. The editor lists unavailable capabilities with a short reason, without exposing credentials.

Any owner can manage any shared Space; this is not a new owner role hierarchy. Shared-Space admin routes retain current session, live account, CSRF, and origin checks. No guest-facing web UI. Panel tabs: Overview (binding, status, audience), Permissions (preset and allow/ask/deny), Memory, Usage, and activity/approvals using existing views. Approval and audit views identify the actor, Space, action, and outcome; durable logs apply the existing secret guard.

Start Spaces paused until metadata validation and an explicit owner save. Pause preserves data. Clearing memory/history is an explicit owner operation and increments the Space revision, cancelling old turns before they can write old context back. Removing a Space archives its documents and retains scoped machine state for owner review; no automatic destructive cascade. Recreating a Space uses a new ID and cannot accidentally resurrect old memory. Rebinding a configured Space to a different group is forbidden: create a new Space instead.

## Usage and scheduling

For a concrete first release, use a hard daily model-call allowance, a hard daily image-call allowance, and per-actor request rate limits. Display measured token usage separately. These are usage controls, not an exact currency spending guarantee; do not label model-call quotas as dollar budgets. Every model invocation counts, including tool-loop iterations, compaction, retries, and optional summarisation; avoid model-generated titles for group conversations. All provider calls must pass the one reservation hook.

Defaults proposed for new shared Spaces: 100 model calls/day, 5 directed requests/person/minute, images disabled. UTC day boundaries are visible in the panel. Unlimited is an explicit nullable value, never a storage/read-error fallback. Owners in groups use the same counters as guests. Private owner usage retains existing behaviour.

Reserve a quota slot atomically in SQLite immediately before each paid invocation. A reservation has a unique operation key and started/settled/uncertain outcome. Before outbound invocation mark it started; after a crash an uncertain started request remains charged. Release only a reservation proven not to have reached the provider. Quota failures produce bounded deterministic replies with no model call. Opportunistic expiry/retention runs on existing request paths; no quota worker loop.

Provider adapters enforce a per-response output limit for shared calls using a provider-neutral request field; unsupported backends cannot run shared calls until they support it. This bounds individual response size but does not promise an exact bill. Document the configured model and provider pricing as an operator concern. Token-threshold and currency-budget features are outside this first plan.

One active shared turn per Space initially; an incoming directed request while busy gets a deterministic retry response without model work or persistence as a pending prompt. Guests do not steer another person's turn. This deliberately avoids merging speaker identities and racing shared state. Different Spaces retain the existing runtime's concurrency bounds. No per-Space goroutine, queue, scheduler, or agent is created.

Before every model request, tool execution, approval execution, and delivery, check active binding and policy revision. Use deterministic tests for pause/edit races. A call already accepted by an external provider cannot be undone; cancellation blocks subsequent steps and suppresses stale delivery. Local writes use the same revision coordination as policy edits, with the config lock acquired before any SQLite transaction; never hold a database transaction during network I/O. Pause returns once the invalidation is published, and the panel distinguishes in-flight external work from fully idle state.

## Adapter details and documented constraints

Telegram privacy mode does not replace Eggy's own addressed-message filter. Bot administration/privacy settings affect which updates Telegram sends. Use exact message entities and verified reply author IDs, not substring checks. Validate group type and handle group-to-supergroup migration only from authenticated platform metadata; update the existing binding through config authority and increment revision, or pause it on conflict. [Telegram bot FAQ](https://core.telegram.org/bots/faq#what-messages-will-my-bot-get), [Telegram Bot API](https://core.telegram.org/bots/api).

Discord Gateway intake and HTTP interaction intake have different authentication mechanics; do not claim signature verification for Gateway events. The Gateway uses an authenticated bot connection; HTTP interactions require their documented signature checks if that transport is chosen. Message-content availability depends on intents and exceptions. Full reply-without-mention support must be tested with the deployment's granted intents; when unavailable, report setup incomplete rather than claiming replies work. Do not fetch surrounding channel history to compensate. Disable unintended mentions in outgoing messages. [Discord Gateway documentation](https://docs.discord.com/developers/events/gateway), [Discord message resource](https://docs.discord.com/developers/resources/message).

Discord construction and lifetime are owned by bootstrap. Its optional connection loop exists only when configured. No credentials or Discord wire types enter the kernel. Owner Discord linking uses stable user IDs, not tags. External bot creation, installation, permissions, secrets, and privileged intent approval remain operator setup tasks.

## Footprint and decisions deliberately revised

This is a feature expansion, not a line-count cleanup. Estimated net production addition: 600–1,100 lines for personal Discord first, 1,800–3,000 for deferred foundation/editor/Telegram, and 300–700 for the later Discord shared-channel extension, subject to repository review; estimates are not acceptance targets. Delete replaced owner-only routing branches, fixed-Telegram destination fallback, and duplicate scope resolution as each migration completes. Keep one loop, one context adapter, one history store, one approval gate, one config authority, one database, and no new native tools for Spaces itself.

Config budget: one `spaces` collection; 15 per-record keys including three platform binding fields, three nested limit keys, and the existing-style permission map; add one owner Discord ID field and one optional Discord config block in the personal Discord milestone, then reuse them for groups. Count the final actual keys in review. Durable additions are quota reservations/counters, shared policy audit records, and generalised pending identity links; existing records gain scope/actor metadata. Background loops: zero for core Spaces/Telegram, one optional Discord transport lifecycle. UI/API registration is absent on unconfigured channel capabilities; owner Space management reuses the existing authenticated panel and does not add prompt schemas.

Update AGENTS.md during implementation to explicitly replace private-only Telegram, owner-only ingress, account-only data scope, and the universal `/mode` statement with these precise shared-Space rules. Amend the memory `InternalTool()` rationale for shared writes. Record Discord as the requested channel exception. Preserve the prohibitions on multi-agent routing, child agents, runtime plugins, repository shipping, and alternative persistence forms. Keep TODO.md for unfinished implementation only; do not duplicate the design there.

## Acceptance

Tests must prove anonymous DMs and unapproved groups cost no model/tool/media work; owners in groups cannot access personal context; ordinary chatter leaves no durable content; equal conversation IDs across Spaces cannot collide; guest writes retain actor ownership; newly added tools remain denied; stale approvals and pause races fail closed; concurrent quota reservations cannot overspend counts; and existing owner sessions, private histories, modes, schedules, and OAuth grants survive migration unchanged.

Required implementation checks: focused tests first, then `make fmt vet test race build`, `cd website && bun test`, and `make smoke` when Docker is available. Record unavailable Docker as a blocker. This design-writing turn does not run or claim implementation verification.
