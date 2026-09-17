package discord

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/kernel/events"
)

type fakeTransport struct {
	mu       sync.Mutex
	channels map[string]DMInfo
	lookups  int
	sent     []string // channel:content
	edits    []string
	typing   []string
	sendErr  error
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{channels: map[string]DMInfo{
		"dm-a":  {OneToOne: true, RecipientID: "user-a"},
		"dm-b":  {OneToOne: true, RecipientID: "user-b"},
		"dm-x":  {OneToOne: true, RecipientID: "stranger"},
		"group": {OneToOne: false},
		"guild": {OneToOne: false},
	}}
}

func (f *fakeTransport) SendMessage(_ context.Context, channelID, content string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return "", f.sendErr
	}
	f.sent = append(f.sent, channelID+":"+content)
	return "m" + string(rune('0'+len(f.sent))), nil
}
func (f *fakeTransport) EditMessage(_ context.Context, channelID, messageID, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, channelID+":"+messageID+":"+content)
	return nil
}
func (f *fakeTransport) Typing(_ context.Context, channelID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typing = append(f.typing, channelID)
	return nil
}
func (f *fakeTransport) DMChannel(_ context.Context, channelID string) (DMInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	info, ok := f.channels[channelID]
	if !ok {
		return DMInfo{}, errors.New("unknown channel")
	}
	return info, nil
}

type sink struct {
	events []events.Event
	err    error
}

func (s *sink) accept(_ context.Context, event events.Event) error {
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, event)
	return nil
}

func linkedUsers(userID string) (string, bool) {
	switch userID {
	case "user-a":
		return "alice", true
	case "user-b":
		return "bob", true
	}
	return "", false
}

func newTestHandler(t *testing.T) (*Handler, *fakeTransport, *sink) {
	t.Helper()
	transport := newFakeTransport()
	events := &sink{}
	now := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	return NewHandler(linkedUsers, events.accept, transport, func() time.Time { return now }, nil), transport, events
}

func TestLinkedHumanInAOneToOneDMBecomesAnOwnerTurn(t *testing.T) {
	handler, transport, out := newTestHandler(t)
	in := Inbound{MessageID: "1", ChannelID: "dm-a", AuthorID: "user-a", Content: " hello ", Reference: &Reference{AuthorID: "bot", AuthorIsBot: true, Content: "earlier answer"}}
	if err := handler.Handle(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(out.events) != 1 {
		t.Fatalf("events=%d", len(out.events))
	}
	event := out.events[0]
	if event.ID != "discord:message:1" || event.Source != Source || event.Owner != "alice" || event.Type != events.TypeMessage {
		t.Fatalf("event=%+v", event)
	}
	if event.Destination != (destination.Destination{Kind: destination.Discord, ChannelID: "dm-a"}) {
		t.Fatalf("destination=%+v", event.Destination)
	}
	var message events.Message
	if err := json.Unmarshal(event.Payload, &message); err != nil {
		t.Fatal(err)
	}
	if message.Text != "hello" || message.Quote == nil || message.Quote.Text != "earlier answer" || !message.Quote.OwnMessage {
		t.Fatalf("message=%+v", message)
	}
	if len(transport.sent) != 0 {
		t.Fatalf("intake sent %v, want nothing", transport.sent)
	}
	// The DM verdict is looked up once per channel, not per message.
	if err := handler.Handle(context.Background(), Inbound{MessageID: "2", ChannelID: "dm-a", AuthorID: "user-a", Content: "again"}); err != nil {
		t.Fatal(err)
	}
	if transport.lookups != 1 {
		t.Fatalf("lookups=%d", transport.lookups)
	}
}

func TestUnknownHumanGetsOneRateLimitedDenialAndNoAgentWork(t *testing.T) {
	handler, transport, out := newTestHandler(t)
	for range 3 {
		if err := handler.Handle(context.Background(), Inbound{MessageID: "1", ChannelID: "dm-x", AuthorID: "stranger", Content: "let me in"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(out.events) != 0 {
		t.Fatalf("events=%v, want none", out.events)
	}
	if len(transport.sent) != 1 || !strings.Contains(transport.sent[0], "doesn't know you") {
		t.Fatalf("sent=%v, want one denial", transport.sent)
	}
	if strings.Contains(strings.Join(transport.sent, ""), "let me in") {
		t.Fatal("the stranger's text was echoed")
	}
}

func TestValidLinkingTokenGoesToTheCoordinatorOnly(t *testing.T) {
	handler, transport, out := newTestHandler(t)
	var linked []string
	handler.WithLinkConsumer(func(_ context.Context, payload, userID string) error {
		if payload != "tok" {
			return errors.New("bad token")
		}
		linked = append(linked, userID)
		return nil
	})
	if err := handler.Handle(context.Background(), Inbound{MessageID: "1", ChannelID: "dm-x", AuthorID: "stranger", Content: "/link tok"}); err != nil {
		t.Fatal(err)
	}
	if len(linked) != 1 || linked[0] != "stranger" || len(out.events) != 0 {
		t.Fatalf("linked=%v events=%v", linked, out.events)
	}
	if len(transport.sent) != 1 || !strings.HasPrefix(transport.sent[0], "dm-x:Linked.") {
		t.Fatalf("sent=%v", transport.sent)
	}
	if err := handler.Handle(context.Background(), Inbound{MessageID: "2", ChannelID: "dm-x", AuthorID: "stranger", Content: "/link nope"}); err != nil {
		t.Fatal(err)
	}
	if len(transport.sent) != 2 || !strings.Contains(transport.sent[1], "invalid or expired") || strings.Contains(transport.sent[1], "nope") {
		t.Fatalf("sent=%v", transport.sent)
	}
	// A linked owner's /link is intercepted, never a turn.
	if err := handler.Handle(context.Background(), Inbound{MessageID: "3", ChannelID: "dm-a", AuthorID: "user-a", Content: "/link tok"}); err != nil {
		t.Fatal(err)
	}
	if len(out.events) != 0 || len(linked) != 1 {
		t.Fatalf("events=%v linked=%v", out.events, linked)
	}
}

func TestGuildGroupBotWebhookAndForeignDMsDoNoWork(t *testing.T) {
	handler, transport, out := newTestHandler(t)
	cases := map[string]Inbound{
		"owner in guild channel":   {MessageID: "1", ChannelID: "guild", GuildID: "g", AuthorID: "user-a", Content: "hi"},
		"owner in group DM":        {MessageID: "2", ChannelID: "group", AuthorID: "user-a", Content: "hi"},
		"bot":                      {MessageID: "3", ChannelID: "dm-a", AuthorID: "user-a", AuthorIsBot: true, Content: "hi"},
		"webhook":                  {MessageID: "4", ChannelID: "dm-a", AuthorID: "user-a", WebhookID: "w", Content: "hi"},
		"owner id in someone's DM": {MessageID: "5", ChannelID: "dm-b", AuthorID: "user-a", Content: "hi"},
		"spoofed author, no id":    {MessageID: "6", ChannelID: "dm-a", Content: "hi"},
		"empty text":               {MessageID: "7", ChannelID: "dm-a", AuthorID: "user-a", Content: "   "},
	}
	for name, in := range cases {
		if err := handler.Handle(context.Background(), in); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if len(out.events) != 0 || len(transport.sent) != 0 {
		t.Fatalf("events=%v sent=%v, want zero work", out.events, transport.sent)
	}
}

func TestOwnerBIsAnIndependentOwnerAndDecisionsAndAttachmentsAreRefused(t *testing.T) {
	handler, transport, out := newTestHandler(t)
	if err := handler.Handle(context.Background(), Inbound{MessageID: "1", ChannelID: "dm-b", AuthorID: "user-b", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if out.events[0].Owner != "bob" || out.events[0].Destination.ConversationID() != "discord:dm:dm-b" {
		t.Fatalf("event=%+v", out.events[0])
	}
	for _, in := range []Inbound{
		{MessageID: "2", ChannelID: "dm-b", AuthorID: "user-b", Content: "/approve 123"},
		{MessageID: "3", ChannelID: "dm-b", AuthorID: "user-b", Content: "/Reject"},
		{MessageID: "4", ChannelID: "dm-b", AuthorID: "user-b", Content: "look", Attachments: 1},
	} {
		if err := handler.Handle(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	if len(out.events) != 1 {
		t.Fatalf("events=%d, want the refused messages to start no turn", len(out.events))
	}
	if len(transport.sent) != 3 || !strings.Contains(transport.sent[0], "web panel") || !strings.Contains(transport.sent[2], "Attachments") {
		t.Fatalf("sent=%v", transport.sent)
	}
}

func TestQuoteIsBoundedAndSinkErrorsSurface(t *testing.T) {
	handler, _, out := newTestHandler(t)
	long := strings.Repeat("x", maxQuoteLength+50)
	if err := handler.Handle(context.Background(), Inbound{MessageID: "1", ChannelID: "dm-a", AuthorID: "user-a", Content: "why", Reference: &Reference{Content: long}}); err != nil {
		t.Fatal(err)
	}
	var message events.Message
	_ = json.Unmarshal(out.events[0].Payload, &message)
	if len(message.Quote.Text) != maxQuoteLength || message.Quote.OwnMessage {
		t.Fatalf("quote len=%d own=%v", len(message.Quote.Text), message.Quote.OwnMessage)
	}
	out.err = errors.New("queue full")
	if err := handler.Handle(context.Background(), Inbound{MessageID: "2", ChannelID: "dm-a", AuthorID: "user-a", Content: "again"}); err == nil {
		t.Fatal("a sink failure must be reported")
	}
}
