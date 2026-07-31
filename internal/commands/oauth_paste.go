// Both OAuth surfaces finish a login the same way: the owner pastes back what
// the browser landed on. The parsing is here rather than in either command
// because neither owns it -- /mcp login takes a callback URL Eggy itself
// served, /google login takes a loopback address nothing served, and the query
// string is the only thing they have in common.
package commands

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// parseOAuthRedirect accepts either the whole redirect URL or the bare code,
// because both are things an owner plausibly has in hand: the address bar, or
// the code copied out of it. A redirect carrying an error is reported with the
// authorization server's own reason rather than as a missing code, which is
// what a denied consent otherwise looks like from here.
func parseOAuthRedirect(pasted string) (code, state string, err error) {
	pasted = strings.TrimSpace(pasted)
	if !strings.HasPrefix(pasted, "http://") && !strings.HasPrefix(pasted, "https://") {
		return pasted, "", nil
	}
	redirect, parseErr := url.Parse(pasted)
	if parseErr != nil {
		return "", "", fmt.Errorf("that does not parse as a URL: %w", parseErr)
	}
	query := redirect.Query()
	if failure := query.Get("error"); failure != "" {
		if description := query.Get("error_description"); description != "" {
			return "", "", fmt.Errorf("authorization was refused: %s (%s)", failure, description)
		}
		return "", "", fmt.Errorf("authorization was refused: %s", failure)
	}
	if failure := googleAuthError(query.Get("authError")); failure != "" {
		return "", "", errors.New("authorization was refused: " + failure)
	}
	if query.Get("code") == "" {
		return "", "", fmt.Errorf("that URL has no code parameter — paste the address the browser landed on after you approved")
	}
	return query.Get("code"), query.Get("state"), nil
}

// googleAuthError reads the reason off Google's own error page.
//
// A refusal that happens before consent does not come back as ?error= on the
// redirect -- there is no redirect, because the redirect is what Google
// objected to. It lands on accounts.google.com/signin/oauth/error with the
// reason packed into an opaque base64 "authError" blob, and without unpacking
// it the owner is told only that their URL has no code, which describes the
// symptom and hides the cause.
//
// The blob is a length-delimited protobuf whose first field is the error code.
// Rather than depend on that shape, this pulls the printable runs out and
// takes the first token that looks like an OAuth error code.
func googleAuthError(encoded string) string {
	if encoded == "" {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(encoded, "="))
	if err != nil {
		return ""
	}
	code := oauthErrorCode.Find(decoded)
	if code == nil {
		return ""
	}
	failure := string(code)
	// Google's help text says only "register the redirect URI", which does not
	// say where or which of the two repairs applies. Both are real: a web
	// client matches its registered list exactly, port included, and localhost
	// is exempt from the HTTPS rule so it can be registered; a desktop client
	// needs no entry at all because loopback matching ignores the port.
	if failure == "redirect_uri_mismatch" {
		failure += " — the redirect URI in the authorization URL above is not authorized on this OAuth client. " +
			"Add it verbatim to the client's authorized redirect URIs (scheme, port and trailing slash all match exactly), " +
			"or use a Desktop app client, where a loopback redirect needs no entry and the port is not matched"
	}
	return failure
}

// oauthErrorCode matches the snake_case token OAuth error codes use, long
// enough not to catch protobuf framing bytes that happen to be printable.
var oauthErrorCode = regexp.MustCompile(`[a-z][a-z0-9]*(_[a-z0-9]+){1,4}`)
