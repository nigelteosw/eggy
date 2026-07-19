// Package lane defines capability lanes that separate everyday assistant
// work from explicit repository implementation.
package lane

import "strings"

// Lane represents the capability lane for a single turn.
type Lane int

const (
	// Assistant is the everyday assistant lane. Repository modifications
	// are unavailable. Read-only inspection, explanation, planning, and
	// diagnosis are allowed.
	Assistant Lane = iota

	// Implementation carries explicit repository modification authority
	// for the current turn only. It is never carried into later messages,
	// scheduled turns, or heartbeats.
	Implementation
)

// codeContext lists terms that suggest a request is about code or
// repository artefacts rather than general conversation.
var codeContext = []string{
	"code", "file", "function", "method", "class",
	"module", "package", "endpoint", "handler", "route", "api",
	"repo", "repository",
	"test", "bug", "feature", "broken",
	"error", "type", "struct", "interface",
	"the todo", "todo.md",
}

// Detect returns the capability lane for the given message text.
//
// It returns Implementation only when the message contains explicit
// implementation language combined with code or repository context.
// Everything else, including ambiguous requests, returns Assistant.
func Detect(text string) Lane {
	lower := strings.ToLower(text)

	// Asking what to implement next is planning, not implementation authority.
	if strings.Contains(lower, "what would be a good thing to implement next") {
		return Assistant
	}

	// Strong single-word implementation signals.
	for _, kw := range []string{
		"implement", "refactor",
		"create a pr", "create an mr",
		"create a pull request", "create a merge request",
		"open a pr", "open an mr",
		"commit this", "commit the",
	} {
		if strings.Contains(lower, kw) {
			return Implementation
		}
	}

	// Action words that signal implementation only when accompanied
	// by a code-context term.
	actions := []string{"fix", "change", "modify", "add", "remove",
		"update", "rewrite", "patch"}

	for _, action := range actions {
		if strings.Contains(lower, action) {
			for _, ctx := range codeContext {
				if strings.Contains(lower, ctx) {
					return Implementation
				}
			}
		}
	}

	return Assistant
}
