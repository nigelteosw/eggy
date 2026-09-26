package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// TransportHTTP reaches a hosted server over streamable HTTP.
	TransportHTTP = "streamable-http"
	// TransportStdio spawns the server as a local subprocess and speaks to it
	// over stdin and stdout, which is how most of the MCP ecosystem ships.
	TransportStdio = "stdio"
)

// baseEnvironment is forwarded to every stdio child regardless of its
// allowlist. Without PATH the command cannot be located at all, and without
// HOME an `npx`-launched server cannot find its package cache, so withholding
// these two would mean no stdio server ever starts.
var baseEnvironment = []string{"HOME", "PATH"}

type clientSession interface {
	ListTools(context.Context, *sdk.ListToolsParams) (*sdk.ListToolsResult, error)
	CallTool(context.Context, *sdk.CallToolParams) (*sdk.CallToolResult, error)
	Close() error
}

type connector func(context.Context, ServerConfig, *http.Client, auth.OAuthHandler, *sdk.ClientOptions) (clientSession, error)

func connectSDK(ctx context.Context, cfg ServerConfig, httpClient *http.Client, handler auth.OAuthHandler, opts *sdk.ClientOptions) (clientSession, error) {
	client := sdk.NewClient(&sdk.Implementation{Name: "eggy", Version: "1"}, opts)
	switch cfg.Transport {
	case TransportStdio:
		return connectStdio(ctx, client, cfg)
	case TransportHTTP, "":
		transport := &sdk.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: httpClient, OAuthHandler: handler}
		return client.Connect(ctx, transport, nil)
	default:
		return nil, fmt.Errorf("unsupported MCP transport %q", cfg.Transport)
	}
}

// connectStdio spawns the server as a child process. Two properties are
// deliberate. The child's environment is built from an explicit allowlist
// rather than inherited, so credentials Eggy holds for other capabilities
// never reach it. And the child leads its own process group, so closing the
// session can kill the whole group: an `npx` server that spawns node would
// otherwise leave the node behind on every reconnect.
//
// What this does not do is isolate the child. It runs as the same user with
// the same filesystem access as Eggy itself, which is the same trusted-
// repository assumption the terminal tool already makes. Container isolation
// is tracked as its own roadmap item and would cover both.
func connectStdio(ctx context.Context, client *sdk.Client, cfg ServerConfig) (clientSession, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, errors.New("stdio MCP server has no command")
	}
	command := exec.Command(cfg.Command, cfg.Args...)
	command.Env = childEnvironment(cfg.EnvAllowlist, os.Getenv)
	configureProcessGroup(command)
	session, err := client.Connect(ctx, &sdk.CommandTransport{Command: command}, nil)
	if err != nil {
		// Connect may have started the process before failing the handshake.
		terminateProcessGroup(command)
		return nil, err
	}
	return &commandSession{clientSession: session, command: command}, nil
}

// childEnvironment resolves the allowlisted variables from Eggy's own
// environment. An allowlisted variable that is unset is simply absent rather
// than passed as empty, so a server sees the same thing it would see if it
// had been launched by hand without it.
func childEnvironment(allowlist []string, getenv func(string) string) []string {
	names := append(append([]string(nil), baseEnvironment...), allowlist...)
	slices.Sort(names)
	names = slices.Compact(names)
	environment := make([]string, 0, len(names))
	for _, name := range names {
		if value := getenv(name); value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}

// commandSession adds process-group cleanup to the SDK's session. The SDK
// closes stdin and signals the child it started; the group kill is what
// reaches anything that child spawned in turn.
type commandSession struct {
	clientSession
	command *exec.Cmd
}

func (s *commandSession) Close() error {
	err := s.clientSession.Close()
	terminateProcessGroup(s.command)
	return err
}
