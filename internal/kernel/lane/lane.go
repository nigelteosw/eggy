// Package lane defines capability lanes that separate everyday assistant
// work from explicit repository implementation.
package lane

import (
	"path/filepath"
	"strings"
	"unicode"
)

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
	"test", "bug", "feature", "broken",
	"error", "type", "struct", "interface", "vulnerability",
	"tool", "capability", "integration", "search",
}

var codeExtensions = map[string]bool{
	".c": true, ".cc": true, ".cpp": true, ".cs": true, ".css": true,
	".go": true, ".h": true, ".html": true, ".java": true, ".js": true,
	".json": true, ".jsx": true, ".md": true, ".py": true, ".rb": true,
	".rs": true, ".sh": true, ".sql": true, ".swift": true, ".toml": true,
	".ts": true, ".tsx": true, ".yaml": true, ".yml": true,
}

// Detect returns the capability lane for the given message text.
//
// It returns Implementation only for affirmative implementation language or
// an explicit mutation verb with concrete code context. Everything else,
// including ambiguous requests, returns Assistant.
func Detect(text string) Lane {
	lower := strings.ToLower(strings.ReplaceAll(text, "’", "'"))
	lower = strings.ReplaceAll(lower, "don't", "do not")
	lower = strings.ReplaceAll(lower, "dont", "do not")
	tokens := strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	// Questions about possible future implementation are planning, not
	// implementation authority.
	if hasAnyPhrase(lower, "what should", "what would", "which should", "should we", "should i") ||
		(isPlanningQuestion(tokens) && hasAnyToken(tokens, "implement", "refactor", "create", "open", "commit")) {
		return Assistant
	}

	// Explicit repository lifecycle requests are unambiguous coding workflow
	// requests even when they do not mention a particular code artefact.
	for _, phrase := range []string{
		"create a pr", "create an mr",
		"create a pull request", "create a merge request",
		"open a pr", "open an mr",
		"commit this", "commit the",
	} {
		if phraseIndex := strings.Index(lower, phrase); phraseIndex >= 0 && !textBeforeIsNegated(lower[:phraseIndex]) {
			return Implementation
		}
	}

	// Resumption is an explicit owner-controlled implementation lifecycle
	// request only when it names a coding run or session. Everyday requests to
	// continue an explanation remain in the assistant lane.
	if hasAnyToken(tokens, "run", "session") {
		for i, token := range tokens {
			if (token == "continue" || token == "resume") && !negated(tokens, i) {
				return Implementation
			}
		}
	}

	// Implement and refactor are strong signals, but only when affirmative.
	for i, token := range tokens {
		if (token == "implement" || token == "refactor") && !negated(tokens, i) {
			return Implementation
		}
	}

	// Other mutation verbs require both an exact verb token and concrete code
	// context. Exact tokens avoid treating "fixed" or "modified" as commands.
	if !hasCodeContext(tokens, lower) {
		return Assistant
	}
	for i, token := range tokens {
		switch token {
		case "fix", "change", "modify", "add", "remove", "update", "rewrite", "patch", "build":
			if !negated(tokens, i) {
				return Implementation
			}
		}
	}

	return Assistant
}

func hasAnyPhrase(text string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func hasAnyToken(tokens []string, candidates ...string) bool {
	for _, token := range tokens {
		for _, candidate := range candidates {
			if token == candidate {
				return true
			}
		}
	}
	return false
}

func isPlanningQuestion(tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	switch tokens[0] {
	case "what", "which", "how", "why", "when", "where":
		return true
	default:
		return false
	}
}

func textBeforeIsNegated(text string) bool {
	tokens := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return negated(tokens, len(tokens))
}

func negated(tokens []string, actionIndex int) bool {
	start := actionIndex - 3
	if start < 0 {
		start = 0
	}
	for _, token := range tokens[start:actionIndex] {
		if token == "not" || token == "never" || token == "without" {
			return true
		}
	}
	return false
}

func hasCodeContext(tokens []string, text string) bool {
	for _, token := range tokens {
		for _, context := range codeContext {
			if token == context {
				return true
			}
		}
	}
	for _, field := range strings.Fields(text) {
		name := strings.Trim(field, "\"'`()[]{}<>,;:!?")
		if codeExtensions[strings.ToLower(filepath.Ext(name))] {
			return true
		}
	}
	return false
}
