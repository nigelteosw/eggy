package deepseek

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestModelTranslatesChatCompletionWithoutLeakingCredential(t *testing.T) {
	var authorization string
	var body []byte
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		authorization = request.Header.Get("Authorization")
		body, _ = io.ReadAll(request.Body)
		response := `{"choices":[{"message":{"role":"assistant","content":"checking","tool_calls":[{"id":"call-1","type":"function","function":{"name":"status","arguments":"{\"verbose\":true}"}}]}}]}`
		return jsonResponse(http.StatusOK, response), nil
	})}
	model := New("https://api.deepseek.test/chat/completions", "top-secret-key", httpClient)
	result, err := model.Generate(context.Background(), ports.ModelRequest{
		Model:    "deepseek-v4-flash",
		Messages: []ports.Message{{Role: ports.RoleUser, Content: "How is Eggy?"}},
		Tools:    []ports.ToolDefinition{{Name: "status", Description: "Read status", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer top-secret-key" || strings.Contains(string(body), "top-secret-key") {
		t.Fatalf("authorization=%q body=%s", authorization, body)
	}
	if result.Message.Content != "checking" || len(result.Message.ToolCalls) != 1 || result.Message.ToolCalls[0].Name != "status" {
		t.Fatalf("result=%#v", result)
	}
	if string(result.Message.ToolCalls[0].Arguments) != `{"verbose":true}` {
		t.Fatalf("arguments=%s", result.Message.ToolCalls[0].Arguments)
	}
}

func TestModelReturnsRedactedProviderError(t *testing.T) {
	attempts := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return jsonResponse(http.StatusUnauthorized, `{"error":{"message":"bad key top-secret-key"}}`), nil
	})}
	model := New("https://api.deepseek.test/chat/completions", "top-secret-key", httpClient)
	_, err := model.Generate(context.Background(), ports.ModelRequest{Model: "flash"})
	if err == nil || strings.Contains(err.Error(), "top-secret-key") || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("error=%v", err)
	}
	if attempts != 1 {
		t.Fatalf("non-transient response attempted %d times", attempts)
	}
}

func TestModelRetriesTransientProviderResponse(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return jsonResponse(http.StatusServiceUnavailable, `{}`), nil
		}
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"recovered"}}]}`), nil
	})}
	result, err := New("https://api.deepseek.test/chat", "key", client).Generate(context.Background(), ports.ModelRequest{Model: "flash"})
	if err != nil || result.Message.Content != "recovered" || attempts != 2 {
		t.Fatalf("result=%#v attempts=%d err=%v", result, attempts, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
