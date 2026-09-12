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

// ChatResolver maps the acting account to the private chat the bot talks to
// it in. It is a closure over validated config, wired by bootstrap: an
// account with no Telegram sender resolves to nothing, and nothing is where
// its Telegram output goes -- never the first configured person's chat.
type ChatResolver func(accountID string) (chatID string, ok bool)

// FixedChat resolves every account to one chat. It is the single-owner
// shape and what tests use; a multi-account deployment never constructs it.
func FixedChat(chatID string) ChatResolver {
	return func(string) (string, bool) { return chatID, true }
}

// ErrNoRecipient reports a delivery for an account that has no Telegram
// chat: a web-only person, or a turn with no principal at all.
var ErrNoRecipient = errors.New("no Telegram chat for this account")

type Client struct {
	baseURL string
	token   string
	// chats resolves the destination chat from the principal on each call.
	// Telegram is one fixed conversation per person, so the chat is a
	// property of who Eggy is talking to rather than of the message.
	chats ChatResolver
	http  *http.Client
}

func NewClient(baseURL, token string, chats ChatResolver, client *http.Client) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, chats: chats, http: client}
}

// chatID is the one place a call learns which chat it is for.
func (c *Client) chatID(ctx context.Context) (string, error) {
	principal, _ := ports.PrincipalFromContext(ctx)
	chatID, ok := c.chats(principal.AccountID)
	if !ok || chatID == "" {
		return "", ErrNoRecipient
	}
	return chatID, nil
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

func (c *Client) DeliverSelection(ctx context.Context, prompt, selectionID string, options []SelectOption) error {
	rows := make([][]map[string]string, 0, len(options))
	for index, option := range options {
		callbackData := "select:" + selectionID + ":" + strconv.Itoa(index)
		if len(callbackData) > 64 {
			return errors.New("Telegram selection callback exceeds 64 bytes")
		}
		rows = append(rows, []map[string]string{{
			"text":          option.Label,
			"callback_data": callbackData,
		}})
	}
	_, err := c.deliver(ctx, prompt, map[string]any{
		"reply_markup": map[string]any{"inline_keyboard": rows},
	})
	return err
}

func (c *Client) EditText(ctx context.Context, messageID, text string) error {
	chatID, err := c.chatID(ctx)
	if err != nil {
		return err
	}
	build := func(html bool) map[string]any {
		payload := map[string]any{"chat_id": chatID, "message_id": messageID}
		if html {
			payload["text"] = toTelegramHTML(text)
			payload["parse_mode"] = "HTML"
		} else {
			payload["text"] = text
		}
		return payload
	}
	_, err = c.call(ctx, "editMessageText", build(true))
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
	chatID, err := c.chatID(ctx)
	if err != nil {
		return err
	}
	_, err = c.call(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": "typing"})
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
	chatID, err := c.chatID(ctx)
	if err != nil {
		return "", err
	}
	build := func(html bool) map[string]any {
		payload := map[string]any{"chat_id": chatID}
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
