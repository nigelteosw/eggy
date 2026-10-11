package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/core/events"
)

func TestWebhookQueuesControlsWithoutResolvingOrConsumingThem(t *testing.T) {
	attempts := 0
	handler := NewWebhookHandler(SingleOwner(42), "secret", func(_ context.Context, event events.Event) error {
		attempts++
		if event.Type != events.TypeControl || event.Owner != "42" || event.SenderID != "42" {
			t.Fatalf("event=%+v", event)
		}
		var request events.ControlRequest
		if err := json.Unmarshal(event.Payload, &request); err != nil || request.Data != "control:opaque:0" || request.MessageID != "12" {
			t.Fatalf("payload=%s error=%v", event.Payload, err)
		}
		if attempts == 1 {
			return errors.New("queue full")
		}
		return nil
	}, nil).WithSelectionResolver(func(context.Context, string) (string, bool) {
		t.Fatal("control entered model-selection path")
		return "", false
	})
	for _, want := range []int{http.StatusInternalServerError, http.StatusNoContent} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"update_id":77,"callback_query":{"id":"cb","from":{"id":42},"data":"control:opaque:0","message":{"message_id":12,"chat":{"id":42}}}}`))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("status=%d want=%d", response.Code, want)
		}
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d", attempts)
	}
}
