package commands

import (
	"context"
	"strings"
	"testing"
)

// /status is where an owner checks whether a change they made is live. A
// pending restart is the answer to that, so it is reported beside the model.
func TestStatusSaysWhenAChangeIsWaitingForARestart(t *testing.T) {
	pending := true
	service := New(Options{RestartPending: func() bool { return pending }})
	output, _, err := service.Execute(context.Background(), "/status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Waiting for a restart") || !strings.Contains(output, "/restart") {
		t.Fatalf("status does not report the pending restart: %q", output)
	}

	pending = false
	output, _, err = service.Execute(context.Background(), "/status")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "Waiting for a restart") {
		t.Fatalf("status reports a restart nothing is waiting for: %q", output)
	}
}

// A tap on a settings button redraws the menu in place. Its result is one line
// above the menu, not the whole reply pasted over it, so the message does not
// grow with each tap.
func TestControlTapReportsOneLineAboveTheMenu(t *testing.T) {
	models := &fakeModels{selected: "fast"}
	service := New(Options{AgentRuntime: models, DefaultModel: "fast", ModelAliases: []string{"fast", "smart"}})
	panel, err := service.Control(context.Background(), "set:/heartbeat on")
	if err != nil {
		t.Fatal(err)
	}
	first, rest, _ := strings.Cut(panel.Text, "\n")
	if !strings.HasPrefix(first, "✓ ") || strings.Count(panel.Text, "**My settings**") != 1 {
		t.Fatalf("tap result is not one line above a single menu: %q", panel.Text)
	}
	if strings.Count(rest, "\n") > 40 {
		t.Fatalf("the menu grew: %d lines", strings.Count(rest, "\n"))
	}
}
