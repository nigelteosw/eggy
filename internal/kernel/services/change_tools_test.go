package services

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type changeToolFixture struct {
	tools       map[string]ports.Tool
	workspaces  *WorkspaceSessions
	changes     *Changes
	transcripts *Transcripts
	repository  *fakeRepository
	shipper     *fakeShipper
	threads     *fakeThreadStore
}

func newChangeToolFixture(t *testing.T, shipper *fakeShipper) changeToolFixture {
	t.Helper()
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	threads := newFakeThreadStore()
	repository := &fakeRepository{}
	workspaces := NewWorkspaceSessions(store, threads, &fakeReadWorkspaceRunner{workspace: "/tmp/runs/workspace-1"}, repository, func() string { return "1" }, nil, nil)
	changes := NewChanges(newMemoryChangeStore(), time.Now)
	transcripts := NewTranscripts(newMemoryTranscriptStore(), 0, time.Now)
	byName := map[string]ports.Tool{}
	for _, tool := range NewChangeTools(store, workspaces, changes, transcripts, repository, shipper, func() string { return "run-1" }, nil) {
		byName[tool.Definition().Name] = tool
	}
	for _, tool := range workspaces.Tools() {
		byName[tool.Definition().Name] = tool
	}
	return changeToolFixture{tools: byName, workspaces: workspaces, changes: changes, transcripts: transcripts, repository: repository, shipper: shipper, threads: threads}
}

// The whole point of collapsing the run into the thread: the checkout the
// owner opened to read is the one the edits land in, branched in place.
func TestWorkspaceEditBranchesTheThreadsExistingCheckoutInPlace(t *testing.T) {
	fixture := newChangeToolFixture(t, &fakeShipper{})
	ctx := webThread("thread-a")
	if _, err := fixture.tools["workspace_open"].Execute(ctx, json.RawMessage(`{"repository":"eggy"}`)); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.tools["workspace_edit"].Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var edit map[string]any
	if err := json.Unmarshal(result, &edit); err != nil {
		t.Fatal(err)
	}
	if edit["status"] != "editing" || edit["branch"] != "eggy/run-1" || edit["change"] != "run-1" {
		t.Fatalf("edit=%v", edit)
	}
	if fixture.repository.clones != 1 {
		t.Fatalf("clones=%d, want the open checkout branched rather than re-cloned", fixture.repository.clones)
	}
	binding, err := fixture.workspaces.Resolve(ctx)
	if err != nil || binding.Path != "/tmp/runs/workspace-1" || !binding.Writable || binding.Change != "run-1" {
		t.Fatalf("binding=%#v err=%v", binding, err)
	}
	// The change records what it is, not where it lives: the checkout is the
	// thread's, and the binding above is what says where.
	change, err := fixture.changes.Load(ctx, "run-1")
	if err != nil || change.Branch != "eggy/run-1" || change.BaseRevision != "abc123" || change.Repository != "eggy" {
		t.Fatalf("change=%#v err=%v", change, err)
	}
}

// The model alias is stamped on the change when the branch is created, so
// /runs show reports what did the work even after the owner runs /model.
func TestWorkspaceEditRecordsTheSelectedModel(t *testing.T) {
	fixture := newChangeToolFixture(t, &fakeShipper{})
	ctx := WithSelectedModel(webThread("thread-a"), "opus")
	if _, err := fixture.tools["workspace_edit"].Execute(ctx, json.RawMessage(`{"repository":"eggy"}`)); err != nil {
		t.Fatal(err)
	}
	change, err := fixture.changes.Load(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if change.Model != "opus" {
		t.Fatalf("change.Model=%q, want the alias the turn was running on", change.Model)
	}
}

// Outside a turn there is no model to attribute the work to, and the change
// says so rather than borrowing the current selection.
func TestWorkspaceEditOutsideATurnRecordsNoModel(t *testing.T) {
	fixture := newChangeToolFixture(t, &fakeShipper{})
	ctx := webThread("thread-a")
	if _, err := fixture.tools["workspace_edit"].Execute(ctx, json.RawMessage(`{"repository":"eggy"}`)); err != nil {
		t.Fatal(err)
	}
	change, err := fixture.changes.Load(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if change.Model != "" {
		t.Fatalf("change.Model=%q, want empty outside a turn", change.Model)
	}
}

// Asking twice in a thread keeps the branch it already has, rather than
// starting a second one over the first one's uncommitted work.
func TestWorkspaceEditIsIdempotentWithinAThread(t *testing.T) {
	fixture := newChangeToolFixture(t, &fakeShipper{})
	ctx := webThread("thread-a")
	if _, err := fixture.tools["workspace_edit"].Execute(ctx, json.RawMessage(`{"repository":"eggy"}`)); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.tools["workspace_edit"].Execute(ctx, json.RawMessage(`{"repository":"eggy"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"already_editing"`) || fixture.repository.branches != 1 {
		t.Fatalf("result=%s branches=%d", result, fixture.repository.branches)
	}
}

// propose_change is not terminal and not a run: it ships what is in the
// checkout and hands the pull-request URL back as an ordinary tool result.
func TestProposeChangeShipsTheThreadsBranchAndReturnsThePullRequestURL(t *testing.T) {
	shipper := &fakeShipper{pr: ports.PullRequest{URL: "https://example.test/pr/7", Number: 7}}
	fixture := newChangeToolFixture(t, shipper)
	ctx := webThread("thread-a")
	if _, err := fixture.tools["workspace_edit"].Execute(ctx, json.RawMessage(`{"repository":"eggy"}`)); err != nil {
		t.Fatal(err)
	}
	fixture.repository.diff = "diff --git a/main.go b/main.go"

	result, err := fixture.tools["propose_change"].Execute(ctx, json.RawMessage(`{"summary":"fixed the bug","validation":"go test ./... passed","commit_message":"fix: the bug"}`))
	if err != nil {
		t.Fatal(err)
	}
	var shipped map[string]any
	if err := json.Unmarshal(result, &shipped); err != nil {
		t.Fatal(err)
	}
	if shipped["status"] != "shipped" || shipped["pull_request_url"] != "https://example.test/pr/7" {
		t.Fatalf("shipped=%v", shipped)
	}
	if shipper.target.ChangeID != "run-1" || shipper.branch != "eggy/run-1" || shipper.commitMessage != "fix: the bug" {
		t.Fatalf("shipper=%#v", shipper)
	}
	// Eggy captured the diff and validation itself, independently of what
	// the model said about them.
	session, err := fixture.changes.Load(ctx, "run-1")
	if err != nil || session.Diff != "diff --git a/main.go b/main.go" || session.Validation != "go test ./... passed" {
		t.Fatalf("session=%#v err=%v", session, err)
	}
}

// The pre-ship equality checks survive the collapse: they were never a
// property of having a separate run.
func TestProposeChangeRefusesWhenTheCheckoutMovedBehindTheApproval(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fakeRepository)
		want   string
	}{
		{name: "branch", mutate: func(r *fakeRepository) { r.branch = "feat/unapproved" }, want: "moved from branch"},
		{name: "head", mutate: func(r *fakeRepository) { r.head = "unapproved-commit" }, want: "HEAD moved"},
	} {
		t.Run(test.name, func(t *testing.T) {
			shipper := &fakeShipper{}
			fixture := newChangeToolFixture(t, shipper)
			ctx := webThread("thread-a")
			if _, err := fixture.tools["workspace_edit"].Execute(ctx, json.RawMessage(`{"repository":"eggy"}`)); err != nil {
				t.Fatal(err)
			}
			fixture.repository.diff = "diff"
			test.mutate(fixture.repository)

			_, err := fixture.tools["propose_change"].Execute(ctx, json.RawMessage(`{"summary":"s","validation":"v","commit_message":"c"}`))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v, want a refusal mentioning %q", err, test.want)
			}
			if shipper.calls != 0 {
				t.Fatal("nothing may ship once the checkout moved")
			}
		})
	}
}

func TestProposeChangeRequiresABranchedWorkspace(t *testing.T) {
	fixture := newChangeToolFixture(t, &fakeShipper{})
	ctx := webThread("thread-a")
	if _, err := fixture.tools["workspace_open"].Execute(ctx, json.RawMessage(`{"repository":"eggy"}`)); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.tools["propose_change"].Execute(ctx, json.RawMessage(`{"summary":"s","validation":"v","commit_message":"c"}`))
	if !errors.Is(err, ErrWorkspaceNotEditable) {
		t.Fatalf("err=%v, want ErrWorkspaceNotEditable", err)
	}
}

func TestProposeChangeRefusesAnEmptyDiff(t *testing.T) {
	shipper := &fakeShipper{}
	fixture := newChangeToolFixture(t, shipper)
	ctx := webThread("thread-a")
	if _, err := fixture.tools["workspace_edit"].Execute(ctx, json.RawMessage(`{"repository":"eggy"}`)); err != nil {
		t.Fatal(err)
	}
	fixture.repository.noDiff = true
	if _, err := fixture.tools["propose_change"].Execute(ctx, json.RawMessage(`{"summary":"s","validation":"v","commit_message":"c"}`)); err == nil || !strings.Contains(err.Error(), "no changes") {
		t.Fatalf("err=%v", err)
	}
	if shipper.calls != 0 {
		t.Fatal("an empty diff must not ship")
	}
}

func TestProposeChangeReportsAPartialShipWithoutFailing(t *testing.T) {
	shipper := &fakeShipper{note: "Committed. Push is unavailable for the configured repository provider."}
	fixture := newChangeToolFixture(t, shipper)
	ctx := webThread("thread-a")
	if _, err := fixture.tools["workspace_edit"].Execute(ctx, json.RawMessage(`{"repository":"eggy"}`)); err != nil {
		t.Fatal(err)
	}
	fixture.repository.diff = "diff"
	result, err := fixture.tools["propose_change"].Execute(ctx, json.RawMessage(`{"summary":"s","validation":"v","commit_message":"c"}`))
	if err != nil {
		t.Fatal(err)
	}
	var partial map[string]any
	_ = json.Unmarshal(result, &partial)
	if partial["status"] != "partial" || partial["note"] != shipper.note {
		t.Fatalf("partial=%v", partial)
	}
}

// A second round in the same thread reuses the same branch and session, so
// shipping updates the pull request already open for it.
func TestProposingTwiceInAThreadKeepsTheSameBranchAndSession(t *testing.T) {
	shipper := &fakeShipper{pr: ports.PullRequest{URL: "https://example.test/pr/7", Number: 7}}
	fixture := newChangeToolFixture(t, shipper)
	ctx := webThread("thread-a")
	if _, err := fixture.tools["workspace_edit"].Execute(ctx, json.RawMessage(`{"repository":"eggy"}`)); err != nil {
		t.Fatal(err)
	}
	fixture.repository.diff = "first round"
	// Committing moves HEAD; the session's baseline must follow it, so a
	// second round verifies against where the branch actually is now.
	shipper.onShip = func() { fixture.repository.head = "committed-head" }
	if _, err := fixture.tools["propose_change"].Execute(ctx, json.RawMessage(`{"summary":"s","validation":"v","commit_message":"c"}`)); err != nil {
		t.Fatal(err)
	}
	fixture.repository.diff = "second round"
	if _, err := fixture.tools["propose_change"].Execute(ctx, json.RawMessage(`{"summary":"s2","validation":"v2","commit_message":"c2"}`)); err != nil {
		t.Fatal(err)
	}
	if shipper.calls != 2 || shipper.branch != "eggy/run-1" {
		t.Fatalf("shipper=%#v", shipper)
	}
}

func TestProposeChangeRequiresValidationEvidence(t *testing.T) {
	fixture := newChangeToolFixture(t, &fakeShipper{})
	ctx := webThread("thread-a")
	if _, err := fixture.tools["propose_change"].Execute(ctx, json.RawMessage(`{"summary":"s","validation":"","commit_message":"c"}`)); err == nil || !strings.Contains(err.Error(), "validation must not be empty") {
		t.Fatalf("err=%v", err)
	}
}

// An unprompted turn shares its thread with the owner (proactive output is
// one channel), so the tools -- not the allowlist -- are what keep a
// heartbeat from adopting a branch the owner left mid-change and proposing
// it as its own.
func TestUnpromptedTurnCannotEditOrProposeTheOwnersOpenChange(t *testing.T) {
	fixture := newChangeToolFixture(t, &fakeShipper{})
	owner := webThread("thread-a")
	if _, err := fixture.tools["workspace_open"].Execute(owner, json.RawMessage(`{"repository":"eggy"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.tools["workspace_edit"].Execute(owner, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}

	unprompted := WithUnpromptedTurn(owner)
	if _, err := fixture.tools["workspace_edit"].Execute(unprompted, json.RawMessage(`{}`)); !errors.Is(err, ErrOwnerChangeInProgress) {
		t.Fatalf("workspace_edit err=%v, want ErrOwnerChangeInProgress", err)
	}
	_, err := fixture.tools["propose_change"].Execute(unprompted, json.RawMessage(`{"summary":"s","validation":"go test","commit_message":"feat: x"}`))
	if !errors.Is(err, ErrOwnerChangeInProgress) {
		t.Fatalf("propose_change err=%v, want ErrOwnerChangeInProgress", err)
	}
	if fixture.shipper.calls != 0 {
		t.Fatalf("shipper calls=%d, want nothing shipped", fixture.shipper.calls)
	}
}

// The mirror of the test above: an unprompted turn working on a change it
// opened itself proposes normally, and marks the proposal a draft.
func TestUnpromptedTurnProposesItsOwnChangeAsADraft(t *testing.T) {
	fixture := newChangeToolFixture(t, &fakeShipper{})
	ctx := WithUnpromptedTurn(webThread("thread-a"))
	if _, err := fixture.tools["workspace_open"].Execute(ctx, json.RawMessage(`{"repository":"eggy"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.tools["workspace_edit"].Execute(ctx, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.tools["propose_change"].Execute(ctx, json.RawMessage(`{"summary":"s","validation":"go test","commit_message":"feat: x"}`)); err != nil {
		t.Fatal(err)
	}
	if !fixture.shipper.target.Draft {
		t.Fatalf("target=%#v, want Draft set for an unprompted proposal", fixture.shipper.target)
	}
}
