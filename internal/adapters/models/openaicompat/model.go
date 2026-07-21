package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type Model struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func New(baseURL, apiKey string, client *http.Client) *Model {
	if client == nil {
		client = http.DefaultClient
	}
	return &Model{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: client}
}

type requestBody struct {
	Model           string            `json:"model"`
	Messages        []providerMessage `json:"messages"`
	Tools           []providerTool    `json:"tools,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
}

type providerMessage struct {
	Role       string             `json:"role"`
	Content    string             `json:"content,omitempty"`
	Name       string             `json:"name,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
	ToolCalls  []providerToolCall `json:"tool_calls,omitempty"`
}

type providerTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type providerToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (m *Model) Generate(ctx context.Context, input ports.ModelRequest) (ports.ModelResponse, error) {
	body := requestBody{Model: input.Model, ReasoningEffort: input.ReasoningEffort}
	for _, message := range input.Messages {
		translated := providerMessage{Role: string(message.Role), Content: message.Content, Name: message.Name, ToolCallID: message.ToolCallID}
		for _, call := range message.ToolCalls {
			providerCall := providerToolCall{ID: call.ID, Type: "function"}
			providerCall.Function.Name, providerCall.Function.Arguments = call.Name, string(call.Arguments)
			translated.ToolCalls = append(translated.ToolCalls, providerCall)
		}
		body.Messages = append(body.Messages, translated)
	}
	for _, tool := range input.Tools {
		translated := providerTool{Type: "function"}
		translated.Function.Name, translated.Function.Description, translated.Function.Parameters = tool.Name, tool.Description, tool.Schema
		body.Tools = append(body.Tools, translated)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return ports.ModelResponse{}, fmt.Errorf("encode model request: %w", err)
	}
	response, err := m.request(ctx, encoded)
	if err != nil {
		return ports.ModelResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ports.ModelResponse{}, statusError(response.StatusCode)
	}
	var result struct {
		Choices []struct {
			Message providerMessage `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int64 `json:"prompt_tokens"`
			CompletionTokens    int64 `json:"completion_tokens"`
			TotalTokens         int64 `json:"total_tokens"`
			PromptTokensDetails struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionTokenDetails struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return ports.ModelResponse{}, fmt.Errorf("decode provider response: %w", err)
	}
	if len(result.Choices) == 0 {
		return ports.ModelResponse{}, errors.New("provider returned no choices")
	}
	providerResult := result.Choices[0].Message
	message := ports.Message{Role: ports.Role(providerResult.Role), Content: providerResult.Content, Name: providerResult.Name, ToolCallID: providerResult.ToolCallID}
	for _, call := range providerResult.ToolCalls {
		arguments := json.RawMessage(call.Function.Arguments)
		if !json.Valid(arguments) {
			return ports.ModelResponse{}, fmt.Errorf("provider returned invalid arguments for tool %q", call.Function.Name)
		}
		message.ToolCalls = append(message.ToolCalls, ports.ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: arguments})
	}
	return ports.ModelResponse{Message: message, Usage: ports.ModelUsage{
		PromptTokens: result.Usage.PromptTokens, CompletionTokens: result.Usage.CompletionTokens, TotalTokens: result.Usage.TotalTokens,
		CachedPromptTokens: result.Usage.PromptTokensDetails.CachedTokens, ReasoningTokens: result.Usage.CompletionTokenDetails.ReasoningTokens,
	}}, nil
}

func (m *Model) request(ctx context.Context, body []byte) (*http.Response, error) {
	for attempt := 0; attempt < 3; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+m.apiKey)
		request.Header.Set("Content-Type", "application/json")
		response, err := m.http.Do(request)
		transient := err != nil || response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		if !transient || attempt == 2 {
			if err != nil {
				return nil, fmt.Errorf("provider request: %w", err)
			}
			return response, nil
		}
		if response != nil {
			response.Body.Close()
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, errors.New("provider request failed")
}

func statusError(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("provider authentication failed (HTTP %d)", status)
	case http.StatusTooManyRequests:
		return fmt.Errorf("provider rate limit exceeded (HTTP %d)", status)
	case http.StatusRequestTimeout:
		return fmt.Errorf("provider request timed out (HTTP %d)", status)
	default:
		if status >= 500 {
			return fmt.Errorf("provider unavailable (HTTP %d)", status)
		}
		return fmt.Errorf("provider rejected request (HTTP %d)", status)
	}
}
