package telegram

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func newTrackingClient(t *testing.T, editErr bool) (*Client, *[]map[string]any, *[]map[string]any) {
	t.Helper()
	var sent, edited []map[string]any
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		switch {
		case strings.Contains(r.URL.Path, "editMessageText"):
			if editErr {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":false,"description":"Bad Request: message to edit not found"}`))}, nil
			}
			edited = append(edited, payload)
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`))}, nil
		default:
			sent = append(sent, payload)
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":1}}`))}, nil
		}
	})}
	return NewClient("https://api.telegram.test", "token", httpClient), &sent, &edited
}

func TestProgressTrackerKeepsAConciseTimelineForEachRun(t *testing.T) {
	client, sent, edited := newTrackingClient(t, false)
	tracker := NewProgressTracker(client, "42")
	tracker.Deliver(ports.CodingProgress{RunID: "run-1", Kind: "started", Message: "Codex run started"})
	tracker.Deliver(ports.CodingProgress{RunID: "run-1", Kind: "command", Message: "go test ./..."})
	if len(*sent) != 1 || len(*edited) != 1 {
		t.Fatalf("sent=%v edited=%v", *sent, *edited)
	}
	text, _ := (*edited)[0]["text"].(string)
	if !strings.Contains(text, "Codex run started") || !strings.Contains(text, "go test ./...") {
		t.Fatalf("edited=%v", *edited)
	}
}

func TestProgressTrackerClearsTrackingOnTerminalKind(t *testing.T) {
	client, sent, edited := newTrackingClient(t, false)
	tracker := NewProgressTracker(client, "42")
	tracker.Deliver(ports.CodingProgress{RunID: "run-1", Kind: "started", Message: "started"})
	tracker.Deliver(ports.CodingProgress{RunID: "run-1", Kind: "completed", Message: "done"})
	tracker.Deliver(ports.CodingProgress{RunID: "run-1", Kind: "started", Message: "started again"})
	if len(*sent) != 2 {
		t.Fatalf("expected a fresh message after the terminal event, got %v", *sent)
	}
	text, _ := (*edited)[0]["text"].(string)
	if len(*edited) != 1 || !strings.Contains(text, "started") || !strings.Contains(text, "done") {
		t.Fatalf("expected the terminal event to edit the existing message before clearing tracking, got %v", *edited)
	}
}

func TestProgressTrackerTracksSeparateRunsIndependently(t *testing.T) {
	client, sent, edited := newTrackingClient(t, false)
	tracker := NewProgressTracker(client, "42")
	tracker.Deliver(ports.CodingProgress{RunID: "run-1", Kind: "started", Message: "run one started"})
	tracker.Deliver(ports.CodingProgress{RunID: "run-2", Kind: "started", Message: "run two started"})
	tracker.Deliver(ports.CodingProgress{RunID: "run-1", Kind: "command", Message: "run one step"})
	if len(*sent) != 2 || len(*edited) != 1 {
		t.Fatalf("sent=%v edited=%v", *sent, *edited)
	}
}

func TestProgressTrackerFallsBackToNewMessageWhenEditFails(t *testing.T) {
	client, sent, _ := newTrackingClient(t, true)
	tracker := NewProgressTracker(client, "42")
	tracker.Deliver(ports.CodingProgress{RunID: "run-1", Kind: "started", Message: "started"})
	tracker.Deliver(ports.CodingProgress{RunID: "run-1", Kind: "command", Message: "step"})
	if len(*sent) != 2 {
		t.Fatalf("expected a fallback new message when editing failed, got %v", *sent)
	}
}

func TestProgressTrackerIgnoresEmptyMessages(t *testing.T) {
	client, sent, edited := newTrackingClient(t, false)
	tracker := NewProgressTracker(client, "42")
	tracker.Deliver(ports.CodingProgress{RunID: "run-1", Kind: "diagnostic", Message: ""})
	if len(*sent) != 0 || len(*edited) != 0 {
		t.Fatalf("unexpected delivery for an empty message: sent=%v edited=%v", *sent, *edited)
	}
}
