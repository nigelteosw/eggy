package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type menuFake struct{ calls []string }

func (m *menuFake) Control(_ context.Context, action string) (ports.ControlPanel, error) {
	m.calls = append(m.calls, action)
	return ports.ControlPanel{Text: "Settings", Options: []ports.ControlOption{{Label: "Thinking on", Action: "set:/model settings thinking on"}}}, nil
}

func TestFailedReplacementKeepsVisibleMenuUsable(t *testing.T) {
	var callback string
	fail := false
	client := NewClient("https://api.telegram.test", "token", FixedChat("99"), &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if fail {
			return nil, errors.New("offline")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		callback = body["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)[0].(map[string]any)["callback_data"].(string)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":12}}`))}, nil
	})})
	menu := &menuFake{}
	control := NewControl(client, menu, time.Now)
	ctx := selectionContext("alice")
	if err := control.Show(ctx, "menu", ""); err != nil {
		t.Fatal(err)
	}
	original := callback
	fail = true
	if err := control.Show(ctx, "settings", ""); err == nil {
		t.Fatal("expected failed send")
	}
	fail = false
	if err := control.Callback(ctx, original, "12", ""); err != nil {
		t.Fatal(err)
	}
	if len(menu.calls) != 3 {
		t.Fatalf("visible menu was invalidated: calls=%v", menu.calls)
	}
}

func TestControlCallbacksAreOwnedBoundedAndConsumedAfterDispatch(t *testing.T) {
	var data string
	client := NewClient("https://api.telegram.test", "token", FixedChat("99"), &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body struct {
			ReplyMarkup struct {
				Rows [][]struct {
					Data string `json:"callback_data"`
				} `json:"inline_keyboard"`
			} `json:"reply_markup"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.ReplyMarkup.Rows) > 0 {
			data = body.ReplyMarkup.Rows[0][0].Data
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":12}}`)), Header: make(http.Header)}, nil
	})})
	menu := &menuFake{}
	control := NewControl(client, menu, time.Now)
	alice := selectionContext("alice")
	bob := selectionContext("bob")
	if err := control.Show(alice, "settings", ""); err != nil {
		t.Fatal(err)
	}
	original := data
	if len(data) > 64 || !strings.HasPrefix(data, "control:") {
		t.Fatalf("callback=%q", data)
	}
	if err := control.Callback(bob, data, "12", ""); err != nil {
		t.Fatal(err)
	}
	if len(menu.calls) != 1 {
		t.Fatal("another account executed an action")
	}
	if err := control.Callback(alice, data, "13", ""); err != nil {
		t.Fatal(err)
	}
	if len(menu.calls) != 1 {
		t.Fatal("wrong message executed an action")
	}
	if err := control.Callback(alice, original, "12", ""); err != nil {
		t.Fatal(err)
	}
	if len(menu.calls) != 2 || menu.calls[1] != "set:/model settings thinking on" {
		t.Fatalf("calls=%v", menu.calls)
	}
	if err := control.Callback(alice, original, "12", ""); err != nil {
		t.Fatal(err)
	}
	if len(menu.calls) != 2 {
		t.Fatal("duplicate executed twice")
	}
}

// The toast is the menu's result line in plain text, cut to what Telegram
// allows, so a tap says what changed without the markdown around it.
func TestToastTextIsTheFirstPlainLineWithinTheLimit(t *testing.T) {
	if got := toastText("**Check-ins on.**\n\nThe rest of the menu"); got != "Check-ins on." {
		t.Fatalf("toastText = %q", got)
	}
	long := strings.Repeat("x", 300)
	if got := []rune(toastText(long)); len(got) != 200 {
		t.Fatalf("toast length = %d, want 200", len(got))
	}
}
