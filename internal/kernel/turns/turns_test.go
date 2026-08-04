package turns

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// These are the tests that could not exist while this code lived in
// internal/bootstrap: which turns are unprompted, and what each kind of turn
// may reach, are now properties of a kernel package with a kernel test.

type fakeLoop struct {
	ctx     context.Context
	options agent.RunOptions
	history []ports.Message
	reply   string
}

func (l *fakeLoop) Run(ctx context.Context, _, _, _ string, history []ports.Message, options agent.RunOptions) (agent.RunResult, error) {
	l.ctx, l.options, l.history = ctx, options, history
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
		Channel: channel, Now: func() time.Time { return time.Unix(0, 0).UTC() },
	})
}

// Every unprompted turn is read-only. Repository mutation and shell tools are
// absent from Eggy entirely, not merely hidden from one kind of turn.
func TestUnpromptedTurnsRunWithARestrictedAllowlist(t *testing.T) {
	t.Run("scheduled turns are read-only", func(t *testing.T) {
		loop := &fakeLoop{}
		if err := newTestService(loop, &fakeChannel{}).ScheduledTurn(context.Background(), "improve something"); err != nil {
			t.Fatal(err)
		}
		if loop.options.AllowedTools == nil {
			t.Fatal("a scheduled turn ran with the unrestricted tool set")
		}
		for _, tool := range []string{"terminal", "workspace_edit", "propose_change", "patch", "write_file"} {
			if loop.options.AllowedTools[tool] {
				t.Fatalf("scheduled turn reached repository mutation or shell tool %q", tool)
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
	for name, options := range map[string]agent.RunOptions{"read-only": ReadOnlyTools()} {
		for tool := range options.AllowedTools {
			if strings.Contains(tool, "__") {
				t.Fatalf("%s allowlist names MCP tool %q", name, tool)
			}
		}
	}
}

// chattyConversation has a recent window, so a turn that carries no ambient
// history is proved by the absence of these rather than by an empty store.
type chattyConversation struct{ fakeConversation }

func (chattyConversation) RecentMessages(context.Context, string) ([]ports.Message, error) {
	return []ports.Message{{Role: ports.RoleUser, Content: "earlier owner chat"}}, nil
}

// The whole feature: a heartbeat that has nothing to say says nothing. A
// recurring scheduled turn cannot do this, which is why the heartbeat is a
// separate mechanism rather than a cron entry.
func TestHeartbeatTurnDeliversOnlyWhenItHasSomethingToSay(t *testing.T) {
	for name, tt := range map[string]struct {
		reply string
		want  []string
	}{
		"bare sentinel":       {reply: agent.HeartbeatSentinel, want: nil},
		"trailing pleasantry": {reply: agent.HeartbeatSentinel + " — all quiet!", want: nil},
		"leading sentinel":    {reply: "HEARTBEAT_OK\n\nNothing to report.", want: nil},
		"empty reply":         {reply: "   ", want: nil},
		"a real finding":      {reply: "The deploy has been failing for an hour.", want: []string{"The deploy has been failing for an hour."}},
		// The leniency only applies once the model has declared nothing to
		// report, so a genuine short alert is never swallowed.
		"short alert without the sentinel": {reply: "Disk is full.", want: []string{"Disk is full."}},
	} {
		t.Run(name, func(t *testing.T) {
			channel := &fakeChannel{}
			if err := newTestService(&fakeLoop{reply: tt.reply}, channel).HeartbeatTurn(context.Background(), "check in"); err != nil {
				t.Fatal(err)
			}
			if len(channel.delivered) != len(tt.want) {
				t.Fatalf("delivered=%v, want %v", channel.delivered, tt.want)
			}
			for i, want := range tt.want {
				if channel.delivered[i] != want {
					t.Fatalf("delivered[%d]=%q, want %q", i, channel.delivered[i], want)
				}
			}
		})
	}
}

// A heartbeat inherits ScheduledTurn's isolation by construction: the same
// read-only allowlist, and no ambient conversation history, so an owner's
// earlier chat cannot steer a turn they are not present for.
func TestHeartbeatTurnIsIsolatedLikeAScheduledTurn(t *testing.T) {
	loop := &fakeLoop{reply: "a finding"}
	service := New(Options{
		Registry: &fakeRegistry{}, Conversation: &chattyConversation{}, Context: fakeContextStore{},
		Store: fakeStore{}, Runtime: fakeRuntime{}, Skills: fakeSkills{}, Loop: loop,
		Channel: &fakeChannel{}, Now: func() time.Time { return time.Unix(0, 0).UTC() },
	})
	if err := service.HeartbeatTurn(context.Background(), "check in"); err != nil {
		t.Fatal(err)
	}
	if loop.options.AllowedTools == nil {
		t.Fatal("a heartbeat ran with the unrestricted tool set")
	}
	for _, tool := range []string{"terminal", "workspace_edit", "propose_change", "patch", "write_file"} {
		if loop.options.AllowedTools[tool] {
			t.Fatalf("heartbeat reached repository mutation or shell tool %q", tool)
		}
	}
	for _, message := range loop.history {
		if strings.Contains(message.Content, "earlier owner chat") {
			t.Fatal("a heartbeat carried ambient conversation history")
		}
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

// TestApprovedOutcomeReadsAsASentenceWhenTheToolWroteOne is the owner-facing
// half of an approve tap. A tool that can describe what it just did carries the
// sentence in its own result; everything else -- an MCP tool this repository
// cannot write a sentence for -- still reports the record, because a message
// nobody can read is better than no message at all.
func TestApprovedOutcomeReadsAsASentenceWhenTheToolWroteOne(t *testing.T) {
	created := `{"id":"180f11b61b3d","instruction":"a long instruction the owner already wrote","summary":"Reminder set for Wed 5 Aug 2026, 8:00 PM +08. Cancel with id 180f11b61b3d."}`
	outcome := approvalOutcomeText(created)
	if !strings.HasPrefix(outcome, "Done. Reminder set for Wed 5 Aug 2026") {
		t.Fatalf("outcome=%q", outcome)
	}
	if strings.Contains(outcome, "instruction") {
		t.Fatalf("the summary did not replace the record: %q", outcome)
	}

	for name, result := range map[string]any{
		"no summary field": `{"deployed":true}`,
		"blank summary":    `{"summary":"   "}`,
		"not json at all":  "plain text",
		"not a string":     42,
	} {
		if outcome := approvalOutcomeText(result); !strings.HasPrefix(outcome, "Approved action completed:") {
			t.Fatalf("%s: outcome=%q", name, outcome)
		}
	}
}
