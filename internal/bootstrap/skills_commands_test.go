package bootstrap

import (
	"context"
	"strings"
	"testing"

	skillsadapter "github.com/nigelteosw/eggy/internal/adapters/skills"
	"github.com/nigelteosw/eggy/internal/adapters/state/jsonfile"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/services"
)

func TestCommandSkillsAddShowDisableEnableRemove(t *testing.T) {
	store := jsonfile.Open(t.TempDir() + "/state.json")
	skillsStore := skillsadapter.Open(t.TempDir(), 32<<10)
	gateway := &commandTestApprovalGateway{approval: approvals.Approval{ID: "approval-1"}}
	skills := services.NewSkillsService(skillsStore, store, gateway, gateway, nil)
	var delivered approvals.Approval
	channel := &commandTestChannel{onApproval: func(approval approvals.Approval) { delivered = approval }}
	commands := &CommandService{store: store, skills: skills, channel: channel, owner: "42"}
	ctx := context.Background()

	output, handled, err := commands.Execute(ctx, "/skills")
	if err != nil || !handled || !strings.Contains(output, "No skills installed.") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/skills add fix-flaky-tests\nUse when a test intermittently fails on timing\n\n1. Rerun with -count=10\n2. Look for shared state")
	if err != nil || !handled || !strings.Contains(output, "awaiting approval") || delivered.ID != "approval-1" {
		t.Fatalf("output=%q handled=%v err=%v delivered=%#v", output, handled, err, delivered)
	}
	if _, err := skillsStore.Read(ctx, "fix-flaky-tests"); err == nil {
		t.Fatal("skill must not exist before approval executes")
	}

	approval := delivered
	approval.Status = approvals.Approved
	if _, err := skills.ExecuteApproved(ctx, approval); err != nil {
		t.Fatal(err)
	}

	output, handled, err = commands.Execute(ctx, "/skills")
	if err != nil || !handled || !strings.Contains(output, "fix-flaky-tests") || !strings.Contains(output, "enabled") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/skills show fix-flaky-tests")
	if err != nil || !handled || !strings.Contains(output, "Use when a test intermittently fails on timing") || !strings.Contains(output, "Rerun with -count=10") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/skills disable fix-flaky-tests")
	if err != nil || !handled || !strings.Contains(output, "Disabled fix-flaky-tests.") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
	output, handled, err = commands.Execute(ctx, "/skills")
	if err != nil || !handled || !strings.Contains(output, "disabled") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/skills enable fix-flaky-tests")
	if err != nil || !handled || !strings.Contains(output, "Enabled fix-flaky-tests.") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, handled, err = commands.Execute(ctx, "/skills remove fix-flaky-tests")
	if err != nil || !handled || !strings.Contains(output, "awaiting approval") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
	deleteApproval := delivered
	deleteApproval.Status = approvals.Approved
	if _, err := skills.ExecuteApproved(ctx, deleteApproval); err != nil {
		t.Fatal(err)
	}
	if _, err := skillsStore.Read(ctx, "fix-flaky-tests"); err == nil {
		t.Fatal("expected skill to be removed")
	}

	output, handled, err = commands.Execute(ctx, "/skills add")
	if err != nil || !handled || !strings.Contains(output, "Usage:") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
	output, handled, err = commands.Execute(ctx, "/skills show missing")
	if err != nil || !handled || !strings.Contains(output, "Error:") {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
}

func TestSkillProposeToolStagesApprovalWithoutWriting(t *testing.T) {
	store := jsonfile.Open(t.TempDir() + "/state.json")
	skillsStore := skillsadapter.Open(t.TempDir(), 32<<10)
	gateway := &commandTestApprovalGateway{approval: approvals.Approval{ID: "approval-1"}}
	skills := services.NewSkillsService(skillsStore, store, gateway, gateway, nil)
	var delivered approvals.Approval
	channel := &commandTestChannel{onApproval: func(approval approvals.Approval) { delivered = approval }}
	tool := skillProposeTool(skills, channel, "42")

	ctx := context.Background()
	result, err := tool.Execute(ctx, []byte(`{"name":"fix-flaky-tests","description":"Use when a test intermittently fails","content":"1. Rerun with -count=10"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"status":"awaiting_owner"`) {
		t.Fatalf("result=%s", result)
	}
	if delivered.ID != "approval-1" || delivered.Action != approvals.SkillWrite {
		t.Fatalf("delivered=%#v", delivered)
	}
	if _, err := skillsStore.Read(ctx, "fix-flaky-tests"); err == nil {
		t.Fatal("skill must not be written before owner approval")
	}
}

func TestParseSkillProposalSplitsNameDescriptionAndBody(t *testing.T) {
	tests := []struct {
		name                            string
		tail                            string
		wantName, wantDescription, body string
		ok                              bool
	}{
		{"space then newline body", "fix-flaky-tests description here\nbody line", "fix-flaky-tests", "description here", "body line", true},
		{"newline separated name", "fix-flaky-tests\ndescription here\nbody line", "fix-flaky-tests", "description here", "body line", true},
		{"missing body", "fix-flaky-tests description only", "", "", "", false},
		{"empty", "", "", "", "", false},
		{"name only", "fix-flaky-tests", "", "", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name, description, body, ok := parseSkillProposal(test.tail)
			if ok != test.ok {
				t.Fatalf("ok=%v want=%v (name=%q description=%q body=%q)", ok, test.ok, name, description, body)
			}
			if ok && (name != test.wantName || description != test.wantDescription || body != test.body) {
				t.Fatalf("got name=%q description=%q body=%q", name, description, body)
			}
		})
	}
}
