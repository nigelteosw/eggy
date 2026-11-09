package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/core/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

var statusNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// accountStates is a state store that keeps each account's state apart, as
// the real one does: Load answers for the principal on ctx and nobody else.
type accountStates struct {
	states map[string]ports.State
	err    error
}

func (s *accountStates) Load(ctx context.Context) (ports.State, error) {
	if s.err != nil {
		return ports.State{}, s.err
	}
	principal, _ := ports.PrincipalFromContext(ctx)
	return s.states[principal.AccountID], nil
}

func (s *accountStates) Update(context.Context, uint64, func(*ports.State) error) (ports.State, error) {
	return ports.State{}, errors.New("status must not write state")
}

func pendingApproval(id string, created time.Time) approvals.Approval {
	return approvals.Approval{
		ID: id, Action: "google", Summary: "Send " + id, Status: approvals.Pending,
		CreatedAt: created, ExpiresAt: created.Add(30 * time.Minute),
	}
}

func statusAs(t *testing.T, service *CommandService, account string) string {
	t.Helper()
	ctx := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: account})
	output, handled, err := service.Execute(ctx, "/status")
	if err != nil || !handled {
		t.Fatalf("status: handled=%v err=%v", handled, err)
	}
	return output
}

// A long queue is summarized, not pasted: the oldest few are named, the rest
// are counted, and the web panel is where the whole list lives.
func TestStatusBoundsTheApprovalList(t *testing.T) {
	queue := map[string]approvals.Approval{}
	for i := range 8 {
		id := fmt.Sprintf("a%d", i)
		queue[id] = pendingApproval(id, statusNow.Add(time.Duration(i-10)*time.Minute))
	}
	store := &accountStates{states: map[string]ports.State{"owner": {Approvals: queue}}}
	output := statusAs(t, New(Options{Store: store, Now: func() time.Time { return statusNow }}), "owner")

	if !strings.Contains(output, "**Pending approvals:** 8") {
		t.Fatalf("count missing: %q", output)
	}
	for i := range statusListLimit {
		if !strings.Contains(output, fmt.Sprintf("Send a%d ", i)) {
			t.Fatalf("oldest approval a%d not named: %q", i, output)
		}
	}
	if strings.Contains(output, "Send a5 ") {
		t.Fatalf("listed past the bound: %q", output)
	}
	if !strings.Contains(output, "3 more") || !strings.Contains(output, "/web") {
		t.Fatalf("remaining count or /web pointer missing: %q", output)
	}
	first, second := strings.Index(output, "Send a0 "), strings.Index(output, "Send a1 ")
	if first < 0 || second < first {
		t.Fatalf("approvals not oldest first: %q", output)
	}
}

// An approval past its expiry can no longer be approved, so it is not counted
// as waiting; it is mentioned once so the owner knows why it disappeared.
func TestStatusDoesNotCountExpiredApprovalsAsWaiting(t *testing.T) {
	store := &accountStates{states: map[string]ports.State{"owner": {Approvals: map[string]approvals.Approval{
		"fresh": pendingApproval("fresh", statusNow.Add(-time.Minute)),
		"stale": pendingApproval("stale", statusNow.Add(-time.Hour)),
	}}}}
	output := statusAs(t, New(Options{Store: store, Now: func() time.Time { return statusNow }}), "owner")
	if !strings.Contains(output, "**Pending approvals:** 1") || !strings.Contains(output, "Send fresh") {
		t.Fatalf("fresh approval not reported as the one waiting: %q", output)
	}
	if strings.Contains(output, "Send stale") {
		t.Fatalf("expired approval listed as waiting: %q", output)
	}
	if !strings.Contains(output, "1 expired") {
		t.Fatalf("expired approval not mentioned: %q", output)
	}
}

func TestStatusReportsOnlyTheCallingAccountsApprovals(t *testing.T) {
	store := &accountStates{states: map[string]ports.State{
		"owner":   {Approvals: map[string]approvals.Approval{"mine": pendingApproval("mine", statusNow)}},
		"partner": {Approvals: map[string]approvals.Approval{"theirs": pendingApproval("theirs", statusNow)}},
	}}
	output := statusAs(t, New(Options{Store: store, Now: func() time.Time { return statusNow }}), "owner")
	if !strings.Contains(output, "Send mine") || strings.Contains(output, "theirs") {
		t.Fatalf("status crossed accounts: %q", output)
	}
}

type reasoningModels struct {
	fakeModels
	effort   string
	thinking bool
}

func (m *reasoningModels) ReasoningEffort(context.Context) (string, error) { return m.effort, nil }
func (m *reasoningModels) ShowThinking(context.Context) (bool, error)      { return m.thinking, nil }

func TestStatusReportsTheEffectiveReasoningSettings(t *testing.T) {
	models := &reasoningModels{fakeModels: fakeModels{selected: "smart"}, effort: "high"}
	output := statusAs(t, New(Options{AgentRuntime: models}), "owner")
	if !strings.Contains(output, "smart") || !strings.Contains(output, "effort high") || !strings.Contains(output, "thinking hidden") {
		t.Fatalf("reasoning settings missing: %q", output)
	}

	models.effort, models.thinking = "", true
	output = statusAs(t, New(Options{AgentRuntime: models}), "owner")
	if !strings.Contains(output, "effort default") || !strings.Contains(output, "thinking shown") {
		t.Fatalf("default reasoning settings missing: %q", output)
	}
}

// A deployment without Google or MCP servers is not told about them.
func TestStatusOmitsUnconfiguredIntegrations(t *testing.T) {
	output := statusAs(t, New(Options{MCP: &fakeMCPRuntime{}}), "owner")
	if strings.Contains(output, "Google") || strings.Contains(output, "MCP") {
		t.Fatalf("unconfigured integration reported: %q", output)
	}
}

func TestStatusReportsTheSharedGoogleConnectionAndHowToRecover(t *testing.T) {
	cases := []struct {
		name   string
		status GoogleStatus
		want   []string
	}{
		{"connected", GoogleStatus{Authorized: true, Email: "eggy@example.com", ExpectedEmail: "eggy@example.com"}, []string{"eggy@example.com", "shared"}},
		{"not authorized", GoogleStatus{}, []string{"not connected", "/google login"}},
		{"wrong identity", GoogleStatus{Authorized: true, Email: "me@example.com", ExpectedEmail: "eggy@example.com"}, []string{"me@example.com", "expected eggy@example.com", "/google login"}},
	}
	for _, tc := range cases {
		output := statusAs(t, New(Options{Google: &fakeGoogleRuntime{status: tc.status}}), "owner")
		for _, want := range tc.want {
			if !strings.Contains(output, want) {
				t.Fatalf("%s: %q missing from %q", tc.name, want, output)
			}
		}
	}
}

// One section that cannot be read must not cost the owner the rest of the
// report: /status is what they reach for when something is wrong.
func TestStatusReportsTheRestWhenOneReadFails(t *testing.T) {
	service := New(Options{
		AgentRuntime: &fakeModels{selected: "fast"},
		Store:        &accountStates{err: errors.New("disk on fire")},
		Google:       &fakeGoogleRuntime{statusErr: errors.New("grant unreadable")},
	})
	output := statusAs(t, service, "owner")
	if !strings.Contains(output, "fast") {
		t.Fatalf("model missing after a failed read: %q", output)
	}
	if !strings.Contains(output, "**Pending approvals:** unavailable") || !strings.Contains(output, "**Google:** unavailable") {
		t.Fatalf("failed sections not marked: %q", output)
	}
}

// Servers needing attention are named in a stable order and bounded, so the
// same state always reads the same way and a broken fleet stays one line.
func TestStatusBoundsAndOrdersMCPServersNeedingAttention(t *testing.T) {
	runtime := &fakeMCPRuntime{statuses: []MCPStatus{
		{Name: "zeta", State: "failed"}, {Name: "alpha", State: "login_required"},
		{Name: "mid", State: "failed"}, {Name: "beta", State: "failed"}, {Name: "ok", State: "ready", Tools: 2},
	}}
	output := statusAs(t, New(Options{MCP: runtime}), "owner")
	if !strings.Contains(output, "1/5 ready") {
		t.Fatalf("readiness missing: %q", output)
	}
	if !strings.Contains(output, "alpha (login_required), beta (failed), mid (failed)") || strings.Contains(output, "zeta") {
		t.Fatalf("attention list not sorted and bounded: %q", output)
	}
	if !strings.Contains(output, "1 more") {
		t.Fatalf("remaining server count missing: %q", output)
	}
}
