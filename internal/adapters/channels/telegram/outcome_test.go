package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDeliverOutcomeEditsInPlaceWhenMessageIDPresent(t *testing.T) {
	var edited, sent []map[string]any
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		if strings.Contains(r.URL.Path, "editMessageText") {
			edited = append(edited, payload)
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`))}, nil
		}
		sent = append(sent, payload)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`))}, nil
	})}
	client := NewClient("https://api.telegram.test", "token", httpClient)
	if err := DeliverOutcome(context.Background(), client, "42", "555", "Action rejected."); err != nil {
		t.Fatal(err)
	}
	if len(edited) != 1 || edited[0]["chat_id"] != "42" || edited[0]["message_id"] != "555" {
		t.Fatalf("edited=%v", edited)
	}
	if len(sent) != 0 {
		t.Fatalf("unexpected new message sent: %v", sent)
	}
}

func TestDeliverOutcomeSendsNewMessageWhenMessageIDAbsent(t *testing.T) {
	var edited, sent []map[string]any
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		if strings.Contains(r.URL.Path, "editMessageText") {
			edited = append(edited, payload)
		} else {
			sent = append(sent, payload)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`))}, nil
	})}
	client := NewClient("https://api.telegram.test", "token", httpClient)
	if err := DeliverOutcome(context.Background(), client, "42", "", "Action rejected."); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent=%v", sent)
	}
	if len(edited) != 0 {
		t.Fatalf("unexpected edit: %v", edited)
	}
}

func TestDeliverOutcomeFallsBackToNewMessageWhenEditFails(t *testing.T) {
	var sent []map[string]any
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "editMessageText") {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":false,"description":"Bad Request: message to edit not found"}`))}, nil
		}
		var payload map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		sent = append(sent, payload)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`))}, nil
	})}
	client := NewClient("https://api.telegram.test", "token", httpClient)
	if err := DeliverOutcome(context.Background(), client, "42", "555", "Action rejected."); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("expected a fallback new message when editing failed, got %v", sent)
	}
}
