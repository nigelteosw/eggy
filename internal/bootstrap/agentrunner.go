package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strconv"
	"time"

	"github.com/nigelteosw/eggy/internal/auth/grants"
	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/core/agent"
	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/extensions/agentrunner"
	"github.com/nigelteosw/eggy/internal/home"
	"github.com/nigelteosw/eggy/internal/ports"
)

// probeTimeout bounds the whole startup sandbox check, so a hung probe cannot
// hold startup.
const probeTimeout = 2 * time.Minute

// newAgentRunner builds the agent runner extension, or nothing at all. Disabled,
// it has no service, no tool, no worker and no workspace root.
//
// Enabled, it does three things in order, and refuses to run commands unless
// the first succeeds: lock the data directory so the coder user cannot read
// Eggy's state, then probe the sandbox. A failed probe withholds bash but keeps
// run_check, as the design says; a failed lock refuses every command, because
// the lock is part of what makes a command safe to run at all.
func newAgentRunner(cfg config.Config, secrets config.Secrets, store ports.AgentRunStore, records grants.Records, targets map[string]agent.ModelTarget, layout home.Layout, channel ports.Channel, now func() time.Time) (*agentrunner.Service, []ports.Tool, error) {
	if !cfg.AgentRunner.Enabled {
		return nil, nil, nil
	}
	dataDir := layout.Root
	logger := slog.Default()

	var runner agentrunner.CommandRunner
	sandboxOK, sandboxReason := false, ""
	sandbox, err := agentrunner.NewSandbox()
	switch {
	case err != nil:
		runner, sandboxReason = agentrunner.Refused{Reason: err}, err.Error()
	default:
		if err := sandbox.LockDataDir(dataDir); err != nil {
			runner, sandboxReason = agentrunner.Refused{Reason: err}, "data directory not locked: "+err.Error()
			break
		}
		runner = sandbox
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		defer cancel()
		port, err := listenPort(cfg.Server.Listen)
		if err != nil {
			sandboxReason = err.Error()
		} else if err := sandbox.Probe(ctx, dataDir, port); err != nil {
			sandboxReason = err.Error()
		} else {
			sandboxOK = true
		}
	}
	if !sandboxOK {
		logger.Warn("agent runner: bash is not offered", "reason", sandboxReason)
	}

	gh := agentrunner.NewGitHub(nil)
	creds, err := agentrunner.NewCredentials(records, secrets.EncryptionKey, gh)
	if err != nil {
		return nil, nil, err
	}
	service, err := agentrunner.New(agentrunner.Options{
		Store:         store,
		Credentials:   creds,
		GitHub:        gh,
		Workspaces:    agentrunner.NewWorkspaces(filepath.Join(dataDir, "agent-runs"), runner),
		Repositories:  func() []config.RepositoryConfig { return cfg.Repositories },
		Models:        targets,
		Model:         cfg.AgentRunner.Model,
		Skills:        cfg.AgentRunner.Skills,
		SandboxOK:     sandboxOK,
		SandboxReason: sandboxReason,
		Notify: func(ctx context.Context, run ports.AgentRun, text string) error {
			return deliverToOrigin(ctx, channel, run, text)
		},
		Redact:       newRedactor(io.Discard, secrets.Values()).scrub,
		SecretValues: secrets.Values,
		Now:          now,
	})
	if err != nil {
		return nil, nil, err
	}
	return service, []ports.Tool{agentrunner.NewTool(service)}, nil
}

// listenPort reads the port from server.listen, such as ":8080".
func listenPort(listen string) (int, error) {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return 0, fmt.Errorf("server.listen %q: %w", listen, err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 0, fmt.Errorf("server.listen port: %w", err)
	}
	return n, nil
}

// deliverToOrigin sends a run's message back to the chat it started from. A run
// with no recorded origin, such as one the panel started, has nowhere to send
// it and the panel shows it instead.
func deliverToOrigin(ctx context.Context, channel ports.Channel, run ports.AgentRun, text string) error {
	if run.OriginDestination == "" {
		return nil
	}
	var dest destination.Destination
	if err := json.Unmarshal([]byte(run.OriginDestination), &dest); err != nil {
		return fmt.Errorf("run origin: %w", err)
	}
	if dest.Kind == "" {
		return nil
	}
	if channel == nil {
		return errors.New("no channel to deliver the run's message")
	}
	return channel.Deliver(destination.With(ctx, dest), text)
}

// codeAdapter presents the agent runner to /code. It fills in the origin from
// the verified ingress on the context, so a caller can never choose where a
// run reports back.
type codeAdapter struct {
	service *agentrunner.Service
}

func (c codeAdapter) StartRun(ctx context.Context, repository, task string) (ports.AgentRun, error) {
	origin := destination.FromContext(ctx)
	raw, err := json.Marshal(origin)
	if err != nil {
		return ports.AgentRun{}, err
	}
	return c.service.Start(ctx, agentrunner.StartRequest{
		Repository: repository, Task: task, Channel: origin.Kind, Destination: string(raw),
	})
}

// StartPanelRun starts a run from the web panel. It has no chat origin, and it
// must not inherit one from the context, because an unset context reads as the
// Telegram default. Its result is shown in the panel and never sent to a chat.
func (c codeAdapter) StartPanelRun(ctx context.Context, repository, task string) (ports.AgentRun, error) {
	return c.service.Start(ctx, agentrunner.StartRequest{Repository: repository, Task: task, Channel: "panel"})
}

func (c codeAdapter) Runs(ctx context.Context) ([]ports.AgentRun, error) {
	return c.service.List(ctx)
}

func (c codeAdapter) CancelRun(ctx context.Context, id string) (ports.AgentRun, error) {
	return c.service.Cancel(ctx, id)
}

func (c codeAdapter) AnswerRun(ctx context.Context, id, text string) (ports.AgentRun, error) {
	return c.service.Answer(ctx, id, text)
}

func (c codeAdapter) Repositories(ctx context.Context) ([]commands.CodeRepository, error) {
	repos, err := c.service.Repositories(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]commands.CodeRepository, 0, len(repos))
	for _, repo := range repos {
		out = append(out, commands.CodeRepository{Name: repo.Name, Enabled: repo.Enabled, PushAccess: repo.PushAccess})
	}
	return out, nil
}

func (c codeAdapter) SetRepositoryEnabled(ctx context.Context, name string, enabled bool) error {
	return c.service.SetRepositoryEnabled(ctx, name, enabled)
}

// codeRunnerFor returns the /code adapter, or nil when the extension is off, so
// /code reports that instead of calling a service that does not exist.
func codeRunnerFor(service *agentrunner.Service) commands.CodeRunner {
	if service == nil {
		return nil
	}
	return codeAdapter{service: service}
}
