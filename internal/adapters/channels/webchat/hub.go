// Package webchat implements ports.Channel over Server-Sent Events: a Hub
// broadcasts to every currently-open browser connection for Eggy's single
// owner. There is no per-connection identity beyond "currently open" -- if
// two tabs are open, both receive every push.
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

type Hub struct {
	mu          sync.Mutex
	connections map[uint64]chan Event
	nextConnID  uint64
	nextMsgID   uint64
}

func NewHub() *Hub {
	return &Hub{connections: map[uint64]chan Event{}}
}

// Register opens a new connection and returns its event stream and an
// unregister function the caller must call exactly once (typically via
// defer) when the connection closes.
func (h *Hub) Register() (connID string, events <-chan Event, unregister func()) {
	h.mu.Lock()
	id := h.nextConnID
	h.nextConnID++
	channel := make(chan Event, connectionBuffer)
	h.connections[id] = channel
	h.mu.Unlock()

	return strconv.FormatUint(id, 10), channel, func() {
		h.mu.Lock()
		if channel, ok := h.connections[id]; ok {
			delete(h.connections, id)
			close(channel)
		}
		h.mu.Unlock()
	}
}

// Broadcast sends event to every open connection without ever blocking the
// caller: a connection whose buffer is full has the event dropped for that
// connection only.
func (h *Hub) Broadcast(event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, channel := range h.connections {
		select {
		case channel <- event:
		default:
		}
	}
}

// NextMessageID returns a unique ID safe to use in webchat's half of
// multiChannel's compound trackable-message IDs: it never contains ':' or
// '|', the two characters that scheme uses as separators.
func (h *Hub) NextMessageID() string {
	id := atomic.AddUint64(&h.nextMsgID, 1)
	return strconv.FormatUint(id, 36)
}
