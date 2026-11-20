package bootstrap

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"path/filepath"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/extensions/agentrunner"
	"github.com/nigelteosw/eggy/internal/extensions/terminal"
	"github.com/nigelteosw/eggy/internal/home"
	"github.com/nigelteosw/eggy/internal/panel"
	"github.com/nigelteosw/eggy/internal/sandbox"
)

// newTerminal builds the terminal extension, or nothing. Disabled, or without
// a passing sandbox probe, there is no service, no route and no herdr-core:
// a terminal is a shell, and a shell is only offered where the confinement has
// been proved, as bash is for the agent runner.
func newTerminal(cfg config.Config, secrets config.Secrets, layout home.Layout, box sandboxState, runner *agentrunner.Service) (*terminal.Service, error) {
	if !cfg.Terminal.Enabled {
		return nil, nil
	}
	if !box.terminalOK {
		slog.Warn("terminal: not offered, the sandbox probe did not pass", "reason", box.terminalReason)
		return nil, nil
	}
	dirName, err := accountDirName(secrets.EncryptionKey)
	if err != nil {
		return nil, err
	}
	opts := terminal.Options{
		Root:     filepath.Join(layout.Root, sandbox.WorkspaceDir),
		DirName:  dirName,
		Prepare:  box.sandbox.PrepareAccountDir,
		Launcher: terminal.SandboxLauncher{Start: box.sandbox.Terminal},
	}
	if runner != nil {
		// The agent runner's repositories are spaces too, cloned from the
		// mirrors it keeps.
		opts.Repos, opts.Mirror = runner.RepositoryNames, runner.MirrorFor
	}
	svc, err := terminal.New(opts)
	if err == nil {
		slog.Info("terminal: ready")
	}
	return svc, err
}

// accountDirName names each account's workspace directory, shared by its
// terminals and its agent runs, with a keyed hash
// of its id. Each account's terminals already run as their own uid, which is
// what keeps accounts apart; an unguessable name is defence in depth, and keeps
// account ids out of the paths every program in the container can see.
func accountDirName(key string) (func(string) string, error) {
	if key == "" {
		return nil, errors.New("workspace: needs the encryption key to name account directories")
	}
	return func(account string) string {
		mac := hmac.New(sha256.New, []byte(key))
		// The label predates the shared workspace and stays, so existing
		// directories keep their names.
		mac.Write([]byte("eggy terminal directory\x00" + account))
		return hex.EncodeToString(mac.Sum(nil))[:32]
	}, nil
}

// panelTerminal presents the service to the panel, translating its types.
type panelTerminal struct {
	svc *terminal.Service
}

func panelTerminalInfo(t terminal.Terminal) panel.TerminalInfo {
	return panel.TerminalInfo{
		Address: t.Address, Space: t.Space, Name: t.Name, Title: t.Title, Live: t.Live,
		Status: t.Status, Message: t.Message, App: t.App, Changed: t.Changed, ExitCode: t.ExitCode,
	}
}

func panelTerminalErr(err error) error {
	if errors.Is(err, terminal.ErrNotFound) {
		return panel.ErrTerminalNotFound
	}
	return err
}

func (p panelTerminal) Commands() []string { return terminal.Commands }

func (p panelTerminal) Spaces(ctx context.Context) ([]panel.TerminalSpace, error) {
	spaces, err := p.svc.Spaces(ctx)
	if err != nil {
		return nil, panelTerminalErr(err)
	}
	out := make([]panel.TerminalSpace, 0, len(spaces))
	for _, s := range spaces {
		ts := make([]panel.TerminalInfo, 0, len(s.Terminals))
		for _, t := range s.Terminals {
			ts = append(ts, panelTerminalInfo(t))
		}
		out = append(out, panel.TerminalSpace{Name: s.Name, Repo: s.Repo, Preparing: s.Preparing, Terminals: ts})
	}
	return out, nil
}

func (p panelTerminal) CreateSpace(ctx context.Context, name string) error {
	return panelTerminalErr(p.svc.CreateSpace(ctx, name))
}

func (p panelTerminal) DeleteSpace(ctx context.Context, name string) error {
	return panelTerminalErr(p.svc.DeleteSpace(ctx, name))
}

func (p panelTerminal) UpdateRepo(ctx context.Context, name string) error {
	return panelTerminalErr(p.svc.UpdateRepo(ctx, name))
}

func (p panelTerminal) Start(ctx context.Context, space, command string, cols, rows uint16) (panel.TerminalInfo, error) {
	t, err := p.svc.Start(ctx, space, command, cols, rows)
	return panelTerminalInfo(t), panelTerminalErr(err)
}

func (p panelTerminal) Kill(ctx context.Context, address string) error {
	return panelTerminalErr(p.svc.Kill(ctx, address))
}

func (p panelTerminal) Attach(ctx context.Context, address string, cols, rows uint16) (panel.TerminalStream, error) {
	s, err := p.svc.Attach(ctx, address, cols, rows)
	if err != nil {
		return nil, panelTerminalErr(err)
	}
	return s, nil
}

func (p panelTerminal) NeedsYou(ctx context.Context) ([]panel.TerminalInfo, error) {
	ts, err := p.svc.NeedsYou(ctx)
	if err != nil {
		return nil, panelTerminalErr(err)
	}
	out := make([]panel.TerminalInfo, 0, len(ts))
	for _, t := range ts {
		out = append(out, panelTerminalInfo(t))
	}
	return out, nil
}
