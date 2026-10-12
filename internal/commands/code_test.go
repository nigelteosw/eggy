package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

// fakeCode records what /code asked the agent runner to do.
type fakeCode struct {
	started   []string
	toggled   []string
	cancelled string
	answered  string
	repos     []CodeRepository
}

func (f *fakeCode) StartRun(_ context.Context, repository, task string) (ports.AgentRun, error) {
	f.started = append(f.started, repository+"|"+task)
	return ports.AgentRun{ID: "run1", Repository: repository, State: ports.AgentRunQueued}, nil
}
func (f *fakeCode) Runs(context.Context) ([]ports.AgentRun, error) {
	return []ports.AgentRun{{ID: "run1", Repository: "eggy", State: ports.AgentRunRunning}}, nil
}
func (f *fakeCode) CancelRun(_ context.Context, id string) (ports.AgentRun, error) {
	f.cancelled = id
	return ports.AgentRun{ID: id, State: ports.AgentRunCancelled}, nil
}
func (f *fakeCode) AnswerRun(_ context.Context, id, text string) (ports.AgentRun, error) {
	f.answered = id + "|" + text
	return ports.AgentRun{ID: id, State: ports.AgentRunQueued}, nil
}
func (f *fakeCode) Repositories(context.Context) ([]CodeRepository, error) { return f.repos, nil }
func (f *fakeCode) SetRepositoryEnabled(_ context.Context, name string, enabled bool) error {
	if name == "missing" {
		return errors.New("unknown repository")
	}
	f.toggled = append(f.toggled, name)
	_ = enabled
	return nil
}

func telegramCtx() context.Context {
	return destination.With(context.Background(), destination.Destination{Kind: destination.Telegram})
}

func TestCodeCommandGrammar(t *testing.T) {
	fake := &fakeCode{repos: []CodeRepository{{Name: "eggy", Enabled: true, PushAccess: true}}}
	svc := New(Options{Code: fake})
	cases := []struct {
		input string
		want  string
	}{
		{"/code run eggy add a line to the readme", "run1"},
		{"/code status", "run1"},
		{"/code cancel run1", "cancelled"},
		{"/code answer run1 use README.md", "Answer sent"},
		{"/code repos", "eggy"},
		{"/code repos enable eggy", "enabled"},
		{"/code repos disable eggy", "disabled"},
		{"/code run", "Usage:"},
		{"/code frobnicate", "Usage:"},
		{"/code repos enable missing", "unknown repository"},
	}
	for _, tc := range cases {
		out, handled, err := svc.Execute(telegramCtx(), tc.input)
		if err != nil || !handled {
			t.Fatalf("%q: handled=%v err=%v", tc.input, handled, err)
		}
		if !strings.Contains(out, tc.want) && !strings.Contains(strings.ToLower(out), tc.want) {
			t.Errorf("%q = %q, want it to mention %q", tc.input, out, tc.want)
		}
	}
	if len(fake.started) != 1 || fake.started[0] != "eggy|add a line to the readme" {
		t.Fatalf("started = %v", fake.started)
	}
	if fake.answered != "run1|use README.md" {
		t.Fatalf("answered = %q", fake.answered)
	}
}

func TestCodeRunRefusedFromDiscord(t *testing.T) {
	fake := &fakeCode{}
	svc := New(Options{Code: fake})
	ctx := destination.With(context.Background(), destination.Destination{Kind: destination.Discord})
	out, _, _ := svc.Execute(ctx, "/code run eggy do it")
	if !strings.Contains(out, "can't be started from Discord") || len(fake.started) != 0 {
		t.Fatalf("discord run: out=%q started=%v", out, fake.started)
	}
}

func TestCodeDisabledMessage(t *testing.T) {
	svc := New(Options{})
	out, handled, _ := svc.Execute(telegramCtx(), "/code status")
	if !handled || !strings.Contains(out, "not enabled") {
		t.Fatalf("out = %q handled=%v", out, handled)
	}
}
