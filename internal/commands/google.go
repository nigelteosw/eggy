package commands

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// GoogleRuntime is the owner's half of the Google integration: one grant to
// start, finish, inspect, or discard. There is no per-product command because
// there is no per-product grant -- authorizing once covers every configured
// product, which is the difference that made this path worth building.
type GoogleRuntime interface {
	BeginLogin(ctx context.Context) (string, error)
	CompleteLogin(ctx context.Context, code, state string) error
	Status() (GoogleStatus, error)
	Logout() error
}

type GoogleStatus struct {
	Authorized bool
	Scopes     []string
	Expiry     time.Time
}

func (s *CommandService) googleCommand(ctx context.Context, args []string) (string, bool, error) {
	if s.google == nil {
		return "Google Workspace is not configured. Set `google.enabled` and `google.client_id` in config.yaml, then restart.", true, nil
	}
	action := "status"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "status":
		return s.googleStatus()
	case "login":
		return s.googleLogin(ctx, args[1:])
	case "logout":
		if err := s.google.Logout(); err != nil {
			return fmt.Sprintf("Could not sign out of Google: %v", err), true, nil
		}
		return "Signed out of Google. Run /google login to authorize again.", true, nil
	default:
		return googleUsage(), true, nil
	}
}

// googleLogin is the Hermes flow, and the reason this integration authorizes
// where the MCP one did not: the redirect is a loopback address nothing is
// listening on, so the browser fails to connect and the owner pastes the URL
// it failed on. Nothing has to be registered, reachable, or public.
func (s *CommandService) googleLogin(ctx context.Context, args []string) (string, bool, error) {
	if len(args) > 1 {
		return "Usage: /google login [pasted redirect URL or code]", true, nil
	}
	if len(args) == 0 {
		authorizationURL, err := s.google.BeginLogin(ctx)
		if err != nil {
			return fmt.Sprintf("Could not start the Google login: %v", err), true, nil
		}
		return strings.Join([]string{
			"Authorize Google here:",
			authorizationURL,
			"",
			"The browser will fail to load the page it lands on — that is expected, nothing is listening there.",
			"Copy that failed page's address and send:",
			"/google login <paste the whole URL>",
		}, "\n"), true, nil
	}
	code, state, err := parseOAuthRedirect(args[0])
	if err != nil {
		return fmt.Sprintf("Could not finish the Google login: %v", err), true, nil
	}
	if err := s.google.CompleteLogin(ctx, code, state); err != nil {
		return fmt.Sprintf("Could not finish the Google login: %v", err), true, nil
	}
	return "Authorized Google. Its tools are available on the next turn.", true, nil
}

func (s *CommandService) googleStatus() (string, bool, error) {
	status, err := s.google.Status()
	if err != nil {
		return fmt.Sprintf("Could not read Google status: %v", err), true, nil
	}
	if !status.Authorized {
		return "**Google** — not authorized\nRun /google login", true, nil
	}
	lines := []string{"**Google** — authorized"}
	if !status.Expiry.IsZero() {
		// The access token's expiry, not the grant's. Saying so avoids a weekly
		// panic about a token that renews itself every hour.
		lines = append(lines, "access token renews at "+status.Expiry.UTC().Format(time.RFC3339))
	}
	if len(status.Scopes) > 0 {
		lines = append(lines, "granted scopes:\n"+strings.Join(status.Scopes, "\n"))
	}
	return strings.Join(lines, "\n"), true, nil
}

func googleUsage() string {
	return strings.Join([]string{
		"Usage:",
		"/google — whether Google is authorized, and with which scopes",
		"/google login — start authorization and return the URL to approve",
		"/google login <pasted redirect URL or code> — finish it",
		"/google logout — discard the stored grant",
	}, "\n")
}
