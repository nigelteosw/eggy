package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

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
	if result.Message.Content != "checking" || len(result.Message.ToolCalls) != 1 || result.Message.ToolCalls[0].Name != "status" {
		t.Fatalf("message=%#v", result.Message)
	}
	want := ports.ModelUsage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14, CachedPromptTokens: 3, ReasoningTokens: 2}
	if result.Usage != want {
		t.Fatalf("usage=%#v want=%#v", result.Usage, want)
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
