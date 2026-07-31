package commands

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
)

type fakeGoogleRuntime struct {
	loginURL  string
	loginErr  error
	completed []completedLogin
	failure   error
	status    GoogleStatus
	loggedOut bool
}

func (f *fakeGoogleRuntime) BeginLogin(context.Context) (string, error) {
	return f.loginURL, f.loginErr
}

func (f *fakeGoogleRuntime) CompleteLogin(_ context.Context, code, state string) error {
	f.completed = append(f.completed, completedLogin{server: "google", code: code, state: state})
	return f.failure
}

func (f *fakeGoogleRuntime) Status() (GoogleStatus, error) { return f.status, nil }
func (f *fakeGoogleRuntime) Logout() error                 { f.loggedOut = true; return nil }

func googleService(runtime GoogleRuntime) *CommandService {
	return New(Options{Google: runtime})
}

// The whole point of the desktop-client flow: the browser cannot load the
// redirect, so the reply has to say that is expected and ask for the paste.
func TestGoogleLoginExplainsTheDeadRedirect(t *testing.T) {
	runtime := &fakeGoogleRuntime{loginURL: "https://accounts.google.com/o/oauth2/v2/auth?state=abc"}
	service := googleService(runtime)

	output := run(t, service, "/google login")
	if !strings.Contains(output, runtime.loginURL) {
		t.Fatalf("output=%q", output)
	}
	if !strings.Contains(output, "fail to load") || !strings.Contains(output, "/google login <paste") {
		t.Fatalf("output does not prepare the owner for a dead page: %q", output)
	}
}

func TestGoogleLoginCompletesFromAPaste(t *testing.T) {
	runtime := &fakeGoogleRuntime{}
	service := googleService(runtime)

	output := run(t, service, "/google login http://localhost:1/?state=abc&code=4/xyz")
	if !strings.Contains(output, "Authorized Google") {
		t.Fatalf("output=%q", output)
	}
	if len(runtime.completed) != 1 || runtime.completed[0].code != "4/xyz" || runtime.completed[0].state != "abc" {
		t.Fatalf("completed=%#v", runtime.completed)
	}

	run(t, service, "/google login 4/bare")
	if last := runtime.completed[len(runtime.completed)-1]; last.code != "4/bare" || last.state != "" {
		t.Fatalf("bare code completed=%#v", last)
	}

	runtime.failure = errors.New("Google token exchange failed: invalid_grant")
	if output := run(t, service, "/google login 4/expired"); !strings.Contains(output, "invalid_grant") {
		t.Fatalf("exchange failure output=%q", output)
	}
}

// A denied consent and a URL copied too early are different repairs, and both
// have to stop before an exchange is attempted.
func TestGoogleLoginReportsWhatTheRedirectSays(t *testing.T) {
	runtime := &fakeGoogleRuntime{}
	service := googleService(runtime)

	if output := run(t, service, "/google login http://localhost:1/?error=access_denied"); !strings.Contains(output, "access_denied") {
		t.Fatalf("denied output=%q", output)
	}
	if output := run(t, service, "/google login http://localhost:1/"); !strings.Contains(output, "no code parameter") {
		t.Fatalf("codeless output=%q", output)
	}
	if len(runtime.completed) != 0 {
		t.Fatalf("completed=%#v", runtime.completed)
	}
}

func TestGoogleStatusReportsGrantedScopes(t *testing.T) {
	runtime := &fakeGoogleRuntime{status: GoogleStatus{
		Authorized: true, Scopes: []string{"https://www.googleapis.com/auth/calendar"},
		Expiry: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
	}}
	service := googleService(runtime)

	output := run(t, service, "/google")
	if !strings.Contains(output, "authorized") || !strings.Contains(output, "auth/calendar") {
		t.Fatalf("output=%q", output)
	}

	runtime.status = GoogleStatus{}
	if output := run(t, service, "/google"); !strings.Contains(output, "/google login") {
		t.Fatalf("unauthorized output=%q", output)
	}
}

// An unconfigured capability must say so rather than fail on a nil runtime.
func TestGoogleCommandWithoutConfiguration(t *testing.T) {
	output := run(t, New(Options{}), "/google login")
	if !strings.Contains(output, "not configured") {
		t.Fatalf("output=%q", output)
	}
}

func TestGoogleLogout(t *testing.T) {
	runtime := &fakeGoogleRuntime{}
	if output := run(t, googleService(runtime), "/google logout"); !runtime.loggedOut || !strings.Contains(output, "/google login") {
		t.Fatalf("loggedOut=%v output=%q", runtime.loggedOut, output)
	}
}

// The same "one administration authority" property /mcp add has: a Telegram
// edit is readable by the helper the web panel calls, because both went
// through internal/config.
func TestGoogleSetWritesThroughInternalConfig(t *testing.T) {
	path := mcpTestConfig(t)
	service := New(Options{ConfigPath: path})

	output := run(t, service, "/google set client_id=x.apps.googleusercontent.com client_secret_env=GOOGLE_CLIENT_SECRET products=calendar,gmail")
	if !strings.Contains(output, "Restart") || !strings.Contains(output, "GOOGLE_CLIENT_SECRET") {
		t.Fatalf("output=%q", output)
	}
	stored, err := config.GetGoogleConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Enabled || stored.ClientID != "x.apps.googleusercontent.com" || len(stored.Products) != 2 {
		t.Fatalf("stored=%#v", stored)
	}
}

// Configuration has to be reachable before the adapter exists, or an owner on
// a phone can never get to the state where /google login is possible.
func TestGoogleSetWorksWithNoRunningAdapter(t *testing.T) {
	path := mcpTestConfig(t)
	service := New(Options{ConfigPath: path})
	if output := run(t, service, "/google login"); !strings.Contains(output, "not configured") {
		t.Fatalf("login without an adapter output=%q", output)
	}
	if output := run(t, service, "/google set client_id=x.apps.googleusercontent.com products=calendar"); !strings.Contains(output, "Saved") {
		t.Fatalf("set without an adapter output=%q", output)
	}
}

// A pasted secret must never be echoed back into the transcript, and the field
// takes a variable name in the first place.
func TestGoogleSetRefusesASecretValue(t *testing.T) {
	service := New(Options{ConfigPath: mcpTestConfig(t)})
	output := run(t, service, "/google set client_id=x.apps.googleusercontent.com client_secret_env=GOCSPX-realsecret")
	if strings.Contains(output, "GOCSPX-realsecret") {
		t.Fatalf("the pasted secret was echoed: %q", output)
	}
	if !strings.Contains(output, "rotate it") {
		t.Fatalf("output=%q", output)
	}
}

func TestGoogleSetRejectsAnUnknownField(t *testing.T) {
	service := New(Options{ConfigPath: mcpTestConfig(t)})
	if output := run(t, service, "/google set scopes=everything"); !strings.Contains(output, "Unknown field") {
		t.Fatalf("output=%q", output)
	}
}
