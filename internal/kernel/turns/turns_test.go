package turns

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

// These are the tests that could not exist while this code lived in
// internal/bootstrap: which turns are unprompted, and what each kind of turn
// may reach, are now properties of a kernel package with a kernel test.

type fakeLoop struct {
	ctx     context.Context
	options agent.RunOptions
	reply   string
}

func (l *fakeLoop) Run(ctx context.Context, _, _, _ string, _ []ports.Message, options agent.RunOptions) (agent.RunResult, error) {
	l.ctx, l.options = ctx, options
	return agent.RunResult{Message: ports.Message{Role: ports.RoleAssistant, Content: l.reply}}, nil
}

func (l *fakeLoop) ToolNames(options agent.RunOptions) []string {
	names := make([]string, 0, len(options.AllowedTools))
	for name := range options.AllowedTools {
		names = append(names, name)
	}
	return names
}

type fakeRegistry struct{ steered bool }

func (r *fakeRegistry) Begin(ctx context.Context, _ bool) (context.Context, func()) {
	return ctx, func() {}
}
func (r *fakeRegistry) Steer(context.Context, string) bool      { return r.steered }
func (r *fakeRegistry) Pending(context.Context) []ports.Message { return nil }
func (r *fakeRegistry) Active() bool                            { return false }

type fakeConversation struct{ recorded []ports.Message }

func (c *fakeConversation) Record(_ context.Context, _ string, message ports.Message, _ string) error {
	c.recorded = append(c.recorded, message)
	return nil
}
func (c *fakeConversation) RecentMessages(context.Context, string) ([]ports.Message, error) {
	return nil, nil
}

type fakeContextStore struct{}

func (fakeContextStore) Load(context.Context) (ports.AgentContext, error) {
	return ports.AgentContext{}, nil
}
func (fakeContextStore) AddEntry(context.Context, ports.ContextDocument, string) error { return nil }
func (fakeContextStore) ReplaceEntry(context.Context, ports.ContextDocument, string, string) error {
	return nil
}
func (fakeContextStore) RemoveEntry(context.Context, ports.ContextDocument, string) error { return nil }

type fakeStore struct{}

func (fakeStore) Load(context.Context) (ports.State, error) { return ports.State{}, nil }
func (fakeStore) Update(context.Context, uint64, func(*ports.State) error) (ports.State, error) {
	return ports.State{}, nil
}

type fakeRuntime struct{}

func (fakeRuntime) SelectedModel(context.Context) (string, error)   { return "alias", nil }
func (fakeRuntime) ReasoningEffort(context.Context) (string, error) { return "medium", nil }
func (fakeRuntime) ShowThinking(context.Context) (bool, error)      { return false, nil }
func (fakeRuntime) RecordUsage(context.Context, string, ports.ModelUsage) error {
	return nil
}

type fakeSkills struct{}

func (fakeSkills) Enabled(context.Context) ([]ports.SkillSummary, error) { return nil, nil }

type fakeChannel struct{ delivered []string }

func (c *fakeChannel) Deliver(_ context.Context, text string) error {
	c.delivered = append(c.delivered, text)
	return nil
}
func (c *fakeChannel) DeliverApproval(context.Context, approvals.Approval) error { return nil }

func newTestService(loop *fakeLoop, channel *fakeChannel) *Service {
	return New(Options{
		Registry: &fakeRegistry{}, Conversation: &fakeConversation{}, Context: fakeContextStore{},
		Store: fakeStore{}, Runtime: fakeRuntime{}, Skills: fakeSkills{}, Loop: loop,
		Channel: channel, Heartbeat: allowSending{}, Now: func() time.Time { return time.Unix(0, 0).UTC() },
	})
}

type allowSending struct{}

func (allowSending) CanSend(ports.State, time.Time) bool { return true }
func (allowSending) Record(context.Context, ports.StateStore, time.Time) error {
	return nil
}

// The heart of the narrowed invariant: a turn nobody asked for is marked as
// such, and an owner's message is not. Everything the kernel does to confine
// an unprompted turn -- draft pull requests, the base-branch refusal, the
// owner's-change refusal -- keys off this mark, so if the wrong turns carry
// it the rest is decoration.
func TestScheduledAndHeartbeatTurnsAreMarkedUnpromptedAndOwnerMessagesAreNot(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		call       func(*Service) error
		unprompted bool
	}{
		{"owner message", func(s *Service) error { return s.OwnerMessage(context.Background(), "hi", "telegram") }, false},
		{"checks turn", func(s *Service) error { return s.ChecksTurn(context.Background(), "fix it") }, false},
		{"scheduled turn", func(s *Service) error { return s.ScheduledTurn(context.Background(), "check things") }, true},
		{"heartbeat", func(s *Service) error { return s.Heartbeat(context.Background()) }, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			loop := &fakeLoop{reply: services.HeartbeatNoReportSentinel}
			if err := testCase.call(newTestService(loop, &fakeChannel{})); err != nil {
				t.Fatal(err)
			}
			if loop.ctx == nil {
				t.Fatal("the loop was never reached")
			}
			if got := services.IsUnpromptedTurn(loop.ctx); got != testCase.unprompted {
				t.Fatalf("IsUnpromptedTurn=%v, want %v", got, testCase.unprompted)
			}
		})
	}
}

// An unprompted turn reaches the propose path or nothing, never the whole
// tool set an owner-prompted turn carries.
func TestUnpromptedTurnsRunWithARestrictedAllowlist(t *testing.T) {
	t.Run("scheduled turns may propose", func(t *testing.T) {
		loop := &fakeLoop{}
		if err := newTestService(loop, &fakeChannel{}).ScheduledTurn(context.Background(), "improve something"); err != nil {
			t.Fatal(err)
		}
		if loop.options.AllowedTools == nil {
			t.Fatal("a scheduled turn ran with the unrestricted tool set")
		}
		for _, tool := range []string{"workspace_edit", "propose_change", "patch", "write_file"} {
			if !loop.options.AllowedTools[tool] {
				t.Fatalf("scheduled turn cannot reach %q", tool)
			}
		}
	})

	t.Run("heartbeats may not write to a repository", func(t *testing.T) {
		loop := &fakeLoop{reply: services.HeartbeatNoReportSentinel}
		if err := newTestService(loop, &fakeChannel{}).Heartbeat(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !loop.options.AllowedTools["memory"] {
			t.Fatal("a heartbeat cannot curate durable context")
		}
		for _, tool := range []string{"workspace_edit", "propose_change", "patch", "write_file"} {
			if loop.options.AllowedTools[tool] {
				t.Fatalf("heartbeat reached repository write tool %q", tool)
			}
		}
	})

	t.Run("owner messages carry everything", func(t *testing.T) {
		loop := &fakeLoop{}
		if err := newTestService(loop, &fakeChannel{}).OwnerMessage(context.Background(), "hi", "telegram"); err != nil {
			t.Fatal(err)
		}
		if loop.options.AllowedTools != nil {
			t.Fatalf("an owner turn was restricted to %v", loop.options.AllowedTools)
		}
	})
}

// No allowlist names an MCP tool, so an unprompted turn reaches none however
// many servers are configured. The name shape is guaranteed by
// normalizeToolName, so the prefix test is the whole class.
func TestNoUnpromptedAllowlistNamesAnMCPTool(t *testing.T) {
	for name, options := range map[string]agent.RunOptions{
		"read-only": ReadOnlyTools(), "propose-only": ProposeOnlyTools(), "heartbeat": HeartbeatTools(),
	} {
		for tool := range options.AllowedTools {
			if strings.Contains(tool, "__") {
				t.Fatalf("%s allowlist names MCP tool %q", name, tool)
			}
		}
	}
}

func TestCapabilityManifestSeparatesRepositoryAndShippingReadiness(t *testing.T) {
	service := New(Options{Manifest: agent.CapabilityManifest{RepositoryCommitReady: true}})
	withoutRepository := service.capabilityManifest(ports.State{}, "deepseek-pro", nil)
	if withoutRepository.RepositoryCommitReady || withoutRepository.RepositoryPushReady || withoutRepository.PullRequestReady {
		t.Fatalf("without repository=%#v", withoutRepository)
	}
	withRepository := service.capabilityManifest(ports.State{Repositories: map[string]ports.Repository{"eggy": {Name: "eggy"}}}, "deepseek-pro", nil)
	if !withRepository.RepositoryCommitReady || withRepository.RepositoryPushReady || withRepository.PullRequestReady {
		t.Fatalf("with repository=%#v", withRepository)
	}
}

func TestCapabilityManifestConvertsEnabledSkillsToDescriptors(t *testing.T) {
	manifest := New(Options{}).capabilityManifest(ports.State{}, "deepseek-pro", []ports.SkillSummary{
		{Name: "fix-flaky-tests", Description: "Use when a test intermittently fails"},
	})
	if len(manifest.Skills) != 1 || manifest.Skills[0].Name != "fix-flaky-tests" || manifest.Skills[0].Description != "Use when a test intermittently fails" {
		t.Fatalf("skills=%#v", manifest.Skills)
	}
}

func TestTruncateThreadTitleBoundsLongFirstMessages(t *testing.T) {
	if got := truncateThreadTitle("  short  "); got != "short" {
		t.Fatalf("got %q", got)
	}
	long := ""
	for range 100 {
		long += "a"
	}
	got := truncateThreadTitle(long)
	if len([]rune(got)) != 61 || got[len(got)-len("…"):] != "…" {
		t.Fatalf("got %q (%d runes)", got, len([]rune(got)))
	}
}
