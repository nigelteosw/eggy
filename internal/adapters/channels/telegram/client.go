package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
)

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
	return c.send(ctx, map[string]any{
		"chat_id":              chatID,
		"text":                 text,
		"link_preview_options": map[string]bool{"is_disabled": true},
	})
}

func (c *Client) DeliverApproval(ctx context.Context, chatID string, approval approvals.Approval) error {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    approval.Summary,
		"reply_markup": map[string]any{"inline_keyboard": [][]map[string]string{{
			{"text": "Approve", "callback_data": "approval:" + approval.ID + ":approve"},
			{"text": "Reject", "callback_data": "approval:" + approval.ID + ":reject"},
		}}},
	}
	return c.send(ctx, payload)
}

func (c *Client) send(ctx context.Context, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("Telegram request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Telegram returned HTTP %d", response.StatusCode)
	}
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode Telegram response: %w", err)
	}
	if !result.OK {
		if result.Description == "" {
			result.Description = "request failed"
		}
		return errors.New(result.Description)
	}
	return nil
}
