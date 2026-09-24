package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
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
			Parts: []ports.ContentPart{{Type: ports.ModalityImage, MediaType: "image/png", Data: []byte("png")}},
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
	var header string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ = io.ReadAll(request.Body)
		header = request.Header.Get("X-Session-Id")
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`), nil
	})}
	ctx := destination.With(context.Background(), destination.Destination{Kind: destination.Web, ThreadID: "thread-123"})

	if _, err := New("https://openrouter.ai/api/v1", "key", client).Generate(ctx, ports.ModelRequest{Model: "openai/gpt-5"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"session_id":"thread-123"`) {
		t.Fatalf("body=%s, want OpenRouter session_id", body)
	}
	if header != "thread-123" {
		t.Fatalf("x-session-id=%q, want the conversation ID in the header as well", header)
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
	if strings.Contains(string(body), "session_id") || strings.Contains(string(body), "cache_control") ||
		strings.Contains(string(body), `"reasoning"`) || strings.Contains(string(body), `"provider"`) {
		t.Fatalf("body=%s, want no OpenRouter extensions", body)
	}
}

func TestOpenRouterLeavesNonAnthropicModelsUncached(t *testing.T) {
	body := openRouterRequestBody(t, ports.ModelRequest{Model: "openai/gpt-5"})
	if strings.Contains(body, "cache_control") {
		t.Fatalf("body=%s, want no cache_control for a non-Anthropic model", body)
	}
	if body := openRouterRequestBody(t, ports.ModelRequest{Model: "~anthropic/claude-sonnet-4.6"}); !strings.Contains(body, "cache_control") {
		t.Fatalf("body=%s, want cache_control for a ~-prefixed Anthropic model", body)
	}
}

func TestOpenRouterNestsReasoningEffortAndLeavesTheDefaultToOpenRouter(t *testing.T) {
	body := openRouterRequestBody(t, ports.ModelRequest{Model: "anthropic/claude-sonnet-4.6", ReasoningEffort: "high"})
	if !strings.Contains(body, `"reasoning":{"effort":"high"}`) || strings.Contains(body, "reasoning_effort") {
		t.Fatalf("body=%s, want nested reasoning.effort only", body)
	}
	if body = openRouterRequestBody(t, ports.ModelRequest{Model: "openai/gpt-5", ReasoningEffort: "none"}); !strings.Contains(body, `"reasoning":{"effort":"none"}`) {
		t.Fatalf("body=%s, want an explicit none passed through", body)
	}
	// No chosen effort is OpenRouter's default for the model, not Eggy's
	// guess: the request says nothing about reasoning at all.
	for _, model := range []string{"~anthropic/claude-sonnet-4.6", "google/gemini-2.5-pro", "vendor/unlisted"} {
		if body = openRouterRequestBody(t, ports.ModelRequest{Model: model}); strings.Contains(body, "reasoning") {
			t.Fatalf("body=%s, want no reasoning field for %s without a chosen effort", body, model)
		}
	}
}

// openRouterCatalog is the shape of OpenRouter's /models entries as of
// 2026-09: a reasoning descriptor with an off switch and levels for Claude,
// mandatory with no levels for Gemini, and absent for a non-reasoning model.
const openRouterCatalog = `{"data":[
	{"id":"anthropic/claude-sonnet-4.6","name":"Claude Sonnet 4.6","context_length":200000,"reasoning":{"mandatory":false,"supported_efforts":["max","high","medium","low"],"default_effort":"medium"}},
	{"id":"google/gemini-2.5-pro","name":"Gemini 2.5 Pro","context_length":1048576,"reasoning":{"mandatory":true}},
	{"id":"deepseek/deepseek-chat","name":"DeepSeek V3","context_length":163840,"reasoning":null}
]}`

func TestOpenRouterForwardsProviderRouting(t *testing.T) {
	routing := json.RawMessage(`{"order":["anthropic","amazon-bedrock"],"allow_fallbacks":false}`)
	body := openRouterRequestBody(t, ports.ModelRequest{Model: "anthropic/claude-sonnet-4.6", ProviderRouting: routing})
	if !strings.Contains(body, `"provider":{"order":["anthropic","amazon-bedrock"],"allow_fallbacks":false}`) {
		t.Fatalf("body=%s, want the routing object forwarded verbatim", body)
	}
}

func TestOpenRouterReplaysItsOwnReasoningDetailsOnly(t *testing.T) {
	var bodies [][]byte
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		bodies = append(bodies, body)
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"","reasoning":"thinking","reasoning_details":[{"type":"reasoning.encrypted","data":"opaque"}],"tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`), nil
	})}
	model := New("https://openrouter.ai/api/v1", "key", client)

	result, err := model.Generate(context.Background(), ports.ModelRequest{Model: "anthropic/claude-sonnet-4.6"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReasoningContent != "thinking" || result.Message.ProviderReasoningOrigin != "openrouter" ||
		string(result.Message.ProviderReasoning) != `[{"type":"reasoning.encrypted","data":"opaque"}]` {
		t.Fatalf("result=%#v", result)
	}

	foreign := result.Message
	foreign.ProviderReasoningOrigin = "bedrock"
	if _, err := model.Generate(context.Background(), ports.ModelRequest{Model: "anthropic/claude-sonnet-4.6", Messages: []ports.Message{result.Message, foreign}}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(bodies[1]), `"reasoning_details":[{"type":"reasoning.encrypted","data":"opaque"}]`) != 1 {
		t.Fatalf("second request body=%s, want exactly the openrouter-origin blob replayed", bodies[1])
	}
	if strings.Contains(string(bodies[1]), `"reasoning":"thinking"`) {
		t.Fatalf("second request body=%s, want visible reasoning never replayed", bodies[1])
	}
}

func TestOpenRouterReportsCacheWritesAndCost(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":300,"completion_tokens":5,"total_tokens":305,"cost":0.0125,"prompt_tokens_details":{"cached_tokens":100,"cache_write_tokens":150}}}`), nil
	})}
	result, err := New("https://openrouter.ai/api/v1", "key", client).Generate(context.Background(), ports.ModelRequest{Model: "anthropic/claude-sonnet-4.6"})
	if err != nil {
		t.Fatal(err)
	}
	want := ports.ModelUsage{PromptTokens: 300, CompletionTokens: 5, TotalTokens: 305, CachedPromptTokens: 100, CacheWriteTokens: 150, CostUSD: 0.0125}
	if result.Usage != want {
		t.Fatalf("usage=%+v, want %+v", result.Usage, want)
	}
}

func TestProviderErrorsQuoteTheBodyExceptForAuthentication(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusBadRequest, `{"error":{"message":"Provider returned error","code":400,"metadata":{"raw":"max_tokens exceeds 8192 for key sk-secret","provider_name":"Anthropic"}}}`), nil
	})}
	_, err := New("https://openrouter.ai/api/v1", "sk-secret", client).Generate(context.Background(), ports.ModelRequest{Model: "anthropic/claude-sonnet-4.6"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "Provider returned error; Anthropic: max_tokens exceeds 8192 for key [redacted]") {
		t.Fatalf("error=%v", err)
	}

	client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusBadRequest, `not json`), nil
	})}
	_, err = New("https://api.example", "key", client).Generate(context.Background(), ports.ModelRequest{Model: "model"})
	if err == nil || err.Error() != "provider rejected request (HTTP 400)" {
		t.Fatalf("error=%v, want the bare status error for an undecodable body", err)
	}
}

// openRouterRequestBody returns the JSON body a single OpenRouter Generate
// sends for input.
func openRouterRequestBody(t *testing.T, input ports.ModelRequest) string {
	t.Helper()
	var body []byte
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/models") {
			return jsonResponse(http.StatusOK, openRouterCatalog), nil
		}
		body, _ = io.ReadAll(request.Body)
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`), nil
	})}
	if _, err := New("https://openrouter.ai/api/v1", "key", client).Generate(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	return string(body)
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
		return jsonResponse(http.StatusOK, `{"data":[{"id":"anthropic/claude-sonnet-5","name":"Claude Sonnet 5","context_length":200000,"reasoning":{"mandatory":false,"supported_efforts":["high","low"]}},{"id":"openai/gpt-5","reasoning":{"mandatory":true}},{"id":"   "}]}`), nil
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
		{ID: "anthropic/claude-sonnet-5", Name: "Claude Sonnet 5", ContextLength: 200000, Reasoning: &ports.CatalogReasoning{Efforts: []string{"high", "low"}}},
		{ID: "openai/gpt-5", Reasoning: &ports.CatalogReasoning{Mandatory: true}},
	}
	if len(models) != len(want) {
		t.Fatalf("models=%#v want=%#v", models, want)
	}
	for i := range want {
		if models[i].ID != want[i].ID || models[i].Name != want[i].Name || models[i].ContextLength != want[i].ContextLength ||
			models[i].Reasoning.Mandatory != want[i].Reasoning.Mandatory || strings.Join(models[i].Reasoning.Efforts, ",") != strings.Join(want[i].Reasoning.Efforts, ",") {
			t.Fatalf("models[%d]=%#v want=%#v", i, models[i], want[i])
		}
	}
}

// OpenRouter reports what a model accepts as input. An entry that carries the
// architecture block states the answer either way; an entry that omits it has
// no answer, which is what keeps a provider that does not report modalities
// from being read as text-only.
func TestListModelsParsesImageModalityOnlyWhenReported(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"data":[{"id":"vendor/vision","architecture":{"input_modalities":["text","image","file"]}},{"id":"vendor/text-only","architecture":{"input_modalities":["text"]}},{"id":"vendor/silent"}]}`), nil
	})}
	models, err := New("https://openrouter.ai/api/v1", "key", client).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("models=%#v", models)
	}
	accepts := func(model ports.CatalogModel, modality ports.Modality) string {
		supported, known := model.Accepts(modality)
		if !known {
			return "unknown"
		}
		return map[bool]string{true: "yes", false: "no"}[supported]
	}
	want := map[string][2]string{
		"vendor/vision":    {"yes", "yes"},
		"vendor/text-only": {"no", "no"},
		// No architecture block means the provider said nothing, not that the
		// model is text-only.
		"vendor/silent": {"unknown", "unknown"},
	}
	for _, model := range models {
		got := [2]string{accepts(model, ports.ModalityImage), accepts(model, ports.ModalityFile)}
		if got != want[model.ID] {
			t.Fatalf("%s: image,file=%v want %v", model.ID, got, want[model.ID])
		}
	}
}

// A document goes on the wire as a file part named for the model, and on
// OpenRouter the file parser is pinned to the model's own reading: the
// default engine is a paid OCR pass, which a model that takes files does
// not need. A plain Chat Completions provider gets the part and nothing else.
func TestModelSendsDocumentsAsFileParts(t *testing.T) {
	for _, tc := range []struct {
		name, baseURL string
		wantPlugins   string
	}{
		{name: "openrouter", baseURL: "https://openrouter.ai/api/v1", wantPlugins: `[{"id":"file-parser","pdf":{"engine":"native"}}]`},
		{name: "plain", baseURL: "https://api.example/v1", wantPlugins: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				body, _ = io.ReadAll(request.Body)
				return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"read"}}]}`), nil
			})}
			_, err := New(tc.baseURL, "key", client).Generate(context.Background(), ports.ModelRequest{
				Model: "openai/gpt-5.6-luna",
				Messages: []ports.Message{{
					Role: ports.RoleUser, Content: "summarise this",
					Parts: []ports.ContentPart{{Type: ports.ModalityFile, MediaType: "application/pdf", Filename: "list.pdf", Data: []byte("%PDF-")}},
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var request struct {
				Messages []struct {
					Content json.RawMessage `json:"content"`
				} `json:"messages"`
				Plugins json.RawMessage `json:"plugins"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Fatal(err)
			}
			want := `[{"type":"text","text":"summarise this"},{"type":"file","file":{"filename":"list.pdf","file_data":"data:application/pdf;base64,JVBERi0="}}]`
			if string(request.Messages[0].Content) != want {
				t.Fatalf("content=%s want=%s", request.Messages[0].Content, want)
			}
			if string(request.Plugins) != tc.wantPlugins {
				t.Fatalf("plugins=%s want=%s", request.Plugins, tc.wantPlugins)
			}
		})
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

// A provider refusing the image or file itself is told apart from every other
// rejection, so the turn can say "this model does not take images" rather than
// quote a deserialization error. Each body is what that provider actually
// sends when handed a part its model cannot read.
func TestProviderRejectingAnAttachedPartIsUnsupportedInput(t *testing.T) {
	image := []ports.Message{{Role: ports.RoleUser, Content: "Describe this image.", Parts: []ports.ContentPart{{Type: ports.ModalityImage, MediaType: "image/png", Data: []byte("png")}}}}
	pdf := []ports.Message{{Role: ports.RoleUser, Content: "Read this file.", Parts: []ports.ContentPart{{Type: ports.ModalityFile, MediaType: "application/pdf", Filename: "a.pdf", Data: []byte("%PDF")}}}}
	text := []ports.Message{{Role: ports.RoleUser, Content: "hello"}}
	cases := []struct {
		name     string
		status   int
		body     string
		messages []ports.Message
		want     bool
	}{
		{"deepseek image", http.StatusBadRequest, `{"error":{"message":"Failed to deserialize the JSON body into the target type: messages[1]: unknown variant ` + "`image_url`, expected `text`" + ` at line 1 column 912","type":"invalid_request_error"}}`, image, true},
		{"deepseek file", http.StatusBadRequest, `{"error":{"message":"Failed to deserialize the JSON body into the target type: messages[1]: unknown variant ` + "`file`, expected `text`" + ` at line 1 column 80","type":"invalid_request_error"}}`, pdf, true},
		{"openai text model", http.StatusBadRequest, `{"error":{"message":"Invalid content type. image_url is only supported by certain models.","type":"invalid_request_error"}}`, image, true},
		{"openrouter no vision endpoint", http.StatusNotFound, `{"error":{"message":"No endpoints found that support image input","code":404}}`, image, true},
		{"unrelated rejection with an image", http.StatusBadRequest, `{"error":{"message":"This model's maximum context length is 65536 tokens"}}`, image, false},
		{"image wording without a part", http.StatusBadRequest, `{"error":{"message":"image_url is only supported by certain models"}}`, text, false},
		{"rate limit with an image", http.StatusTooManyRequests, `{"error":{"message":"image quota exceeded"}}`, image, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(tc.status, tc.body), nil
			})}
			_, err := New("https://api.example", "key", client).Generate(context.Background(), ports.ModelRequest{Model: "m", Messages: tc.messages})
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := errors.Is(err, ports.ErrUnsupportedInput); got != tc.want {
				t.Fatalf("unsupported=%v, want %v: %v", got, tc.want, err)
			}
		})
	}
}
