package config

import (
	"errors"
	"reflect"
	"testing"
)

// The whole reason Values exists: the panel and the chat surface describe the
// same server with different words, and must still produce the same config.
// When each mapped its own fields, they drifted -- bearer_env against
// bearer_token_env, client_id against oauth_client_id -- so one server had two
// spellings depending on which surface the owner was holding.
func TestBothSurfacesDecodeToTheSameMCPServer(t *testing.T) {
	fromChat := Values{
		"url":               "https://mcp.example.com",
		"auth":              "bearer-env",
		"bearer_env":        "EXAMPLE_TOKEN",
		"client_id":         "abc123",
		"client_secret_env": "EXAMPLE_SECRET",
		"enabled":           "true",
	}
	fromPanel := Values{
		"url":                     "https://mcp.example.com",
		"auth":                    "bearer-env",
		"bearer_token_env":        "EXAMPLE_TOKEN",
		"oauth_client_id":         "abc123",
		"oauth_client_secret_env": "EXAMPLE_SECRET",
		"enabled":                 "true",
	}
	chat, err := fromChat.MCPServerInput("example")
	if err != nil {
		t.Fatalf("chat decode: %v", err)
	}
	panel, err := fromPanel.MCPServerInput("example")
	if err != nil {
		t.Fatalf("panel decode: %v", err)
	}
	if !reflect.DeepEqual(chat, panel) {
		t.Fatalf("surfaces disagree:\n chat = %+v\npanel = %+v", chat, panel)
	}
	if chat.BearerTokenEnv != "EXAMPLE_TOKEN" || chat.OAuthClientID != "abc123" {
		t.Fatalf("decoded = %+v", chat)
	}
}

// A value where a variable name belongs is almost always the secret itself.
// Both surfaces must refuse it, and must refuse it as the same kind of error,
// because the chat surface's reply to this one tells the owner to rotate it.
func TestEnvironmentFieldsRefuseAValue(t *testing.T) {
	for name, values := range map[string]Values{
		"mcp bearer, chat spelling":  {"bearer_env": "sk-live-not-a-name"},
		"mcp bearer, panel spelling": {"bearer_token_env": "sk-live-not-a-name"},
		"mcp oauth secret":           {"oauth_client_secret_env": "sk-live-not-a-name"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := values.MCPServerInput("example")
			assertFieldError(t, err, NotAnEnvironmentName)
		})
	}

	_, err := Values{"client_secret_env": "sk-live-not-a-name"}.GoogleInput()
	assertFieldError(t, err, NotAnEnvironmentName)
}

func TestUnknownFieldIsRefusedRatherThanIgnored(t *testing.T) {
	_, err := Values{"url": "https://x.example", "nonsense": "1"}.MCPServerInput("example")
	assertFieldError(t, err, UnknownField)

	_, err = Values{"clientid": "typo"}.GoogleInput()
	assertFieldError(t, err, UnknownField)
}

func TestBooleanFieldRefusesAnythingElse(t *testing.T) {
	_, err := Values{"enabled": "yes"}.MCPServerInput("example")
	assertFieldError(t, err, NotABoolean)
}

// The panel layers require_approval on itself, against the live action
// catalog. The decoder must not treat those keys as typos.
func TestGoogleApprovalKeysAreNotTypos(t *testing.T) {
	input, err := Values{
		"client_id":             "abc",
		"require_approval_mode": "custom",
		"require_approval":      "gmail.send",
	}.GoogleInput()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if input.RequireApproval != nil {
		t.Fatal("GoogleInput decoded require_approval; the caller owning the catalog must")
	}
}

func TestGoogleProductsSplitAndTrim(t *testing.T) {
	input, err := Values{"products": " calendar , gmail ,"}.GoogleInput()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(input.Products, []string{"calendar", "gmail"}) {
		t.Fatalf("products = %#v", input.Products)
	}
}

// A new server is enabled unless the surface says otherwise, which is what
// /mcp add has always done and what the panel's own form defaults to.
func TestAbsentEnabledDefaultsOn(t *testing.T) {
	input, err := Values{"url": "https://x.example"}.MCPServerInput("example")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !input.Enabled {
		t.Fatal("absent enabled decoded as off")
	}
	if input.Auth != "none" {
		t.Fatalf("auth = %q, want none", input.Auth)
	}
}

// ValidEnvironmentName must stay the rule Validate enforces. They were two
// byte-identical regexps in two packages; this is what now stops them drifting.
func TestValidEnvironmentNameMatchesValidation(t *testing.T) {
	for value, want := range map[string]bool{
		"GOOGLE_CLIENT_SECRET": true,
		"A":                    true,
		"lowercase":            false,
		"1LEADING_DIGIT":       false,
		"has-dash":             false,
		"":                     false,
	} {
		if got := ValidEnvironmentName(value); got != want {
			t.Fatalf("ValidEnvironmentName(%q)=%v, want %v", value, got, want)
		}
		if got := environmentNamePattern.MatchString(value); got != want {
			t.Fatalf("environmentNamePattern(%q)=%v, want %v", value, got, want)
		}
	}
}

func assertFieldError(t *testing.T, err error, want FieldErrorKind) {
	t.Helper()
	var fieldErr FieldError
	if !errors.As(err, &fieldErr) {
		t.Fatalf("error = %v, want a FieldError", err)
	}
	if fieldErr.Kind != want {
		t.Fatalf("error kind = %q, want %q", fieldErr.Kind, want)
	}
}
