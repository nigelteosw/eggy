# Trusted Temporal Context Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Eggy resolve relative Calendar dates from a trusted runtime clock instead of model knowledge.

**Architecture:** Bootstrap resolves the owner's IANA timezone and supplies a deterministic clock to the prompt builder and time-aware tools. The agent receives volatile temporal context, while `calendar_list` resolves named ranges in Go and returns the exact queried interval.

**Tech Stack:** Go 1.26, standard library `time`, existing ports-and-adapters runtime.

## Global Constraints

- Keep `internal/kernel` and `internal/ports` provider-neutral.
- Treat `calendar.timezone` as the owner timezone and fall back to `scheduler.quiet_hours.timezone` only when it is empty.
- Preserve existing explicit RFC3339 Calendar list calls and all Calendar mutation approvals.
- Resolve local-day boundaries with calendar arithmetic, not fixed 24-hour durations.
- Add behavior test-first and introduce no dependencies.

---

### Task 1: Inject trusted temporal context

**Files:**
- Modify: `internal/kernel/agent/prompt.go`
- Modify: `internal/kernel/agent/prompt_test.go`
- Modify: `internal/bootstrap/app.go`
- Modify: `internal/bootstrap/app_test.go`

**Interfaces:**
- Produces: `agent.TemporalContext{Now time.Time, Timezone string}` accepted by `BuildInstructions`
- Consumes: `App.now` and the resolved owner `*time.Location`

- [ ] **Step 1: Write failing prompt and bootstrap tests**

Assert the final instruction contains `current_time: 2026-07-19T12:34:56+08:00` and `timezone: Asia/Singapore`, and assert the app's provider request includes those values when `AppOptions.Now` is fixed.

- [ ] **Step 2: Verify the focused tests fail**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/kernel/agent ./internal/bootstrap -run 'TestBuildInstructions|TestAppComposes' -count=1
```

Expected: FAIL because `BuildInstructions` has no temporal input or output.

- [ ] **Step 3: Implement the temporal prompt layer**

Add:

```go
type TemporalContext struct {
    Now      time.Time
    Timezone string
}
```

Make it the final `BuildInstructions` argument and append the trusted temporal system message after durable context. Resolve the owner location in bootstrap, convert `App.now()` into it for every message and heartbeat, and strengthen the hard policy for relative dates.

- [ ] **Step 4: Verify and commit**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/kernel/agent ./internal/bootstrap -run 'TestBuildInstructions|TestAppComposes' -count=1
git add internal/kernel/agent internal/bootstrap/app.go internal/bootstrap/app_test.go
git commit -m "feat: inject trusted temporal context"
```

### Task 2: Add trusted time and Calendar range tools

**Files:**
- Modify: `internal/bootstrap/assistant_tools.go`
- Modify: `internal/bootstrap/assistant_tools_test.go`
- Modify: `internal/bootstrap/app.go`

**Interfaces:**
- Produces: `currentTimeTool(now func() time.Time, location *time.Location, timezone string) ports.Tool`
- Changes: `calendarTools` accepts the clock, location, and timezone

- [ ] **Step 1: Write failing tool tests**

With a fixed `2026-07-19T12:34:56+08:00` clock, assert `current_time` returns that timestamp. Assert `calendar_list` resolves `today`, `tomorrow`, and `this_week`, preserves explicit ranges, returns the interval envelope, and rejects mixed or incomplete modes.

- [ ] **Step 2: Verify the focused tests fail**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/bootstrap -run 'TestCurrentTimeTool|TestCalendarListRanges' -count=1
```

Expected: FAIL because the time tool and range mode do not exist.

- [ ] **Step 3: Implement deterministic tools**

Register `current_time` unconditionally. Change `calendar_list` to accept optional `range`, `from`, and `to`, validate exactly one mode, resolve local midnight boundaries with `time.Date` and `AddDate`, and return:

```json
{"calendar_id":"primary","from":"...","to":"...","timezone":"Asia/Singapore","events":[]}
```

- [ ] **Step 4: Verify and commit**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/bootstrap -run 'TestCurrentTimeTool|TestCalendarListRanges' -count=1
git add internal/bootstrap/assistant_tools.go internal/bootstrap/assistant_tools_test.go internal/bootstrap/app.go
git commit -m "feat: resolve trusted Calendar ranges"
```

### Task 3: Full verification and integration

**Files:**
- Modify only exact files required by verified failures.

**Interfaces:**
- Consumes: Tasks 1 and 2
- Produces: verified merged behavior

- [ ] **Step 1: Run required verification**

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod make fmt vet test race build
git diff --check
```

Expected: all commands exit successfully.

- [ ] **Step 2: Merge and verify**

Fast-forward `feat/trusted-temporal-context` into `main`, run `go test ./... -count=1` plus focused race tests, then remove the worktree and merged branch.
