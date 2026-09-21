package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
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
	selector := NewSelector(NewClient("https://api.telegram.test", "token", FixedChat("99"), httpClient), func() time.Time { return now }, 10*time.Minute)

	result, err := selector.Tool().Execute(selectionContext("alice"), json.RawMessage(`{
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
	if value, ok := selector.Resolve(selectionContext("alice"), secondCallback); !ok || value != "staging" {
		t.Fatalf("resolved value=%q ok=%v", value, ok)
	}
	if value, ok := selector.Resolve(selectionContext("alice"), secondCallback); ok || value != "" {
		t.Fatalf("duplicate resolved value=%q ok=%v", value, ok)
	}
}

func TestSelectorRejectsInvalidOrOverlappingQuestions(t *testing.T) {
	selector := NewSelector(NewClient("https://api.telegram.test", "token", FixedChat("99"), &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
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
		if _, err := selector.Tool().Execute(selectionContext("alice"), json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid input: %s", raw)
		}
	}

	if _, err := selector.Tool().Execute(selectionContext("alice"), json.RawMessage(`{"prompt":"First","options":[{"label":"A","value":"a"},{"label":"B","value":"b"}]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := selector.Tool().Execute(selectionContext("alice"), json.RawMessage(`{"prompt":"Second","options":[{"label":"C","value":"c"},{"label":"D","value":"d"}]}`)); err == nil {
		t.Fatal("accepted a second active selection")
	}
}

func TestSelectorExpiresPendingQuestion(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	selector := NewSelector(NewClient("https://api.telegram.test", "token", FixedChat("99"), &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`))}, nil
	})}), func() time.Time { return now }, time.Minute)
	if _, err := selector.Tool().Execute(selectionContext("alice"), json.RawMessage(`{"prompt":"Pick","options":[{"label":"A","value":"a"},{"label":"B","value":"b"}]}`)); err != nil {
		t.Fatal(err)
	}
	selector.mu.Lock()
	callback := "select:" + selector.pending[selectionScope{"alice", "telegram"}].id + ":0"
	selector.mu.Unlock()
	now = now.Add(2 * time.Minute)
	if value, ok := selector.Resolve(selectionContext("alice"), callback); ok || value != "" {
		t.Fatalf("expired selection resolved value=%q ok=%v", value, ok)
	}
}

func TestSelectorRefusesCallsFromWebChat(t *testing.T) {
	selector := NewSelector(NewClient("https://api.telegram.test", "token", FixedChat("99"), nil), time.Now, time.Minute)
	ctx := destination.With(context.Background(), destination.Destination{Kind: destination.Web, ThreadID: "thread-1"})
	if _, err := selector.Tool().Execute(ctx, json.RawMessage(`{"prompt":"Pick","options":[{"label":"A","value":"a"},{"label":"B","value":"b"}]}`)); err == nil {
		t.Fatal("web chat was allowed to send a Telegram selection")
	}
}

func selectionContext(account string) context.Context {
	return ports.WithPrincipal(context.Background(), ports.Principal{AccountID: account})
}

func TestSelectorAllowsIndependentAccounts(t *testing.T) {
	selector := NewSelector(NewClient("https://api.telegram.test", "token", FixedChat("99"), &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`))}, nil
	})}), time.Now, time.Minute)
	for _, account := range []string{"alice", "bob"} {
		if _, err := selector.Tool().Execute(selectionContext(account), json.RawMessage(`{"prompt":"Pick","options":[{"label":"A","value":"a"},{"label":"B","value":"b"}]}`)); err != nil {
			t.Fatalf("%s could not ask an independent question: %v", account, err)
		}
	}
}

func TestSelectorOwnershipBoundsAndCleanup(t *testing.T) {
	now := time.Now()
	failDelivery := false
	selector := NewSelector(NewClient("https://api.telegram.test", "token", FixedChat("99"), &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		if failDelivery {
			return nil, fmt.Errorf("delivery failed")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`))}, nil
	})}), func() time.Time { return now }, time.Minute)
	raw := json.RawMessage(`{"prompt":"Pick","options":[{"label":"A","value":"a"},{"label":"B","value":"b"}]}`)
	if _, err := selector.Tool().Execute(context.Background(), raw); err == nil {
		t.Fatal("missing principal accepted")
	}
	for _, account := range []string{"alice", "bob"} {
		if _, err := selector.Tool().Execute(selectionContext(account), raw); err != nil {
			t.Fatal(err)
		}
	}
	callback := "select:" + selector.pending[selectionScope{"alice", "telegram"}].id + ":0"
	for _, ctx := range []context.Context{context.Background(), selectionContext("bob"), destination.With(selectionContext("alice"), destination.Destination{Kind: destination.Web, ThreadID: "web"})} {
		if _, ok := selector.Resolve(ctx, callback); ok {
			t.Fatal("foreign selection accepted")
		}
	}
	for _, invalid := range []string{"select:stale:0", strings.TrimSuffix(callback, ":0") + ":-1", strings.TrimSuffix(callback, ":0") + ":9", "select:bad:not-a-number"} {
		if _, ok := selector.Resolve(selectionContext("alice"), invalid); ok {
			t.Fatal("invalid selection accepted")
		}
	}
	if value, ok := selector.Resolve(selectionContext("alice"), callback); !ok || value != "a" {
		t.Fatal("foreign callback consumed alice's selection")
	}
	if _, ok := selector.Resolve(selectionContext("alice"), callback); ok {
		t.Fatal("repeated selection accepted")
	}
	bobCallback := "select:" + selector.pending[selectionScope{"bob", "telegram"}].id + ":1"
	if value, ok := selector.Resolve(selectionContext("bob"), bobCallback); !ok || value != "b" {
		t.Fatal("bob's selection lost")
	}
	failDelivery = true
	if _, err := selector.Tool().Execute(selectionContext("alice"), raw); err == nil || len(selector.pending) != 0 {
		t.Fatal("failed delivery was retained")
	}
	failDelivery = false
	for i := 0; i < maxPendingSelections; i++ {
		if _, err := selector.Tool().Execute(selectionContext(fmt.Sprint(i)), raw); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := selector.Tool().Execute(selectionContext("overflow"), raw); err == nil {
		t.Fatal("unbounded selections")
	}
	now = now.Add(time.Minute)
	if _, err := selector.Tool().Execute(selectionContext("overflow"), raw); err != nil || len(selector.pending) != 1 {
		t.Fatalf("expired entries not reclaimed: %d, %v", len(selector.pending), err)
	}
}
