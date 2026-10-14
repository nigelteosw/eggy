package agentrunner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/core/agent"
	"github.com/nigelteosw/eggy/internal/ports"
)

// Constants the design fixes. They are not config: a run's cost ceiling and
// its authority window are part of what the owner agreed to.
const (
	mandateTTL        = 24 * time.Hour
	maxBriefBytes     = 8 << 10
	maxRunTokens      = 2_000_000
	maxRunActive      = 30 * time.Minute
	maxOutputBytes    = 2 << 20
	maxCodingSteps    = 200
	maxAskQuestionLen = 1000
	listLimit         = 50
	sweepEvery        = 30 * time.Second
	// retainedFailure is how long a failed, cancelled or interrupted run keeps
	// its workspace, so the owner can inspect what the agent did.
	retainedFailure = 7 * 24 * time.Hour
	// maxWorkspaceBytes is the disk all run directories together may use.
	// A run is refused at admission when the directories already exceed it.
	maxWorkspaceBytes = 10 << 30
)

// Options is everything the service needs. Bootstrap builds it; nothing here
// reads config or the environment itself.
type Options struct {
	Store        ports.AgentRunStore
	Credentials  *Credentials
	Workspaces   *Workspaces
	GitHub       *GitHub
	Repositories func() []config.RepositoryConfig // live view, so a removed repository stops a queued run
	Models       map[string]agent.ModelTarget
	Model        string // the alias coding turns use; never the account's /model
	Skills       map[string]bool
	// SandboxOK and SandboxReason come from the startup probe. bash is offered
	// only when SandboxOK is true.
	SandboxOK     bool
	SandboxReason string
	// Notify delivers text to the channel a run started from.
	Notify func(ctx context.Context, run ports.AgentRun, text string) error
	// Redact scrubs live secrets from anything persisted or delivered.
	Redact func(string) string
	// SecretValues lists the live secrets a change must not contain. It is
	// Secrets.Values() from config, the only list of live credentials.
	SecretValues func() []string
	// GitURL is the git remote a repository is cloned and pushed through. It is
	// the repository's clone_url; tests point it at a local remote.
	GitURL func(config.RepositoryConfig) string
	Now    func() time.Time
	NewID  func() string
}

// Service owns run state. It is the one write path for runs: the tool, the
// panel and the Telegram commands are all thin callers of it.
type Service struct {
	opts    Options
	wake    chan struct{}
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// New validates the options and returns the service.
func New(o Options) (*Service, error) {
	switch {
	case o.Store == nil || o.Credentials == nil || o.Workspaces == nil || o.Repositories == nil:
		return nil, errors.New("agent runner: store, credentials, workspaces and repositories are required")
	case o.Models[o.Model].Model == nil:
		return nil, fmt.Errorf("agent runner: model alias %q is not configured", o.Model)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.NewID == nil {
		o.NewID = newRunID
	}
	if o.SecretValues == nil {
		o.SecretValues = func() []string { return nil }
	}
	if o.Redact == nil {
		// Without a configured redactor, scrub the live secrets anyway: a missing
		// wiring must fail safe, not let a secret into a stored or sent text.
		secrets := o.SecretValues
		o.Redact = func(text string) string {
			values := slices.Clone(secrets())
			slices.SortFunc(values, func(a, b string) int { return len(b) - len(a) })
			for _, value := range values {
				if len(value) >= minSecretLength {
					text = strings.ReplaceAll(text, value, "[redacted]")
				}
			}
			return text
		}
	}
	if o.GitURL == nil {
		o.GitURL = func(repo config.RepositoryConfig) string { return repo.CloneURL }
	}
	return &Service{opts: o, wake: make(chan struct{}, 1), cancels: map[string]context.CancelFunc{}}, nil
}

// newRunID is 16 random bytes, hex encoded, matching Workspaces.Dir's rule.
func newRunID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("agent runner: no randomness: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// StartRequest is one run request from a trusted ingress: the verified web
// session or a verified private Telegram sender. Nothing else may build one.
type StartRequest struct {
	Repository  string
	Task        string
	Channel     string
	Destination string
}

// RepoStatus is one allowed repository as an account sees it.
type RepoStatus struct {
	Name       string     `json:"name"`
	Enabled    bool       `json:"enabled"`
	PushAccess bool       `json:"push_access"`
	Checks     [][]string `json:"checks"`
}

func (s *Service) repository(name string) (config.RepositoryConfig, bool) {
	for _, repo := range s.opts.Repositories() {
		if repo.Name == name {
			return repo, true
		}
	}
	return config.RepositoryConfig{}, false
}

// Start admits a run and records its mandate. It refuses, with a reason the
// owner can act on, a repository that is not allowed, not enabled for the
// account, or not pushable with the account's token, and a second queued run.
func (s *Service) Start(ctx context.Context, req StartRequest) (ports.AgentRun, error) {
	repo, ok := s.repository(req.Repository)
	if !ok {
		return ports.AgentRun{}, fmt.Errorf("unknown repository %q", req.Repository)
	}
	enabled, err := s.opts.Store.EnabledRepositories(ctx)
	if err != nil {
		return ports.AgentRun{}, err
	}
	if !slices.Contains(enabled, repo.Name) {
		return ports.AgentRun{}, fmt.Errorf("repository %q is not enabled for your account; enable it first", repo.Name)
	}
	status, err := s.accessStatus(ctx)
	if err != nil {
		return ports.AgentRun{}, err
	}
	if !status.Connected {
		return ports.AgentRun{}, errors.New("connect a GitHub token in settings first")
	}
	if !status.Repositories[repo.Name] {
		return ports.AgentRun{}, fmt.Errorf("your GitHub token has no push access to %q", repo.Name)
	}
	task := strings.TrimSpace(req.Task)
	if task == "" || len(task) > maxBriefBytes {
		return ports.AgentRun{}, fmt.Errorf("the task must be 1 to %d bytes", maxBriefBytes)
	}
	recent, err := s.opts.Store.ListAgentRuns(ctx, listLimit)
	if err != nil {
		return ports.AgentRun{}, err
	}
	for _, run := range recent {
		if run.State == ports.AgentRunQueued {
			return ports.AgentRun{}, errors.New("you already have a queued run; wait for it to start or cancel it")
		}
	}
	used, err := s.opts.Workspaces.Usage()
	if err != nil {
		return ports.AgentRun{}, err
	}
	if used >= maxWorkspaceBytes {
		return ports.AgentRun{}, errors.New("the agent runner's disk is full; wait for old runs to be removed")
	}
	now := s.opts.Now().UTC()
	digest := sha256.Sum256([]byte(task))
	base := repo.BaseBranch
	if base == "" {
		base = "main"
	}
	id := s.opts.NewID()
	run := ports.AgentRun{
		ID: id, Repository: repo.Name, Brief: task, BriefDigest: hex.EncodeToString(digest[:]),
		BaseBranch: base, Branch: "eggy/" + id, State: ports.AgentRunQueued,
		PATGeneration: status.Generation, MandateExpires: now.Add(mandateTTL),
		OriginChannel: req.Channel, OriginDestination: req.Destination,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.opts.Store.CreateAgentRun(ctx, run); err != nil {
		return ports.AgentRun{}, err
	}
	s.signal()
	return s.opts.Store.GetAgentRun(ctx, id)
}

// Get, List: reads of the account's own runs.
func (s *Service) Get(ctx context.Context, id string) (ports.AgentRun, error) {
	return s.opts.Store.GetAgentRun(ctx, id)
}

// Steps returns what happened in one of the account's runs, in order. The run
// is read first, so another account's run reads as not found, not as empty.
func (s *Service) Steps(ctx context.Context, id string) ([]ports.AgentRunEvent, error) {
	if _, err := s.opts.Store.GetAgentRun(ctx, id); err != nil {
		return nil, err
	}
	return s.opts.Store.ListAgentRunEvents(ctx, id)
}

func (s *Service) List(ctx context.Context) ([]ports.AgentRun, error) {
	return s.opts.Store.ListAgentRuns(ctx, listLimit)
}

// Cancel stops a run. A queued or waiting run is cancelled at once. A running
// one is cancelled through its context and the worker records the outcome.
// Delete removes a finished run for its owner: the record and its steps first,
// then the workspace. If the workspace cannot be removed, what is left is an
// orphan directory the sweep removes, never a record pointing at nothing.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.opts.Store.DeleteAgentRun(ctx, id); err != nil {
		return err
	}
	return s.opts.Workspaces.Remove(id)
}

func (s *Service) Cancel(ctx context.Context, id string) (ports.AgentRun, error) {
	run, err := s.opts.Store.GetAgentRun(ctx, id)
	if err != nil {
		return ports.AgentRun{}, err
	}
	switch run.State {
	case ports.AgentRunRunning, ports.AgentRunPublishing:
		s.mu.Lock()
		cancel := s.cancels[id]
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return run, nil
	case ports.AgentRunQueued, ports.AgentRunWaitingForUser:
		run.State = ports.AgentRunCancelled
		run.Failure = "cancelled by owner"
		updated, err := s.opts.Store.UpdateAgentRun(ctx, run)
		if err == nil {
			s.note(ctx, run.ID, "cancelled", "cancelled by owner")
		}
		return updated, err
	default:
		return run, fmt.Errorf("run is already %s", run.State)
	}
}

// Answer resumes a run that asked the owner a question. A run not waiting, or
// whose mandate has lapsed, refuses the answer.
func (s *Service) Answer(ctx context.Context, id, text string) (ports.AgentRun, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return ports.AgentRun{}, errors.New("the answer is empty")
	}
	run, err := s.opts.Store.GetAgentRun(ctx, id)
	if err != nil {
		return ports.AgentRun{}, err
	}
	if run.State != ports.AgentRunWaitingForUser {
		return ports.AgentRun{}, fmt.Errorf("run is %s, not waiting for an answer", run.State)
	}
	if !s.opts.Now().Before(run.MandateExpires) {
		return ports.AgentRun{}, errors.New("the run's mandate has expired")
	}
	run.Answer = text
	run.State = ports.AgentRunQueued
	updated, err := s.opts.Store.UpdateAgentRun(ctx, run)
	if err != nil {
		return ports.AgentRun{}, err
	}
	s.signal()
	return updated, nil
}

// Repositories lists the allowed repositories as this account sees them.
func (s *Service) Repositories(ctx context.Context) ([]RepoStatus, error) {
	enabled, err := s.opts.Store.EnabledRepositories(ctx)
	if err != nil {
		return nil, err
	}
	status, err := s.accessStatus(ctx)
	if err != nil {
		// A GitHub outage must not hide the repository list: show what is known.
		if status, err = s.opts.Credentials.Status(ctx); err != nil {
			return nil, err
		}
	}
	var out []RepoStatus
	for _, repo := range s.opts.Repositories() {
		out = append(out, RepoStatus{
			Name:       repo.Name,
			Enabled:    slices.Contains(enabled, repo.Name),
			PushAccess: status.Repositories[repo.Name],
			Checks:     repo.Checks,
		})
	}
	return out, nil
}

// SetRepositoryEnabled turns cloning on or off for one allowed repository. A
// disable cancels this account's active runs on it, so nothing keeps acting on
// a repository the owner just turned off.
func (s *Service) SetRepositoryEnabled(ctx context.Context, name string, enabled bool) error {
	if _, ok := s.repository(name); !ok {
		return fmt.Errorf("unknown repository %q", name)
	}
	if !enabled {
		runs, err := s.opts.Store.ListAgentRuns(ctx, listLimit)
		if err != nil {
			return err
		}
		for _, run := range runs {
			if run.Repository != name {
				continue
			}
			switch run.State {
			case ports.AgentRunQueued, ports.AgentRunWaitingForUser, ports.AgentRunRunning:
				if _, err := s.Cancel(ctx, run.ID); err != nil {
					return err
				}
			}
		}
	}
	return s.opts.Store.SetRepositoryEnabled(ctx, name, enabled)
}

// SandboxStatus reports the startup probe's verdict, for the page to show.
func (s *Service) SandboxStatus() (bool, string) {
	return s.opts.SandboxOK, s.opts.SandboxReason
}

func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// PAT reports the account's GitHub token without its value.
func (s *Service) PAT(ctx context.Context) (PATStatus, error) {
	return s.opts.Credentials.Status(ctx)
}

// SetPAT verifies and stores the account's token. The returned status is what
// GitHub confirmed, never the token itself.
func (s *Service) SetPAT(ctx context.Context, token string) (PATStatus, error) {
	return s.opts.Credentials.Set(ctx, token, s.opts.Repositories())
}

// DisconnectPAT removes the account's token. Its live runs are cancelled first,
// because a run that could still push would be acting on authority the owner
// just withdrew.
func (s *Service) DisconnectPAT(ctx context.Context) error {
	runs, err := s.opts.Store.ListAgentRuns(ctx, listLimit)
	if err != nil {
		return err
	}
	for _, run := range runs {
		switch run.State {
		case ports.AgentRunQueued, ports.AgentRunWaitingForUser, ports.AgentRunRunning:
			if _, err := s.Cancel(ctx, run.ID); err != nil {
				return err
			}
		}
	}
	return s.opts.Credentials.Disconnect(ctx)
}

// accessStatus is the account's token status, with access to any configured
// repository the token has never been checked against filled in using the
// stored token. A page load or a run start therefore makes no GitHub call
// unless a repository was added since the token was last checked.
func (s *Service) accessStatus(ctx context.Context) (PATStatus, error) {
	status, err := s.opts.Credentials.Status(ctx)
	if err != nil || !status.Connected {
		return status, err
	}
	repos := s.opts.Repositories()
	for _, repo := range repos {
		if _, checked := status.Repositories[repo.Name]; !checked {
			return s.opts.Credentials.Refresh(ctx, repos)
		}
	}
	return status, nil
}
