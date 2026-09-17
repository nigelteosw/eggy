package discord

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

func recipients(accountID string) (string, bool) {
	switch accountID {
	case "alice":
		return "user-a", true
	case "bob":
		return "user-b", true
	}
	return "", false
}

func dmCtx(account, channelID string) context.Context {
	ctx := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: account})
	return destination.With(ctx, destination.Destination{Kind: destination.Discord, ChannelID: channelID})
}

func newTestChannel(t *testing.T) (*Channel, *fakeTransport) {
	t.Helper()
	handler, transport, _ := newTestHandler(t)
	return NewChannel(transport, recipients, "https://eggy.example/", handler), transport
}

func TestDeliverReachesOnlyTheOwnersOwnDM(t *testing.T) {
	channel, transport := newTestChannel(t)
	if err := channel.Deliver(dmCtx("alice", "dm-a"), "hello"); err != nil {
		t.Fatal(err)
	}
	if len(transport.sent) != 1 || transport.sent[0] != "dm-a:hello" {
		t.Fatalf("sent=%v", transport.sent)
	}
	// Alice's turn addressed at Bob's DM, a group, or with no DM: refused.
	for name, ctx := range map[string]context.Context{
		"someone else's DM": dmCtx("alice", "dm-b"),
		"group":             dmCtx("alice", "group"),
		"no channel":        dmCtx("alice", ""),
		"unlinked account":  dmCtx("carol", "dm-a"),
		"telegram":          ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "alice"}),
	} {
		if err := channel.Deliver(ctx, "leak"); err == nil {
			t.Fatalf("%s: expected refusal", name)
		}
	}
	if len(transport.sent) != 1 {
		t.Fatalf("sent=%v, want nothing more", transport.sent)
	}
}

func TestLongTextIsSplitAndEditsKeepTheHead(t *testing.T) {
	channel, transport := newTestChannel(t)
	text := strings.Repeat("line of text\n", 400)
	id, err := channel.DeliverTrackable(dmCtx("alice", "dm-a"), text)
	if err != nil || id == "" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if len(transport.sent) < 3 {
		t.Fatalf("sent=%d chunks", len(transport.sent))
	}
	for _, sent := range transport.sent {
		if len(sent) > maxMessageLength+len("dm-a:") {
			t.Fatalf("chunk too long: %d", len(sent))
		}
	}
	if err := channel.EditText(dmCtx("alice", "dm-a"), "m1", "done"); err != nil {
		t.Fatal(err)
	}
	if len(transport.edits) != 1 || transport.edits[0] != "dm-a:m1:done" {
		t.Fatalf("edits=%v", transport.edits)
	}
	if err := channel.SendTyping(dmCtx("alice", "dm-a")); err != nil || len(transport.typing) != 1 {
		t.Fatalf("typing=%v err=%v", transport.typing, err)
	}
	transport.sendErr = errors.New("discord down")
	if err := channel.Deliver(dmCtx("alice", "dm-a"), "x"); err == nil {
		t.Fatal("delivery errors must be reported")
	}
}

func TestDeliverApprovalIsANoticePointingAtThePanel(t *testing.T) {
	channel, transport := newTestChannel(t)
	if err := channel.DeliverApproval(dmCtx("alice", "dm-a"), approvals.Approval{ID: "a1", Summary: "Delete branch x"}); err != nil {
		t.Fatal(err)
	}
	if len(transport.sent) != 1 {
		t.Fatalf("sent=%v", transport.sent)
	}
	notice := transport.sent[0]
	if !strings.Contains(notice, "Delete branch x") || !strings.Contains(notice, "https://eggy.example") || strings.Contains(notice, "/approve") {
		t.Fatalf("notice=%q", notice)
	}
}

func TestSplitMessageBoundaries(t *testing.T) {
	if got := splitMessage(""); len(got) != 1 || got[0] != "" {
		t.Fatalf("empty=%q", got)
	}
	long := strings.Repeat("a", maxMessageLength*2+5)
	chunks := splitMessage(long)
	if len(chunks) != 3 || len(chunks[0]) != maxMessageLength || len(chunks[2]) != 5 {
		t.Fatalf("chunks=%d lens=%d,%d", len(chunks), len(chunks[0]), len(chunks[len(chunks)-1]))
	}
}
