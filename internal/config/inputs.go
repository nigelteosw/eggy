package config

import (
	"fmt"
	"strings"
)

// Values is the untyped bag a surface collects before it becomes a typed
// Input: a web form's JSON object, or a chat command's key=value arguments.
//
// It exists because both surfaces hand-mapped the same fields onto the same
// Input structs, so the field list lived in two places and had already drifted:
// the same MCP field was bearer_env from Telegram and bearer_token_env from the
// panel, and client_id from one was oauth_client_id in the other -- one server,
// two spellings, depending on which surface the owner was holding. Both
// spellings are still accepted, because they are what is in every /mcp add
// already typed, but there is now one list saying so.
//
// Only decoding is shared. What a surface *says* about a bad field is its own
// business, which is why FieldError carries the field and the reason instead of
// a finished sentence: the chat surface answers a bad *_env with "rotate that
// secret, it is in your message history now", and flattening that into an HTTP
// 400 would lose the only advice that matters.
type Values map[string]string

// FieldErrorKind says what was wrong, so a surface picks its own wording
// without parsing an error string.
type FieldErrorKind string

const (
	UnknownField         FieldErrorKind = "unknown"
	NotAnEnvironmentName FieldErrorKind = "env_name"
	NotABoolean          FieldErrorKind = "boolean"
)

type FieldError struct {
	Field string
	Kind  FieldErrorKind
}

func (e FieldError) Error() string {
	switch e.Kind {
	case NotAnEnvironmentName:
		return fmt.Sprintf("%s must name an environment variable, not hold a value", e.Field)
	case NotABoolean:
		return fmt.Sprintf("%s must be true or false", e.Field)
	}
	return fmt.Sprintf("unknown field %q", e.Field)
}

// ValidEnvironmentName is the rule Validate enforces, exported so a surface can
// refuse a pasted secret at the point of entry with advice a validation error
// cannot give. It was a second, byte-identical regexp in internal/commands.
func ValidEnvironmentName(value string) bool { return environmentNamePattern.MatchString(value) }

func envName(field, value string) (string, error) {
	if value != "" && !ValidEnvironmentName(value) {
		return "", FieldError{Field: field, Kind: NotAnEnvironmentName}
	}
	return value, nil
}

func boolean(field, value string, fallback bool) (bool, error) {
	switch value {
	case "":
		return fallback, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, FieldError{Field: field, Kind: NotABoolean}
}

// MCPServerInput decodes the fields a surface may set on an MCP server. name is
// separate because the chat surface takes it positionally and the panel takes
// it in the body. A new server defaults to no auth and enabled, which is what
// /mcp add has always assumed and what the panel's form starts on.
func (v Values) MCPServerInput(name string) (MCPServerInput, error) {
	input := MCPServerInput{Name: name, Auth: "none", Enabled: true}
	var err error
	for key, value := range v {
		switch key {
		case "name":
		case "url":
			input.URL = value
		case "transport":
			input.Transport = value
		case "auth":
			if value != "" {
				input.Auth = value
			}
		case "oauth_client_id", "client_id":
			input.OAuthClientID = value
		case "bearer_token_env", "bearer_env":
			if input.BearerTokenEnv, err = envName(key, value); err != nil {
				return MCPServerInput{}, err
			}
		case "oauth_client_secret_env", "client_secret_env":
			if input.OAuthClientSecretEnv, err = envName(key, value); err != nil {
				return MCPServerInput{}, err
			}
		case "enabled":
			if input.Enabled, err = boolean(key, value, true); err != nil {
				return MCPServerInput{}, err
			}
		default:
			return MCPServerInput{}, FieldError{Field: key, Kind: UnknownField}
		}
	}
	return input, nil
}

// GoogleInput decodes the Google fields a surface may set. require_approval is
// accepted and ignored here: it is validated against the running adapter's
// action catalog, which only the panel holds, so the caller with one layers it
// on rather than this decoding it blind.
func (v Values) GoogleInput() (GoogleInput, error) {
	input := GoogleInput{Enabled: true}
	var err error
	for key, value := range v {
		switch key {
		case "client_id":
			input.ClientID = value
		case "products":
			input.Products = SplitCommaList(value)
		case "require_approval", "require_approval_mode":
		case "client_secret_env":
			if input.ClientSecretEnv, err = envName(key, value); err != nil {
				return GoogleInput{}, err
			}
		case "enabled":
			if input.Enabled, err = boolean(key, value, true); err != nil {
				return GoogleInput{}, err
			}
		default:
			return GoogleInput{}, FieldError{Field: key, Kind: UnknownField}
		}
	}
	return input, nil
}

// SplitCommaList is how list-valued fields arrive from both surfaces alike.
// Blank entries are dropped, so a trailing comma is not a product named "".
func SplitCommaList(value string) []string {
	var list []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			list = append(list, trimmed)
		}
	}
	return list
}
