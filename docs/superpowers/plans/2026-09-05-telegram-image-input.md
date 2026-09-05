# Telegram Image Input Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Eggy receive one Telegram photo or image document with an optional caption and send it to the selected vision-capable model in the same owner turn.

**Architecture:** Extend Eggy's existing provider-neutral message with optional image parts, have the Telegram adapter download and validate the selected file, carry the complete message through the existing event and turn path, and translate image parts to OpenAI-compatible base64 `image_url` content. Persist and trace only caption text plus an attachment marker; keep image bytes in memory for the active turn.

**Tech Stack:** Go 1.26 standard library, Telegram Bot API, OpenAI-compatible Chat Completions JSON, existing Eggy event/turn/conversation architecture.

**Spec:** `docs/superpowers/specs/2026-09-05-telegram-image-input-design.md`

## Global Constraints

- Support exactly one Telegram image sent as either a compressed photo or an image document.
- Accept JPEG, PNG, WebP, and GIF only, with a fixed 20 MB pre-base64 limit.
- Keep Telegram file IDs, paths, URLs, and credentials inside `plugins/channels/telegram`.
- Keep image bytes current-turn-only; never write them to SQLite, traces, logs, search, or durable context.
- Preserve string-valued provider `content` for every text-only message.
- Add no config keys, tools, routes, stores, goroutines, durable record types, or background loops.
- Do not add model capability discovery or an OCR fallback; preserve the provider's existing error path for a non-vision model.
- Preserve webhook authentication, owner allowlisting, update deduplication, timeouts, and cancellation.

---

### Task 1: Provider-neutral image parts and provider translation

**Files:**
- Modify: `internal/ports/ports.go:24-30`
- Modify: `plugins/models/openaicompat/model.go:31-96`
- Test: `plugins/models/openaicompat/model_test.go`

**Interfaces:**
- Produces: `ports.ContentType`, `ports.ContentTypeImage`, `ports.ContentPart`, and `ports.Message.Parts []ContentPart`.
- Produces: OpenAI-compatible text-first multipart request content for messages carrying image parts.
- Preserves: `ports.Message.Content string` and string-valued provider content for text-only messages.

- [ ] **Step 1: Write the failing multipart translation test**

Add a test that captures and structurally decodes the outgoing request:

```go
func TestModelTranslatesImagePartsToMultipartContent(t *testing.T) {
	var body []byte
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ = io.ReadAll(request.Body)
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"seen"}}]}`), nil
	})}

	_, err := New("https://openrouter.ai/api/v1", "key", client).Generate(context.Background(), ports.ModelRequest{
		Model: "openai/gpt-5.6-luna",
		Messages: []ports.Message{{
			Role: ports.RoleUser, Content: "read this list",
			Parts: []ports.ContentPart{{Type: ports.ContentTypeImage, MediaType: "image/png", Data: []byte("png")}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"text","text":"read this list"},{"type":"image_url","image_url":{"url":"data:image/png;base64,cG5n"}}]`
	if string(request.Messages[0].Content) != want {
		t.Fatalf("content=%s want=%s", request.Messages[0].Content, want)
	}
}
```

Extend `TestModelTranslatesChatCompletionAndUsage` to decode its first message's `content` and assert that it is the JSON string `"How is Eggy?"`, pinning the no-image wire format.

- [ ] **Step 2: Run the focused tests and verify RED**

Run:

```bash
go test ./plugins/models/openaicompat -run 'TestModelTranslates(ImagePartsToMultipartContent|ChatCompletionAndUsage)' -count=1
```

Expected: compilation fails because `ports.ContentPart`, `ports.ContentTypeImage`, and `Message.Parts` do not exist.

- [ ] **Step 3: Add the provider-neutral content types**

Add beside `ports.Message`:

```go
type ContentType string

const ContentTypeImage ContentType = "image"

type ContentPart struct {
	Type      ContentType `json:"type"`
	MediaType string      `json:"media_type"`
	Data      []byte      `json:"data"`
}

type Message struct {
	Role       Role          `json:"role"`
	Content    string        `json:"content,omitempty"`
	Parts      []ContentPart `json:"parts,omitempty"`
	Name       string        `json:"name,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
}
```

- [ ] **Step 4: Implement conditional provider content encoding**

Change `providerMessage.Content` from `string` to `any`. Add private provider content structs with exact Chat Completions keys:

```go
type providerContentPart struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	ImageURL *providerImageURL `json:"image_url,omitempty"`
}

type providerImageURL struct {
	URL string `json:"url"`
}
```

In `Generate`, leave `Content` as `message.Content` when `len(message.Parts) == 0`. Otherwise build a slice whose first item is `{Type: "text", Text: message.Content}` and whose remaining image items contain:

```go
"data:" + part.MediaType + ";base64," + base64.StdEncoding.EncodeToString(part.Data)
```

Reject an unknown part type, empty media type, or empty data with an error naming the invalid field but not including bytes. Decode provider response content back to a string by using a separate response message type, so accepting polymorphic request content does not weaken response decoding.

- [ ] **Step 5: Run the focused package tests and verify GREEN**

Run:

```bash
go test ./plugins/models/openaicompat -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the message contract and translation**

```bash
git add internal/ports/ports.go plugins/models/openaicompat/model.go plugins/models/openaicompat/model_test.go
git commit -m "feat: translate image message parts"
```

---

### Task 2: Bounded Telegram image download

**Files:**
- Create: `plugins/channels/telegram/media.go`
- Test: `plugins/channels/telegram/client_test.go`

**Interfaces:**
- Consumes: the existing `Client.call(context.Context, string, any)` Telegram Bot API helper.
- Produces: `func (c *Client) DownloadImage(ctx context.Context, fileID string, declaredSize int64, declaredMediaType string) (ports.ContentPart, error)`.
- Produces: private `const maxImageBytes int64 = 20 << 20` and strict MIME normalization for JPEG, PNG, WebP, and GIF.

- [ ] **Step 1: Write failing tests for a valid download and the size boundary**

Use an `httptest.Server` whose handler records both requests. Assert that `getFile` receives `file_id`, the second request is `/file/bottoken/photos/list.png`, and the returned part contains `ContentTypeImage`, `image/png`, and the exact bytes.

```go
part, err := NewClient(server.URL, "token", "42", server.Client()).DownloadImage(
	context.Background(), "file-1", int64(len(png)), "image/png",
)
if err != nil {
	t.Fatal(err)
}
if part.Type != ports.ContentTypeImage || part.MediaType != "image/png" || !bytes.Equal(part.Data, png) {
	t.Fatalf("part=%#v", part)
}
```

Add table cases proving a declared size above `maxImageBytes`, a streamed body of `maxImageBytes+1`, an unsupported declared MIME type, and downloaded text bytes all return an error. Assert error strings contain neither `token` nor `/file/bot`.

- [ ] **Step 2: Run the download tests and verify RED**

Run:

```bash
go test ./plugins/channels/telegram -run 'TestClientDownloadImage' -count=1
```

Expected: compilation fails because `DownloadImage` and `maxImageBytes` do not exist.

- [ ] **Step 3: Implement the Telegram-local downloader**

In `media.go`, validate `fileID`, `declaredSize`, and the optional normalized declared MIME type before network access. Call `getFile` and decode:

```go
var file struct {
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
}
```

Reject missing paths and a returned size above the limit. Construct the download request only from `c.baseURL`, `c.token`, and `file.FilePath`; do not accept a URL from the update. Read through `io.LimitReader(response.Body, maxImageBytes+1)`, reject non-2xx responses, and reject the extra byte.

Use `http.DetectContentType` plus known signatures to canonicalize to exactly `image/jpeg`, `image/png`, `image/webp`, or `image/gif`. When Telegram supplies a declared MIME type, require it to normalize to the detected type; accept `image/jpg` as an alias for `image/jpeg`. Return errors that identify the operation without interpolating the authenticated URL.

- [ ] **Step 4: Add and run a cancellation test**

Create a server handler that waits on `request.Context().Done()`, cancel the calling context, and assert `DownloadImage` returns `context.Canceled` through `errors.Is`.

Run:

```bash
go test ./plugins/channels/telegram -run 'TestClientDownloadImage' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the downloader**

```bash
git add plugins/channels/telegram/media.go plugins/channels/telegram/client_test.go
git commit -m "feat: download bounded Telegram images"
```

---

### Task 3: Normalize Telegram photos and image documents

**Files:**
- Modify: `internal/kernel/events/events.go:40-42`
- Modify: `plugins/channels/telegram/handler.go:23-165`
- Modify: `plugins/channels/telegram/telegram_test.go`
- Modify: `internal/bootstrap/app.go` at Telegram client/handler construction

**Interfaces:**
- Consumes: `ports.ContentPart` and `(*telegram.Client).DownloadImage` from Tasks 1 and 2.
- Produces: `events.Message{Text string, Parts []ports.ContentPart}`.
- Produces: Telegram-local `ImageDownloader` with `DownloadImage(context.Context, string, int64, string) (ports.ContentPart, error)`.

- [ ] **Step 1: Write the failing photo normalization test**

Add a fake downloader that records its arguments and returns a known content part. Send a webhook update containing two photo sizes and a caption:

```json
{"update_id":13,"message":{"message_id":5,"from":{"id":42},"chat":{"id":99},"caption":"read this list","photo":[{"file_id":"small","file_size":100},{"file_id":"large","file_size":300}]}}
```

Assert the downloader receives `large`, `300`, and `image/jpeg`; the event message contains the caption and returned part; and the sink receives exactly one event.

- [ ] **Step 2: Write the failing image-document and rejection tests**

Send a document update with `file_id`, `file_size`, `file_name`, `mime_type: "image/png"`, and a caption. Assert it downloads and enqueues like a photo. Add cases for `application/pdf`, missing MIME type with a non-image extension, and a downloader error; assert no event is enqueued and the response is non-2xx.

Add a photo-without-caption case asserting event text is `Describe this image.`. Preserve the existing text-message and callback cases unchanged.

- [ ] **Step 3: Run the handler tests and verify RED**

Run:

```bash
go test ./plugins/channels/telegram -run 'TestWebhook.*(Photo|ImageDocument|ImageDownload)' -count=1
```

Expected: compilation or assertion failure because the webhook has no downloader, caption, photo, document, or event parts.

- [ ] **Step 4: Extend the event and Telegram update shapes**

Add to `events.Message`:

```go
Parts []ports.ContentPart `json:"parts,omitempty"`
```

Add Telegram-local update fields for `Caption`, `Photo []photoSize`, and `Document *document`, where photo size contains `file_id` and `file_size`, and document contains `file_id`, `file_name`, `mime_type`, and `file_size`.

Add `downloader ImageDownloader` to `WebhookHandler` and its constructor. Update bootstrap to pass the already-created Telegram client; fake-adapter mode may pass `nil` only when it cannot receive a real webhook.

- [ ] **Step 5: Implement normalization without a second event path**

Make `normalize` accept `context.Context`. For a normal text message, retain the current payload. For a photo, select the largest entry (the last Telegram entry), download it as JPEG, and encode caption plus part. For a document, allow only a normalized image MIME or recognized image filename suffix, then let downloaded-byte validation make the final decision.

Use one helper that returns `events.Message` for text, photo, or document; do not add a separate media event type. Propagate errors to `ServeHTTP`, ensuring failed media never reaches the sink or starts an empty turn.

- [ ] **Step 6: Run all Telegram tests and verify GREEN**

Run:

```bash
go test ./plugins/channels/telegram -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Telegram normalization**

```bash
git add internal/kernel/events/events.go plugins/channels/telegram/handler.go plugins/channels/telegram/telegram_test.go internal/bootstrap/app.go
git commit -m "feat: accept Telegram image messages"
```

---

### Task 4: Carry complete owner messages through turns without persisting bytes

**Files:**
- Modify: `internal/bootstrap/app_events.go:49-66`
- Modify: `internal/kernel/services/turns.go:18-104`
- Modify: `internal/kernel/turns/turns.go:215-470`
- Modify: `internal/kernel/agent/loop.go:155-174`
- Test: `internal/kernel/services/turns_test.go`
- Test: `internal/kernel/turns/turns_test.go`
- Test: `internal/kernel/agent/loop_test.go`
- Test: `internal/bootstrap/app_test.go`

**Interfaces:**
- Consumes: `events.Message.Parts` and `ports.Message.Parts`.
- Changes: `OwnerMessage(context.Context, ports.Message, string) error`.
- Changes: `ActiveTurns.Steer(context.Context, ports.Message) bool`.
- Changes: `Loop.Run(context.Context, string, string, ports.Message, []ports.Message, agent.RunOptions) (agent.RunResult, error)`.
- Produces: private `durableMessageText(ports.Message) string`, returning trimmed text plus `[image attached]` when parts exist.

- [ ] **Step 1: Write failing agent-loop and active-steering tests**

Change one loop test to pass `ports.Message{Role: ports.RoleUser, Content: "inspect", Parts: parts}` and assert the fake model sees the same parts on its user message.

Add an `ActiveTurns` test that begins a steerable turn, steers a complete image message, drains `Pending`, and asserts the part bytes survive unchanged. Add a case proving an empty text message with image parts is accepted rather than rejected.

- [ ] **Step 2: Run the focused tests and verify RED**

Run:

```bash
go test ./internal/kernel/agent ./internal/kernel/services -run 'Test.*(Image|Steer)' -count=1
```

Expected: compilation fails while `Loop.Run` and `Steer` still accept strings.

- [ ] **Step 3: Change the internal turn signatures**

Make `Loop.Run` accept a complete input message, set its role to `RoleUser`, and append it when either trimmed content is non-empty or parts are present. Make `ActiveTurns.Steer` accept and queue the complete message under the same condition.

Make `turns.Service.OwnerMessage` accept `ports.Message`. Keep `ScheduledTurn` and `HeartbeatTurn` text-based at their public boundary, wrapping their text in a user message only when calling the shared `run` function.

- [ ] **Step 4: Write failing turn-policy tests**

Add tests proving:

```go
input := ports.Message{
	Content: "/status",
	Parts: []ports.ContentPart{{Type: ports.ContentTypeImage, MediaType: "image/png", Data: []byte("png")}},
}
```

does not execute the `/status` command and reaches the loop; and that recording this input writes `/status\n[image attached]` as the user message with no parts or bytes. Add a captionless case expecting `[image attached]`. Assert the trace input uses the same durable text.

- [ ] **Step 5: Implement command, persistence, and trace handling**

In the shared turn runner:

- Execute commands only when `len(input.Parts) == 0`.
- Pass the complete input into steering and the loop.
- Use `durableMessageText(input)` for conversation recording and trace input.
- Record a fresh text-only `ports.Message` rather than passing the transient input to `Conversation.Record`.
- Preserve the original caption for model input and web thread auto-titling.

Implement the helper with a constant marker:

```go
const imageAttachmentMarker = "[image attached]"

func durableMessageText(message ports.Message) string {
	text := strings.TrimSpace(message.Content)
	if len(message.Parts) == 0 {
		return text
	}
	if text == "" || text == "Describe this image." {
		return imageAttachmentMarker
	}
	return text + "\n" + imageAttachmentMarker
}
```

- [ ] **Step 6: Pass the complete event message through bootstrap**

In `processEvent`, construct:

```go
ownerMessage := ports.Message{Role: ports.RoleUser, Content: message.Text, Parts: message.Parts}
return a.turnService.OwnerMessage(destination.With(ctx, event.Destination), ownerMessage, source)
```

Do not change scheduled or pre-rendered message handling.

Add a bootstrap test whose fake model inspects its request and proves an image event reaches it once, while recent SQLite history contains only caption plus marker.

- [ ] **Step 7: Run the turn and bootstrap tests and verify GREEN**

Run:

```bash
go test ./internal/kernel/agent ./internal/kernel/services ./internal/kernel/turns ./internal/bootstrap -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit the complete-message turn path**

```bash
git add internal/bootstrap/app_events.go internal/bootstrap/app_test.go internal/kernel/services/turns.go internal/kernel/services/turns_test.go internal/kernel/turns/turns.go internal/kernel/turns/turns_test.go internal/kernel/agent/loop.go internal/kernel/agent/loop_test.go
git commit -m "feat: carry images through owner turns"
```

---

### Task 5: Owner documentation and complete verification

**Files:**
- Modify: `docs/src/content/docs/use/telegram.md`

**Interfaces:**
- Documents: photos and JPEG/PNG/WebP/GIF image documents, optional captions, 20 MB limit, current-turn-only behavior, and vision-model requirement.
- Verifies: all repository workflow targets required by `AGENTS.md`.

- [ ] **Step 1: Update Telegram owner documentation**

Add a concise "Images" section stating:

```md
## Images

Send one photo or image file with an optional caption. Eggy accepts JPEG, PNG,
WebP, and GIF images up to 20 MB and sends the image to the selected model for
that turn. The selected model must support image input.

Image bytes are not retained in conversation history. To ask about the same
image in a later turn, attach it again.
```

Use the repository's docs source of truth; do not hand-edit generated output unless the existing docs workflow requires it.

- [ ] **Step 2: Run formatting and focused regression tests**

Run:

```bash
make fmt
go test ./plugins/channels/telegram ./plugins/models/openaicompat ./internal/kernel/agent ./internal/kernel/services ./internal/kernel/turns ./internal/bootstrap -count=1
```

Expected: PASS with no formatting diff beyond task-owned files.

- [ ] **Step 3: Run the required full verification matrix**

Run from `/Users/nigel/Projects/Eggy/eggy`:

```bash
make fmt vet test race build
```

Expected: PASS. If a failure is unrelated or environmental, record its exact command and output and do not broaden this change.

- [ ] **Step 4: Run smoke validation when Docker is available**

Run:

```bash
docker info
make smoke
```

Expected: `make smoke` passes. If `docker info` reports an unavailable daemon, report smoke as blocked rather than passed.

- [ ] **Step 5: Inspect the final task-owned diff**

Run:

```bash
git status --short
git diff --check
git diff --stat HEAD
```

Confirm there are no unrelated files, image bytes, credentials, temporary media, generated caches, or unplanned schema/config changes.

- [ ] **Step 6: Commit documentation and any verification-only corrections**

```bash
git add docs/src/content/docs/use/telegram.md
git commit -m "docs: document Telegram image input"
```

Do not stage unrelated working-tree changes. If formatting touched task-owned Go files after their earlier commits, stage those explicit files in this final commit and name them in the handoff.

- [ ] **Step 7: Request final code review**

Use `superpowers:requesting-code-review` to review the complete range from the parent of the Task 1 commit through `HEAD`, checking the approved spec, security bounds, absence of durable image bytes, and test evidence. Address only findings within this feature's scope, then rerun the affected focused tests and the required full matrix before claiming completion.
