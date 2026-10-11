package bootstrap

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/core/events"
)

func TestTelegramMenuAndSettingsDispatchWithoutModelCalls(t *testing.T) {
	var buttons map[string]string
	modelCalls := 0
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "deepseek.test" {
			modelCalls++
			return appJSON(500, `{}`), nil
		}
		if strings.HasSuffix(request.URL.Path, "getMe") {
			return appJSON(200, `{"ok":true,"result":{"username":"eggy_test"}}`), nil
		}
		if strings.HasSuffix(request.URL.Path, "sendMessage") || strings.HasSuffix(request.URL.Path, "editMessageText") {
			var body struct {
				Markup struct {
					Rows [][]struct {
						Text string `json:"text"`
						Data string `json:"callback_data"`
					} `json:"inline_keyboard"`
				} `json:"reply_markup"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			buttons = map[string]string{}
			for _, row := range body.Markup.Rows {
				for _, button := range row {
					buttons[button.Text] = button.Data
				}
			}
			return appJSON(200, `{"ok":true,"result":{"message_id":12}}`), nil
		}
		return appJSON(200, `{"ok":true,"result":true}`), nil
	})}
	app, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("key"), AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.database.Close() })
	payload, _ := json.Marshal(events.Message{Text: "/menu"})
	event := events.Event{ID: "menu", Owner: "42", Source: "telegram", SenderID: "42", Destination: destination.Destination{Kind: destination.Telegram}, Type: events.TypeMessage, Payload: payload}
	if err := app.HandleEvent(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	for i, label := range []string{"My settings", "Thinking: on", "Off"} {
		data := buttons[label]
		if data == "" {
			t.Fatalf("missing %q in %v", label, buttons)
		}
		event.ID = "control-" + label
		event.Type = events.TypeControl
		event.Payload, _ = json.Marshal(events.ControlRequest{Data: data, MessageID: "12"})
		if err := app.HandleEvent(t.Context(), event); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	show, err := app.runtime.ShowThinking(ownerCtx())
	if err != nil || show {
		t.Fatalf("thinking=%v error=%v", show, err)
	}
	if modelCalls != 0 {
		t.Fatalf("model calls=%d", modelCalls)
	}
}
