package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSplitMessageKeepsShortTextWhole(t *testing.T) {
	chunks := splitMessage("short message")
	if len(chunks) != 1 || chunks[0] != "short message" {
		t.Fatalf("chunks=%#v", chunks)
	}
}

func TestSplitMessageSplitsAtNewlineBoundary(t *testing.T) {
	var lines []string
	for i := 0; i < 400; i++ {
		lines = append(lines, "line number is here")
	}
	original := strings.Join(lines, "\n")
	chunks := splitMessage(original)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if len([]rune(chunk)) > maxMessageLength {
			t.Fatalf("chunk exceeds max length: %d runes", len([]rune(chunk)))
		}
	}
	if strings.Join(chunks, "\n") != original {
		t.Fatalf("splitting lost or reordered content")
	}
}

func TestClientDeliverSplitsLongMessagesAcrossMultipleSends(t *testing.T) {
	var sends int
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sends++
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":1}}`))}, nil
	})}
	client := NewClient("https://api.telegram.test", "token", httpClient)
	var lines []string
	for i := 0; i < 400; i++ {
		lines = append(lines, "line number is here")
	}
	if err := client.Deliver(context.Background(), "99", strings.Join(lines, "\n")); err != nil {
		t.Fatal(err)
	}
	if sends < 2 {
		t.Fatalf("expected multiple sendMessage calls, got %d", sends)
	}
}

func TestClientDeliverTrackableReturnsFinalChunkMessageID(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":555}}`))}, nil
	})}
	client := NewClient("https://api.telegram.test", "token", httpClient)
	messageID, err := client.DeliverTrackable(context.Background(), "99", "hello there")
	if err != nil {
		t.Fatal(err)
	}
	if messageID != "555" {
		t.Fatalf("messageID=%q", messageID)
	}
}

func TestClientEditTextSendsMessageIDAndFormattedText(t *testing.T) {
	var request map[string]any
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "editMessageText") {
			t.Fatalf("unexpected method call: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &request)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`))}, nil
	})}
	client := NewClient("https://api.telegram.test", "token", httpClient)
	if err := client.EditText(context.Background(), "99", "555", "**Approved.**"); err != nil {
		t.Fatal(err)
	}
	if request["chat_id"] != "99" || request["message_id"] != "555" || request["text"] != "<b>Approved.</b>" || request["parse_mode"] != "HTML" {
		t.Fatalf("request=%#v", request)
	}
}

func TestClientAnswerCallbackCallsAnswerCallbackQuery(t *testing.T) {
	var called bool
	var request map[string]any
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = strings.Contains(r.URL.Path, "answerCallbackQuery")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &request)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`))}, nil
	})}
	client := NewClient("https://api.telegram.test", "token", httpClient)
	if err := client.AnswerCallback(context.Background(), "cb-1"); err != nil {
		t.Fatal(err)
	}
	if !called || request["callback_query_id"] != "cb-1" {
		t.Fatalf("called=%v request=%#v", called, request)
	}
}

func TestClientSendTypingCallsSendChatAction(t *testing.T) {
	var request map[string]any
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "sendChatAction") {
			t.Fatalf("unexpected method call: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &request)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`))}, nil
	})}
	client := NewClient("https://api.telegram.test", "token", httpClient)
	if err := client.SendTyping(context.Background(), "99"); err != nil {
		t.Fatal(err)
	}
	if request["chat_id"] != "99" || request["action"] != "typing" {
		t.Fatalf("request=%#v", request)
	}
}

func TestClientSetCommandsSendsCommandListToTelegram(t *testing.T) {
	var request map[string]any
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "setMyCommands") {
			t.Fatalf("unexpected method call: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &request)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`))}, nil
	})}
	client := NewClient("https://api.telegram.test", "token", httpClient)
	err := client.SetCommands(context.Background(), []BotCommand{
		{Name: "status", Description: "Show operational status"},
		{Name: "new", Description: "Start a new conversation"},
	})
	if err != nil {
		t.Fatal(err)
	}
	commands, ok := request["commands"].([]any)
	if !ok || len(commands) != 2 {
		t.Fatalf("request=%#v", request)
	}
	first := commands[0].(map[string]any)
	if first["command"] != "status" || first["description"] != "Show operational status" {
		t.Fatalf("first=%#v", first)
	}
}

func TestClientDeliverFallsBackToPlainTextWhenTelegramRejectsFormatting(t *testing.T) {
	var requests []map[string]any
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		requests = append(requests, payload)
		if payload["parse_mode"] != nil {
			return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":false,"description":"Bad Request: can't parse entities: unexpected"}`))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":9}}`))}, nil
	})}
	client := NewClient("https://api.telegram.test", "token", httpClient)
	if err := client.Deliver(context.Background(), "99", "some *odd_ markdown"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("expected an HTML attempt followed by a plain-text retry, got %d requests", len(requests))
	}
	if requests[0]["parse_mode"] != "HTML" {
		t.Fatalf("first attempt=%#v", requests[0])
	}
	if requests[1]["parse_mode"] != nil || requests[1]["text"] != "some *odd_ markdown" {
		t.Fatalf("fallback attempt=%#v", requests[1])
	}
}
