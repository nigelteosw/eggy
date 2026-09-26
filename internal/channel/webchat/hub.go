// Package webchat implements ports.Channel over Server-Sent Events: a Hub
// broadcasts to every browser connection currently open for one thread.
// Each SSE connection registers with the thread ID from its URL, and a
// broadcast only reaches connections registered for the target thread.
package webchat

import (
	"strconv"
	"sync"
	"sync/atomic"
)

type EventKind string

const (
	EventMessage  EventKind = "message"
	EventTyping   EventKind = "typing"
	EventEdit     EventKind = "edit"
	EventApproval EventKind = "approval"
	// EventProgress carries a transient status line ("Calling web_search...")
	// the browser draws beside its typing indicator. It is not a message:
	// it never enters history and the next reply supersedes it.
	EventProgress EventKind = "progress"
)

type ApprovalPayload struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

type Event struct {
	Kind     EventKind        `json:"kind"`
	ID       string           `json:"id,omitempty"`
	Text     string           `json:"text,omitempty"`
	Approval *ApprovalPayload `json:"approval,omitempty"`
}

// connectionBuffer bounds how many undelivered events a single slow or
// abandoned connection can accumulate before Broadcast starts dropping
// events for that connection specifically -- never blocking the caller.
const connectionBuffer = 32

type connection struct {
	accountID string
	threadID  string
	events    chan Event
}

type Hub struct {
	mu          sync.Mutex
	connections map[uint64]connection
	nextConnID  uint64
	// nextMsgID is atomic, not mu-guarded, unlike nextConnID directly above
	// it. The type carries that distinction so the two cannot be confused.
	nextMsgID atomic.Uint64
}

func NewHub() *Hub {
	return &Hub{connections: map[uint64]connection{}}
}

// Register opens a new connection scoped to one account's thread and
// returns its event stream and an unregister function the caller must call
// exactly once (typically via defer) when the connection closes. The account
// comes from the caller's verified session, never from the browser: a
// connection registered for another account's thread ID receives nothing,
// because broadcasts are keyed by both.
func (h *Hub) Register(accountID, threadID string) (connID string, events <-chan Event, unregister func()) {
	h.mu.Lock()
	id := h.nextConnID
	h.nextConnID++
	channel := make(chan Event, connectionBuffer)
	h.connections[id] = connection{accountID: accountID, threadID: threadID, events: channel}
	h.mu.Unlock()

	return strconv.FormatUint(id, 10), channel, func() {
		h.mu.Lock()
		if conn, ok := h.connections[id]; ok {
			delete(h.connections, id)
			close(conn.events)
		}
		h.mu.Unlock()
	}
}

// Broadcast sends event to every connection currently registered for the
// account's thread, without ever blocking the caller: a connection whose
// buffer is full has the event dropped for that connection only.
func (h *Hub) Broadcast(accountID, threadID string, event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, conn := range h.connections {
		if conn.accountID != accountID || conn.threadID != threadID {
			continue
		}
		select {
		case conn.events <- event:
		default:
		}
	}
}

// CloseAccount ends every stream the account has open. It is what session
// revocation calls, so a signed-out or removed person's tab stops receiving
// the moment the session goes rather than when its next request fails.
func (h *Hub) CloseAccount(accountID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, conn := range h.connections {
		if conn.accountID != accountID {
			continue
		}
		delete(h.connections, id)
		close(conn.events)
	}
}

// NextMessageID returns a unique ID for a trackable webchat message
// (DeliverTrackable/EditText).
func (h *Hub) NextMessageID() string {
	id := h.nextMsgID.Add(1)
	return strconv.FormatUint(id, 36)
}
