# Source-Based Repository Tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make repository implementation tools available on direct owner messages without parsing the message text, while keeping scheduled and heartbeat turns read-only.

**Architecture:** Replace the `lane.Detect` text classifier with a source-based tool policy in bootstrap. Direct Telegram messages use the complete outer tool set; schedules and heartbeats continue to pass explicit read-only tool allowlists. The capability manifest remains derived from the actual filtered tool list.

**Tech Stack:** Go 1.26 standard library, existing Go test suite, file-backed Eggy runtime.

## Global Constraints

- Keep `internal/kernel` and `internal/ports` provider-neutral.
- Do not relax workspace, environment, timeout, output, process-group, protected-branch, or independent approval checks.
- Scheduled and heartbeat events must not expose `repository_modify` or `repository_continue`.
- Add behavior test-first and run focused tests before the full verification matrix.

---

### Task 1: Replace lexical lane filtering with source-based tool availability

**Files:**
- Delete: `internal/kernel/lane/lane.go`
- Delete: `internal/kernel/lane/lane_test.go`
- Modify: `internal/bootstrap/app.go`
- Modify: `internal/bootstrap/app_test.go`
- Modify: `internal/kernel/agent/prompt.go`
- Modify: `internal/kernel/agent/prompt_test.go`

**Interfaces:**
- Consumes: `events.TypeMessage`, `events.TypeSchedule`, and `agent.RunOptions`.
- Produces: direct owner messages whose tool definitions include `repository_modify` and `repository_continue`; scheduled and heartbeat requests whose definitions omit both tools.

- [ ] **Step 1: Write failing regression tests**

Add a bootstrap test that sends the direct message `yes make the change` and asserts the serialized model request contains `repository_modify` and `repository_continue`. Keep the existing scheduled-message assertion and extend it to assert both implementation tools are absent. Update the prompt-policy test to require source-based wording and reject the legacy `reads as an explicit implementation request` wording.

- [ ] **Step 2: Run focused tests to verify failure**

Run:

```bash
env GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/bootstrap ./internal/kernel/agent
```

Expected: the new direct-message test fails because `yes make the change` currently receives the Assistant lane and omits `repository_modify`.

- [ ] **Step 3: Implement the smallest source-based policy**

Remove `internal/kernel/lane`. In `App.processEvent`, direct message events call `handleMessage` with default `agent.RunOptions{}`; schedule events call it with an explicit allowlist that contains only the existing read-only tools. Change `handleMessage` to accept `agent.RunOptions` directly and use it unchanged for both `ToolNames` and `RunSelected`. Keep `handleHeartbeat`'s existing explicit read-only allowlist. Rewrite the hard runtime policy so tools are available for direct owner messages but are called only for an explicit owner request; it must state that shipping-readiness fields do not grant write access.

- [ ] **Step 4: Run focused tests to verify success**

Run:

```bash
env GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/bootstrap ./internal/kernel/agent
```

Expected: PASS. The direct confirmation request advertises both implementation tools; schedule and heartbeat requests remain read-only.

- [ ] **Step 5: Run the required verification matrix**

Run:

```bash
env GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod make fmt vet test race build
```

Then run `make smoke` if Docker is available.

- [ ] **Step 6: Commit**

```bash
git add internal/bootstrap/app.go internal/bootstrap/app_test.go internal/kernel/agent/loop.go internal/kernel/agent/loop_test.go internal/kernel/agent/prompt.go internal/kernel/agent/prompt_test.go internal/kernel/lane internal/kernel/services/implementer_test.go docs/superpowers/specs/2026-07-20-native-coding-harness-design.md docs/superpowers/specs/2026-07-21-durable-implementation-sessions-design.md docs/superpowers/plans/2026-07-21-source-based-repository-tools.md
git commit -m "fix: expose repository tools on owner messages"
```
