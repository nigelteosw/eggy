package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestClientDownloadImage(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 32)...)
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/bottoken/getFile":
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["file_id"] != "file-1" {
				t.Fatalf("payload=%#v", payload)
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":{"file_path":"photos/list.png","file_size":40}}`)
		case "/file/bottoken/photos/list.png":
			_, _ = w.Write(png)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	part, err := NewClient(server.URL, "token", "42", server.Client()).DownloadImage(
		context.Background(), "file-1", int64(len(png)), "image/png",
	)
	if err != nil {
		t.Fatal(err)
	}
	if part.Type != ports.ContentTypeImage || part.MediaType != "image/png" || !bytes.Equal(part.Data, png) {
		t.Fatalf("part=%#v", part)
	}
	if len(paths) != 2 || paths[0] != "/bottoken/getFile" || paths[1] != "/file/bottoken/photos/list.png" {
		t.Fatalf("paths=%v", paths)
	}
}

func TestClientDownloadImageRejectsInvalidInputAndContent(t *testing.T) {
	tests := []struct {
		name          string
		declaredSize  int64
		declaredType  string
		body          []byte
		wantNoRequest bool
	}{
		{name: "declared too large", declaredSize: maxImageBytes + 1, declaredType: "image/png", wantNoRequest: true},
		{name: "unsupported declared type", declaredSize: 4, declaredType: "application/pdf", wantNoRequest: true},
		{name: "downloaded text", declaredSize: 4, declaredType: "image/png", body: []byte("text")},
		{name: "streamed too large", declaredSize: 0, declaredType: "image/png", body: bytes.Repeat([]byte{0}, int(maxImageBytes+1))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var requests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if strings.HasSuffix(r.URL.Path, "/getFile") {
					_, _ = io.WriteString(w, `{"ok":true,"result":{"file_path":"files/input","file_size":0}}`)
					return
				}
				_, _ = w.Write(tc.body)
			}))
			defer server.Close()
			_, err := NewClient(server.URL, "top-secret-token", "42", server.Client()).DownloadImage(
				context.Background(), "file-1", tc.declaredSize, tc.declaredType,
			)
			if err == nil {
				t.Fatal("DownloadImage succeeded")
			}
			if tc.wantNoRequest && requests != 0 {
				t.Fatalf("requests=%d, want none", requests)
			}
			if strings.Contains(err.Error(), "top-secret-token") || strings.Contains(err.Error(), "/file/bot") {
				t.Fatalf("error leaked authenticated download details: %v", err)
			}
		})
	}
}

func TestClientDownloadImageHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getFile") {
			_, _ = io.WriteString(w, `{"ok":true,"result":{"file_path":"photos/wait.png"}}`)
			return
		}
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := NewClient(server.URL, "token", "42", server.Client()).DownloadImage(ctx, "file-1", 0, "image/png")
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
}

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
	client := NewClient("https://api.telegram.test", "token", "99", httpClient)
	var lines []string
	for i := 0; i < 400; i++ {
		lines = append(lines, "line number is here")
	}
	if err := client.Deliver(context.Background(), strings.Join(lines, "\n")); err != nil {
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
	client := NewClient("https://api.telegram.test", "token", "99", httpClient)
	messageID, err := client.DeliverTrackable(context.Background(), "hello there")
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
	client := NewClient("https://api.telegram.test", "token", "99", httpClient)
	if err := client.EditText(context.Background(), "555", "**Approved.**"); err != nil {
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
	client := NewClient("https://api.telegram.test", "token", "99", httpClient)
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
	client := NewClient("https://api.telegram.test", "token", "99", httpClient)
	if err := client.SendTyping(context.Background()); err != nil {
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
	client := NewClient("https://api.telegram.test", "token", "99", httpClient)
	err := client.SetCommands(context.Background(), []BotCommand{
		{Name: "status", Description: "Show operational status"},
		{Name: "clear", Description: "Clear the context window"},
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
	client := NewClient("https://api.telegram.test", "token", "99", httpClient)
	if err := client.Deliver(context.Background(), "some *odd_ markdown"); err != nil {
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
