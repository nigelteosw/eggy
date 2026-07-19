package telegram

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/events"
)

type EventSink func(context.Context, events.Event) error

type WebhookHandler struct {
	ownerID int64
	secret  string
	sink    EventSink
}

func NewWebhookHandler(ownerID int64, secret string, sink EventSink) *WebhookHandler {
	return &WebhookHandler{ownerID: ownerID, secret: secret, sink: sink}
}

type update struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64  `json:"message_id"`
		From      user   `json:"from"`
		Chat      chat   `json:"chat"`
		Text      string `json:"text"`
	} `json:"message"`
	Callback *struct {
		ID      string `json:"id"`
		From    user   `json:"from"`
		Data    string `json:"data"`
		Message struct {
			MessageID int64 `json:"message_id"`
			Chat      chat  `json:"chat"`
		} `json:"message"`
	} `json:"callback_query"`
}

type user struct {
	ID int64 `json:"id"`
}
type chat struct {
	ID int64 `json:"id"`
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	provided := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if len(provided) != len(h.secret) || subtle.ConstantTimeCompare([]byte(provided), []byte(h.secret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var incoming update
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&incoming); err != nil {
		http.Error(w, "invalid Telegram update", http.StatusBadRequest)
		return
	}
	event, owner, err := normalize(incoming)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if owner != h.ownerID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := h.sink(r.Context(), event); err != nil {
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func normalize(incoming update) (events.Event, int64, error) {
	base := events.Event{
		ID: "telegram:" + strconv.FormatInt(incoming.UpdateID, 10), Source: "telegram",
		Timestamp: time.Now().UTC(), CorrelationID: "telegram:" + strconv.FormatInt(incoming.UpdateID, 10),
	}
	if incoming.Message != nil {
		payload, _ := json.Marshal(events.Message{ChatID: strconv.FormatInt(incoming.Message.Chat.ID, 10), Text: incoming.Message.Text})
		base.Type, base.Owner, base.Payload = events.TypeMessage, strconv.FormatInt(incoming.Message.From.ID, 10), payload
		return base, incoming.Message.From.ID, nil
	}
	if incoming.Callback != nil {
		parts := strings.Split(incoming.Callback.Data, ":")
		if len(parts) != 3 || parts[0] != "approval" || (parts[2] != "approve" && parts[2] != "reject") {
			return events.Event{}, 0, fmt.Errorf("invalid approval callback")
		}
		payload, _ := json.Marshal(events.ApprovalDecision{
			ApprovalID:      parts[1],
			Approved:        parts[2] == "approve",
			CallbackQueryID: incoming.Callback.ID,
			MessageID:       strconv.FormatInt(incoming.Callback.Message.MessageID, 10),
		})
		base.Type, base.Owner, base.Payload = events.TypeApproval, strconv.FormatInt(incoming.Callback.From.ID, 10), payload
		return base, incoming.Callback.From.ID, nil
	}
	return events.Event{}, 0, fmt.Errorf("unsupported Telegram update")
}
