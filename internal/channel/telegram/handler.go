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
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/core/events"
	"github.com/nigelteosw/eggy/internal/ports"
)

type EventSink func(context.Context, events.Event) error
type PairingConsumer func(context.Context, string, int64) error

// PairingNotifier answers a pairing attempt in the sender's own private chat.
// The sender has no account yet, so the chat is named rather than resolved.
// *Client implements it.
type PairingNotifier interface {
	DeliverToChat(ctx context.Context, chatID, text string) error
}

// CallbackAcknowledger clears the loading spinner Telegram shows on a
// tapped inline button. *Client implements it.
type CallbackAcknowledger interface {
	AnswerCallback(ctx context.Context, callbackQueryID, text string) error
	// ClearButtons removes the inline keyboard from a message in the acting
	// account's chat, once a choice on it is settled.
	ClearButtons(ctx context.Context, messageID string) error
}

// FileDownloader fetches a Telegram photo or document as a content part.
// *Client implements it.
type FileDownloader interface {
	DownloadFile(ctx context.Context, fileID string, declaredSize int64, declaredMediaType, filename string) (ports.ContentPart, error)
}

// Replier tells a verified sender why their message was dropped. It is
// addressed through the principal on the context, like any delivery, and
// may be nil (fake-adapter mode), in which case the drop is silent.
type Replier interface {
	Deliver(ctx context.Context, text string) error
}

// SenderResolver maps a verified numeric Telegram sender to the account it
// speaks for. A closure over validated config, wired by bootstrap; an
// unmapped sender resolves to nothing and is refused.
type SenderResolver func(senderID int64) (accountID string, ok bool)

// SingleOwner is the legacy mapping: one sender, whose account ID is the
// sender's own numeric ID spelled out. Tests use it; bootstrap resolves
// from config in both shapes.
func SingleOwner(ownerID int64) SenderResolver {
	return func(senderID int64) (string, bool) {
		if senderID != ownerID {
			return "", false
		}
		return strconv.FormatInt(ownerID, 10), true
	}
}

type WebhookHandler struct {
	resolve SenderResolver
	secret  string
	sink    EventSink
	// acknowledger acks a tapped button as the update arrives. Acking is
	// part of receiving a Telegram update, not of delivering a reply, so it
	// happens here rather than on the asynchronous event path -- and it
	// keeps the callback query ID, a Telegram-only concept, out of the
	// events the rest of Eggy handles. May be nil (fake-adapter mode).
	acknowledger     CallbackAcknowledger
	downloader       FileDownloader
	resolveSelection func(context.Context, string) (string, bool)
	pairing          PairingConsumer
	pairingNotifier  PairingNotifier
	replier          Replier
	// albums holds media groups still waiting for their next item; see
	// album.go. albumWindow is the quiet gap that ends one (zero means the
	// default).
	albumMu     sync.Mutex
	albums      map[string]*album
	albumWindow time.Duration
}

func (h *WebhookHandler) WithReplier(replier Replier) *WebhookHandler {
	h.replier = replier
	return h
}

// WithPairingConsumer redeems "/start <token>" from an unmapped sender, and
// answers the attempt through notify when it is non-nil.
func (h *WebhookHandler) WithPairingConsumer(consume PairingConsumer, notify PairingNotifier) *WebhookHandler {
	h.pairing = consume
	h.pairingNotifier = notify
	return h
}

// staleChoice is the toast for a choice that can no longer be made.
const staleChoice = "That choice expired or was already used. Ask again if you still need it."

// settleChoice answers a tap on a model-offered choice and removes the
// buttons it was on, whether or not the choice still stood: a current one has
// just been made, and a stale one can never be made from that message again.
// Both calls are best-effort; neither decides what the tap did.
func (h *WebhookHandler) settleChoice(ctx context.Context, callback *callbackQuery, current bool) {
	if h.acknowledger == nil {
		return
	}
	toast := ""
	if !current {
		toast = staleChoice
	}
	_ = h.acknowledger.AnswerCallback(ctx, callback.ID, toast)
	_ = h.acknowledger.ClearButtons(ctx, strconv.FormatInt(callback.Message.MessageID, 10))
}

// pairedMessage and pairingRefused are the two answers a pairing attempt
// gets. A refusal says the same thing whatever the reason -- expired, used,
// or an account already linked elsewhere -- so an unknown sender learns
// nothing about which accounts exist.
const (
	pairedMessage  = "You're linked. Hi, I'm Eggy: send me anything, or use /menu for the controls. /web opens the web panel with a one-tap sign-in link."
	pairingRefused = "That link didn't work: it may have expired or already been used, or the account may already be linked to another Telegram. Make a new link in the Eggy web panel and open it again."
)

// answerPairing replies to a pairing attempt in the chat it came from, which
// is the sender's own: the handler only reaches here for a private chat. A
// failed reply is not retried: a successful link already stands, and
// redelivering the update would only find its token spent.
func (h *WebhookHandler) answerPairing(ctx context.Context, sender int64, err error) {
	if h.pairingNotifier == nil {
		return
	}
	text := pairedMessage
	if err != nil {
		text = pairingRefused
	}
	_ = h.pairingNotifier.DeliverToChat(ctx, strconv.FormatInt(sender, 10), text)
}

func NewWebhookHandler(resolve SenderResolver, secret string, sink EventSink, acknowledger CallbackAcknowledger) *WebhookHandler {
	return &WebhookHandler{resolve: resolve, secret: secret, sink: sink, acknowledger: acknowledger}
}

func (h *WebhookHandler) WithSelectionResolver(resolve func(context.Context, string) (string, bool)) *WebhookHandler {
	h.resolveSelection = resolve
	return h
}

func (h *WebhookHandler) WithFileDownloader(downloader FileDownloader) *WebhookHandler {
	h.downloader = downloader
	return h
}

type update struct {
	UpdateID int64          `json:"update_id"`
	Message  *message       `json:"message"`
	Callback *callbackQuery `json:"callback_query"`
}

type callbackQuery struct {
	ID      string `json:"id"`
	From    user   `json:"from"`
	Data    string `json:"data"`
	Message struct {
		MessageID int64 `json:"message_id"`
		Chat      chat  `json:"chat"`
	} `json:"message"`
}

type message struct {
	MessageID int64  `json:"message_id"`
	From      user   `json:"from"`
	Chat      chat   `json:"chat"`
	Text      string `json:"text"`
	Caption   string `json:"caption"`
	// MediaGroupID is shared by the updates of one album; Telegram delivers
	// each item as its own update.
	MediaGroupID string      `json:"media_group_id"`
	Photo        []photoSize `json:"photo"`
	Document     *document   `json:"document"`
	// The kinds of message Eggy cannot read. Only their presence matters, so
	// they are kept raw; unsupportedKind names whichever arrived.
	Voice     json.RawMessage `json:"voice"`
	Audio     json.RawMessage `json:"audio"`
	Video     json.RawMessage `json:"video"`
	VideoNote json.RawMessage `json:"video_note"`
	Animation json.RawMessage `json:"animation"`
	Sticker   json.RawMessage `json:"sticker"`
	Location  json.RawMessage `json:"location"`
	Contact   json.RawMessage `json:"contact"`
	Poll      json.RawMessage `json:"poll"`
	// ReplyTo is the message the owner replied to, and Quote the part of it
	// they selected when they replied to a passage rather than the whole.
	ReplyTo *message `json:"reply_to_message"`
	Quote   *struct {
		Text string `json:"text"`
	} `json:"quote"`
}

// quote maps a Telegram reply onto the surface-neutral events.Quote. A
// partial selection wins over the whole message because it is what the owner
// pointed at; a caption stands in for text on media. Whether the passage is
// Eggy's own comes from Telegram itself: in the owner's private chat the only
// bot is Eggy.
func (m *message) quote() *events.Quote {
	if m.ReplyTo == nil {
		return nil
	}
	text := m.ReplyTo.Text
	if text == "" {
		text = m.ReplyTo.Caption
	}
	if m.Quote != nil && strings.TrimSpace(m.Quote.Text) != "" {
		text = m.Quote.Text
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return &events.Quote{Text: text, OwnMessage: m.ReplyTo.From.IsBot}
}

type user struct {
	ID    int64 `json:"id"`
	IsBot bool  `json:"is_bot"`
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
	// Everything from here down that Eggy declines is dropped with a 204,
	// never a 4xx: Telegram retries any non-2xx update and delivers nothing
	// further until it gives up, so refusing a stranger or a group message
	// with 403 would let anyone who can find the bot silence it for its
	// people. The secret check above stays a 401 because that request did
	// not come from Telegram.
	sender, chatID, ok := incomingSender(incoming)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Private chats only: in a group the sender and the chat differ, and a
	// reply would land where other people read it.
	if chatID != sender {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	accountID, ok := h.resolve(sender)
	if !ok {
		// An unmapped sender is ignored, silently, unless the message is a
		// pairing link, which is answered either way.
		if incoming.Message != nil && h.pairing != nil {
			parts := strings.Fields(incoming.Message.Text)
			if len(parts) == 2 && parts[0] == "/start" && strings.TrimSpace(parts[1]) != "" {
				h.answerPairing(r.Context(), sender, h.pairing(r.Context(), parts[1], sender))
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	choice := incoming.Callback != nil && strings.HasPrefix(incoming.Callback.Data, "select:")
	if incoming.Callback != nil && h.acknowledger != nil && !choice && !strings.HasPrefix(incoming.Callback.Data, "control:") {
		// Best-effort: a failed ack only leaves a spinner on the owner's
		// button, and must not stop the decision itself from being handled.
		// A menu tap is answered by its control, once the action has run, so
		// the toast can say what changed; a choice is answered once it has
		// resolved, below, for the same reason.
		_ = h.acknowledger.AnswerCallback(r.Context(), incoming.Callback.ID, "")
	}
	verified := ports.WithPrincipal(r.Context(), ports.Principal{AccountID: accountID})
	verified = destination.With(verified, destination.Destination{Kind: destination.Telegram})
	event, err := h.normalize(verified, incoming)
	if choice {
		h.settleChoice(verified, incoming.Callback, err == nil)
	}
	if err == nil {
		// The owner is the resolved account, never the sender's number as
		// text: the dispatcher validates it against configured accounts.
		event.Owner = accountID
		// Only a message typed in the verified private chat carries its
		// sender as metadata. A selection callback does not, even when the
		// selected text is a command: pressing a button is not typing.
		if incoming.Message != nil {
			event.SenderID = strconv.FormatInt(sender, 10)
		}
	}
	if err != nil {
		// A well-formed update from a verified sender is acknowledged even
		// when Eggy cannot act on it. Telegram retries any non-2xx response
		// and delivers nothing further until it gives up, so refusing one
		// PDF with a 400 silences the bot for everyone behind it. The
		// sender is told why instead; a failed reply is not worth a retry
		// of the update that caused it.
		if incoming.Message != nil && h.replier != nil {
			_ = h.replier.Deliver(verified, "I couldn't read that message: "+err.Error()+".")
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if incoming.Message != nil && incoming.Message.MediaGroupID != "" {
		key := strconv.FormatInt(sender, 10) + ":" + incoming.Message.MediaGroupID
		if err := h.addToAlbum(verified, key, event, incoming.Message.Caption); err != nil {
			http.Error(w, "update failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.sink(r.Context(), event); err != nil {
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func incomingSender(incoming update) (sender, chat int64, ok bool) {
	if incoming.Message != nil {
		return incoming.Message.From.ID, incoming.Message.Chat.ID, true
	}
	if incoming.Callback != nil {
		return incoming.Callback.From.ID, incoming.Callback.Message.Chat.ID, true
	}
	return 0, 0, false
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
		if strings.HasPrefix(incoming.Callback.Data, "control:") {
			payload, _ := json.Marshal(events.ControlRequest{Data: incoming.Callback.Data, MessageID: strconv.FormatInt(incoming.Callback.Message.MessageID, 10), CallbackID: incoming.Callback.ID})
			base.Type, base.Payload = events.TypeControl, payload
			base.SenderID = strconv.FormatInt(incoming.Callback.From.ID, 10)
			return base, nil
		}
		if strings.HasPrefix(incoming.Callback.Data, "select:") {
			if h.resolveSelection == nil {
				return events.Event{}, fmt.Errorf("invalid selection callback")
			}
			value, ok := h.resolveSelection(ctx, incoming.Callback.Data)
			if !ok || strings.HasPrefix(strings.TrimSpace(value), "/") {
				// A model-authored choice is never authority to run a direct command.
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

// unsupportedKind names a message Eggy cannot read, or "" for one it can.
// Telegram sends an animation with a document beside it, so this is checked
// before the document is.
func (m *message) unsupportedKind() string {
	for _, kind := range []struct {
		raw  json.RawMessage
		name string
	}{
		{m.Voice, "a voice message"}, {m.Audio, "an audio file"}, {m.Video, "a video"},
		{m.VideoNote, "a video message"}, {m.Animation, "a GIF"}, {m.Sticker, "a sticker"},
		{m.Location, "a location"}, {m.Contact, "a contact"}, {m.Poll, "a poll"},
	} {
		if len(kind.raw) > 0 && string(kind.raw) != "null" {
			return kind.name
		}
	}
	return ""
}

func (h *WebhookHandler) normalizeMessage(ctx context.Context, message *message) (events.Message, error) {
	// Refused before anything is downloaded or enqueued: a turn with nothing
	// to read costs a model call with nothing to answer, and a caption alone
	// would be answered as if it were the whole message.
	if kind := message.unsupportedKind(); kind != "" {
		return events.Message{}, fmt.Errorf("%s isn't something I can read yet; send text, a photo, or a PDF", kind)
	}
	if len(message.Photo) == 0 && message.Document == nil {
		if strings.TrimSpace(message.Text) == "" {
			return events.Message{}, errors.New("there was nothing in it I can read; send text, a photo, or a PDF")
		}
		return events.Message{Text: message.Text, Quote: message.quote()}, nil
	}
	if h.downloader == nil {
		return events.Message{}, errors.New("Telegram file download is unavailable")
	}
	fileID, size, mediaType, filename := "", int64(0), "", ""
	if len(message.Photo) > 0 {
		photo := message.Photo[len(message.Photo)-1]
		fileID, size, mediaType = photo.FileID, photo.FileSize, "image/jpeg"
	} else {
		fileID, size, filename = message.Document.FileID, message.Document.FileSize, message.Document.FileName
		mediaType = documentMediaType(message.Document.MediaType, message.Document.FileName)
		if mediaType == "" {
			return events.Message{}, errors.New("Telegram document is not a supported image or PDF")
		}
	}
	part, err := h.downloader.DownloadFile(ctx, fileID, size, mediaType, filename)
	if errors.Is(err, errFileUnavailable) {
		// Holding the update for a retry would hold every later one behind
		// it; the owner resending is the retry.
		return events.Message{}, errors.New("Telegram didn't hand the file over just now; send it again")
	}
	if err != nil {
		return events.Message{}, fmt.Errorf("download Telegram file: %w", err)
	}
	text := message.Caption
	if strings.TrimSpace(text) == "" {
		text = "Describe this image."
		if part.Type == ports.ModalityFile {
			text = "Read this file."
		}
	}
	return events.Message{Text: text, Parts: []ports.ContentPart{part}, Quote: message.quote()}, nil
}

// documentMediaType is what a Telegram document claims to be: its declared
// type when it is one Eggy reads, its extension when it declares nothing,
// and nothing when it declares something else. The download sniffs the
// bytes and holds them to this answer.
func documentMediaType(mediaType, fileName string) string {
	if normalized, _, ok := canonicalMediaType(mediaType); ok {
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
	case ".pdf":
		return "application/pdf"
	default:
		return ""
	}
}
