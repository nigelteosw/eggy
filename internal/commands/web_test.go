package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestWebCommandRequiresVerifiedSender(t *testing.T) {
	calls := 0
	svc := New(Options{
		PublicBaseURL: "https://eggy.example",
		WebLoginLink: func(context.Context, string) (string, error) {
			calls++
			return "https://eggy.example/auth/link#token=secret", nil
		},
	})
	ctx := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "partner"})
	reply, handled, err := svc.Execute(ctx, "/web")
	if err != nil || !handled || calls != 0 || strings.Contains(reply, "token=") {
		t.Fatalf("unverified mint: handled=%v calls=%d err=%v", handled, calls, err)
	}
	_, _, err = svc.Execute(WithWebLoginSender(ctx, "123"), "/web")
	if err != nil || calls != 1 {
		t.Fatalf("verified mint: calls=%d err=%v", calls, err)
	}
	// An empty sender is no sender.
	if _, _, err := svc.Execute(WithWebLoginSender(ctx, "  "), "/web"); err != nil || calls != 1 {
		t.Fatalf("blank sender minted: calls=%d err=%v", calls, err)
	}
}

func TestWebCommandPassesTheSenderAndReportsTheLink(t *testing.T) {
	var seen string
	svc := New(Options{
		PublicBaseURL: "https://eggy.example",
		WebLoginLink: func(_ context.Context, sender string) (string, error) {
			seen = sender
			return "https://eggy.example/auth/link#token=" + strings.Repeat("a", 43), nil
		},
	})
	reply, _, err := svc.Execute(WithWebLoginSender(context.Background(), "123"), "/web")
	if err != nil || seen != "123" {
		t.Fatalf("sender=%q err=%v", seen, err)
	}
	if !strings.Contains(reply, "/auth/link#token=") || !strings.Contains(reply, "Continue") || !strings.Contains(reply, "expires in 5 minutes") {
		t.Fatalf("reply=%q", reply)
	}
	// A refused mint falls back to the address without a token.
	svc.WebLoginLink = func(context.Context, string) (string, error) { return "", errors.New("this chat is not mapped") }
	reply, _, err = svc.Execute(WithWebLoginSender(context.Background(), "123"), "/web")
	if err != nil || strings.Contains(reply, "token=") || !strings.Contains(reply, "not mapped") {
		t.Fatalf("refused reply=%q err=%v", reply, err)
	}
}
