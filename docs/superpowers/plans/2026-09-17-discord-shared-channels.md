# Discord Shared Channels Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Use superpowers:subagent-driven-development only if the user explicitly chooses delegated execution. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the existing personal Discord adapter with shared channels on the completed Spaces foundation without introducing Discord-specific authorization in the kernel.

**Architecture:** One optional `plugins/channels/discord` adapter normalises authenticated Gateway events and delivers to verified destinations. Reuse the common Space resolver, shared harness, quotas, approval gate, owner panel, and generalised identity-link coordinator. Bootstrap owns the connection lifetime.

**Tech Stack:** Go 1.26, the existing Eggy stack, and one pinned Discord client SDK confined to the adapter. Use DiscordGo's low-level REST/Gateway bindings rather than implementing a second Gateway client; review and pin a release at implementation time in go.mod/go.sum. This is a transport dependency, not an agent framework. [DiscordGo upstream](https://github.com/bwmarrin/discordgo).

**Spec:** [Spaces design](../specs/2026-09-16-spaces-design.md). **Status:** deferred. **Prerequisites:** the [personal Discord plan](2026-09-16-spaces-discord.md) and all tasks in the [Spaces foundation plan](2026-09-16-spaces.md) pass. Telegram and Discord group delivery both follow personal Discord; their relative release order can be chosen when group work begins. Do not repeat adapter construction, owner linking, or personal routing from the first milestone.

## Global constraints

- One Telegram group or Discord channel maps to one shared Space. No cross-group sharing or bridging.
- Only existing Eggy owners may administer Spaces or access internals. Platform group administrators gain no Eggy privileges.
- Only directed messages are retained and used. A directed reply may include its single referenced message, never a surrounding-history fetch.
- No guest accounts, no guest DMs, and no group approval controls.
- Secrets stay operator-managed in the environment; no writes to .env files or secrets in config, traces, memory, or approval records.
- The adapter has no running connection, tool schema, or registered surface when unconfigured.
- No D&D/image capability is implemented by this adapter.

## Task 1: Adapter authentication, addressed intake, and delivery

**Files:** extend `plugins/channels/discord/client.go`, `handler.go`, `client_test.go`, `handler_test.go` from the personal milestone. Reuse its pinned SDK and transport lifecycle.

**Contract:** satisfy existing `ports.Channel` and optional typing/edit interfaces only if needed. Intake emits `events.Event` with Actor and explicit destination from the foundation. No provider SDK types cross the package boundary. Internally accept a narrow fakeable client interface:

```go
type Message struct {
    ID, GuildID, ChannelID, ParentChannelID, AuthorID, AuthorName string
    Content, ReferencedMessageID, ReferencedAuthorID string
    MentionsBot, IsBot, IsWebhook, IsPrivateThread bool
}
type Transport interface {
    Send(context.Context, string, string) (string, error)
    // channel ID, bounded text; production disables automatic mentions
    Close() error
}
```

These types are adapter-internal, not new kernel ports. Normalisation builds them from authenticated Gateway messages and interactions. Use SDK rate-limit/reconnect behaviour through a narrow lifecycle wrapper; do not build a parallel REST retry stack.

- [ ] Add table fixtures with expected admission and zero-cost rejection assertions:

```text
unknown owner DM -> deterministic denied, no agent turn
linked owner DM -> personal Space
guest + mention + approved channel -> shared Space
owner + mention + approved channel -> same shared policy, no owner Principal
guest + unapproved channel -> ignored
reply to Eggy -> admitted only when content is available
reply to another person without mention/command -> ignored
public thread -> verified parent binding, own conversation ID
private thread/group DM/webhook/bot -> denied
spoofed tag, channel name, quoted role claim -> no authorization effect
```

- [ ] Run `go test ./plugins/channels/discord` and verify fixtures fail before implementation.
- [ ] Implement normalisation, author/connection namespace, exact mention matching, command targeting, quote bounds, and parent-channel validation. Do not retain ordinary message bodies in SDK state caches or Eggy logs; disable message caching and inspect SDK defaults. No broad channel-history fetch.
- [ ] Process slash-command interactions through the same Gateway connection to avoid adding a second HTTP intake surface. Acknowledge/defer interactions according to the SDK/API contract before expensive work; admission still controls whether model work occurs. Use `/ask`, `/help`, `/space`, with `/link` private-only. Group approvals never become buttons or generic selections.
- [ ] Implement destination-bound text delivery with output splitting, disabled mass/role/user mention parsing, and current-policy checks before sends. A transport failure reports an error without invoking the model again. `DeliverApproval` is unavailable for shared destinations; personal Discord approvals retain their existing owner-panel flow; shared approvals also use the panel with the foundation's distinct shared authorization rules.
- [ ] Test missing message content explicitly. Do not silently mark setup healthy if reply-without-mention cannot work. Document required intent access for the promised behaviour; ordinary message bodies received due to platform requirements are discarded immediately.
- [ ] Re-run adapter tests. Suggested checkpoint: `feat: add Discord space channel adapter`.

## Task 2: Bootstrap lifecycle, metadata checks, and optionality

**Files:** create `internal/bootstrap/discord_spaces_test.go`; extend `internal/bootstrap/discord.go`, `discord_test.go`; modify `internal/bootstrap/app.go`, `app_events.go`, `spaces.go`, `internal/config/discord.go` and associated tests; extend the existing Space editor with Discord binding fields.

**Contract:** the adapter lifecycle is composed in bootstrap and feeds the existing event dispatcher; no new event loop for an agent. Metadata lookup validates the exact guild/channel ID and supported channel type before a Space activates. Settings hold IDs as strings end to end.

- [ ] Add fake-adapter tests for disabled/unconfigured Discord: zero construction, network, handler registration, bot commands, or goroutines. Enabled Discord starts one connection lifecycle; restart shuts it down once and creates one replacement.
- [ ] Run `go test ./internal/bootstrap ./internal/config -run 'Discord|Space'` before wiring.
- [ ] Wire credential/config construction, live identity resolution, metadata verification, event dispatch, and delivery in bootstrap. Reject forbidden channels before model work. On confirmed permission loss/removal, pause the affected Space through config authority; transient metadata/network errors fail closed without permanently rewriting policy; do not attempt delivery into a fallback channel.
- [ ] Respect Eggy's existing restart drain: stop accepting new channel work, let admitted turns finish under current policy, then close the transport. Shutdown/cancellation tests use channels/barriers. Keep paused/revoked spaces from completing stale deliveries during drain.
- [ ] Add an integration test where equal Discord/Telegram user IDs and equal conversation names cannot share counters, history, approvals, or personal identities. Test reconnect redelivery against the foundation's durable receipt mechanism.
- [ ] Re-run Go tests and the Space editor's Bun tests. Suggested checkpoint: `feat: wire optional Discord spaces into Eggy`.

## Task 3: Documentation and verification

**Files:** create `docs/src/content/docs/configure/discord.md`; modify `docs/src/content/docs/use/spaces.md`, `docs/src/content/docs/configure/accounts.md`, `AGENTS.md`, `scripts/docker-smoke.sh`.

- [ ] Document operator-owned bot creation/installation/token configuration, required channel visibility and messaging permissions, intents, owner linking, approval-channel isolation, and supported public text-channel/thread scope. No admin bot permission recommendation. Do not request broad guild-member access just to avoid registering guests; authenticated message authors are sufficient for basic attribution.
- [ ] Record the explicit Discord exception in AGENTS.md and the adapter-only dependency boundary. Add a fake Discord smoke scenario; no production token or external server mutation is needed for automated checks.
- [ ] Run focused adapter/bootstrap tests, `make fmt vet test race build`, and `cd website && bun test`. Run `make smoke` if Docker is available. Report unavailable live Discord credentials/intents and Docker separately from passing fake tests.
- [ ] When an operator supplies an already approved test server and credentials, verify mentions, plain replies, commands, public threads, unknown DMs, owner linking/unlinking, pause, quota exhaustion, and approval delivery only to the web panel. This live check is not permission to create accounts, install bots, or change a server on the user's behalf.
- [ ] Inspect task-owned diff and footprint. Suggested checkpoint: `docs: verify and document Discord spaces`.

## Deferred design checks before execution

- Public threads have separate recent history but share parent-Space memory and Space-scoped recall. The editor must state this audience boundary; private threads remain excluded.
- Acknowledge interactions within Discord's deadline, but deliver accepted shared results to the verified channel/thread using the bot. Interaction tokens are transient adapter data, never durable approval destinations. Test approval after token expiry.
- Validate thread-specific send permissions. Refuse delayed delivery into archived or locked threads initially; record delivery failure without reopening the thread or rerunning a mutation.
- Finalise whether the first shared release requires plain replies or begins with slash commands and explicit mentions. The original plain-reply requirement remains until the owner approves a narrower contract; verify granted message-content access before claiming support.
- Test that editing a message does not retrigger paid work, reconnect replay does not repeat an action, and one active thread makes the whole Space busy under the foundation's admission policy.

## References and completion criteria

Use current primary documentation during implementation: [Gateway](https://docs.discord.com/developers/events/gateway), [message resource](https://docs.discord.com/developers/resources/message), and [DiscordGo source](https://github.com/bwmarrin/discordgo). Gateway traffic is authenticated through the bot connection; HTTP signature verification applies to HTTP interaction intake, which this plan does not introduce.

Complete means the foundation's isolation contract passes unchanged through Discord, owner identities are linked by stable IDs, and the adapter is absent when unconfigured. D&D and image generation remain separate capabilities. Planning does not imply a passing live-platform check.
