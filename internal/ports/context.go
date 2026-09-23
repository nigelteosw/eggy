package ports

import (
	"context"
	"strings"
)

type AgentContext struct {
	Soul   string `json:"soul"`
	User   string `json:"user"`
	Memory string `json:"memory"`
	// Watch is the heartbeat's checklist: what the owner asked Eggy to keep
	// an eye on, and what Eggy has already said about each item. Unlike the
	// other three it is not rendered into every turn's system prompt -- a
	// heartbeat turn appends it itself, so ordinary turns pay nothing for it
	// and it cannot churn the prompt prefix that R6 wants byte-stable.
	Watch string `json:"watch"`
	// UserMaxBytes, MemoryMaxBytes, and WatchMaxBytes are the write budgets
	// ContextStore enforces on the agent-writable documents, used to render an
	// in-context usage indicator. Zero suppresses the indicator. Soul is
	// bounded too, but carries no indicator: it is rewritten whole, rarely,
	// and only when the owner asks.
	UserMaxBytes   int64 `json:"user_max_bytes,omitempty"`
	MemoryMaxBytes int64 `json:"memory_max_bytes,omitempty"`
	WatchMaxBytes  int64 `json:"watch_max_bytes,omitempty"`
}

type ContextDocument string

const (
	ContextSoul   ContextDocument = "soul"
	ContextUser   ContextDocument = "user"
	ContextMemory ContextDocument = "memory"
	// ContextWatch is the heartbeat's watch list. It holds things to look at,
	// never things with their own cadence: an entry that wants a time is a
	// schedule and belongs in the schedule store. That rule is what keeps it
	// from becoming a second scheduler, which is what retired the last
	// heartbeat (see retiredConfigFields in internal/config/config_init.go).
	ContextWatch ContextDocument = "watch"
)

// WatchListIsEmpty reports whether a watch list holds nothing to check.
//
// Blank lines and Markdown headings do not count: a document that is only its
// own title is what a store returns before anyone has written to it, and
// beating on it would run a model call to look at nothing.
//
// It lives here because two packages that may not import each other both need
// the same answer -- the daemon, deciding whether to skip a heartbeat tick,
// and the web panel, telling the owner whether their heartbeat is armed. When
// each had its own copy the panel could report an armed heartbeat while the
// daemon skipped every beat, and the only thing keeping them in agreement was
// a comment asking the next editor to change both.
func WatchListIsEmpty(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return false
	}
	return true
}

// ContextStore holds the agent's durable context documents. Load never fails
// on a document's content: a missing, blank, or unreadable document loads as
// its built-in default. Soul is prose and is only ever written whole, through
// ReplaceDocument; the entry methods refuse it.
//
// Entries are plain lines, addressed by a substring of their text rather than
// by any structural key, so the agent never has to model the file's layout.
// AddEntry appends one. ReplaceEntry and RemoveEntry act on the single entry
// containing oldText, and error when it matches no entry or more than one.
type ContextStore interface {
	Load(context.Context) (AgentContext, error)
	AddEntry(ctx context.Context, document ContextDocument, text string) error
	ReplaceEntry(ctx context.Context, document ContextDocument, oldText, text string) error
	RemoveEntry(ctx context.Context, document ContextDocument, oldText string) error
	// ReplaceDocument overwrites document wholesale. The entry methods above
	// are the right shape for a document edited a fact at a time; a heartbeat
	// rewriting its whole watch list is not that, and expressing it as N
	// substring matches would leave the list half-updated when one missed.
	ReplaceDocument(ctx context.Context, document ContextDocument, content string) error
}
