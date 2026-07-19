# Disable Telegram Link Previews Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent Telegram link-preview crawlers from consuming Eggy's single-use Google Calendar enrollment links.

**Architecture:** Add the Telegram Bot API link-preview suppression option at the outbound text adapter boundary. Preserve the channel port, message text, OAuth enrollment, and approval behavior.

**Tech Stack:** Go 1.26, standard library HTTP/JSON, Telegram Bot API.

## Global Constraints

- Keep provider-neutral ports and kernel packages unchanged.
- Preserve independent approval checks and single-use OAuth enrollment behavior.
- Add behavior test-first and run the focused test before the full suite.
- Introduce no dependencies.

---

### Task 1: Disable previews for ordinary Telegram delivery

**Files:**
- Modify: `internal/adapters/channels/telegram/telegram_test.go`
- Modify: `internal/adapters/channels/telegram/client.go`
- Modify only if full verification reproduces the existing contention failure: `internal/kernel/services/agent_runtime.go`

**Interfaces:**
- Consumes: `(*Client).Deliver(context.Context, string, string) error`
- Produces: the existing Telegram `sendMessage` JSON payload with `link_preview_options.is_disabled=true`

- [ ] **Step 1: Write the failing request serialization test**

Extend the ordinary delivery test to decode the request body and assert:

```go
var payload struct {
    LinkPreviewOptions struct {
        IsDisabled bool `json:"is_disabled"`
    } `json:"link_preview_options"`
}
if err := json.Unmarshal(body, &payload); err != nil {
    t.Fatal(err)
}
if !payload.LinkPreviewOptions.IsDisabled {
    t.Fatal("ordinary delivery did not disable Telegram link previews")
}
```

- [ ] **Step 2: Verify the test fails for the missing option**

Run:

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/adapters/channels/telegram -run TestClientDeliversMessage -count=1
```

Expected: FAIL with `ordinary delivery did not disable Telegram link previews`.

- [ ] **Step 3: Add the minimal Telegram payload option**

Change ordinary delivery to send:

```go
return c.send(ctx, map[string]any{
    "chat_id": chatID,
    "text": text,
    "link_preview_options": map[string]bool{"is_disabled": true},
})
```

- [ ] **Step 4: Verify focused and full behavior**

Run:

```sh
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/adapters/channels/telegram -count=1
GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod make fmt vet test race build
git diff --check
```

Expected: all commands exit successfully.

If the race suite reproduces `state update remained conflicted after 8 attempts` under the existing 16-writer usage test, raise the bounded optimistic-lock attempt budget to 32 and verify that exact race test repeatedly. Eight attempts cannot guarantee progress among 16 simultaneous valid writers.

- [ ] **Step 5: Commit and merge**

```sh
git add internal/adapters/channels/telegram
git commit -m "fix: disable Telegram link previews"
```

Merge `fix/disable-telegram-link-previews` into `main`, repeat `go test ./... -count=1`, then remove the worktree and branch.
