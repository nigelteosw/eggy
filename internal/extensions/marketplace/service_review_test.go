package marketplace

import (
	"errors"
	"strings"
	"testing"
)

const shaExt = "3333333333333333333333333333333333333333"

// The catalog lists a plugin that lives in another repository. The approval
// names the catalog commit; the plan's own commit is the external one.
func TestInstallExternalGitHubItem(t *testing.T) {
	h := newHarness(t)
	h.fetch.trees["o/team@"+sha1Commit] = memTree(map[string]string{
		".claude-plugin/marketplace.json": `{"name":"team","plugins":[{"name":"ext-tools","source":{"source":"github","repo":"o/ext","ref":"v1"}}]}`,
	})
	h.fetch.refs["o/ext@v1"] = shaExt
	h.fetch.trees["o/ext@"+shaExt] = memTree(map[string]string{
		"skills/x/SKILL.md": "---\nname: x\ndescription: External skill\n---\n\nbody\n",
	})
	req := h.approve(t, PlanRequest{Source: "team", Item: "ext-tools"})
	if req.SHA != sha1Commit {
		t.Fatalf("approval sha = %s, want the catalog commit", req.SHA)
	}
	inst, err := h.svc.Install(h.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if inst.Repo != "o/ext" || inst.CommitSHA != shaExt {
		t.Fatalf("record = %+v, want repo o/ext at the external commit", inst)
	}
	if h.skills.skills["x"] == nil || h.skills.skills["x"].source != "team/ext-tools" {
		t.Fatalf("skill x = %+v", h.skills.skills["x"])
	}
}

func TestInstallRejectsANonSHACommit(t *testing.T) {
	h := newHarness(t)
	req := h.approve(t, deployReq())
	req.SHA = "main"
	if _, err := h.svc.Install(h.ctx, req); err == nil || !strings.Contains(err.Error(), "full SHA") {
		t.Fatalf("err = %v, want a full-SHA refusal", err)
	}
	if len(h.skills.skills) != 0 || len(h.installs.rows) != 0 {
		t.Fatal("a refused install wrote something")
	}
}

// An install owns an MCP entry named deploy-tools-api. A hand-placed skill with
// the same name must still be a conflict when upstream adds a skill by that name.
func TestUpdateDoesNotLetAnMCPNameOwnASkill(t *testing.T) {
	h := newHarness(t)
	inst, err := h.svc.Install(h.ctx, h.approve(t, deployReq()))
	if err != nil {
		t.Fatal(err)
	}
	h.skills.skills["deploy-tools-api"] = &fakeSkill{desc: "mine", body: "hand written", files: map[string]string{}}
	tree := deployRepo("Fill forms.")
	tree.Files["plugins/deploy/skills/api/SKILL.md"] = []byte("---\nname: deploy-tools-api\ndescription: Upstream\n---\n\nupstream\n")
	h.fetch.refs["o/team@main"] = sha2Commit
	h.fetch.trees["o/team@"+sha2Commit] = tree

	req := ApproveRequest{PlanRequest: PlanRequest{Source: "team", Item: "deploy-tools", InstallID: inst.ID}}
	plan, err := h.svc.Plan(h.ctx, req.PlanRequest)
	if err != nil {
		t.Fatal(err)
	}
	req.SHA, req.Digest = plan.CatalogSHA, plan.Digest
	if _, err := h.svc.Update(h.ctx, inst.ID, req, nil); !errors.Is(err, ErrConflicts) {
		t.Fatalf("err = %v, want ErrConflicts", err)
	}
	if h.skills.skills["deploy-tools-api"].body != "hand written" {
		t.Fatal("hand-placed skill overwritten through an MCP name")
	}
}

// The owner enabled the server (a trust decision, with its approval list). An
// upstream URL change must not remove and re-add it.
func TestUpdateKeepsAnOwnerEnabledMCPServer(t *testing.T) {
	h := newHarness(t)
	inst, err := h.svc.Install(h.ctx, h.approve(t, deployReq()))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.mcp.SetEnabled("deploy-tools-api", true); err != nil {
		t.Fatal(err)
	}
	tree := deployRepo("Fill forms.")
	tree.Files["plugins/deploy/.mcp.json"] = []byte(`{"mcpServers":{"api":{"url":"https://moved.example.com/mcp"}}}`)
	h.fetch.refs["o/team@main"] = sha2Commit
	h.fetch.trees["o/team@"+sha2Commit] = tree

	req := ApproveRequest{PlanRequest: PlanRequest{Source: "team", Item: "deploy-tools", InstallID: inst.ID}}
	plan, err := h.svc.Plan(h.ctx, req.PlanRequest)
	if err != nil {
		t.Fatal(err)
	}
	req.SHA, req.Digest = plan.CatalogSHA, plan.Digest
	if _, err := h.svc.Update(h.ctx, inst.ID, req, nil); err != nil {
		t.Fatal(err)
	}
	server := h.mcp.servers["deploy-tools-api"]
	if !server.enabled || server.url != "https://mcp.example.com/mcp" {
		t.Fatalf("server = %+v, want the owner's enabled server untouched", server)
	}
}

func TestRemoveSkipsAHandDeletedSkill(t *testing.T) {
	h := newHarness(t)
	inst, err := h.svc.Install(h.ctx, h.approve(t, deployReq()))
	if err != nil {
		t.Fatal(err)
	}
	delete(h.skills.skills, "pdf")
	if err := h.svc.Remove(h.ctx, inst.ID, nil); err != nil {
		t.Fatalf("remove after a hand delete: %v", err)
	}
	if len(h.installs.rows) != 0 {
		t.Fatal("record survived removal")
	}
	if _, ok := h.mcp.servers["deploy-tools-api"]; ok {
		t.Fatal("MCP entry survived removal")
	}
}

// The owner deleted a skill by hand. No later update brings it back: the
// record keeps it as owned, so each update knows it is the owner's deletion.
func TestUpdatesNeverRecreateAHandDeletedSkill(t *testing.T) {
	h := newHarness(t)
	inst, err := h.svc.Install(h.ctx, h.approve(t, deployReq()))
	if err != nil {
		t.Fatal(err)
	}
	delete(h.skills.skills, "pdf")
	for i, commit := range []string{sha2Commit, shaExt} {
		h.fetch.refs["o/team@main"] = commit
		h.fetch.trees["o/team@"+commit] = deployRepo("Fill forms v" + string(rune('2'+i)) + ".")
		req := ApproveRequest{PlanRequest: PlanRequest{Source: "team", Item: "deploy-tools", InstallID: inst.ID}}
		plan, err := h.svc.Plan(h.ctx, req.PlanRequest)
		if err != nil {
			t.Fatal(err)
		}
		req.SHA, req.Digest = plan.CatalogSHA, plan.Digest
		if _, err := h.svc.Update(h.ctx, inst.ID, req, nil); err != nil {
			t.Fatalf("update %d: %v", i+1, err)
		}
		if _, ok := h.skills.skills["pdf"]; ok {
			t.Fatalf("update %d brought back a skill the owner deleted", i+1)
		}
	}
}

func TestUpdateRollsBackWhenTheRecordCannotBeWritten(t *testing.T) {
	h := newHarness(t)
	inst, err := h.svc.Install(h.ctx, h.approve(t, deployReq()))
	if err != nil {
		t.Fatal(err)
	}
	h.fetch.refs["o/team@main"] = sha2Commit
	h.fetch.trees["o/team@"+sha2Commit] = deployRepo("Fill forms v2.")
	req := ApproveRequest{PlanRequest: PlanRequest{Source: "team", Item: "deploy-tools", InstallID: inst.ID}}
	plan, err := h.svc.Plan(h.ctx, req.PlanRequest)
	if err != nil {
		t.Fatal(err)
	}
	req.SHA, req.Digest = plan.CatalogSHA, plan.Digest
	h.installs.replaceErr = errors.New("database unavailable")
	if _, err := h.svc.Update(h.ctx, inst.ID, req, nil); err == nil {
		t.Fatal("update succeeded with a failing record write")
	}
	if got := h.skills.skills["pdf"]; got == nil || got.body != "Fill forms." || got.source != "team/deploy-tools" {
		t.Fatalf("skill after rollback = %+v, want the installed version back", got)
	}
}

func TestRemoveRollsBackWhenTheRecordCannotBeDeleted(t *testing.T) {
	h := newHarness(t)
	inst, err := h.svc.Install(h.ctx, h.approve(t, deployReq()))
	if err != nil {
		t.Fatal(err)
	}
	h.installs.deleteErr = errors.New("database unavailable")
	if err := h.svc.Remove(h.ctx, inst.ID, nil); err == nil {
		t.Fatal("remove succeeded with a failing record delete")
	}
	if got := h.skills.skills["pdf"]; got == nil || got.source != "team/deploy-tools" {
		t.Fatalf("skill after rollback = %+v, want it restored", got)
	}
	if server, ok := h.mcp.servers["deploy-tools-api"]; !ok || server.enabled {
		t.Fatalf("server after rollback = %+v, want it restored and disabled", server)
	}
}

func TestRemoveKeepsFrontmatterExtrasOnAKeptSkill(t *testing.T) {
	h := newHarness(t)
	tree := deployRepo("Fill forms.")
	tree.Files["plugins/deploy/skills/pdf/SKILL.md"] = []byte("---\nname: pdf\ndescription: Fill PDF forms\nlicense: MIT\n---\n\nFill forms.\n")
	h.fetch.trees["o/team@"+sha1Commit] = tree
	inst, err := h.svc.Install(h.ctx, h.approve(t, deployReq()))
	if err != nil {
		t.Fatal(err)
	}
	h.skills.skills["pdf"].body = "owner edited this"
	if err := h.svc.Remove(h.ctx, inst.ID, nil); err != nil {
		t.Fatal(err)
	}
	kept := h.skills.skills["pdf"]
	if kept == nil || kept.source != "" || kept.extra["license"] != "MIT" {
		t.Fatalf("kept skill = %+v, want hand-placed with license kept", kept)
	}
}

func TestPlanRejectsANonSHAExternalPin(t *testing.T) {
	h := newHarness(t)
	h.fetch.trees["o/team@"+sha1Commit] = memTree(map[string]string{
		".claude-plugin/marketplace.json": `{"name":"team","plugins":[{"name":"ext-tools","source":{"source":"github","repo":"o/ext","sha":"main"}}]}`,
	})
	if _, err := h.svc.Plan(h.ctx, PlanRequest{Source: "team", Item: "ext-tools"}); err == nil || !strings.Contains(err.Error(), "not a full commit") {
		t.Fatalf("err = %v, want a refusal of the branch name pin", err)
	}
}

// A crafted update for install A naming item B must not write B under A.
func TestUpdateIsBoundToTheInstallRecord(t *testing.T) {
	h := newHarness(t)
	inst, err := h.svc.Install(h.ctx, h.approve(t, deployReq()))
	if err != nil {
		t.Fatal(err)
	}
	req := ApproveRequest{PlanRequest: PlanRequest{Source: "team", Item: "something-else", InstallID: inst.ID}, SHA: sha1Commit}
	plan, err := h.svc.Plan(h.ctx, PlanRequest{Source: "team", Item: "deploy-tools", InstallID: inst.ID})
	if err != nil {
		t.Fatal(err)
	}
	req.Digest = plan.Digest
	updated, err := h.svc.Update(h.ctx, inst.ID, req, nil)
	if err != nil {
		t.Fatalf("update with a mismatched item in the body: %v", err)
	}
	if updated.Item != "deploy-tools" || updated.SourceName != "team" {
		t.Fatalf("record = %+v, want it still bound to team/deploy-tools", updated)
	}
}

func TestAnIncompleteRollbackIsReported(t *testing.T) {
	h := newHarness(t)
	h.mcp.addErr = errors.New("config write failed")
	h.skills.deleteErr = errors.New("disk is read-only")
	_, err := h.svc.Install(h.ctx, h.approve(t, deployReq()))
	if err == nil || !strings.Contains(err.Error(), "rollback incomplete") {
		t.Fatalf("err = %v, want it to say the rollback was incomplete", err)
	}
}

// The owner deleted skill pdf from an install and later wrote their own pdf.
// The install's tombstone must not own it: no edit label, no update overwrite,
// and Remove leaves it alone.
func TestATombstoneNeverTouchesALaterSkillOfTheSameName(t *testing.T) {
	h := newHarness(t)
	inst, err := h.svc.Install(h.ctx, h.approve(t, deployReq()))
	if err != nil {
		t.Fatal(err)
	}
	delete(h.skills.skills, "pdf")
	h.fetch.refs["o/team@main"] = sha2Commit
	h.fetch.trees["o/team@"+sha2Commit] = deployRepo("Fill forms v2.")
	update := func() error {
		req := ApproveRequest{PlanRequest: PlanRequest{Source: "team", Item: "deploy-tools", InstallID: inst.ID}}
		plan, err := h.svc.Plan(h.ctx, req.PlanRequest)
		if err != nil {
			return err
		}
		req.SHA, req.Digest = plan.CatalogSHA, plan.Digest
		_, err = h.svc.Update(h.ctx, inst.ID, req, []string{"pdf"})
		return err
	}
	if err := update(); err != nil {
		t.Fatal(err)
	}
	h.skills.skills["pdf"] = &fakeSkill{desc: "mine", body: "the owner's own pdf", files: map[string]string{}}

	statuses, err := h.svc.Installs(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range statuses[0].Edited {
		if name == "pdf" {
			t.Fatal("the owner's own pdf is labelled an edit of the install")
		}
	}
	if err := update(); !errors.Is(err, ErrConflicts) {
		t.Fatalf("update err = %v, want a conflict rather than an overwrite", err)
	}
	if err := h.svc.Remove(h.ctx, inst.ID, []string{"pdf"}); err != nil {
		t.Fatal(err)
	}
	if got := h.skills.skills["pdf"]; got == nil || got.body != "the owner's own pdf" {
		t.Fatalf("pdf = %+v, want the owner's skill untouched", got)
	}
}
