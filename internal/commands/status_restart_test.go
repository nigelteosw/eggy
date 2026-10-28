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
