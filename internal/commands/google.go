package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
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
	action := "status"
	if len(args) > 0 {
		action = args[0]
	}
	// Configuration is reachable before the capability exists -- that is the
	// point of having it here. Everything else needs a running adapter, which
	// only a restart after a config write can produce.
	if action == "set" {
		return s.googleSet(args[1:])
	}
	if s.Google == nil {
		return "Google Workspace is not configured.\n\n" + googleUsage(), true, nil
	}
	switch action {
	case "status":
		return s.googleStatus()
	case "login":
		return s.googleLogin(ctx, args[1:])
	case "logout":
		if err := s.Google.Logout(); err != nil {
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
		authorizationURL, err := s.Google.BeginLogin(ctx)
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
	if err := s.Google.CompleteLogin(ctx, code, state); err != nil {
		return fmt.Sprintf("Could not finish the Google login: %v", err), true, nil
	}
	return "Authorized Google. Its tools are available on the next turn.", true, nil
}

func (s *CommandService) googleStatus() (string, bool, error) {
	status, err := s.Google.Status()
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

// googleSet is why an owner holding a phone can set this up at all. It writes
// through the same internal/config helper the web panel calls, under the same
// lock and the same validation -- one administration authority, two views onto
// it, exactly as /mcp add does.
func (s *CommandService) googleSet(args []string) (string, bool, error) {
	if s.ConfigPath == "" {
		return "Google configuration is unavailable.", true, nil
	}
	if len(args) == 0 {
		return googleSetUsage, true, nil
	}
	input := config.GoogleInput{Enabled: true}
	for _, argument := range args {
		key, value, ok := strings.Cut(argument, "=")
		if !ok {
			return "Arguments are key=value pairs. " + googleSetUsage, true, nil
		}
		switch key {
		case "client_id":
			input.ClientID = value
		case "client_secret_env":
			// The name is config; the value is not. The same rule /mcp add
			// follows, and for the same reason: what lands here lands in the
			// message history.
			if !environmentName.MatchString(value) {
				return secretNameHint(key), true, nil
			}
			input.ClientSecretEnv = value
		case "products":
			input.Products = strings.Split(value, ",")
		case "enabled":
			switch value {
			case "true":
				input.Enabled = true
			case "false":
				input.Enabled = false
			default:
				return "enabled must be true or false.", true, nil
			}
		default:
			return fmt.Sprintf("Unknown field %q. %s", key, googleSetUsage), true, nil
		}
	}
	if err := config.SetGoogle(s.ConfigPath, input); err != nil {
		return fmt.Sprintf("Could not save Google configuration: %v", err), true, nil
	}
	message := "Saved Google Workspace. " + restartNotice
	if input.ClientSecretEnv != "" {
		message += fmt.Sprintf("\nSet %s in the deployment's environment — Eggy reads the client secret from there, never from chat.", input.ClientSecretEnv)
	}
	if input.Enabled {
		message += "\nThen run /google login to authorize it."
	}
	return message, true, nil
}

const googleSetUsage = "/google set client_id=<desktop client id> [client_secret_env=VAR] " +
	"[products=calendar,gmail,drive,docs,sheets,contacts] [enabled=true|false]"

func googleUsage() string {
	return strings.Join([]string{
		"Usage:",
		"/google — whether Google is authorized, and with which scopes",
		googleSetUsage,
		"/google login — start authorization and return the URL to approve",
		"/google login <pasted redirect URL or code> — finish it",
		"/google logout — discard the stored grant",
		"",
		"The OAuth client must be a Desktop app client. A Web application client cannot authorize this way.",
	}, "\n")
}
