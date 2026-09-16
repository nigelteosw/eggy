package openaicompat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

type Model struct {
	baseURL string
	apiKey  string
	http    *http.Client
	// OpenRouter extends the otherwise OpenAI-compatible request with routing
	// and cache hints. Keeping the switch here prevents those fields leaking
	// into providers that implement only the standard Chat Completions shape.
	openRouter bool
	// catalog caches what OpenRouter's /models says about each model's
	// reasoning, fetched once per process on first need. Telling a model
	// not to reason is only correct for one that can be told, and the
	// catalog is the only place that says which those are.
	catalog struct {
		sync.Mutex
		reasoning map[string]*ports.CatalogReasoning
	}
}

func New(baseURL, apiKey string, client *http.Client) *Model {
	if client == nil {
		client = http.DefaultClient
	}
	return &Model{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: client, openRouter: isOpenRouterURL(baseURL)}
}

// openRouterOrigin tags ports.Message.ProviderReasoning written by this
// adapter when talking to OpenRouter, so only OpenRouter requests replay it.
const openRouterOrigin = "openrouter"

type requestBody struct {
	Model    string                   `json:"model"`
	Messages []providerRequestMessage `json:"messages"`
	Tools    []providerTool           `json:"tools,omitempty"`
	// ReasoningEffort is the standard Chat Completions spelling; Reasoning is
	// OpenRouter's nested normalisation of the same knob across vendors, and
	// the only one of the two that can say "none". Exactly one is sent.
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	Reasoning       *reasoningOptions `json:"reasoning,omitempty"`
	SessionID       string            `json:"session_id,omitempty"`
	CacheControl    *cacheControl     `json:"cache_control,omitempty"`
	// Provider is OpenRouter's routing preference, forwarded as the bytes the
	// alias was configured with.
	Provider json.RawMessage `json:"provider,omitempty"`
}

type reasoningOptions struct {
	Effort string `json:"effort"`
}

type cacheControl struct {
	Type string `json:"type"`
}

// providerRequestMessage carries the role as Eggy spells it. Eggy only ever
// sends "system" for instructions, never OpenAI's newer "developer": every
// provider on this wire format accepts "system", while "developer" via
// OpenRouter is only honoured for anthropic/* and openai/* models.
type providerRequestMessage struct {
	Role       string             `json:"role"`
	Content    any                `json:"content,omitempty"`
	Name       string             `json:"name,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
	ToolCalls  []providerToolCall `json:"tool_calls,omitempty"`
	// ReasoningDetails is OpenRouter's opaque reasoning state, sent back
	// exactly as it arrived so a reasoning model keeps its own thinking
	// across tool-call rounds. See ports.Message.ProviderReasoning.
	ReasoningDetails json.RawMessage `json:"reasoning_details,omitempty"`
}

type providerResponseMessage struct {
	Role       string             `json:"role"`
	Content    string             `json:"content,omitempty"`
	Name       string             `json:"name,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
	ToolCalls  []providerToolCall `json:"tool_calls,omitempty"`
	// ReasoningContent is the visible chain-of-thought (DeepSeek's spelling);
	// Reasoning is OpenRouter's. Either is surfaced, neither is replayed.
	ReasoningContent string `json:"reasoning_content,omitempty"`
	Reasoning        string `json:"reasoning,omitempty"`
	// ReasoningDetails is kept as raw bytes: its entries are provider-specific
	// and some are encrypted, and the one thing OpenRouter asks is that they
	// go back unmodified.
	ReasoningDetails json.RawMessage `json:"reasoning_details,omitempty"`
}

type providerContentPart struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	ImageURL *providerImageURL `json:"image_url,omitempty"`
}

type providerImageURL struct {
	URL string `json:"url"`
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
	var headers http.Header
	if m.openRouter {
		// The conversation ID is the sticky-routing key, so consecutive turns
		// land on the same upstream and its prompt cache. OpenRouter reads it
		// from either the body or the header; both are sent so it is found
		// whichever one a given endpoint honours.
		body.SessionID = destination.FromContext(ctx).ConversationID()
		if body.SessionID != "" {
			headers = http.Header{"X-Session-Id": {body.SessionID}}
		}
		if isAnthropicModel(input.Model) {
			body.CacheControl = &cacheControl{Type: "ephemeral"}
		}
		body.Provider = input.ProviderRouting
		body.ReasoningEffort = ""
		if input.ReasoningEffort != "" {
			body.Reasoning = &reasoningOptions{Effort: input.ReasoningEffort}
		} else if input.ReasoningSupported && m.reasoningOptional(ctx, input.Model) {
			// The alias offers levels and none is chosen: say so, otherwise
			// a model that reasons by default keeps doing it. Only for a
			// model the catalog says can be switched off -- one that always
			// reasons is left alone rather than sent a parameter it refuses.
			body.Reasoning = &reasoningOptions{Effort: "none"}
		}
	}
	for _, message := range input.Messages {
		translated := providerRequestMessage{Role: string(message.Role), Content: message.Content, Name: message.Name, ToolCallID: message.ToolCallID}
		if m.openRouter && message.ProviderReasoningOrigin == openRouterOrigin {
			translated.ReasoningDetails = message.ProviderReasoning
		}
		if len(message.Parts) > 0 {
			content := []providerContentPart{{Type: "text", Text: message.Content}}
			for _, part := range message.Parts {
				if part.Type != ports.ContentTypeImage {
					return ports.ModelResponse{}, fmt.Errorf("unsupported message content type %q", part.Type)
				}
				if strings.TrimSpace(part.MediaType) == "" {
					return ports.ModelResponse{}, errors.New("image content is missing a media type")
				}
				if len(part.Data) == 0 {
					return ports.ModelResponse{}, errors.New("image content is empty")
				}
				content = append(content, providerContentPart{Type: "image_url", ImageURL: &providerImageURL{
					URL: "data:" + part.MediaType + ";base64," + base64.StdEncoding.EncodeToString(part.Data),
				}})
			}
			translated.Content = content
		}
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
	response, err := m.request(ctx, http.MethodPost, "/chat/completions", encoded, headers)
	if err != nil {
		return ports.ModelResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ports.ModelResponse{}, m.providerError(response)
	}
	var result struct {
		Choices []struct {
			Message providerResponseMessage `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens         int64 `json:"prompt_tokens"`
			CompletionTokens     int64 `json:"completion_tokens"`
			TotalTokens          int64 `json:"total_tokens"`
			PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
			PromptTokensDetails  struct {
				CachedTokens int64 `json:"cached_tokens"`
				// CacheWriteTokens is OpenRouter's separate write count; it
				// is not subtracted from cached_tokens, which are reads.
				CacheWriteTokens int64 `json:"cache_write_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionTokenDetails struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
			// Cost is OpenRouter's charge in USD for this call.
			Cost float64 `json:"cost"`
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
	if m.openRouter && len(providerResult.ReasoningDetails) > 0 && string(providerResult.ReasoningDetails) != "null" {
		message.ProviderReasoning, message.ProviderReasoningOrigin = providerResult.ReasoningDetails, openRouterOrigin
	}
	for _, call := range providerResult.ToolCalls {
		arguments := json.RawMessage(call.Function.Arguments)
		if !json.Valid(arguments) {
			return ports.ModelResponse{}, fmt.Errorf("provider returned invalid arguments for tool %q", call.Function.Name)
		}
		message.ToolCalls = append(message.ToolCalls, ports.ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: arguments})
	}
	reasoning := providerResult.ReasoningContent
	if reasoning == "" {
		reasoning = providerResult.Reasoning
	}
	return ports.ModelResponse{Message: message, ReasoningContent: reasoning, Usage: ports.ModelUsage{
		PromptTokens: result.Usage.PromptTokens, CompletionTokens: result.Usage.CompletionTokens, TotalTokens: result.Usage.TotalTokens,
		CachedPromptTokens: max(result.Usage.PromptTokensDetails.CachedTokens, result.Usage.PromptCacheHitTokens),
		CacheWriteTokens:   result.Usage.PromptTokensDetails.CacheWriteTokens,
		ReasoningTokens:    result.Usage.CompletionTokenDetails.ReasoningTokens,
		CostUSD:            result.Usage.Cost,
	}}, nil
}

// reasoningOptional reports whether OpenRouter's catalog says model reasons
// and can be told not to. Unknown -- the model is not listed, or the catalog
// could not be fetched -- reads as false, so nothing is sent that the model
// might reject. A failed fetch is not cached, so the next call tries again.
func (m *Model) reasoningOptional(ctx context.Context, model string) bool {
	m.catalog.Lock()
	defer m.catalog.Unlock()
	if m.catalog.reasoning == nil {
		models, err := m.ListModels(ctx)
		if err != nil {
			return false
		}
		m.catalog.reasoning = make(map[string]*ports.CatalogReasoning, len(models))
		for _, entry := range models {
			m.catalog.reasoning[entry.ID] = entry.Reasoning
		}
	}
	reasoning, ok := m.catalog.reasoning[strings.TrimPrefix(model, "~")]
	return ok && reasoning != nil && !reasoning.Mandatory
}

func isOpenRouterURL(baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai")
}

func isAnthropicModel(model string) bool {
	return strings.HasPrefix(strings.TrimPrefix(model, "~"), "anthropic/")
}

// ListModels reports the provider's own catalog from the same /models listing
// OpenAI defines, which every service speaking this wire format serves. It
// satisfies ports.ModelCatalog.
//
// The listing is returned as the provider gives it, minus entries with no id:
// deciding which of them are worth running is the owner's call at the moment
// they write an alias, and a filter here would only be this package guessing
// on their behalf. OpenRouter alone returns several hundred entries including
// image and audio models, so callers are expected to offer a search box.
func (m *Model) ListModels(ctx context.Context) ([]ports.CatalogModel, error) {
	response, err := m.request(ctx, http.MethodGet, "/models", nil, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, m.providerError(response)
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
			// Name, context_length, and reasoning are OpenRouter's additions
			// rather than part of OpenAI's own response, so all stay optional.
			Name          string `json:"name"`
			ContextLength int64  `json:"context_length"`
			Reasoning     *struct {
				Mandatory        bool     `json:"mandatory"`
				SupportedEfforts []string `json:"supported_efforts"`
			} `json:"reasoning"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode provider model list: %w", err)
	}
	models := make([]ports.CatalogModel, 0, len(result.Data))
	for _, entry := range result.Data {
		if strings.TrimSpace(entry.ID) == "" {
			continue
		}
		model := ports.CatalogModel{ID: entry.ID, Name: entry.Name, ContextLength: entry.ContextLength}
		if entry.Reasoning != nil {
			model.Reasoning = &ports.CatalogReasoning{Mandatory: entry.Reasoning.Mandatory, Efforts: entry.Reasoning.SupportedEfforts}
		}
		models = append(models, model)
	}
	return models, nil
}

// request retries transient failures. body may be nil, which is how a GET is
// spelled; a nil body must stay a nil io.Reader rather than an empty one, so
// that the request carries no Content-Length and reads as a plain GET.
func (m *Model) request(ctx context.Context, method, endpoint string, body []byte, headers http.Header) (*http.Response, error) {
	for attempt := 0; attempt < 3; attempt++ {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		request, err := http.NewRequestWithContext(ctx, method, m.baseURL+endpoint, reader)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+m.apiKey)
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		for name, values := range headers {
			request.Header[name] = values
		}
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

// providerError turns a failed response into an error the owner can act on.
// The status alone says which kind of failure it was; the body says why,
// which for a gateway like OpenRouter includes what the upstream vendor
// said. Every provider on this wire format uses OpenAI's {"error": {...}}
// envelope, so the same decoding serves all of them; a body that is not
// that shape is simply not quoted.
//
// An authentication failure is never quoted: providers have been known to
// echo the presented key, and this error lands in a chat surface. The key
// is scrubbed from every other body for the same reason.
func (m *Model) providerError(response *http.Response) error {
	err := statusError(response.StatusCode)
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return err
	}
	raw, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
	if m.apiKey != "" {
		raw = bytes.ReplaceAll(raw, []byte(m.apiKey), []byte("[redacted]"))
	}
	var envelope struct {
		Error struct {
			Message  string `json:"message"`
			Metadata struct {
				Raw          string `json:"raw"`
				ProviderName string `json:"provider_name"`
			} `json:"metadata"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return err
	}
	detail := strings.TrimSpace(envelope.Error.Message)
	// OpenRouter passes the upstream's own words through metadata.raw, which
	// is usually the only part that says what was actually wrong. It is
	// skipped when message already quotes it, so it is not printed twice.
	if upstream := strings.TrimSpace(envelope.Error.Metadata.Raw); upstream != "" && !strings.Contains(detail, upstream) {
		if name := strings.TrimSpace(envelope.Error.Metadata.ProviderName); name != "" {
			upstream = name + ": " + upstream
		}
		if detail != "" {
			detail += "; "
		}
		detail += upstream
	}
	if detail == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, detail)
}

// maxErrorBody bounds how much of an error response is read: enough for any
// real message, not enough for a misbehaving proxy to make the read the
// expensive part of the failure.
const maxErrorBody = 8 << 10

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
