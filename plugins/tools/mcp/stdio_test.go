package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// stdioHelperEnv makes the test binary re-exec itself as a real MCP server
// over stdin and stdout, so the stdio transport is exercised end to end
// against an actual subprocess rather than a stub session.
const (
	stdioHelperEnv   = "EGGY_MCP_STDIO_HELPER"
	stdioAllowedEnv  = "EGGY_MCP_STDIO_ALLOWED"
	stdioWithheldEnv = "EGGY_MCP_STDIO_WITHHELD"
)

// TestStdioHelperProcess is not a test: it is the server half of
// TestStdioTransportCallsToolAndFiltersEnvironment, and does nothing unless
// the parent asked for it through the environment.
func TestStdioHelperProcess(t *testing.T) {
	if os.Getenv(stdioHelperEnv) != "1" {
		t.Skip("helper process only")
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "helper", Version: "1"}, nil)
	server.AddTool(
		&sdk.Tool{Name: "report_env", Description: "Reports the child's view of two environment variables", InputSchema: map[string]any{"type": "object", "additionalProperties": false}},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			report := "allowed=" + os.Getenv(stdioAllowedEnv) + " withheld=" + os.Getenv(stdioWithheldEnv)
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: report}}}, nil
		},
	)
	if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		os.Exit(1)
	}
	// Exit before the testing package writes its own summary to stdout,
	// which would otherwise be framed as a protocol message.
	os.Exit(0)
}

func TestStdioTransportCallsToolAndFiltersEnvironment(t *testing.T) {
	t.Setenv(stdioHelperEnv, "1")
	t.Setenv(stdioAllowedEnv, "visible")
	t.Setenv(stdioWithheldEnv, "secret")
	config := ServerConfig{
		Name: "helper", Transport: TransportStdio, Auth: "none", Enabled: true,
		Command: os.Args[0], Args: []string{"-test.run=TestStdioHelperProcess"},
		// The withheld variable is deliberately absent: it is set in this
		// process and must not reach the child.
		EnvAllowlist:   []string{stdioHelperEnv, stdioAllowedEnv},
		ConnectTimeout: 20 * time.Second, Timeout: 20 * time.Second, MaxOutputBytes: 4096,
	}
	manager, err := NewManager(context.Background(), []ServerConfig{config}, Options{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer manager.Close()

	status, err := manager.Status("helper")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateReady {
		t.Fatalf("state = %q (%s), want ready", status.State, status.Diagnostic)
	}
	tools := manager.Tools()
	if len(tools) != 1 || tools[0].Definition().Name != "helper__report_env" {
		t.Fatalf("tools = %#v, want one helper__report_env", tools)
	}

	raw, err := tools[0].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(string(raw), "allowed=visible") {
		t.Fatalf("result %s does not report the allowlisted variable", raw)
	}
	if strings.Contains(string(raw), "secret") {
		t.Fatalf("result %s leaked a variable the server did not allowlist", raw)
	}
}

func TestChildEnvironmentForwardsOnlyAllowlistedVariables(t *testing.T) {
	values := map[string]string{"PATH": "/bin", "HOME": "/home/eggy", "GITHUB_TOKEN": "secret", "WANTED": "yes", "UNSET": ""}
	environment := childEnvironment([]string{"WANTED", "UNSET", "PATH"}, func(name string) string { return values[name] })
	want := []string{"HOME=/home/eggy", "PATH=/bin", "WANTED=yes"}
	if len(environment) != len(want) {
		t.Fatalf("environment = %v, want %v", environment, want)
	}
	for index, entry := range want {
		if environment[index] != entry {
			t.Fatalf("environment = %v, want %v", environment, want)
		}
	}
}

func TestStdioTransportRequiresCommand(t *testing.T) {
	config := ServerConfig{Name: "broken", Transport: TransportStdio, Enabled: true, ConnectTimeout: time.Second, Timeout: time.Second, MaxOutputBytes: 1024}
	manager, err := NewManager(context.Background(), []ServerConfig{config}, Options{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer manager.Close()
	status, err := manager.Status("broken")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != StateUnavailable {
		t.Fatalf("state = %q, want unavailable", status.State)
	}
}
