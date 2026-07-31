package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/config"
)

type fakeMCPRuntime struct {
	statuses    []MCPStatus
	loginURL    string
	loginErr    error
	loggedIn    []string
	loggedOut   []string
	logoutError error
}

func (f *fakeMCPRuntime) Statuses() []MCPStatus { return f.statuses }
func (f *fakeMCPRuntime) BeginLogin(_ context.Context, server string) (string, error) {
	f.loggedIn = append(f.loggedIn, server)
	return f.loginURL, f.loginErr
}
func (f *fakeMCPRuntime) Logout(server string) error {
	f.loggedOut = append(f.loggedOut, server)
	return f.logoutError
}

// mcpTestConfig writes the smallest config that loads, so the command under
// test exercises the real internal/config helpers rather than a fake store.
// That is the property worth testing: Telegram and the web panel write through
// the same code path.
func mcpTestConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `server:
  public_base_url: "https://eggy.example"
data_dir: "/data"
owner:
  id: "42"
agent:
  default_model: "chat"
  timezone: UTC
providers:
  deepseek:
    adapter: "openai_compatible"
    base_url: "https://api.deepseek.com"
    api_key_env: "DEEPSEEK_API_KEY"
models:
  chat:
    provider: "deepseek"
    model: "deepseek-chat"
runner:
  root: "/data/runs"
  timeout: "45m"
  retention: "30m"
  max_output_bytes: 1048576
  allowed_env: ["PATH"]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mcpService(t *testing.T, runtime MCPRuntime) (*CommandService, string) {
	t.Helper()
	path := mcpTestConfig(t)
	return New(Options{ConfigPath: path, MCP: runtime}), path
}

func run(t *testing.T, service *CommandService, input string) string {
	t.Helper()
	output, handled, err := service.Execute(context.Background(), input)
	if err != nil || !handled {
		t.Fatalf("%s: handled=%v err=%v", input, handled, err)
	}
	return output
}

// TestMCPAddWritesThroughInternalConfig is the "one administration authority"
// property: a Telegram edit is readable by the same helper the web panel
// calls, because both went through internal/config.
func TestMCPAddWritesThroughInternalConfig(t *testing.T) {
	service, path := mcpService(t, nil)

	output := run(t, service, "/mcp add calendar url=https://calendarmcp.googleapis.com/mcp/v1 auth=oauth")
	if !strings.Contains(output, "Saved MCP server calendar") || !strings.Contains(output, "Restart Eggy") {
		t.Fatalf("output=%q", output)
	}
	if !strings.Contains(output, "/mcp login calendar") {
		t.Fatalf("an oauth server was saved without telling the owner to authorize it: %q", output)
	}
	servers, err := config.GetMCPServersConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	server, ok := servers["calendar"]
	if !ok {
		t.Fatalf("servers=%#v", servers)
	}
	if server.URL != "https://calendarmcp.googleapis.com/mcp/v1" || server.Auth != "oauth" || !server.Enabled {
		t.Fatalf("server=%#v", server)
	}
	if server.Transport != "streamable-http" {
		t.Fatalf("transport=%q, want the streamable-http default", server.Transport)
	}
}

func TestMCPAddAcceptsAnExplicitTransport(t *testing.T) {
	service, path := mcpService(t, nil)

	run(t, service, "/mcp add calendar url=https://calendarmcp.googleapis.com/mcp/v1 auth=oauth transport=streamable-http")
	servers, _ := config.GetMCPServersConfig(path)
	if servers["calendar"].Transport != "streamable-http" {
		t.Fatalf("servers=%#v", servers)
	}

	// A stdio server is a subprocess command line, which is not a chat
	// argument. It must be refused rather than rewritten into an HTTP server.
	output := run(t, service, "/mcp add local transport=stdio")
	if !strings.Contains(output, "config.yaml") {
		t.Fatalf("stdio add output=%q", output)
	}
	servers, _ = config.GetMCPServersConfig(path)
	if _, ok := servers["local"]; ok {
		t.Fatalf("a stdio server was written from chat: %#v", servers)
	}
}

// TestMCPAddNamesTheBearerVariableWithoutCarryingItsValue: the env var name is
// config, its value is not. A chat surface must never accept the token, and
// must say where the value has to come from instead.
func TestMCPAddNamesTheBearerVariableWithoutCarryingItsValue(t *testing.T) {
	service, path := mcpService(t, nil)

	output := run(t, service, "/mcp add tickets url=https://mcp.example.com auth=bearer-env bearer_env=TICKETS_TOKEN")
	if !strings.Contains(output, "TICKETS_TOKEN") || !strings.Contains(output, "environment") {
		t.Fatalf("output=%q", output)
	}
	servers, _ := config.GetMCPServersConfig(path)
	if servers["tickets"].BearerTokenEnv != "TICKETS_TOKEN" {
		t.Fatalf("servers=%#v", servers)
	}
}

func TestMCPRejectsUnknownFieldsAndBadValues(t *testing.T) {
	service, path := mcpService(t, nil)

	for _, input := range []string{
		"/mcp add oops url=https://mcp.example.com secret=hunter2",
		"/mcp add oops url=https://mcp.example.com enabled=maybe",
		"/mcp add oops url=https://mcp.example.com auth=whatever",
		"/mcp add oops url=http://mcp.example.com",
		"/mcp add oops bare-argument",
	} {
		output := run(t, service, input)
		if strings.Contains(output, "Saved MCP server") {
			t.Fatalf("%s was accepted: %q", input, output)
		}
	}
	servers, _ := config.GetMCPServersConfig(path)
	if len(servers) != 0 {
		t.Fatalf("rejected input still wrote config: %#v", servers)
	}
}

func TestMCPEnableDisableAndRemove(t *testing.T) {
	service, path := mcpService(t, nil)
	run(t, service, "/mcp add calendar url=https://mcp.example.com auth=oauth")

	run(t, service, "/mcp disable calendar")
	servers, _ := config.GetMCPServersConfig(path)
	if servers["calendar"].Enabled {
		t.Fatal("disable did not stick")
	}
	run(t, service, "/mcp enable calendar")
	servers, _ = config.GetMCPServersConfig(path)
	if !servers["calendar"].Enabled {
		t.Fatal("enable did not stick")
	}

	output := run(t, service, "/mcp remove calendar")
	if !strings.Contains(output, "credentials are kept") {
		t.Fatalf("output=%q", output)
	}
	servers, _ = config.GetMCPServersConfig(path)
	if len(servers) != 0 {
		t.Fatalf("servers=%#v", servers)
	}

	if output := run(t, service, "/mcp remove calendar"); !strings.Contains(output, "not configured") {
		t.Fatalf("removing a missing server output=%q", output)
	}
}

// TestMCPLoginReturnsTheAuthorizationURL covers the gap this whole command
// exists to close: BeginLogin had no caller anywhere, so an auth: oauth server
// could be configured and never authorized.
func TestMCPLoginReturnsTheAuthorizationURL(t *testing.T) {
	runtime := &fakeMCPRuntime{loginURL: "https://accounts.google.com/o/oauth2/v2/auth?state=abc"}
	service, _ := mcpService(t, runtime)
	run(t, service, "/mcp add calendar url=https://mcp.example.com auth=oauth")

	output := run(t, service, "/mcp login calendar")
	if !strings.Contains(output, runtime.loginURL) {
		t.Fatalf("output=%q", output)
	}
	if len(runtime.loggedIn) != 1 || runtime.loggedIn[0] != "calendar" {
		t.Fatalf("loggedIn=%v", runtime.loggedIn)
	}

	runtime.loginErr = errors.New("server is not configured for OAuth")
	if output := run(t, service, "/mcp login calendar"); !strings.Contains(output, "not configured for OAuth") {
		t.Fatalf("login failure output=%q", output)
	}
}

func TestMCPLogoutIsReachable(t *testing.T) {
	runtime := &fakeMCPRuntime{}
	service, _ := mcpService(t, runtime)

	output := run(t, service, "/mcp logout calendar")
	if len(runtime.loggedOut) != 1 || !strings.Contains(output, "/mcp login calendar") {
		t.Fatalf("loggedOut=%v output=%q", runtime.loggedOut, output)
	}
}

// TestMCPListDistinguishesConfiguredFromRunning is the honesty requirement: a
// server added since startup is in config.yaml but not in the running manager,
// and saying "unavailable" would send the owner hunting a network fault.
func TestMCPListDistinguishesConfiguredFromRunning(t *testing.T) {
	runtime := &fakeMCPRuntime{statuses: []MCPStatus{
		{Name: "railway", State: "ready", Tools: 7},
		{Name: "tickets", State: "login_required", Diagnostic: "login required"},
	}}
	service, path := mcpService(t, runtime)
	for _, input := range []string{
		"/mcp add railway url=https://mcp.railway.com auth=oauth",
		"/mcp add tickets url=https://tickets.example.com auth=oauth",
		"/mcp add calendar url=https://calendarmcp.googleapis.com/mcp/v1 auth=oauth",
	} {
		run(t, service, input)
	}
	if servers, _ := config.GetMCPServersConfig(path); len(servers) != 3 {
		t.Fatalf("servers=%#v", servers)
	}

	output := run(t, service, "/mcp")
	if !strings.Contains(output, "railway") || !strings.Contains(output, "7 tools") {
		t.Fatalf("output=%q", output)
	}
	if !strings.Contains(output, "not running") {
		t.Fatalf("a server added since startup was not reported as not running: %q", output)
	}
	if !strings.Contains(output, "/mcp login tickets") {
		t.Fatalf("a login_required server did not tell the owner how to fix it: %q", output)
	}
}

func TestMCPListWithNothingConfiguredShowsUsage(t *testing.T) {
	service, _ := mcpService(t, nil)
	if output := run(t, service, "/mcp"); !strings.Contains(output, "No MCP servers are configured") || !strings.Contains(output, "/mcp add") {
		t.Fatalf("output=%q", output)
	}
}

// A deployment with no config path (or no MCP at all) must answer, not panic.
func TestMCPWithoutConfigurationIsUnavailableNotFatal(t *testing.T) {
	service := New(Options{})
	if output := run(t, service, "/mcp"); !strings.Contains(output, "unavailable") {
		t.Fatalf("output=%q", output)
	}
	service, _ = mcpService(t, nil)
	if output := run(t, service, "/mcp login calendar"); !strings.Contains(output, "no MCP server is running") {
		t.Fatalf("output=%q", output)
	}
}

func TestStatusReportsMCPState(t *testing.T) {
	runtime := &fakeMCPRuntime{statuses: []MCPStatus{
		{Name: "railway", State: "ready", Tools: 7},
		{Name: "tickets", State: "login_required"},
	}}
	service, _ := mcpService(t, runtime)

	output := run(t, service, "/status")
	if !strings.Contains(output, "1/2 ready") || !strings.Contains(output, "7 tools") {
		t.Fatalf("output=%q", output)
	}
	if !strings.Contains(output, "tickets (login_required)") {
		t.Fatalf("status hid a server needing attention: %q", output)
	}
}

// An authorization server without dynamic client registration needs a client
// the owner registered by hand. The id travels in the authorization URL and is
// not a secret; the secret is named, never carried through chat.
func TestMCPAddAcceptsAPreRegisteredOAuthClient(t *testing.T) {
	service, path := mcpService(t, nil)

	output := run(t, service, "/mcp add calendar url=https://calendarmcp.googleapis.com/mcp/v1 auth=oauth client_id=eggy.apps.googleusercontent.com client_secret_env=GOOGLE_CLIENT_SECRET")
	if !strings.Contains(output, "GOOGLE_CLIENT_SECRET") || !strings.Contains(output, "environment") {
		t.Fatalf("output=%q", output)
	}
	servers, _ := config.GetMCPServersConfig(path)
	server := servers["calendar"]
	if server.OAuthClientID != "eggy.apps.googleusercontent.com" || server.OAuthClientSecretEnv != "GOOGLE_CLIENT_SECRET" {
		t.Fatalf("server=%#v", server)
	}
}
