# Web Chat Interface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the owner have a full, live conversation with Eggy from a browser tab, sharing the exact same conversation Telegram already has, with typing indicators, editable run-progress messages, and in-chat approvals.

**Architecture:** A new `internal/adapters/channels/webchat` package implements `ports.Channel` over Server-Sent Events (a connection `Hub` broadcasts to every open browser tab). A new `multiChannel` in `internal/bootstrap` implements `ports.Channel` by fanning every call out to both the Telegram adapter and `webchat`, using a compound message-ID scheme so editable/trackable messages route back to each channel's own copy. New HTTP routes (`/api/chat/send`, `/api/chat/approve`, `/api/chat/stream`, `/api/chat/history`) enqueue through the exact same `app.Enqueue`/dispatcher path Telegram's webhook already uses — no parallel message- or approval-handling logic. The frontend gets a new `ChatPage` and becomes chat-first, with the existing config UI moved behind a settings toggle.

**Tech Stack:** Go 1.26 stdlib only (`net/http`, `http.Flusher` for SSE — no WebSocket library), React + TypeScript (existing `web/` project, no new frontend dependency).

## Global Constraints

- Full design spec: `docs/superpowers/specs/2026-07-23-web-chat-interface-design.md`.
- Do not change `ports.Channel`'s method signatures. Every existing adapter (Telegram, the existing `noopChannel`) must keep compiling and working unchanged.
- No new conversation history store in this plan — reads/writes go through the existing `State.RecentMessages`, exactly like Telegram. (The separate SQLite memory feature is already shipped and independent of this work.)
- `/api/chat/approve` must reach the exact same `app.handleApproval` code path a Telegram callback reaches — never a second, parallel approval-decision implementation.
- No WebSocket dependency, no client-side router library.
- A `Deliver`/`DeliverTrackable`/etc. call must never block the calling goroutine (the agent loop) waiting on a slow or stuck browser connection — all SSE broadcast sends are non-blocking.
- Every behavioral change is developed test-first; verify with `make fmt vet test race build` after every task.

---

### Task 1: `webchat.Hub` — SSE connection registry and non-blocking broadcast

**Files:**
- Create: `internal/adapters/channels/webchat/hub.go`
- Test: `internal/adapters/channels/webchat/hub_test.go`

**Interfaces:**
- Produces: `type Event struct { Kind EventKind; ID string; Text string; Approval *ApprovalPayload }`, `type EventKind string` (`EventMessage`, `EventTyping`, `EventEdit`, `EventApproval`), `type ApprovalPayload struct { ID, Summary string }`, `NewHub() *Hub`, `(*Hub) Register() (connID string, events <-chan Event, unregister func())`, `(*Hub) Broadcast(Event)`, `(*Hub) NextMessageID() string` — consumed by Task 2 (`webchat.Channel`) and Task 5 (the SSE HTTP handler).

- [ ] **Step 1: Write the failing tests**

```go
package webchat

import (
	"testing"
	"time"
)

func TestHubBroadcastsToEveryRegisteredConnection(t *testing.T) {
	hub := NewHub()
	_, eventsA, unregisterA := hub.Register()
	defer unregisterA()
	_, eventsB, unregisterB := hub.Register()
	defer unregisterB()

	hub.Broadcast(Event{Kind: EventMessage, Text: "hello"})

	for _, events := range []<-chan Event{eventsA, eventsB} {
		select {
		case event := <-events:
			if event.Kind != EventMessage || event.Text != "hello" {
				t.Fatalf("event=%#v", event)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for broadcast")
		}
	}
}

func TestHubUnregisterStopsDelivery(t *testing.T) {
	hub := NewHub()
	_, events, unregister := hub.Register()
	unregister()

	hub.Broadcast(Event{Kind: EventMessage, Text: "after unregister"})

	select {
	case event, ok := <-events:
		if ok {
			t.Fatalf("expected closed channel, got event=%#v", event)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected the channel to be closed promptly after unregister")
	}
}

func TestHubBroadcastNeverBlocksOnASlowReader(t *testing.T) {
	hub := NewHub()
	_, _, unregister := hub.Register() // never read from this connection's channel
	defer unregister()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			hub.Broadcast(Event{Kind: EventMessage, Text: "spam"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked on a slow/unread connection")
	}
}

func TestHubNextMessageIDNeverContainsReservedSeparators(t *testing.T) {
	hub := NewHub()
	for i := 0; i < 100; i++ {
		id := hub.NextMessageID()
		if id == "" {
			t.Fatal("expected a non-empty ID")
		}
		for _, reserved := range []byte{':', '|'} {
			for _, b := range []byte(id) {
				if b == reserved {
					t.Fatalf("id %q contains reserved separator %q", id, reserved)
				}
			}
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/channels/webchat/... -v`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Implement**

```go
// Package webchat implements ports.Channel over Server-Sent Events: a Hub
// broadcasts to every currently-open browser connection for Eggy's single
// owner. There is no per-connection identity beyond "currently open" -- if
// two tabs are open, both receive every push.
package webchat

import (
	"strconv"
	"sync"
	"sync/atomic"
)

type EventKind string

const (
	EventMessage  EventKind = "message"
	EventTyping   EventKind = "typing"
	EventEdit     EventKind = "edit"
	EventApproval EventKind = "approval"
)

type ApprovalPayload struct {
	ID      string
	Summary string
}

type Event struct {
	Kind     EventKind
	ID       string
	Text     string
	Approval *ApprovalPayload
}

// connectionBuffer bounds how many undelivered events a single slow or
// abandoned connection can accumulate before Broadcast starts dropping
// events for that connection specifically -- never blocking the caller.
const connectionBuffer = 32

type Hub struct {
	mu          sync.Mutex
	connections map[uint64]chan Event
	nextConnID  uint64
	nextMsgID   uint64
}

func NewHub() *Hub {
	return &Hub{connections: map[uint64]chan Event{}}
}

// Register opens a new connection and returns its event stream and an
// unregister function the caller must call exactly once (typically via
// defer) when the connection closes.
func (h *Hub) Register() (connID string, events <-chan Event, unregister func()) {
	h.mu.Lock()
	id := h.nextConnID
	h.nextConnID++
	channel := make(chan Event, connectionBuffer)
	h.connections[id] = channel
	h.mu.Unlock()

	return strconv.FormatUint(id, 10), channel, func() {
		h.mu.Lock()
		if channel, ok := h.connections[id]; ok {
			delete(h.connections, id)
			close(channel)
		}
		h.mu.Unlock()
	}
}

// Broadcast sends event to every open connection without ever blocking the
// caller: a connection whose buffer is full has the event dropped for that
// connection only.
func (h *Hub) Broadcast(event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, channel := range h.connections {
		select {
		case channel <- event:
		default:
		}
	}
}

// NextMessageID returns a unique ID safe to use in webchat's half of
// multiChannel's compound trackable-message IDs: it never contains ':' or
// '|', the two characters that scheme uses as separators.
func (h *Hub) NextMessageID() string {
	id := atomic.AddUint64(&h.nextMsgID, 1)
	return strconv.FormatUint(id, 36)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/channels/webchat/... -v -race`
Expected: PASS (all 4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/channels/webchat/hub.go internal/adapters/channels/webchat/hub_test.go
git commit -m "Add webchat.Hub: SSE connection registry with non-blocking broadcast"
```

---

### Task 2: `webchat.Channel` — `ports.Channel` implementation over the Hub

**Files:**
- Create: `internal/adapters/channels/webchat/channel.go`
- Test: `internal/adapters/channels/webchat/channel_test.go`

**Interfaces:**
- Consumes: `Hub`, `Hub.Register`, `Hub.Broadcast`, `Hub.NextMessageID` (Task 1).
- Produces: `New(hub *Hub) *Channel` implementing `ports.Channel` — consumed by Task 3 (`multiChannel`) and Task 4 (wiring into `NewApp`).

- [ ] **Step 1: Write the failing tests**

```go
package webchat

import (
	"context"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
)

func recv(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func TestChannelDeliverBroadcastsAMessageEvent(t *testing.T) {
	hub := NewHub()
	channel := New(hub)
	_, events, unregister := hub.Register()
	defer unregister()

	if err := channel.Deliver(context.Background(), "any-chat-id", "hello"); err != nil {
		t.Fatal(err)
	}
	event := recv(t, events)
	if event.Kind != EventMessage || event.Text != "hello" {
		t.Fatalf("event=%#v", event)
	}
}

func TestChannelDeliverTrackableReturnsAUsableEditableID(t *testing.T) {
	hub := NewHub()
	channel := New(hub)
	_, events, unregister := hub.Register()
	defer unregister()

	id, err := channel.DeliverTrackable(context.Background(), "any-chat-id", "starting...")
	if err != nil || id == "" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	recv(t, events) // the initial message

	if err := channel.EditText(context.Background(), "any-chat-id", id, "done"); err != nil {
		t.Fatal(err)
	}
	event := recv(t, events)
	if event.Kind != EventEdit || event.ID != id || event.Text != "done" {
		t.Fatalf("event=%#v", event)
	}
}

func TestChannelSendTypingBroadcastsATypingEvent(t *testing.T) {
	hub := NewHub()
	channel := New(hub)
	_, events, unregister := hub.Register()
	defer unregister()

	if err := channel.SendTyping(context.Background(), "any-chat-id"); err != nil {
		t.Fatal(err)
	}
	if event := recv(t, events); event.Kind != EventTyping {
		t.Fatalf("event=%#v", event)
	}
}

func TestChannelDeliverApprovalBroadcastsAnApprovalEvent(t *testing.T) {
	hub := NewHub()
	channel := New(hub)
	_, events, unregister := hub.Register()
	defer unregister()

	approval := approvals.Approval{ID: "approval-1", Summary: "Add repository eggy"}
	if err := channel.DeliverApproval(context.Background(), "any-chat-id", approval); err != nil {
		t.Fatal(err)
	}
	event := recv(t, events)
	if event.Kind != EventApproval || event.Approval == nil || event.Approval.ID != "approval-1" || event.Approval.Summary != "Add repository eggy" {
		t.Fatalf("event=%#v", event)
	}
}

func TestChannelAnswerCallbackIsANoOp(t *testing.T) {
	channel := New(NewHub())
	if err := channel.AnswerCallback(context.Background(), "unused"); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/channels/webchat/... -run TestChannel -v`
Expected: FAIL — `Channel`/`New` don't exist yet.

- [ ] **Step 3: Implement**

```go
package webchat

import (
	"context"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
)

// Channel implements ports.Channel over a Hub. It is a browser chat surface,
// not a Telegram-style bot API: chatID is accepted (to satisfy the
// interface) but ignored for routing -- there is exactly one owner and one
// conversation, so every call broadcasts to every open connection.
type Channel struct {
	hub *Hub
}

func New(hub *Hub) *Channel {
	return &Channel{hub: hub}
}

func (c *Channel) Deliver(_ context.Context, _ string, text string) error {
	c.hub.Broadcast(Event{Kind: EventMessage, ID: c.hub.NextMessageID(), Text: text})
	return nil
}

func (c *Channel) DeliverTrackable(_ context.Context, _ string, text string) (string, error) {
	id := c.hub.NextMessageID()
	c.hub.Broadcast(Event{Kind: EventMessage, ID: id, Text: text})
	return id, nil
}

func (c *Channel) EditText(_ context.Context, _ string, messageID string, text string) error {
	c.hub.Broadcast(Event{Kind: EventEdit, ID: messageID, Text: text})
	return nil
}

func (c *Channel) SendTyping(_ context.Context, _ string) error {
	c.hub.Broadcast(Event{Kind: EventTyping})
	return nil
}

func (c *Channel) DeliverApproval(_ context.Context, _ string, approval approvals.Approval) error {
	c.hub.Broadcast(Event{Kind: EventApproval, Approval: &ApprovalPayload{ID: approval.ID, Summary: approval.Summary}})
	return nil
}

// AnswerCallback is a no-op: "answering a callback query" is a Telegram
// button-tap concept with no browser equivalent.
func (c *Channel) AnswerCallback(context.Context, string) error {
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/channels/webchat/... -v -race`
Expected: PASS (all tests in the package)

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/channels/webchat/channel.go internal/adapters/channels/webchat/channel_test.go
git commit -m "Add webchat.Channel: ports.Channel implementation over the Hub"
```

---

### Task 3: `multiChannel` — fan out to Telegram and webchat with compound IDs

**Files:**
- Create: `internal/bootstrap/multi_channel.go`
- Test: `internal/bootstrap/multi_channel_test.go`

**Interfaces:**
- Consumes: `ports.Channel` (both Telegram's and `webchat.Channel` satisfy it).
- Produces: `newMultiChannel(telegram, web ports.Channel) ports.Channel` — consumed by Task 4.

- [ ] **Step 1: Write the failing tests**

```go
package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
)

type fakeChannel struct {
	name              string
	delivered         []string
	trackableID       string
	trackableErr      error
	editCalls         []string
	typingCalls       int
	answerCalls       []string
	approvalDelivered []approvals.Approval
	deliverErr        error
}

func (f *fakeChannel) Deliver(_ context.Context, _ string, text string) error {
	if f.deliverErr != nil {
		return f.deliverErr
	}
	f.delivered = append(f.delivered, text)
	return nil
}
func (f *fakeChannel) DeliverApproval(_ context.Context, _ string, approval approvals.Approval) error {
	f.approvalDelivered = append(f.approvalDelivered, approval)
	return nil
}
func (f *fakeChannel) DeliverTrackable(_ context.Context, _ string, text string) (string, error) {
	if f.trackableErr != nil {
		return "", f.trackableErr
	}
	f.delivered = append(f.delivered, text)
	return f.trackableID, nil
}
func (f *fakeChannel) EditText(_ context.Context, _ string, messageID, text string) error {
	f.editCalls = append(f.editCalls, messageID+":"+text)
	return nil
}
func (f *fakeChannel) AnswerCallback(_ context.Context, callbackQueryID string) error {
	f.answerCalls = append(f.answerCalls, callbackQueryID)
	return nil
}
func (f *fakeChannel) SendTyping(context.Context, string) error {
	f.typingCalls++
	return nil
}

func TestMultiChannelDeliverFansOutToBoth(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newMultiChannel(telegram, web)

	if err := channel.Deliver(context.Background(), "chat", "hello"); err != nil {
		t.Fatal(err)
	}
	if len(telegram.delivered) != 1 || len(web.delivered) != 1 {
		t.Fatalf("telegram=%#v web=%#v", telegram, web)
	}
}

func TestMultiChannelDeliverSucceedsIfOnlyOneChannelSucceeds(t *testing.T) {
	telegram := &fakeChannel{deliverErr: errors.New("telegram down")}
	web := &fakeChannel{}
	channel := newMultiChannel(telegram, web)

	if err := channel.Deliver(context.Background(), "chat", "hello"); err != nil {
		t.Fatalf("expected nil error when at least one channel succeeds, got %v", err)
	}
	if len(web.delivered) != 1 {
		t.Fatalf("web=%#v", web)
	}
}

func TestMultiChannelDeliverFailsIfBothChannelsFail(t *testing.T) {
	telegram := &fakeChannel{deliverErr: errors.New("telegram down")}
	web := &fakeChannel{deliverErr: errors.New("web down")}
	channel := newMultiChannel(telegram, web)

	if err := channel.Deliver(context.Background(), "chat", "hello"); err == nil {
		t.Fatal("expected an error when both channels fail")
	}
}

func TestMultiChannelDeliverTrackableEncodesACompoundID(t *testing.T) {
	telegram := &fakeChannel{trackableID: "123"}
	web := &fakeChannel{trackableID: "abc"}
	channel := newMultiChannel(telegram, web)

	id, err := channel.DeliverTrackable(context.Background(), "chat", "working...")
	if err != nil {
		t.Fatal(err)
	}
	if id != "telegram:123|web:abc" {
		t.Fatalf("id=%q", id)
	}
}

func TestMultiChannelDeliverTrackableOmitsAFailingChannelsHalf(t *testing.T) {
	telegram := &fakeChannel{trackableErr: errors.New("telegram down")}
	web := &fakeChannel{trackableID: "abc"}
	channel := newMultiChannel(telegram, web)

	id, err := channel.DeliverTrackable(context.Background(), "chat", "working...")
	if err != nil {
		t.Fatal(err)
	}
	if id != "web:abc" {
		t.Fatalf("id=%q", id)
	}
}

func TestMultiChannelEditTextRoutesEachHalfToItsChannel(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newMultiChannel(telegram, web)

	if err := channel.EditText(context.Background(), "chat", "telegram:123|web:abc", "done"); err != nil {
		t.Fatal(err)
	}
	if len(telegram.editCalls) != 1 || telegram.editCalls[0] != "123:done" {
		t.Fatalf("telegram.editCalls=%#v", telegram.editCalls)
	}
	if len(web.editCalls) != 1 || web.editCalls[0] != "abc:done" {
		t.Fatalf("web.editCalls=%#v", web.editCalls)
	}
}

func TestMultiChannelEditTextHandlesASingleHalfID(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newMultiChannel(telegram, web)

	if err := channel.EditText(context.Background(), "chat", "web:abc", "done"); err != nil {
		t.Fatal(err)
	}
	if len(telegram.editCalls) != 0 {
		t.Fatalf("telegram.editCalls=%#v, want none", telegram.editCalls)
	}
	if len(web.editCalls) != 1 || web.editCalls[0] != "abc:done" {
		t.Fatalf("web.editCalls=%#v", web.editCalls)
	}
}

func TestMultiChannelAnswerCallbackOnlyReachesTelegram(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newMultiChannel(telegram, web)

	if err := channel.AnswerCallback(context.Background(), "callback-1"); err != nil {
		t.Fatal(err)
	}
	if len(telegram.answerCalls) != 1 {
		t.Fatalf("telegram.answerCalls=%#v", telegram.answerCalls)
	}
	if len(web.answerCalls) != 0 {
		t.Fatalf("web.answerCalls=%#v, want none", web.answerCalls)
	}
}

func TestMultiChannelSendTypingReachesBoth(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newMultiChannel(telegram, web)

	if err := channel.SendTyping(context.Background(), "chat"); err != nil {
		t.Fatal(err)
	}
	if telegram.typingCalls != 1 || web.typingCalls != 1 {
		t.Fatalf("telegram=%d web=%d", telegram.typingCalls, web.typingCalls)
	}
}

func TestMultiChannelDeliverApprovalReachesBoth(t *testing.T) {
	telegram := &fakeChannel{}
	web := &fakeChannel{}
	channel := newMultiChannel(telegram, web)

	approval := approvals.Approval{ID: "approval-1"}
	if err := channel.DeliverApproval(context.Background(), "chat", approval); err != nil {
		t.Fatal(err)
	}
	if len(telegram.approvalDelivered) != 1 || len(web.approvalDelivered) != 1 {
		t.Fatalf("telegram=%#v web=%#v", telegram, web)
	}
}

func TestNewMultiChannelReturnsTheSingleChannelUnwrappedWhenOnlyOneIsConfigured(t *testing.T) {
	telegram := &fakeChannel{}
	channel := newMultiChannel(telegram, nil)
	if channel != ports.Channel(telegram) {
		t.Fatal("expected newMultiChannel to return the sole non-nil channel directly, not wrap it")
	}
}
```

The last test needs `"github.com/nigelteosw/eggy/internal/ports"` imported; add it alongside the existing imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bootstrap/... -run TestMultiChannel -v`
Expected: FAIL — `newMultiChannel` doesn't exist yet.

- [ ] **Step 3: Implement**

```go
package bootstrap

import (
	"context"
	"errors"
	"strings"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// multiChannel implements ports.Channel by fanning every call out to both
// Telegram and the web chat channel, so the same conversation is observable
// live from both surfaces. See docs/superpowers/specs/2026-07-23-web-chat-interface-design.md
// for the compound trackable-message-ID scheme EditText/DeliverTrackable use.
type multiChannel struct {
	telegram ports.Channel
	web      ports.Channel
}

// newMultiChannel returns telegram or web directly, unwrapped, if only one
// is configured (nil is acceptable for either), a multiChannel if both are,
// or noopChannel{} if neither is.
func newMultiChannel(telegram, web ports.Channel) ports.Channel {
	switch {
	case telegram == nil && web == nil:
		return noopChannel{}
	case web == nil:
		return telegram
	case telegram == nil:
		return web
	default:
		return &multiChannel{telegram: telegram, web: web}
	}
}

func (m *multiChannel) Deliver(ctx context.Context, chatID, text string) error {
	errTelegram := m.telegram.Deliver(ctx, chatID, text)
	errWeb := m.web.Deliver(ctx, chatID, text)
	if errTelegram != nil && errWeb != nil {
		return errors.Join(errTelegram, errWeb)
	}
	return nil
}

func (m *multiChannel) DeliverApproval(ctx context.Context, chatID string, approval approvals.Approval) error {
	errTelegram := m.telegram.DeliverApproval(ctx, chatID, approval)
	errWeb := m.web.DeliverApproval(ctx, chatID, approval)
	if errTelegram != nil && errWeb != nil {
		return errors.Join(errTelegram, errWeb)
	}
	return nil
}

func (m *multiChannel) DeliverTrackable(ctx context.Context, chatID, text string) (string, error) {
	telegramID, errTelegram := m.telegram.DeliverTrackable(ctx, chatID, text)
	webID, errWeb := m.web.DeliverTrackable(ctx, chatID, text)
	var parts []string
	if errTelegram == nil {
		parts = append(parts, "telegram:"+telegramID)
	}
	if errWeb == nil {
		parts = append(parts, "web:"+webID)
	}
	if len(parts) == 0 {
		return "", errors.Join(errTelegram, errWeb)
	}
	return strings.Join(parts, "|"), nil
}

func (m *multiChannel) EditText(ctx context.Context, chatID, messageID, text string) error {
	var errTelegram, errWeb error
	for _, part := range strings.Split(messageID, "|") {
		channel, id, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		switch channel {
		case "telegram":
			errTelegram = m.telegram.EditText(ctx, chatID, id, text)
		case "web":
			errWeb = m.web.EditText(ctx, chatID, id, text)
		}
	}
	if errTelegram != nil && errWeb != nil {
		return errors.Join(errTelegram, errWeb)
	}
	return nil
}

// AnswerCallback only ever reaches Telegram: a callbackQueryID is a
// Telegram button-tap concept webchat's AnswerCallback already treats as a
// no-op, so there is nothing useful to fan out to on the web side.
func (m *multiChannel) AnswerCallback(ctx context.Context, callbackQueryID string) error {
	return m.telegram.AnswerCallback(ctx, callbackQueryID)
}

func (m *multiChannel) SendTyping(ctx context.Context, chatID string) error {
	_ = m.web.SendTyping(ctx, chatID)
	return m.telegram.SendTyping(ctx, chatID)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bootstrap/... -run TestMultiChannel -v`
Expected: PASS (all 11 test functions)

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/multi_channel.go internal/bootstrap/multi_channel_test.go
git commit -m "Add multiChannel: fan Telegram and webchat out to the same conversation"
```

---

### Task 4: Wire `webchat`/`multiChannel` into `App` construction

**Files:**
- Modify: `internal/bootstrap/app.go`

**Interfaces:**
- Consumes: `webchat.NewHub`, `webchat.New` (Tasks 1–2), `newMultiChannel` (Task 3).
- Produces: `app.chatHub *webchat.Hub` (new `App` field) — consumed by Task 5 (the SSE HTTP handler needs the same `Hub` instance to register connections against).

- [ ] **Step 1: Read the current channel construction and App struct**

Run: `sed -n '56,90p;178,185p' internal/bootstrap/app.go` to confirm these line ranges haven't shifted from what this plan assumes before editing (the SQLite memory feature landed since this plan was written and may have moved things further).

- [ ] **Step 2: Implement**

Add a field to the `App` struct (near `channel ports.Channel`):

```go
	channel                 ports.Channel
	chatHub                 *webchat.Hub
```

Add the import:

```go
	"github.com/nigelteosw/eggy/internal/adapters/channels/webchat"
```

Replace the existing channel construction:

```go
	var telegramClient *telegram.Client
	if options.FakeAdapters {
		app.channel = noopChannel{}
	} else {
		telegramClient = telegram.NewClient(options.TelegramBaseURL, secrets.TelegramBotToken, options.HTTPClient)
		app.channel = telegramClient
	}
```

with:

```go
	app.chatHub = webchat.NewHub()
	webChannel := webchat.New(app.chatHub)
	var telegramClient *telegram.Client
	if options.FakeAdapters {
		app.channel = webChannel
	} else {
		telegramClient = telegram.NewClient(options.TelegramBaseURL, secrets.TelegramBotToken, options.HTTPClient)
		app.channel = newMultiChannel(telegramClient, webChannel)
	}
```

(`FakeAdapters` mode keeps `webChannel` alone rather than `noopChannel{}`, so tests exercising `FakeAdapters: true` can still observe chat delivery through the Hub if a future test wants to; this does not change any existing test's observable behavior since nothing today asserts on `noopChannel` specifically — confirm this with Step 3 below.)

- [ ] **Step 3: Verify the whole app builds and existing tests still pass**

Run: `go build ./... && go test ./internal/bootstrap/... -v 2>&1 | tail -60`
Expected: build succeeds; every existing `app_test.go`/`web_test.go` test still passes. If any `FakeAdapters: true` test specifically asserted behavior tied to `noopChannel`, investigate and either keep `noopChannel{}` for that path or fix the test's assumption — do not silently change test semantics.

- [ ] **Step 4: Run the full test suite**

Run: `go test ./...`
Expected: all packages pass.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/app.go
git commit -m "Wire webchat and multiChannel into App construction"
```

---

### Task 5: `GET /api/chat/stream` — the SSE endpoint

**Files:**
- Create: `internal/bootstrap/chat.go`
- Test: `internal/bootstrap/chat_test.go`

**Interfaces:**
- Consumes: `webchat.Hub` (Task 1), `requireWebSession` (existing, `internal/bootstrap/web.go`).
- Produces: `newChatStreamHandler(hub *webchat.Hub) http.HandlerFunc` — consumed by Task 9 (mounting the route on `NewWebHandler`'s mux).

- [ ] **Step 1: Write the failing tests**

```go
package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/channels/webchat"
)

func TestChatStreamDeliversABroadcastEventAsSSE(t *testing.T) {
	hub := webchat.NewHub()
	handler := newChatStreamHandler(hub)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/chat/stream", nil).WithContext(ctx)
	response := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler(response, request)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond) // let the handler register with the hub
	hub.Broadcast(webchat.Event{Kind: webchat.EventMessage, ID: "1", Text: "hello"})

	<-done
	body := response.Body.String()
	if !strings.Contains(body, "event: message") || !strings.Contains(body, `"text":"hello"`) {
		t.Fatalf("body=%q", body)
	}
}

func TestChatStreamSetsSSEHeaders(t *testing.T) {
	hub := webchat.NewHub()
	handler := newChatStreamHandler(hub)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/chat/stream", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type=%q", response.Header().Get("Content-Type"))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bootstrap/... -run TestChatStream -v`
Expected: FAIL — `newChatStreamHandler` doesn't exist yet.

- [ ] **Step 3: Implement**

```go
package bootstrap

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/channels/webchat"
)

const chatKeepaliveInterval = 15 * time.Second

func newChatStreamHandler(hub *webchat.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		_, events, unregister := hub.Register()
		defer unregister()

		keepalive := time.NewTicker(chatKeepaliveInterval)
		defer keepalive.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-keepalive.C:
				if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return
				}
				flusher.Flush()
			case event, ok := <-events:
				if !ok {
					return
				}
				body, err := json.Marshal(event)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Kind, body); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}
```

`webchat.Event` needs JSON tags for a stable wire shape; add them in
`internal/adapters/channels/webchat/hub.go` (revisit Task 1's `Event`/`ApprovalPayload` structs):

```go
type Event struct {
	Kind     EventKind        `json:"kind"`
	ID       string           `json:"id,omitempty"`
	Text     string           `json:"text,omitempty"`
	Approval *ApprovalPayload `json:"approval,omitempty"`
}

type ApprovalPayload struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bootstrap/... -run TestChatStream -v`
Expected: PASS (both tests)

- [ ] **Step 5: Run the full bootstrap package and webchat package tests**

Run: `go test ./internal/bootstrap/... ./internal/adapters/channels/webchat/... -v -race 2>&1 | tail -60`
Expected: all pass (confirms the JSON tags added in Step 3 didn't break Task 1/2's tests, which compare `Event` fields directly rather than JSON output).

- [ ] **Step 6: Commit**

```bash
git add internal/bootstrap/chat.go internal/bootstrap/chat_test.go internal/adapters/channels/webchat/hub.go
git commit -m "Add the /api/chat/stream SSE handler"
```

---

### Task 6: `POST /api/chat/send`

**Files:**
- Modify: `internal/bootstrap/chat.go`
- Modify: `internal/bootstrap/chat_test.go`

**Interfaces:**
- Consumes: `app.Enqueue` (existing, `internal/bootstrap/app.go`), `events.Event`/`events.Message` (existing, `internal/kernel/events/events.go`).
- Produces: `newChatSendHandler(enqueue func(context.Context, events.Event) error) http.HandlerFunc` — consumed by Task 9.

- [ ] **Step 1: Write the failing tests**

```go
func TestChatSendEnqueuesAMessageEvent(t *testing.T) {
	var got events.Event
	enqueue := func(_ context.Context, event events.Event) error {
		got = event
		return nil
	}
	handler := newChatSendHandler(enqueue)

	request := httptest.NewRequest(http.MethodPost, "/api/chat/send", strings.NewReader(`{"text":"hello Eggy"}`))
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got.Type != events.TypeMessage || got.Source != "web" {
		t.Fatalf("event=%#v", got)
	}
	var message events.Message
	if err := json.Unmarshal(got.Payload, &message); err != nil {
		t.Fatal(err)
	}
	if message.Text != "hello Eggy" {
		t.Fatalf("message=%#v", message)
	}
}

func TestChatSendRejectsEmptyText(t *testing.T) {
	handler := newChatSendHandler(func(context.Context, events.Event) error { return nil })

	request := httptest.NewRequest(http.MethodPost, "/api/chat/send", strings.NewReader(`{"text":""}`))
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestChatSendRejectsInvalidBody(t *testing.T) {
	handler := newChatSendHandler(func(context.Context, events.Event) error { return nil })

	request := httptest.NewRequest(http.MethodPost, "/api/chat/send", strings.NewReader(`not json`))
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestChatSendReturns500WhenEnqueueFails(t *testing.T) {
	handler := newChatSendHandler(func(context.Context, events.Event) error { return errors.New("queue full") })

	request := httptest.NewRequest(http.MethodPost, "/api/chat/send", strings.NewReader(`{"text":"hi"}`))
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", response.Code)
	}
}
```

Add `"github.com/nigelteosw/eggy/internal/kernel/events"` and `"errors"` to `chat_test.go`'s imports if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bootstrap/... -run TestChatSend -v`
Expected: FAIL — `newChatSendHandler` doesn't exist yet.

- [ ] **Step 3: Implement**

Add to `internal/bootstrap/chat.go`:

```go
func newChatSendHandler(enqueue func(context.Context, events.Event) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if strings.TrimSpace(input.Text) == "" {
			writeWebError(w, http.StatusBadRequest, "text is required")
			return
		}
		payload, err := json.Marshal(events.Message{Text: input.Text})
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "failed to encode message")
			return
		}
		event := events.Event{
			ID: randomEventID(), Type: events.TypeMessage, Source: "web",
			Timestamp: time.Now(), Payload: payload,
		}
		if err := enqueue(r.Context(), event); err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		body, _ := (CommandResult{State: ResultSuccess, Title: "Message received."}).RenderJSON()
		_, _ = w.Write(body)
	}
}
```

Add the imports `"context"`, `"strings"`, `"time"`, and
`"github.com/nigelteosw/eggy/internal/kernel/events"` to `chat.go` if not
already present from Task 5. `randomEventID` does not exist yet — check
whether `internal/bootstrap` or `internal/kernel/events` already has an
event-ID generator (grep for how the Telegram webhook handler builds
`events.Event.ID` before adding a new one); reuse it if found, otherwise add
a small one next to `newChatSendHandler` using the same `crypto/rand`-backed
approach `approvals.randomID` in `internal/kernel/services/approval_service.go`
uses.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bootstrap/... -run TestChatSend -v`
Expected: PASS (all 4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/chat.go internal/bootstrap/chat_test.go
git commit -m "Add the POST /api/chat/send handler"
```

---

### Task 7: `POST /api/chat/approve`

**Files:**
- Modify: `internal/bootstrap/chat.go`
- Modify: `internal/bootstrap/chat_test.go`

**Interfaces:**
- Consumes: `app.Enqueue`, `events.ApprovalDecision` (existing).
- Produces: `newChatApproveHandler(enqueue func(context.Context, events.Event) error) http.HandlerFunc` — consumed by Task 9.

- [ ] **Step 1: Write the failing tests**

```go
func TestChatApproveEnqueuesAnApprovalDecisionEvent(t *testing.T) {
	var got events.Event
	enqueue := func(_ context.Context, event events.Event) error {
		got = event
		return nil
	}
	handler := newChatApproveHandler(enqueue)

	request := httptest.NewRequest(http.MethodPost, "/api/chat/approve", strings.NewReader(`{"approval_id":"approval-1","approved":true}`))
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got.Type != events.TypeApproval {
		t.Fatalf("event=%#v", got)
	}
	var decision events.ApprovalDecision
	if err := json.Unmarshal(got.Payload, &decision); err != nil {
		t.Fatal(err)
	}
	if decision.ApprovalID != "approval-1" || !decision.Approved {
		t.Fatalf("decision=%#v", decision)
	}
	if decision.CallbackQueryID != "" || decision.MessageID != "" {
		t.Fatalf("expected empty Telegram-only fields, decision=%#v", decision)
	}
}

func TestChatApproveRejectsMissingApprovalID(t *testing.T) {
	handler := newChatApproveHandler(func(context.Context, events.Event) error { return nil })

	request := httptest.NewRequest(http.MethodPost, "/api/chat/approve", strings.NewReader(`{"approved":true}`))
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bootstrap/... -run TestChatApprove -v`
Expected: FAIL — `newChatApproveHandler` doesn't exist yet.

- [ ] **Step 3: Implement**

```go
func newChatApproveHandler(enqueue func(context.Context, events.Event) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ApprovalID string `json:"approval_id"`
			Approved   bool   `json:"approved"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if strings.TrimSpace(input.ApprovalID) == "" {
			writeWebError(w, http.StatusBadRequest, "approval_id is required")
			return
		}
		payload, err := json.Marshal(events.ApprovalDecision{ApprovalID: input.ApprovalID, Approved: input.Approved})
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "failed to encode decision")
			return
		}
		event := events.Event{
			ID: randomEventID(), Type: events.TypeApproval, Source: "web",
			Timestamp: time.Now(), Payload: payload,
		}
		if err := enqueue(r.Context(), event); err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		body, _ := (CommandResult{State: ResultSuccess, Title: "Decision received."}).RenderJSON()
		_, _ = w.Write(body)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bootstrap/... -run TestChatApprove -v`
Expected: PASS (both tests)

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/chat.go internal/bootstrap/chat_test.go
git commit -m "Add the POST /api/chat/approve handler"
```

---

### Task 8: `GET /api/chat/history`

**Files:**
- Modify: `internal/bootstrap/chat.go`
- Modify: `internal/bootstrap/chat_test.go`

**Interfaces:**
- Consumes: `ports.StateStore.Load` (existing), `State.RecentMessages` (existing).
- Produces: `newChatHistoryHandler(store ports.StateStore) http.HandlerFunc` — consumed by Task 9.

- [ ] **Step 1: Write the failing test**

```go
func TestChatHistoryReturnsRecentMessagesAsTableRows(t *testing.T) {
	store := newMemoryStore()
	store.state.RecentMessages = []ports.Message{
		{Role: ports.RoleUser, Content: "hi"},
		{Role: ports.RoleAssistant, Content: "hello!"},
	}
	handler := newChatHistoryHandler(store)

	request := httptest.NewRequest(http.MethodGet, "/api/chat/history", nil)
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded CommandResult
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.TableRows) != 2 || decoded.TableRows[0][0] != "user" || decoded.TableRows[0][1] != "hi" {
		t.Fatalf("rows=%#v", decoded.TableRows)
	}
	if decoded.TableRows[1][0] != "assistant" || decoded.TableRows[1][1] != "hello!" {
		t.Fatalf("rows=%#v", decoded.TableRows)
	}
}
```

Check whether `internal/bootstrap`'s existing tests already define a
`newMemoryStore()`/`memoryStore` test double implementing `ports.StateStore`
(several `_test.go` files in this package construct fakes like this); reuse
it if present instead of adding a duplicate.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bootstrap/... -run TestChatHistory -v`
Expected: FAIL — `newChatHistoryHandler` doesn't exist yet.

- [ ] **Step 3: Implement**

```go
func newChatHistoryHandler(store ports.StateStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, err := store.Load(r.Context())
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rows := make([][]string, 0, len(state.RecentMessages))
		for _, message := range state.RecentMessages {
			rows = append(rows, []string{string(message.Role), message.Content})
		}
		writeWebResult(w, CommandResult{
			State:        ResultSuccess,
			TableHeaders: []string{"role", "content"},
			TableRows:    rows,
		})
	}
}
```

Add `"github.com/nigelteosw/eggy/internal/ports"` to `chat.go`'s imports if
not already present.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bootstrap/... -run TestChatHistory -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/chat.go internal/bootstrap/chat_test.go
git commit -m "Add the GET /api/chat/history handler"
```

---

### Task 9: Mount the chat routes on `NewWebHandler`

**Files:**
- Modify: `internal/bootstrap/web.go`
- Modify: `internal/bootstrap/web_test.go`

**Interfaces:**
- Consumes: `newChatStreamHandler`, `newChatSendHandler`, `newChatApproveHandler`, `newChatHistoryHandler` (Tasks 5–8), `app.chatHub` (Task 4).
- Produces: `NewWebHandler` gains a `hub *webchat.Hub` and `enqueue func(context.Context, events.Event) error` parameter — consumed by Task 10 (the `NewApp` call site must pass `app.chatHub` and `app.Enqueue`).

- [ ] **Step 1: Read the current `NewWebHandler` signature and its one call site**

Run: `sed -n '1,65p' internal/bootstrap/web.go` and
`grep -n "NewWebHandler(" internal/bootstrap/app.go` to confirm the exact
current signature and call site before editing (this plan's earlier tasks
may have shifted line numbers further from what Task 4 assumed).

- [ ] **Step 2: Write the failing test**

Add to `internal/bootstrap/web_test.go`:

```go
func TestWebHandlerMountsChatRoutes(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	hub := webchat.NewHub()
	enqueued := false
	handler := NewWebHandler("", testWebConfig(now), hub, func(context.Context, events.Event) error {
		enqueued = true
		return nil
	})
	cookie := webLoginCookie(t, handler)

	request := httptest.NewRequest(http.MethodPost, "/api/chat/send", strings.NewReader(`{"text":"hi"}`))
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted || !enqueued {
		t.Fatalf("status=%d enqueued=%v", response.Code, enqueued)
	}
}

func TestWebHandlerChatRoutesRequireSession(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now), webchat.NewHub(), func(context.Context, events.Event) error { return nil })

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/chat/stream", nil),
		httptest.NewRequest(http.MethodPost, "/api/chat/send", strings.NewReader(`{"text":"hi"}`)),
		httptest.NewRequest(http.MethodPost, "/api/chat/approve", strings.NewReader(`{"approval_id":"x","approved":true}`)),
		httptest.NewRequest(http.MethodGet, "/api/chat/history", nil),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", request.URL.Path, response.Code)
		}
	}
}
```

Add `"github.com/nigelteosw/eggy/internal/adapters/channels/webchat"` and
`"github.com/nigelteosw/eggy/internal/kernel/events"` to `web_test.go`'s
imports.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/bootstrap/... -run TestWebHandlerChat -v`
Expected: FAIL — compile error (`NewWebHandler` doesn't accept these
parameters yet) until Step 4.

- [ ] **Step 4: Implement**

Change `NewWebHandler`'s signature and add the four route registrations.
`newChatHistoryHandler` needs a `ports.StateStore`, which `NewWebHandler`
does not currently take as a parameter (`webConfigGetRoute`/
`webConfigSetRoute` reach configuration indirectly through `commands`,
which reads `config.yaml` directly, not `state.json`) — add a `stateStore
ports.StateStore` parameter alongside `chatHub`/`enqueue`. In
`internal/bootstrap/web.go`:

```go
func NewWebHandler(configPath string, webConfig WebUIConfig, chatHub *webchat.Hub, enqueue func(context.Context, events.Event) error, stateStore ports.StateStore) http.Handler {
	now := webConfig.Now
	if now == nil {
		now = time.Now
	}
	throttle := webui.NewLoginThrottle(now)
	commands := &CommandService{configPath: configPath}

	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(webui.Assets())))
	mux.HandleFunc("POST /api/login", handleWebLogin(webConfig, throttle, now))
	mux.HandleFunc("POST /api/logout", handleWebLogout())
	mux.Handle("GET /api/session", requireWebSession(webConfig, now, func(w http.ResponseWriter, _ *http.Request) {
		writeWebResult(w, CommandResult{State: ResultSuccess, Title: "Session is valid."})
	}))

	for _, section := range []struct {
		path     string
		get, set []string
	}{
		{"providers", []string{"config", "get", "providers"}, []string{"config", "set", "provider"}},
		{"models", []string{"config", "get", "models"}, []string{"config", "set", "model"}},
		{"calendar", []string{"config", "get", "calendar"}, []string{"config", "set", "calendar"}},
	} {
		mux.Handle("GET /api/config/"+section.path, requireWebSession(webConfig, now, webConfigGetRoute(commands, section.get)))
		mux.Handle("POST /api/config/"+section.path, requireWebSession(webConfig, now, webConfigSetRoute(commands, section.set)))
	}

	mux.Handle("GET /api/config/mcp", requireWebSession(webConfig, now, webMCPListRoute(configPath)))
	mux.Handle("POST /api/config/mcp", requireWebSession(webConfig, now, webMCPSetRoute(configPath)))
	mux.Handle("DELETE /api/config/mcp/{name}", requireWebSession(webConfig, now, webMCPRemoveRoute(configPath)))

	mux.Handle("GET /api/chat/stream", requireWebSession(webConfig, now, newChatStreamHandler(chatHub)))
	mux.Handle("POST /api/chat/send", requireWebSession(webConfig, now, newChatSendHandler(enqueue)))
	mux.Handle("POST /api/chat/approve", requireWebSession(webConfig, now, newChatApproveHandler(enqueue)))
	mux.Handle("GET /api/chat/history", requireWebSession(webConfig, now, newChatHistoryHandler(stateStore)))

	return mux
}
```

Update every existing call to `NewWebHandler` (Task 10's call site, and
every test in `web_test.go` that constructs one directly) to pass a
`stateStore` argument. Existing `web_test.go` tests that only exercise
login/config routes can pass any working fake `ports.StateStore` (check for
one already defined in the package before adding a new one) since
`stateStore` is unused by those paths.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/bootstrap/... -v -race 2>&1 | tail -80`
Expected: PASS for the whole package, including every pre-existing
`web_test.go` test updated for the new `NewWebHandler` signature.

- [ ] **Step 6: Commit**

```bash
git add internal/bootstrap/web.go internal/bootstrap/web_test.go
git commit -m "Mount chat routes on NewWebHandler"
```

---

### Task 10: Update the `NewWebHandler` call site in `App`

**Files:**
- Modify: `internal/bootstrap/app.go`

**Interfaces:**
- Consumes: `NewWebHandler`'s new signature (Task 9), `app.chatHub` (Task 4).

- [ ] **Step 1: Find the current call site**

Run: `grep -n "NewWebHandler(" internal/bootstrap/app.go`.

- [ ] **Step 2: Implement**

Update the call to pass the new parameters, e.g.:

```go
	webHandler := NewWebHandler(options.ConfigPath, WebUIConfig{
		UserEmail: secrets.UIUserEmail, Password: secrets.UIPassword,
		SigningKey: []byte(secrets.EncryptionKey), Now: options.Now,
	}, app.chatHub, app.Enqueue, stateStore)
```

Match the exact parameter order Task 9 actually settled on (`chatHub`,
`enqueue`, `stateStore` — adjust here if Task 9's implementation ordered
them differently than drafted). `stateStore` is already a local variable in
`NewApp` (the same one passed to `jsonfile.Open`/`app.store`) — reuse it,
do not construct a second one.

- [ ] **Step 3: Verify build and full test suite**

Run: `go build ./... && go test ./... 2>&1 | tail -40`
Expected: build succeeds; every package passes.

- [ ] **Step 4: Commit**

```bash
git add internal/bootstrap/app.go
git commit -m "Pass chatHub, Enqueue, and stateStore to NewWebHandler"
```

---

### Task 11: Frontend — chat API client

**Files:**
- Modify: `web/src/api.ts`

**Interfaces:**
- Consumes: nothing new.
- Produces: `sendChatMessage(text: string): Promise<CommandResult>`, `approveChatDecision(approvalId: string, approved: boolean): Promise<CommandResult>`, `getChatHistory(): Promise<CommandResult>`, `type ChatEvent = { kind: "message" | "typing" | "edit" | "approval"; id?: string; text?: string; approval?: { id: string; summary: string } }` — consumed by Task 13 (`ChatPage.tsx`).

- [ ] **Step 1: Add to `web/src/api.ts`**

```ts
export type ChatEvent = {
  kind: "message" | "typing" | "edit" | "approval";
  id?: string;
  text?: string;
  approval?: { id: string; summary: string };
};

export function sendChatMessage(text: string): Promise<CommandResult> {
  return request("/api/chat/send", { method: "POST", body: JSON.stringify({ text }) });
}

export function approveChatDecision(approvalId: string, approved: boolean): Promise<CommandResult> {
  return request("/api/chat/approve", { method: "POST", body: JSON.stringify({ approval_id: approvalId, approved }) });
}

export function getChatHistory(): Promise<CommandResult> {
  return request("/api/chat/history");
}
```

`EventSource` itself (used by `ChatPage.tsx` in Task 13) is a browser
built-in, not wrapped here — `request()`'s `fetch`-based helper doesn't
apply to a streaming `GET`, so the stream connection is opened directly in
the component that owns its lifecycle.

- [ ] **Step 2: Verify TypeScript compiles**

Run: `cd web && npx tsc --noEmit 2>&1`
Expected: no new errors introduced by this file (any remaining error should
be pre-existing/unrelated, not caused by this change).

- [ ] **Step 3: Commit**

```bash
git add web/src/api.ts
git commit -m "Add chat API client functions"
```

---

### Task 12: Frontend — `ChatPage.tsx`

**Files:**
- Create: `web/src/ChatPage.tsx`

**Interfaces:**
- Consumes: `sendChatMessage`, `approveChatDecision`, `getChatHistory`, `ChatEvent`, `SessionExpiredError` (Task 11 and existing `api.ts`).
- Produces: `ChatPage({ onSessionExpired: () => void })` — consumed by Task 13 (`App.tsx`).

- [ ] **Step 1: Create `web/src/ChatPage.tsx`**

```tsx
import { useEffect, useRef, useState } from "react";
import { ChatEvent, SessionExpiredError, approveChatDecision, getChatHistory, sendChatMessage } from "./api";

type ChatMessage = { id: string; role: "user" | "assistant"; text: string };
type PendingApproval = { id: string; summary: string };

export function ChatPage({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [typing, setTyping] = useState(false);
  const [approvals, setApprovals] = useState<PendingApproval[]>([]);
  const [draft, setDraft] = useState("");
  const [error, setError] = useState<string | null>(null);
  const bottomRef = useRef<HTMLDivElement | null>(null);

  function loadHistory() {
    getChatHistory()
      .then((result) => {
        const rows = result.table_rows ?? [];
        setMessages(
          rows.map((row, index) => ({
            id: `history-${index}`,
            role: row[0] === "user" ? "user" : "assistant",
            text: row[1] ?? "",
          })),
        );
      })
      .catch((err) => {
        if (err instanceof SessionExpiredError) onSessionExpired();
      });
  }

  useEffect(() => {
    loadHistory();
    const source = new EventSource("/api/chat/stream");

    source.addEventListener("open", loadHistory);

    source.addEventListener("message", (raw) => {
      const event = JSON.parse((raw as MessageEvent).data) as ChatEvent;
      setTyping(false);
      setMessages((current) => [...current, { id: event.id ?? `msg-${current.length}`, role: "assistant", text: event.text ?? "" }]);
    });

    source.addEventListener("typing", () => setTyping(true));

    source.addEventListener("edit", (raw) => {
      const event = JSON.parse((raw as MessageEvent).data) as ChatEvent;
      setMessages((current) => current.map((message) => (message.id === event.id ? { ...message, text: event.text ?? "" } : message)));
    });

    source.addEventListener("approval", (raw) => {
      const event = JSON.parse((raw as MessageEvent).data) as ChatEvent;
      if (event.approval) {
        setApprovals((current) => [...current, event.approval as PendingApproval]);
      }
    });

    source.onerror = () => {
      if (source.readyState === EventSource.CLOSED) {
        onSessionExpired();
      }
    };

    return () => source.close();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  async function handleSend(event: React.FormEvent) {
    event.preventDefault();
    const text = draft.trim();
    if (!text) return;
    setDraft("");
    setMessages((current) => [...current, { id: `local-${current.length}`, role: "user", text }]);
    try {
      await sendChatMessage(text);
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(err instanceof Error ? err.message : "Failed to send");
    }
  }

  async function handleApproval(approvalId: string, approved: boolean) {
    setApprovals((current) => current.filter((approval) => approval.id !== approvalId));
    try {
      await approveChatDecision(approvalId, approved);
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(err instanceof Error ? err.message : "Failed to record decision");
    }
  }

  return (
    <div className="flex min-h-screen flex-col bg-slate-100">
      <div className="mx-auto flex w-full max-w-2xl flex-1 flex-col gap-3 overflow-y-auto p-6">
        {messages.map((message) => (
          <div
            key={message.id}
            className={`max-w-[80%] rounded-lg px-4 py-2 text-sm ${
              message.role === "user" ? "self-end bg-slate-900 text-white" : "self-start bg-white text-slate-900 shadow"
            }`}
          >
            {message.text}
          </div>
        ))}
        {typing && <div className="self-start text-xs text-slate-400">Eggy is typing...</div>}
        {approvals.map((approval) => (
          <div key={approval.id} className="self-start rounded-lg border border-amber-300 bg-amber-50 p-4 text-sm shadow">
            <p className="mb-3 text-slate-800">{approval.summary}</p>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => handleApproval(approval.id, true)}
                className="rounded bg-slate-900 px-3 py-1 text-white"
              >
                Approve
              </button>
              <button
                type="button"
                onClick={() => handleApproval(approval.id, false)}
                className="rounded border border-slate-300 px-3 py-1 text-slate-700"
              >
                Reject
              </button>
            </div>
          </div>
        ))}
        {error && <p className="text-sm text-red-600">{error}</p>}
        <div ref={bottomRef} />
      </div>
      <form onSubmit={handleSend} className="border-t border-slate-200 bg-white p-4">
        <div className="mx-auto flex max-w-2xl gap-2">
          <input
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            placeholder="Message Eggy..."
            className="flex-1 rounded border border-slate-300 px-3 py-2"
          />
          <button type="submit" className="rounded bg-slate-900 px-4 py-2 text-white">
            Send
          </button>
        </div>
      </form>
    </div>
  );
}
```

- [ ] **Step 2: Verify TypeScript compiles**

Run: `cd web && npx tsc --noEmit 2>&1`
Expected: no errors from `ChatPage.tsx` itself (any remaining error should
be the pre-existing "Cannot find module './App'"-style error only if
`App.tsx` doesn't reference it yet — Task 13 resolves that).

- [ ] **Step 3: Commit**

```bash
git add web/src/ChatPage.tsx
git commit -m "Add ChatPage: message list, typing indicator, inline approvals, send box"
```

---

### Task 13: Frontend — chat-first navigation in `App.tsx`

**Files:**
- Modify: `web/src/App.tsx`

**Interfaces:**
- Consumes: `ChatPage` (Task 12), `ConfigPage` (existing).

- [ ] **Step 1: Read the current `App.tsx`**

Run: `cat web/src/App.tsx` to confirm its current shape before editing (the
MCP card work may have touched this file too).

- [ ] **Step 2: Implement**

```tsx
import { useEffect, useState } from "react";
import { checkSession } from "./api";
import { LoginPage } from "./LoginPage";
import { ChatPage } from "./ChatPage";
import { ConfigPage } from "./ConfigPage";

type Status = "checking" | "authenticated" | "unauthenticated";
type View = "chat" | "config";

export function App() {
  const [status, setStatus] = useState<Status>("checking");
  const [view, setView] = useState<View>("chat");

  useEffect(() => {
    checkSession()
      .then(() => setStatus("authenticated"))
      .catch(() => setStatus("unauthenticated"));
  }, []);

  if (status === "checking") {
    return <div className="flex min-h-screen items-center justify-center text-slate-500">Loading...</div>;
  }
  if (status === "unauthenticated") {
    return <LoginPage onLoggedIn={() => setStatus("authenticated")} />;
  }

  const onSessionExpired = () => setStatus("unauthenticated");

  return (
    <div className="relative min-h-screen">
      <button
        type="button"
        onClick={() => setView(view === "chat" ? "config" : "chat")}
        className="absolute right-4 top-4 z-10 rounded-full bg-white p-2 text-slate-500 shadow hover:text-slate-900"
        aria-label={view === "chat" ? "Open settings" : "Back to chat"}
      >
        {view === "chat" ? "⚙" : "💬"}
      </button>
      {view === "chat" ? <ChatPage onSessionExpired={onSessionExpired} /> : <ConfigPage onSessionExpired={onSessionExpired} />}
    </div>
  );
}
```

- [ ] **Step 3: Run the real frontend build**

Run: `cd web && bun install && bun run build`
Expected: succeeds with no TypeScript errors, writes real assets into
`internal/adapters/webui/dist/`.

- [ ] **Step 4: Commit**

```bash
git add web/src/App.tsx
git commit -m "Make chat the default view, config a settings toggle"
```

---

### Task 14: End-to-end verification

**Files:** none (verification only).

- [ ] **Step 1: Full backend verification**

Run: `go build ./... && go vet ./... && go test ./... && go test -race ./...`
Expected: all pass.

- [ ] **Step 2: Real-HTTP verification (write, run, then delete — do not commit this file)**

Create a temporary `internal/bootstrap/manual_verify_test.go` with an
`httptest.NewServer(NewWebHandler(...))`-based test (mirroring the pattern
already used to verify the config UI and MCP routes in earlier sessions):
log in, open `/api/chat/stream`, `POST /api/chat/send`, and confirm the
resulting `Deliver`/agent-loop response arrives over the stream as an SSE
`event: message` frame within a few seconds. Run it, confirm it passes, then
delete the file — it exists only to prove the wiring works end to end
before you claim the task complete, matching this project's "if you can't
test the UI, say so explicitly" standard applied at the HTTP layer instead
of a browser.

Run: `go test ./internal/bootstrap/... -run TestManualVerify -v`
Expected: PASS.

Then: `rm internal/bootstrap/manual_verify_test.go`

- [ ] **Step 3: Full `make` verification**

Run: `make fmt vet test race build`
Expected: all pass, including the frontend build.

- [ ] **Step 4: Manual note for the owner**

Report clearly that React rendering and actual click-through in a live
browser has not been done by the agent — the HTTP-level verification in
Step 2 confirms the wiring is correct, not that the UI looks or feels right;
recommend the owner try it in a real browser before relying on it.
