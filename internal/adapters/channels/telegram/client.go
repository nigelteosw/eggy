package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// Telegram supports every optional channel affordance: bot API messages can
// be edited in place and the chat action doubles as a typing indicator.
var (
	_ ports.TrackableChannel = (*Client)(nil)
	_ ports.TypingChannel    = (*Client)(nil)
)

const maxMessageLength = 3500

type Client struct {
	baseURL string
	token   string
	// chatID is the single owner chat this bot ever talks to. Telegram is
	// one fixed conversation, so the target is bound here at construction
	// rather than passed on every ports.Channel call.
	chatID string
	http   *http.Client
}

func NewClient(baseURL, token, chatID string, client *http.Client) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, chatID: chatID, http: client}
}

func (c *Client) Deliver(ctx context.Context, text string) error {
	_, err := c.deliver(ctx, text, nil)
	return err
}

func (c *Client) DeliverTrackable(ctx context.Context, text string) (string, error) {
	return c.deliver(ctx, text, nil)
}

func (c *Client) deliver(ctx context.Context, text string, extra map[string]any) (string, error) {
	chunks := splitMessage(text)
	var messageID string
	for i, chunk := range chunks {
		payloadExtra := map[string]any{"link_preview_options": map[string]bool{"is_disabled": true}}
		if i == len(chunks)-1 {
			for key, value := range extra {
				payloadExtra[key] = value
			}
		}
		id, err := c.sendMessage(ctx, chunk, payloadExtra)
		if err != nil {
			return messageID, err
		}
		messageID = id
	}
	return messageID, nil
}

func (c *Client) DeliverApproval(ctx context.Context, approval approvals.Approval) error {
	markup := map[string]any{"inline_keyboard": [][]map[string]string{{
		{"text": "Approve", "callback_data": "approval:" + approval.ID + ":approve"},
		{"text": "Reject", "callback_data": "approval:" + approval.ID + ":reject"},
	}}}
	_, err := c.deliver(ctx, approval.Summary, map[string]any{"reply_markup": markup})
	return err
}

func (c *Client) EditText(ctx context.Context, messageID, text string) error {
	build := func(html bool) map[string]any {
		payload := map[string]any{"chat_id": c.chatID, "message_id": messageID}
		if html {
			payload["text"] = toTelegramHTML(text)
			payload["parse_mode"] = "HTML"
		} else {
			payload["text"] = text
		}
		return payload
	}
	_, err := c.call(ctx, "editMessageText", build(true))
	if isParseError(err) {
		_, err = c.call(ctx, "editMessageText", build(false))
	}
	return err
}

// AnswerCallback acknowledges a button tap so Telegram stops showing the
// user a spinner. It is not part of ports.Channel: it belongs to receiving
// an update, and WebhookHandler calls it as the update arrives.
func (c *Client) AnswerCallback(ctx context.Context, callbackQueryID string) error {
	_, err := c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": callbackQueryID})
	return err
}

func (c *Client) SendTyping(ctx context.Context) error {
	_, err := c.call(ctx, "sendChatAction", map[string]any{"chat_id": c.chatID, "action": "typing"})
	return err
}

type BotCommand struct {
	Name        string
	Description string
}

func (c *Client) SetCommands(ctx context.Context, commands []BotCommand) error {
	payloadCommands := make([]map[string]string, 0, len(commands))
	for _, command := range commands {
		payloadCommands = append(payloadCommands, map[string]string{"command": command.Name, "description": command.Description})
	}
	_, err := c.call(ctx, "setMyCommands", map[string]any{"commands": payloadCommands})
	return err
}

func (c *Client) sendMessage(ctx context.Context, text string, extra map[string]any) (string, error) {
	build := func(html bool) map[string]any {
		payload := map[string]any{"chat_id": c.chatID}
		if html {
			payload["text"] = toTelegramHTML(text)
			payload["parse_mode"] = "HTML"
		} else {
			payload["text"] = text
		}
		for key, value := range extra {
			payload[key] = value
		}
		return payload
	}
	result, err := c.call(ctx, "sendMessage", build(true))
	if isParseError(err) {
		result, err = c.call(ctx, "sendMessage", build(false))
	}
	if err != nil {
		return "", err
	}
	var parsed struct {
		MessageID int64 `json:"message_id"`
	}
	_ = json.Unmarshal(result, &parsed)
	return strconv.FormatInt(parsed.MessageID, 10), nil
}

func isParseError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "can't parse entities")
}

func (c *Client) call(ctx context.Context, method string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Telegram request: %w", err)
	}
	defer response.Body.Close()
	var result struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("Telegram returned HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("decode Telegram response: %w", err)
	}
	if !result.OK {
		if result.Description == "" {
			result.Description = fmt.Sprintf("Telegram returned HTTP %d", response.StatusCode)
		}
		return nil, errors.New(result.Description)
	}
	return result.Result, nil
}

// splitMessage breaks text into chunks that fit Telegram's message length
// limit, preferring to cut at the last newline within the window so
// paragraphs and code fences are not split mid-line where avoidable.
func splitMessage(text string) []string {
	runes := []rune(text)
	if len(runes) <= maxMessageLength {
		return []string{text}
	}
	var chunks []string
	for len(runes) > maxMessageLength {
		window := runes[:maxMessageLength]
		cut := lastIndexRune(window, '\n')
		if cut <= 0 {
			cut = maxMessageLength
		}
		chunks = append(chunks, string(runes[:cut]))
		runes = runes[cut:]
		for len(runes) > 0 && runes[0] == '\n' {
			runes = runes[1:]
		}
	}
	if len(runes) > 0 {
		chunks = append(chunks, string(runes))
	}
	return chunks
}

func lastIndexRune(runes []rune, target rune) int {
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] == target {
			return i
		}
	}
	return -1
}
