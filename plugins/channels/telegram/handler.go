package telegram

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/ports"
)

type EventSink func(context.Context, events.Event) error

// CallbackAcknowledger clears the loading spinner Telegram shows on a
// tapped inline button. *Client implements it.
type CallbackAcknowledger interface {
	AnswerCallback(ctx context.Context, callbackQueryID string) error
}

type ImageDownloader interface {
	DownloadImage(ctx context.Context, fileID string, declaredSize int64, declaredMediaType string) (ports.ContentPart, error)
}

type WebhookHandler struct {
	ownerID int64
	secret  string
	sink    EventSink
	// acknowledger acks a tapped button as the update arrives. Acking is
	// part of receiving a Telegram update, not of delivering a reply, so it
	// happens here rather than on the asynchronous event path -- and it
	// keeps the callback query ID, a Telegram-only concept, out of the
	// events the rest of Eggy handles. May be nil (fake-adapter mode).
	acknowledger     CallbackAcknowledger
	downloader       ImageDownloader
	resolveSelection func(string) (string, bool)
}

func NewWebhookHandler(ownerID int64, secret string, sink EventSink, acknowledger CallbackAcknowledger) *WebhookHandler {
	return &WebhookHandler{ownerID: ownerID, secret: secret, sink: sink, acknowledger: acknowledger}
}

func (h *WebhookHandler) WithSelectionResolver(resolve func(string) (string, bool)) *WebhookHandler {
	h.resolveSelection = resolve
	return h
}

func (h *WebhookHandler) WithImageDownloader(downloader ImageDownloader) *WebhookHandler {
	h.downloader = downloader
	return h
}

type update struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64       `json:"message_id"`
		From      user        `json:"from"`
		Chat      chat        `json:"chat"`
		Text      string      `json:"text"`
		Caption   string      `json:"caption"`
		Photo     []photoSize `json:"photo"`
		Document  *document   `json:"document"`
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

type photoSize struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
}

type document struct {
	FileID    string `json:"file_id"`
	FileName  string `json:"file_name"`
	MediaType string `json:"mime_type"`
	FileSize  int64  `json:"file_size"`
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
	owner, ok := incomingOwner(incoming)
	if !ok {
		http.Error(w, "unsupported Telegram update", http.StatusBadRequest)
		return
	}
	if owner != h.ownerID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if incoming.Callback != nil && h.acknowledger != nil {
		// Best-effort: a failed ack only leaves a spinner on the owner's
		// button, and must not stop the decision itself from being handled.
		_ = h.acknowledger.AnswerCallback(r.Context(), incoming.Callback.ID)
	}
	event, err := h.normalize(r.Context(), incoming)
	if err != nil {
		if incoming.Callback != nil && strings.HasPrefix(incoming.Callback.Data, "select:") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.sink(r.Context(), event); err != nil {
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func incomingOwner(incoming update) (int64, bool) {
	if incoming.Message != nil {
		return incoming.Message.From.ID, true
	}
	if incoming.Callback != nil {
		return incoming.Callback.From.ID, true
	}
	return 0, false
}

func (h *WebhookHandler) normalize(ctx context.Context, incoming update) (events.Event, error) {
	base := events.Event{
		ID: "telegram:" + strconv.FormatInt(incoming.UpdateID, 10), Source: "telegram",
		Timestamp: time.Now().UTC(), CorrelationID: "telegram:" + strconv.FormatInt(incoming.UpdateID, 10),
		Destination: destination.Destination{Kind: destination.Telegram},
	}
	if incoming.Message != nil {
		message, err := h.normalizeMessage(ctx, incoming.Message)
		if err != nil {
			return events.Event{}, err
		}
		payload, _ := json.Marshal(message)
		base.Type, base.Owner, base.Payload = events.TypeMessage, strconv.FormatInt(incoming.Message.From.ID, 10), payload
		return base, nil
	}
	if incoming.Callback != nil {
		if strings.HasPrefix(incoming.Callback.Data, "select:") {
			if h.resolveSelection == nil {
				return events.Event{}, fmt.Errorf("invalid selection callback")
			}
			value, ok := h.resolveSelection(incoming.Callback.Data)
			if !ok {
				return events.Event{}, fmt.Errorf("invalid or expired selection callback")
			}
			payload, _ := json.Marshal(events.Message{Text: value})
			base.Type, base.Owner, base.Payload = events.TypeMessage, strconv.FormatInt(incoming.Callback.From.ID, 10), payload
			return base, nil
		}
		parts := strings.Split(incoming.Callback.Data, ":")
		if len(parts) != 3 || parts[0] != "approval" || (parts[2] != "approve" && parts[2] != "reject") {
			return events.Event{}, fmt.Errorf("invalid approval callback")
		}
		payload, _ := json.Marshal(events.ApprovalDecision{
			ApprovalID: parts[1],
			Approved:   parts[2] == "approve",
			MessageID:  strconv.FormatInt(incoming.Callback.Message.MessageID, 10),
		})
		base.Type, base.Owner, base.Payload = events.TypeApproval, strconv.FormatInt(incoming.Callback.From.ID, 10), payload
		return base, nil
	}
	return events.Event{}, fmt.Errorf("unsupported Telegram update")
}

func (h *WebhookHandler) normalizeMessage(ctx context.Context, message *struct {
	MessageID int64       `json:"message_id"`
	From      user        `json:"from"`
	Chat      chat        `json:"chat"`
	Text      string      `json:"text"`
	Caption   string      `json:"caption"`
	Photo     []photoSize `json:"photo"`
	Document  *document   `json:"document"`
}) (events.Message, error) {
	if len(message.Photo) == 0 && message.Document == nil {
		return events.Message{Text: message.Text}, nil
	}
	if h.downloader == nil {
		return events.Message{}, errors.New("Telegram image download is unavailable")
	}
	fileID, size, mediaType := "", int64(0), ""
	if len(message.Photo) > 0 {
		photo := message.Photo[len(message.Photo)-1]
		fileID, size, mediaType = photo.FileID, photo.FileSize, "image/jpeg"
	} else {
		fileID, size = message.Document.FileID, message.Document.FileSize
		mediaType = documentImageType(message.Document.MediaType, message.Document.FileName)
		if mediaType == "" {
			return events.Message{}, errors.New("Telegram document is not a supported image")
		}
	}
	part, err := h.downloader.DownloadImage(ctx, fileID, size, mediaType)
	if err != nil {
		return events.Message{}, fmt.Errorf("download Telegram image: %w", err)
	}
	text := message.Caption
	if strings.TrimSpace(text) == "" {
		text = "Describe this image."
	}
	return events.Message{Text: text, Parts: []ports.ContentPart{part}}, nil
}

func documentImageType(mediaType, fileName string) string {
	if normalized, ok := canonicalImageType(mediaType); ok {
		return normalized
	}
	if strings.TrimSpace(mediaType) != "" {
		return ""
	}
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return ""
	}
}
