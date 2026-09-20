package commands

import (
	"context"
	"fmt"
	"strings"
)

// webLoginSenderKey carries the verified chat sender a /web command arrived
// from. Only bootstrap stamps it, and only for a Telegram message whose
// sender the webhook verified against the allowlist. Its absence is the
// normal case -- a web thread, a Discord DM, a schedule, a selection
// callback -- and means /web hands out the panel address and nothing more.
type webLoginSenderKey struct{}

// WithWebLoginSender marks ctx as a verified direct-message ingress that
// may mint a browser login link for senderID.
func WithWebLoginSender(ctx context.Context, senderID string) context.Context {
	senderID = strings.TrimSpace(senderID)
	if senderID == "" {
		return ctx
	}
	return context.WithValue(ctx, webLoginSenderKey{}, senderID)
}

func webLoginSender(ctx context.Context) (string, bool) {
	sender, ok := ctx.Value(webLoginSenderKey{}).(string)
	return sender, ok && sender != ""
}

// webCommand hands the person a way into the panel. A phone is the reason
// it exists: the panel's password is the one credential that cannot be
// pasted from the chat that needs it. From a verified Telegram chat the
// reply carries a single-use link that signs in as the account the sender
// maps to; from anywhere else it is the bare address and the login form.
func (s *CommandService) webCommand(ctx context.Context) (string, bool, error) {
	if s.PublicBaseURL == "" {
		return "The web panel address is unknown. Set `server.public_base_url` in config.yaml (or `EGGY_PUBLIC_BASE_URL`) and restart.", true, nil
	}
	sender, verified := webLoginSender(ctx)
	if !verified || s.WebLoginLink == nil {
		return fmt.Sprintf("**Eggy web panel**\n\n%s\n\nSign in there with your username and password.", s.PublicBaseURL), true, nil
	}
	link, err := s.WebLoginLink(ctx, sender)
	if err != nil {
		return fmt.Sprintf("**Eggy web panel**\n\n%s\n\nI could not make a sign-in link for this chat (%s). Sign in there with your username and password.", s.PublicBaseURL, err.Error()), true, nil
	}
	return fmt.Sprintf("**Eggy web panel**\n\n%s\n\nOpen it and press Continue to sign in as you for 12 hours. The link works once and expires in 5 minutes -- do not forward it; anyone who uses it first is signed in as you.", link), true, nil
}
