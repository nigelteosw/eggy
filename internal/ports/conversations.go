package ports

import (
	"context"
	"time"
)

// StoredMessage is one durable, provider-neutral conversation message.
type StoredMessage struct {
	ID int64
	// ConversationID scopes a message to one thread: a web thread's own
	// generated ID, or Telegram's fixed, reserved thread ID. Never empty for
	// a message written through MemoryStore.WriteMessage.
	ConversationID string
	Role           Role
	Content        string
	Source         string
	CreatedAt      time.Time
}

// MemoryStore persists and recalls durable conversation messages.
type MemoryStore interface {
	WriteMessage(context.Context, StoredMessage) error
	// RecentMessages returns conversationID's most recent messages, oldest
	// first, bounded to limit -- the thread-scoped live turn-context window
	// that replaced a former global field on State.
	RecentMessages(ctx context.Context, conversationID string, limit int) ([]StoredMessage, error)
	// ResetConversation clears conversationID's live turn-context window
	// without deleting its durable history.
	ResetConversation(ctx context.Context, conversationID string, at time.Time) error
	// ConversationResetAt reports when conversationID was last cleared,
	// with found=false for a conversation that never has been. It is what
	// lets a trace say which stretch of a long-lived conversation it
	// belongs to: Telegram has exactly one conversation ID forever, so
	// without a reset marker its traces are one endless group.
	ConversationResetAt(ctx context.Context, conversationID string) (at time.Time, found bool, err error)
	SearchText(context.Context, string, int) ([]StoredMessage, error)
}

// Thread is one conversation surface: a web sidebar conversation, or
// Telegram's single fixed thread. Title is empty until auto-titled from the
// thread's first exchange.
//
// A thread is also where an attached workspace lives. Workspace is the
// checkout the thread's primitive tools act on, empty when none is
// attached; WorkspaceRepository names the repository it was cloned from.
// Keeping them here lets repository exploration continue across turns.
type Thread struct {
	ID string
	// Owner is the account the thread belongs to. It is filled by the store,
	// never by a caller: a thread read under a principal is that principal's,
	// and the one cross-account read (ThreadsWithWorkspace) carries it so
	// housekeeping can act as the owner.
	Owner               string
	Title               string
	Channel             string
	Workspace           string
	WorkspaceRepository string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// ThreadStore persists conversation threads. It is deliberately separate
// from MemoryStore: MemoryStore models messages, and a thread is a distinct
// concept that surfaces (web chat) and the core (workspace attachment)
// both need without either depending on a concrete storage adapter.
type ThreadStore interface {
	CreateThread(ctx context.Context, id, channel string, at time.Time) (Thread, error)
	ListThreads(ctx context.Context, channel string) ([]Thread, error)
	// GetThread reports found=false with a nil error when no such thread
	// exists.
	GetThread(ctx context.Context, id string) (thread Thread, found bool, err error)
	// SetThreadTitle auto-titles a thread; a no-op once it has a title.
	SetThreadTitle(ctx context.Context, id, title string) error
	// RenameThread sets a thread's title outright, overwriting any
	// auto-generated one. This is the owner naming the conversation.
	RenameThread(ctx context.Context, id, title string) error
	// DeleteThread removes a thread along with its messages and reset
	// marker. Deleting a thread that does not exist is not an error.
	DeleteThread(ctx context.Context, id string) error
	// AttachWorkspace records a checkout on a thread, creating the thread
	// row if this is a surface (Telegram) that never explicitly created
	// one. Replaces any previously attached workspace.
	AttachWorkspace(ctx context.Context, id, channel, repository, workspace string, at time.Time) error
	// DetachWorkspace clears a thread's attached workspace. Detaching a
	// thread with none is not an error.
	DetachWorkspace(ctx context.Context, id string) error
	// ThreadsWithWorkspace returns every thread that currently has a
	// workspace attached, for boot reconciliation and idle reaping.
	ThreadsWithWorkspace(ctx context.Context) ([]Thread, error)
}

// WorkspaceProbe reports whether a previously created workspace still
// exists on disk. A Runner implements it so a restart can reconcile durable
// thread -> checkout bindings against reality instead of trusting a record
// whose directory a volume wipe removed.
type WorkspaceProbe interface {
	Exists(ctx context.Context, workspace string) (bool, error)
}
