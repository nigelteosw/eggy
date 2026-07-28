package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// The unprompted-turn invariant, stated once: a scheduled or heartbeat turn
// may write to a repository and propose the result, but only ever as a draft
// pull request, only on a branch of its own, and never on top of a change the
// owner has open. These tests are the kernel's guard on that; the surface
// that marks a turn unprompted lives in internal/bootstrap for now.

func unpromptedShippingService(t *testing.T, change ports.Change) (*ShippingService, *fakeRepository) {
	t.Helper()
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main", ProtectedBranches: []string{"main", "release"}}}
	changes, transcripts, _ := shippingFixture(change)
	repository := &fakeRepository{branch: change.Branch}
	// A real ApprovalService, not a fake gateway: the draft flag rides inside
	// the payload each approval is digest-bound to, so a fake that skips the
	// payload would prove nothing about it.
	return NewShippingService(store, changes, transcripts, NewApprovalService(store, time.Now, time.Hour), repository, repository, repository, repository, fullRepositoryCapabilities), repository
}

func TestUnpromptedProposalOpensADraftPullRequest(t *testing.T) {
	change := ports.Change{ID: "run-1", Repository: "eggy", Branch: "eggy/abc", BaseRevision: "abc123", Diff: "diff", Unprompted: true}
	service, repository := unpromptedShippingService(t, change)
	target := shipTargetFor("run-1")
	target.Draft = true

	pr, note, err := service.Ship(context.Background(), target, "eggy/abc", "feat: improve myself")
	if err != nil || note != "" {
		t.Fatalf("pr=%#v note=%q err=%v", pr, note, err)
	}
	if !repository.draftPR {
		t.Fatal("an unprompted turn's proposal must open a draft pull request")
	}
}

func TestOwnerPromptedProposalOpensAReadyPullRequest(t *testing.T) {
	change := ports.Change{ID: "run-1", Repository: "eggy", Branch: "eggy/abc", BaseRevision: "abc123", Diff: "diff"}
	service, repository := unpromptedShippingService(t, change)

	if _, note, err := service.Ship(context.Background(), shipTargetFor("run-1"), "eggy/abc", "feat: asked for"); err != nil || note != "" {
		t.Fatalf("note=%q err=%v", note, err)
	}
	if repository.draftPR {
		t.Fatal("an owner-prompted proposal must not be forced to a draft")
	}
}

func TestUnpromptedProposalCannotTargetABaseOrProtectedBranch(t *testing.T) {
	for _, branch := range []string{"main", "release"} {
		change := ports.Change{ID: "run-1", Repository: "eggy", Branch: branch, BaseRevision: "abc123", Diff: "diff", Unprompted: true}
		service, repository := unpromptedShippingService(t, change)
		target := shipTargetFor("run-1")
		target.Draft = true

		_, _, err := service.Ship(context.Background(), target, branch, "feat: straight to trunk")
		if !errors.Is(err, ErrUnpromptedBaseBranch) {
			t.Fatalf("branch %q: err=%v, want ErrUnpromptedBaseBranch", branch, err)
		}
		// Refused before anything happened, not partway through.
		if repository.commits != 0 || repository.pushes != 0 || repository.prs != 0 {
			t.Fatalf("branch %q: repository=%#v, want nothing executed", branch, repository)
		}
	}
}

func TestUnpromptedTurnMarkingTravelsOnContext(t *testing.T) {
	if IsUnpromptedTurn(context.Background()) {
		t.Fatal("an unmarked context must read as owner-prompted")
	}
	if !IsUnpromptedTurn(WithUnpromptedTurn(context.Background())) {
		t.Fatal("WithUnpromptedTurn must mark the context")
	}
}

func TestChangeOpenedByAnUnpromptedTurnIsRecordedAsSuch(t *testing.T) {
	changes := NewChanges(newMemoryChangeStore(), nil)
	owned, err := changes.Open(context.Background(), "run-owner", "eggy", "eggy/a", "abc", "opus")
	if err != nil {
		t.Fatal(err)
	}
	unprompted, err := changes.Open(WithUnpromptedTurn(context.Background()), "run-heartbeat", "eggy", "eggy/b", "abc", "opus")
	if err != nil {
		t.Fatal(err)
	}
	if owned.Unprompted || !unprompted.Unprompted {
		t.Fatalf("owner=%v unprompted=%v", owned.Unprompted, unprompted.Unprompted)
	}
}
