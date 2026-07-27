package bootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/kernel/events"
)

// deliverOwnerMessage injects an owner message while a turn is already
// running. It is called from inside the model round-tripper, i.e. on the
// running turn's own goroutine, which is the closest a test gets to a
// Telegram message landing mid-turn.
func deliverOwnerMessage(t *testing.T, app *App, id, text string) {
	t.Helper()
	payload, _ := json.Marshal(events.Message{Text: text})
	if err := app.HandleEvent(context.Background(), events.Event{ID: id, Type: events.TypeMessage, Owner: "42", Payload: payload}); err != nil {
		t.Errorf("delivering %q mid-turn: %v", text, err)
	}
}

// TestAMessageDeliveredMidTurnChangesTheTurnsSubsequentToolCalls is the
// steering behaviour: the owner redirects work already in progress instead of
// starting a competing turn or waiting for the first to finish.
func TestAMessageDeliveredMidTurnChangesTheTurnsSubsequentToolCalls(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	var modelBodies [][]byte
	var app *App
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Host == "deepseek.test":
			body, _ := io.ReadAll(request.Body)
			modelBodies = append(modelBodies, body)
			switch len(modelBodies) {
			case 1:
				// The turn is now in flight. The owner changes their mind
				// while this first tool call is being answered.
				deliverOwnerMessage(t, app, "steer-1", "actually, just tell me the time")
				return appJSON(200, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"status","arguments":"{}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`), nil
			case 2:
				return appJSON(200, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-2","type":"function","function":{"name":"current_time","arguments":"{}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`), nil
			default:
				return appJSON(200, `{"choices":[{"message":{"role":"assistant","content":"It is noon."}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`), nil
			}
		case strings.Contains(request.URL.Path, "sendMessage"), strings.Contains(request.URL.Path, "setMyCommands"):
			return appJSON(200, `{"ok":true,"result":{}}`), nil
		default:
			return appJSON(404, `{}`), nil
		}
	})}
	var err error
	app, err = NewApp(cfg, appTestSecrets("deepseek"), AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.Message{Text: "check on things"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "turn-1", Type: events.TypeMessage, Owner: "42", Payload: payload}); err != nil {
		t.Fatal(err)
	}

	if len(modelBodies) < 2 {
		t.Fatalf("model requests=%d, want the turn to continue past the steer", len(modelBodies))
	}
	// The steer joined the turn already running: it reached the *second*
	// model call of that same turn, not a new turn's first call.
	if !strings.Contains(string(modelBodies[1]), "actually, just tell me the time") {
		t.Fatal("the steered message never reached the running turn's next model call")
	}
	if count := strings.Count(string(modelBodies[len(modelBodies)-1]), "actually, just tell me the time"); count != 1 {
		t.Fatalf("steered message appears %d times in the final call, want exactly 1 (it must not replay at every step)", count)
	}
	// A competing turn would have started its own model call without the
	// original instruction; every call here belongs to the one turn.
	for i, body := range modelBodies {
		if !strings.Contains(string(body), "check on things") {
			t.Fatalf("model call %d does not belong to the original turn: a competing turn was started instead of steering", i)
		}
	}
}

// TestStopMidTurnLeavesTheWorkspaceInspectable pins the other half: stopping
// is not a rollback. The turn stops taking steps, and the checkout the owner
// was working in stays attached so they can look at it or continue.
func TestStopMidTurnLeavesTheWorkspaceInspectable(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Repositories = []config.RepositoryConfig{{Name: "eggy", CloneURL: createLocalGitRemote(t), BaseBranch: "main", ProtectedBranches: []string{"main"}}}
	var modelBodies [][]byte
	var delivered []string
	var app *App
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Host == "deepseek.test":
			body, _ := io.ReadAll(request.Body)
			modelBodies = append(modelBodies, body)
			switch len(modelBodies) {
			case 1:
				return appJSON(200, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"workspace_open","arguments":"{\"repository\":\"eggy\"}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`), nil
			default:
				// The checkout is attached and the turn is mid-flight; the
				// owner stops it here.
				deliverOwnerMessage(t, app, "stop-1", "/stop")
				return appJSON(200, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-2","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`), nil
			}
		case strings.Contains(request.URL.Path, "sendMessage"):
			body, _ := io.ReadAll(request.Body)
			delivered = append(delivered, string(body))
			return appJSON(200, `{"ok":true,"result":{}}`), nil
		case strings.Contains(request.URL.Path, "setMyCommands"):
			return appJSON(200, `{"ok":true,"result":true}`), nil
		default:
			return appJSON(404, `{}`), nil
		}
	})}
	var err error
	app, err = NewApp(cfg, appTestSecrets("deepseek"), AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.Message{Text: "have a look at eggy"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "turn-1", Type: events.TypeMessage, Owner: "42", Payload: payload}); err != nil {
		t.Fatal(err)
	}

	// The stopped turn told the owner so, on their own surface.
	if !strings.Contains(strings.Join(delivered, "\n"), "Stopped.") {
		t.Fatalf("expected a stop milestone on the owner's surface: %v", delivered)
	}
	// And the checkout survived: stopping is not a rollback.
	binding, err := app.workspaces.Resolve(context.Background())
	if err != nil || binding.Path == "" || binding.Repository != "eggy" {
		t.Fatalf("binding=%#v err=%v, want the workspace still attached after /stop", binding, err)
	}
}
