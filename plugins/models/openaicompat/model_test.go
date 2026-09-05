package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestModelTranslatesChatCompletionAndUsage(t *testing.T) {
	var authorization, requestURL string
	var body []byte
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		authorization, requestURL = request.Header.Get("Authorization"), request.URL.String()
		body, _ = io.ReadAll(request.Body)
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"checking","tool_calls":[{"id":"call-1","type":"function","function":{"name":"status","arguments":"{\"verbose\":true}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":2}}}`), nil
	})}
	result, err := New("https://api.example/v1/", "top-secret-key", client).Generate(context.Background(), ports.ModelRequest{
		Model: "provider-model", Messages: []ports.Message{{Role: ports.RoleUser, Content: "How is Eggy?"}},
		Tools: []ports.ToolDefinition{{Name: "status", Description: "Read status", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestURL != "https://api.example/v1/chat/completions" || authorization != "Bearer top-secret-key" || strings.Contains(string(body), "top-secret-key") || !strings.Contains(string(body), `"model":"provider-model"`) {
		t.Fatalf("url=%q authorization=%q body=%s", requestURL, authorization, body)
	}
	var request struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if string(request.Messages[0].Content) != `"How is Eggy?"` {
		t.Fatalf("content=%s, want a JSON string", request.Messages[0].Content)
	}
	if result.Message.Content != "checking" || len(result.Message.ToolCalls) != 1 || result.Message.ToolCalls[0].Name != "status" {
		t.Fatalf("message=%#v", result.Message)
	}
	want := ports.ModelUsage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14, CachedPromptTokens: 3, ReasoningTokens: 2}
	if result.Usage != want {
		t.Fatalf("usage=%#v want=%#v", result.Usage, want)
	}
}

func TestModelTranslatesImagePartsToMultipartContent(t *testing.T) {
	var body []byte
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ = io.ReadAll(request.Body)
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"seen"}}]}`), nil
	})}

	_, err := New("https://openrouter.ai/api/v1", "key", client).Generate(context.Background(), ports.ModelRequest{
		Model: "openai/gpt-5.6-luna",
		Messages: []ports.Message{{
			Role: ports.RoleUser, Content: "read this list",
			Parts: []ports.ContentPart{{Type: ports.ContentTypeImage, MediaType: "image/png", Data: []byte("png")}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"text","text":"read this list"},{"type":"image_url","image_url":{"url":"data:image/png;base64,cG5n"}}]`
	if string(request.Messages[0].Content) != want {
		t.Fatalf("content=%s want=%s", request.Messages[0].Content, want)
	}
}

// DeepSeek speaks the same chat-completions wire format but reports context
// cache hits at usage.prompt_cache_hit_tokens rather than in OpenAI's nested
// prompt_tokens_details object. Losing that field makes an automatic cache
// hit look like a miss everywhere Eggy reports usage.
func TestModelTranslatesDeepSeekCacheUsage(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"cached"}}],"usage":{"prompt_tokens":100,"completion_tokens":4,"total_tokens":104,"prompt_cache_hit_tokens":80,"prompt_cache_miss_tokens":20}}`), nil
	})}

	result, err := New("https://api.deepseek.com", "key", client).Generate(context.Background(), ports.ModelRequest{Model: "deepseek-chat"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage.CachedPromptTokens != 80 {
		t.Fatalf("cached prompt tokens = %d, want 80", result.Usage.CachedPromptTokens)
	}
}

func TestOpenRouterSendsConversationAsStickySession(t *testing.T) {
	var body []byte
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ = io.ReadAll(request.Body)
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`), nil
	})}
	ctx := destination.With(context.Background(), destination.Destination{Kind: destination.Web, ThreadID: "thread-123"})

	if _, err := New("https://openrouter.ai/api/v1", "key", client).Generate(ctx, ports.ModelRequest{Model: "openai/gpt-5"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"session_id":"thread-123"`) {
		t.Fatalf("body=%s, want OpenRouter session_id", body)
	}
}

func TestOpenRouterEnablesAutomaticCachingForAnthropicModels(t *testing.T) {
	var body []byte
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ = io.ReadAll(request.Body)
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`), nil
	})}

	if _, err := New("https://openrouter.ai/api/v1", "key", client).Generate(context.Background(), ports.ModelRequest{Model: "anthropic/claude-sonnet-4.6"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"cache_control":{"type":"ephemeral"}`) {
		t.Fatalf("body=%s, want top-level ephemeral cache_control", body)
	}
}

func TestNonOpenRouterRequestsKeepTheStandardChatCompletionsShape(t *testing.T) {
	var body []byte
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ = io.ReadAll(request.Body)
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`), nil
	})}
	ctx := destination.With(context.Background(), destination.Destination{Kind: destination.Web, ThreadID: "thread-123"})

	if _, err := New("https://api.example/v1", "key", client).Generate(ctx, ports.ModelRequest{Model: "anthropic/claude-sonnet-4.6"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "session_id") || strings.Contains(string(body), "cache_control") {
		t.Fatalf("body=%s, want no OpenRouter extensions", body)
	}
}

func TestModelParsesReasoningContentAndNeverReplaysIt(t *testing.T) {
	var bodies [][]byte
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		bodies = append(bodies, body)
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"answer","reasoning_content":"step by step reasoning"}}]}`), nil
	})}
	model := New("https://api.example", "key", client)

	result, err := model.Generate(context.Background(), ports.ModelRequest{Model: "model", Messages: []ports.Message{{Role: ports.RoleUser, Content: "question"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReasoningContent != "step by step reasoning" || result.Message.Content != "answer" {
		t.Fatalf("result=%#v", result)
	}

	if _, err := model.Generate(context.Background(), ports.ModelRequest{Model: "model", Messages: []ports.Message{
		{Role: ports.RoleUser, Content: "question"},
		result.Message,
	}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bodies[1]), "reasoning_content") {
		t.Fatalf("second request body=%s, want reasoning_content never replayed into history", bodies[1])
	}
}

func TestModelSendsReasoningEffortOnlyWhenSet(t *testing.T) {
	var body []byte
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ = io.ReadAll(request.Body)
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`), nil
	})}
	model := New("https://api.example", "key", client)

	if _, err := model.Generate(context.Background(), ports.ModelRequest{Model: "model", ReasoningEffort: "high"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"reasoning_effort":"high"`) {
		t.Fatalf("body=%s, want reasoning_effort=high", body)
	}

	if _, err := model.Generate(context.Background(), ports.ModelRequest{Model: "model"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "reasoning_effort") {
		t.Fatalf("body=%s, want reasoning_effort omitted when unset", body)
	}
}

func TestModelReturnsSafeProviderErrors(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return jsonResponse(http.StatusUnauthorized, `{"error":{"message":"bad key top-secret-key"}}`), nil
	})}
	_, err := New("https://api.example", "top-secret-key", client).Generate(context.Background(), ports.ModelRequest{Model: "model"})
	if err == nil || strings.Contains(err.Error(), "top-secret-key") || strings.Contains(err.Error(), "bad key") || !strings.Contains(err.Error(), "authentication") || attempts != 1 {
		t.Fatalf("attempts=%d error=%v", attempts, err)
	}
}

func TestModelRetriesTransientResponses(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts < 3 {
			return jsonResponse(http.StatusServiceUnavailable, `{}`), nil
		}
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"recovered"}}]}`), nil
	})}
	result, err := New("https://api.example", "key", client).Generate(context.Background(), ports.ModelRequest{Model: "model"})
	if err != nil || result.Message.Content != "recovered" || attempts != 3 {
		t.Fatalf("result=%#v attempts=%d err=%v", result, attempts, err)
	}
}

func TestModelRejectsInvalidToolArgumentsAndEmptyChoices(t *testing.T) {
	responses := []string{
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"status","arguments":"not-json"}}]}}]}`,
		`{"choices":[]}`,
	}
	for _, response := range responses {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, response), nil })}
		if _, err := New("https://api.example", "key", client).Generate(context.Background(), ports.ModelRequest{Model: "model"}); err == nil {
			t.Fatalf("expected error for %s", response)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestListModelsReadsCatalogAsAnAuthenticatedGET(t *testing.T) {
	var method, requestURL, authorization string
	var hasContentType bool
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		method, requestURL = request.Method, request.URL.String()
		authorization = request.Header.Get("Authorization")
		_, hasContentType = request.Header["Content-Type"]
		return jsonResponse(http.StatusOK, `{"data":[{"id":"anthropic/claude-sonnet-5","name":"Claude Sonnet 5","context_length":200000},{"id":"openai/gpt-5"},{"id":"   "}]}`), nil
	})}
	models, err := New("https://openrouter.ai/api/v1/", "top-secret-key", client).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet || requestURL != "https://openrouter.ai/api/v1/models" || authorization != "Bearer top-secret-key" {
		t.Fatalf("method=%q url=%q authorization=%q", method, requestURL, authorization)
	}
	// A GET carrying a JSON content type describes a body it does not have.
	if hasContentType {
		t.Fatal("GET must not declare a Content-Type")
	}
	// The entry with a blank id is dropped; nothing else is filtered, because
	// which models are worth running is the owner's call.
	want := []ports.CatalogModel{
		{ID: "anthropic/claude-sonnet-5", Name: "Claude Sonnet 5", ContextLength: 200000},
		{ID: "openai/gpt-5"},
	}
	if len(models) != len(want) {
		t.Fatalf("models=%#v want=%#v", models, want)
	}
	for i := range want {
		if models[i] != want[i] {
			t.Fatalf("models[%d]=%#v want=%#v", i, models[i], want[i])
		}
	}
}

func TestListModelsReportsAuthenticationFailure(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusUnauthorized, `{"error":"bad key"}`), nil
	})}
	if _, err := New("https://openrouter.ai/api/v1", "wrong-key", client).ListModels(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("err=%v", err)
	}
}

// The adapter is what makes a provider browsable, so the panel's type
// assertion has to keep finding it.
func TestModelSatisfiesCatalogPort(t *testing.T) {
	var _ ports.ModelCatalog = New("https://api.example/v1", "key", nil)
}
