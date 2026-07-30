package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
)

func TestSelectorDeliversModelAuthoredOptionsAndResolvesOnce(t *testing.T) {
	var request map[string]any
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":12}}`)),
		}, nil
	})}
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	selector := NewSelector(NewClient("https://api.telegram.test", "token", "99", httpClient), func() time.Time { return now }, 10*time.Minute)

	result, err := selector.Tool().Execute(context.Background(), json.RawMessage(`{
		"prompt":"Which deployment?",
		"options":[
			{"label":"Production","value":"production"},
			{"label":"Staging","value":"staging"}
		]
	}`))
	if err != nil || string(result) != `{"status":"awaiting_selection"}` {
		t.Fatalf("result=%s err=%v", result, err)
	}
	if request["text"] != "Which deployment?" {
		t.Fatalf("request=%#v", request)
	}
	markup, ok := request["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("reply_markup=%#v", request["reply_markup"])
	}
	rows, ok := markup["inline_keyboard"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("inline_keyboard=%#v", markup["inline_keyboard"])
	}
	first := rows[0].([]any)[0].(map[string]any)
	second := rows[1].([]any)[0].(map[string]any)
	if first["text"] != "Production" || second["text"] != "Staging" {
		t.Fatalf("buttons=%#v %#v", first, second)
	}
	firstCallback := first["callback_data"].(string)
	secondCallback := second["callback_data"].(string)
	if !strings.HasPrefix(firstCallback, "select:") || !strings.HasSuffix(firstCallback, ":0") ||
		!strings.HasPrefix(secondCallback, "select:") || !strings.HasSuffix(secondCallback, ":1") {
		t.Fatalf("callbacks=%q %q", firstCallback, secondCallback)
	}
	if len(firstCallback) > 64 || len(secondCallback) > 64 {
		t.Fatalf("callback exceeds Telegram limit: %q %q", firstCallback, secondCallback)
	}
	if value, ok := selector.Resolve(secondCallback); !ok || value != "staging" {
		t.Fatalf("resolved value=%q ok=%v", value, ok)
	}
	if value, ok := selector.Resolve(secondCallback); ok || value != "" {
		t.Fatalf("duplicate resolved value=%q ok=%v", value, ok)
	}
}

func TestSelectorRejectsInvalidOrOverlappingQuestions(t *testing.T) {
	selector := NewSelector(NewClient("https://api.telegram.test", "token", "99", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`))}, nil
	})}), time.Now, 10*time.Minute)

	cases := []string{
		`{"prompt":"","options":[{"label":"A","value":"a"},{"label":"B","value":"b"}]}`,
		`{"prompt":"Pick","options":[{"label":"A","value":"a"}]}`,
		`{"prompt":"Pick","options":[{"label":"A","value":"a"},{"label":"A","value":"b"}]}`,
		`{"prompt":"Pick","options":[{"label":"A","value":"same"},{"label":"B","value":"same"}]}`,
		`{"prompt":"Pick","options":[{"label":"A","value":"a"},{"label":"B","value":"b"}],"extra":true}`,
	}
	for _, raw := range cases {
		if _, err := selector.Tool().Execute(context.Background(), json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid input: %s", raw)
		}
	}

	if _, err := selector.Tool().Execute(context.Background(), json.RawMessage(`{"prompt":"First","options":[{"label":"A","value":"a"},{"label":"B","value":"b"}]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := selector.Tool().Execute(context.Background(), json.RawMessage(`{"prompt":"Second","options":[{"label":"C","value":"c"},{"label":"D","value":"d"}]}`)); err == nil {
		t.Fatal("accepted a second active selection")
	}
}

func TestSelectorExpiresPendingQuestion(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	selector := NewSelector(NewClient("https://api.telegram.test", "token", "99", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`))}, nil
	})}), func() time.Time { return now }, time.Minute)
	if _, err := selector.Tool().Execute(context.Background(), json.RawMessage(`{"prompt":"Pick","options":[{"label":"A","value":"a"},{"label":"B","value":"b"}]}`)); err != nil {
		t.Fatal(err)
	}
	selector.mu.Lock()
	callback := "select:" + selector.pending.id + ":0"
	selector.mu.Unlock()
	now = now.Add(2 * time.Minute)
	if value, ok := selector.Resolve(callback); ok || value != "" {
		t.Fatalf("expired selection resolved value=%q ok=%v", value, ok)
	}
}

func TestSelectorRefusesCallsFromWebChat(t *testing.T) {
	selector := NewSelector(NewClient("https://api.telegram.test", "token", "99", nil), time.Now, time.Minute)
	ctx := destination.With(context.Background(), destination.Destination{Kind: destination.Web, ThreadID: "thread-1"})
	if _, err := selector.Tool().Execute(ctx, json.RawMessage(`{"prompt":"Pick","options":[{"label":"A","value":"a"},{"label":"B","value":"b"}]}`)); err == nil {
		t.Fatal("web chat was allowed to send a Telegram selection")
	}
}
