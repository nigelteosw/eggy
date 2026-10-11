package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/core/events"
	"github.com/nigelteosw/eggy/internal/ports"
)

// defaultAlbumWindow is how long an album waits for its next photo. Telegram
// sends an album as one update per item, in quick succession, with nothing
// marking the last; a quiet gap is the only end there is.
const defaultAlbumWindow = 1500 * time.Millisecond

// album collects the updates of one media group. Telegram caps a group at ten
// items, which bounds it.
type album struct {
	event   events.Event // the first update's event: its ID, owner and sender stand for the group
	message events.Message
	caption string
	timer   *time.Timer
}

// addToAlbum folds a message that belongs to a media group into its album and
// (re)starts the quiet-window timer; the album is sunk as one message when the
// window passes with nothing new. The webhook has already answered 204 for the
// update by then, so a failed sink cannot be retried by Telegram -- the same
// trade the rest of this adapter makes for updates it cannot act on.
func (h *WebhookHandler) addToAlbum(ctx context.Context, key string, event events.Event, caption string) error {
	var message events.Message
	if err := json.Unmarshal(event.Payload, &message); err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	h.albumMu.Lock()
	defer h.albumMu.Unlock()
	if h.albums == nil {
		h.albums = map[string]*album{}
	}
	a := h.albums[key]
	if a == nil {
		a = &album{event: event, message: events.Message{Quote: message.Quote}}
		h.albums[key] = a
	}
	a.message.Parts = append(a.message.Parts, message.Parts...)
	if a.message.Quote == nil {
		a.message.Quote = message.Quote
	}
	if strings.TrimSpace(caption) != "" && a.caption == "" {
		a.caption = caption
	}
	window := h.albumWindow
	if window <= 0 {
		window = defaultAlbumWindow
	}
	if a.timer != nil {
		a.timer.Stop()
	}
	a.timer = time.AfterFunc(window, func() { h.flushAlbum(ctx, key) })
	return nil
}

func (h *WebhookHandler) flushAlbum(ctx context.Context, key string) {
	h.albumMu.Lock()
	a := h.albums[key]
	delete(h.albums, key)
	h.albumMu.Unlock()
	if a == nil {
		return
	}
	message := a.message
	switch {
	case a.caption != "":
		message.Text = a.caption
	case allImages(message.Parts):
		message.Text = "Describe these images."
	default:
		message.Text = "Read these files."
	}
	payload, _ := json.Marshal(message)
	a.event.Payload = payload
	_ = h.sink(ctx, a.event)
}

func allImages(parts []ports.ContentPart) bool {
	for _, part := range parts {
		if part.Type != ports.ModalityImage {
			return false
		}
	}
	return true
}
