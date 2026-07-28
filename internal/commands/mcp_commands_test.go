package commands

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	mcpadapter "github.com/nigelteosw/eggy/plugins/tools/mcp"
)

func TestMCPCommandsUseManager(t *testing.T) {
	fake := &fakeMCPCommands{statuses: []mcpadapter.ServerStatus{{Name: "railway", State: mcpadapter.StateReady, Tools: 3}}, loginURL: "https://auth.example/authorize?opaque=redacted"}
	commands := &CommandService{mcp: fake}
	tests := []struct{ input, want string }{
		{"/mcp", "railway"},
		{"/mcp status railway", "ready"},
		{"/mcp probe railway", "Latency"},
		{"/mcp login railway", "https://auth.example/authorize"},
		{"/mcp logout railway", "Logged out"},
		{"/mcp reload railway", "Reloaded"},
	}
	for _, test := range tests {
		output, handled, err := commands.Execute(context.Background(), test.input)
		if err != nil || !handled || !strings.Contains(output, test.want) {
			t.Fatalf("%s output=%q handled=%v err=%v", test.input, output, handled, err)
		}
		if strings.Contains(output, "access-token") || strings.Contains(output, "refresh-token") || strings.Contains(output, "verifier") {
			t.Fatalf("credential material in %s output: %s", test.input, output)
		}
	}
	if fake.probes != 1 || fake.logins != 1 || fake.logouts != 1 || fake.refreshes != 1 {
		t.Fatalf("fake=%#v", fake)
	}
}

func TestMCPCommandsValidateConfigurationAndUsage(t *testing.T) {
	commands := &CommandService{}
	output, handled, err := commands.Execute(context.Background(), "/mcp status railway")
	if err != nil || !handled || !strings.Contains(output, "not configured") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
	commands.mcp = &fakeMCPCommands{}
	output, _, err = commands.Execute(context.Background(), "/mcp login")
	if err != nil || !strings.Contains(output, "Usage") {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

// Logging out and reloading are in-process operations now: neither may claim
// a restart, and neither may trigger one.
func TestMCPLogoutAndReloadDoNotRestart(t *testing.T) {
	restarts := 0
	fake := &fakeMCPCommands{statuses: []mcpadapter.ServerStatus{{Name: "railway", State: mcpadapter.StateReady, Tools: 3}}}
	commands := &CommandService{mcp: fake, restart: func() { restarts++ }}
	for _, input := range []string{"/mcp logout railway", "/mcp reload railway"} {
		output, handled, err := commands.Execute(context.Background(), input)
		if err != nil || !handled || strings.Contains(output, "estart") {
			t.Fatalf("%s output=%q handled=%v err=%v", input, output, handled, err)
		}
	}
	if restarts != 0 {
		t.Fatalf("restarts=%d", restarts)
	}
}

type fakeMCPCommands struct {
	statuses  []mcpadapter.ServerStatus
	loginURL  string
	probes    int
	logins    int
	logouts   int
	refreshes int
}

func (f *fakeMCPCommands) Statuses() []mcpadapter.ServerStatus { return f.statuses }
func (f *fakeMCPCommands) Status(name string) (mcpadapter.ServerStatus, error) {
	for _, status := range f.statuses {
		if status.Name == name {
			return status, nil
		}
	}
	return mcpadapter.ServerStatus{}, mcpadapter.ErrServerNotFound
}
func (f *fakeMCPCommands) Probe(context.Context, string) (mcpadapter.ProbeResult, error) {
	f.probes++
	return mcpadapter.ProbeResult{Server: "railway", State: mcpadapter.StateReady, Tools: 3, Latency: 12 * time.Millisecond}, nil
}
func (f *fakeMCPCommands) BeginLogin(context.Context, string) (string, error) {
	f.logins++
	if f.loginURL == "" {
		return "", errors.New("login unavailable")
	}
	return f.loginURL, nil
}
func (f *fakeMCPCommands) Logout(string) error                   { f.logouts++; return nil }
func (f *fakeMCPCommands) Refresh(context.Context, string) error { f.refreshes++; return nil }
