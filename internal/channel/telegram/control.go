package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type ControlService interface {
	Control(context.Context, string) (ports.ControlPanel, error)
}
type controlPending struct {
	owner     selectionScope
	messageID string
	expires   time.Time
	options   []ports.ControlOption
}

// Control is a bounded adapter-local registry of server-authored menu actions.
// Resolution happens after queue acceptance, rather than in the webhook.
type Control struct {
	client  *Client
	service ControlService
	now     func() time.Time
	mu      sync.Mutex
	pending map[string]controlPending
}

func NewControl(client *Client, service ControlService, now func() time.Time) *Control {
	if now == nil {
		now = time.Now
	}
	return &Control{client: client, service: service, now: now, pending: make(map[string]controlPending)}
}

func (c *Control) Show(ctx context.Context, action, messageID string) error {
	_, err := c.show(ctx, action, messageID)
	return err
}

// show renders the menu for an action and returns its first line, the result a
// setting tap reports, for the toast.
func (c *Control) show(ctx context.Context, action, messageID string) (string, error) {
	owner, err := selectionOwner(ctx)
	if err != nil {
		return "", err
	}
	panel, err := c.service.Control(ctx, action)
	if err != nil {
		return "", err
	}
	if text := []rune(panel.Text); len(text) > 3000 {
		panel.Text = string(text[:3000]) + "…\n\nMore details: /help or /web."
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw[:])
	rows := make([][]map[string]string, 0, len(panel.Options))
	for i, option := range panel.Options {
		rows = append(rows, []map[string]string{{"text": option.Label, "callback_data": fmt.Sprintf("control:%s:%d", id, i)}})
	}
	markup := map[string]any{"inline_keyboard": rows}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, p := range c.pending {
		if !c.now().Before(p.expires) {
			delete(c.pending, key)
		}
	}
	replaces := false
	for _, p := range c.pending {
		if p.owner == owner {
			replaces = true
		}
	}
	if len(c.pending) >= maxPendingSelections && !replaces {
		return "", fmt.Errorf("too many open menus; try again later")
	}
	if messageID != "" {
		chatID, chatErr := c.client.chatID(ctx)
		if chatErr != nil {
			return "", chatErr
		}
		_, err = c.client.call(ctx, "editMessageText", map[string]any{"chat_id": chatID, "message_id": messageID, "text": toTelegramHTML(panel.Text), "parse_mode": "HTML", "reply_markup": markup})
	} else {
		messageID, err = c.client.deliver(ctx, panel.Text, map[string]any{"reply_markup": markup})
	}
	if err != nil {
		return "", err
	}
	for key, p := range c.pending {
		if p.owner == owner {
			delete(c.pending, key)
		}
	}
	c.pending[id] = controlPending{owner: owner, messageID: messageID, expires: c.now().Add(defaultSelectionTTL), options: panel.Options}
	return toastText(panel.Text), nil
}

// Callback runs the action a menu button names and answers the tap. The answer
// is sent last, so its toast carries what the action did. A tap that finds no
// menu or no action still gets an answer, so the button stops spinning.
func (c *Control) Callback(ctx context.Context, data, messageID, callbackID string) (err error) {
	notice := ""
	defer func() {
		if callbackID != "" {
			_ = c.client.AnswerCallback(ctx, callbackID, notice)
		}
	}()
	owner, err := selectionOwner(ctx)
	if err != nil {
		return err
	}
	parts := strings.Split(data, ":")
	if len(parts) != 3 || parts[0] != "control" {
		return nil
	}
	index, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil
	}
	c.mu.Lock()
	p, ok := c.pending[parts[1]]
	if !ok || p.owner != owner || p.messageID != messageID || index < 0 || index >= len(p.options) {
		c.mu.Unlock()
		return nil
	}
	if !c.now().Before(p.expires) {
		delete(c.pending, parts[1])
		c.mu.Unlock()
		return c.client.Deliver(ctx, "This menu expired. Send /menu to open a new one.")
	}
	delete(c.pending, parts[1])
	c.mu.Unlock()
	notice, err = c.show(ctx, p.options[index].Action, messageID)
	if err != nil {
		c.mu.Lock()
		c.pending[parts[1]] = p
		c.mu.Unlock()
	}
	return err
}

// toastText is the menu's first line as a Telegram toast reads it: plain, and
// within the 200 characters Telegram allows.
func toastText(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	line = strings.NewReplacer("**", "", "*", "", "`", "").Replace(line)
	runes := []rune(line)
	if len(runes) > 200 {
		return string(runes[:199]) + "…"
	}
	return line
}
