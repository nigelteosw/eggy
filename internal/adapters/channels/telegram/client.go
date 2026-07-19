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
)

const maxMessageLength = 3500

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewClient(baseURL, token string, client *http.Client) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, http: client}
}

func (c *Client) Deliver(ctx context.Context, chatID, text string) error {
	_, err := c.deliver(ctx, chatID, text, nil)
	return err
}

func (c *Client) DeliverTrackable(ctx context.Context, chatID, text string) (string, error) {
	return c.deliver(ctx, chatID, text, nil)
}

func (c *Client) deliver(ctx context.Context, chatID, text string, extra map[string]any) (string, error) {
	chunks := splitMessage(text)
	var messageID string
	for i, chunk := range chunks {
		payloadExtra := map[string]any{"link_preview_options": map[string]bool{"is_disabled": true}}
		if i == len(chunks)-1 {
			for key, value := range extra {
				payloadExtra[key] = value
			}
		}
		id, err := c.sendMessage(ctx, chatID, chunk, payloadExtra)
		if err != nil {
			return messageID, err
		}
		messageID = id
	}
	return messageID, nil
}

func (c *Client) DeliverApproval(ctx context.Context, chatID string, approval approvals.Approval) error {
	markup := map[string]any{"inline_keyboard": [][]map[string]string{{
		{"text": "Approve", "callback_data": "approval:" + approval.ID + ":approve"},
		{"text": "Reject", "callback_data": "approval:" + approval.ID + ":reject"},
	}}}
	_, err := c.deliver(ctx, chatID, approval.Summary, map[string]any{"reply_markup": markup})
	return err
}

func (c *Client) EditText(ctx context.Context, chatID, messageID, text string) error {
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
	_, err := c.call(ctx, "editMessageText", build(true))
	if isParseError(err) {
		_, err = c.call(ctx, "editMessageText", build(false))
	}
	return err
}

func (c *Client) AnswerCallback(ctx context.Context, callbackQueryID string) error {
	_, err := c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": callbackQueryID})
	return err
}

func (c *Client) SendTyping(ctx context.Context, chatID string) error {
	_, err := c.call(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": "typing"})
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

func (c *Client) sendMessage(ctx context.Context, chatID, text string, extra map[string]any) (string, error) {
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
