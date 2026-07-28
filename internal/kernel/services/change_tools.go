package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ErrWorkspaceNotEditable is returned by propose_change when the thread's
// checkout has never been branched, so there is nothing to ship.
var ErrWorkspaceNotEditable = errors.New("this conversation's workspace has no branch: call workspace_edit before proposing a change")

// ErrOwnerChangeInProgress is returned when an unprompted turn (scheduled or
// heartbeat) tries to edit or propose a change the owner opened. An
// unprompted turn works only on a change of its own.
var ErrOwnerChangeInProgress = errors.New("this thread has an owner's change open: an unprompted turn cannot continue it, and must not edit or propose here")

// NewChangeTools returns the two tools that turn an attached checkout into a
// pull request: workspace_edit branches it, propose_change ships what is in
// it. Neither is terminal. propose_change returns the pull-request URL as an
// ordinary tool result, so the model reads it, reports it conversationally,
// and can keep working -- editing again, or proposing a second change later
// in the same thread -- instead of the turn ending because a run ended.
//
// The safety invariants are unchanged and remain Eggy's, not the model's:
// Eggy captures the diff itself, verifies the checkout is still on the
// branch and HEAD it recorded before it commits anything, and every step of
// the commit -> push -> pull-request chain still goes through
// ShippingService's payload-digest approvals.
func NewChangeTools(
	store ports.StateStore,
	workspaces *WorkspaceSessions,
	changes *Changes,
	transcripts *Transcripts,
	repository ports.CodingRepository,
	shipper Shipper,
	newRunID func() string,
	progress ports.ProgressReporter,
) []ports.Tool {
	edit := repositoryTool{definition: ports.ToolDefinition{
		Name:        "workspace_edit",
		Description: "Create a branch in this conversation's attached checkout so patch and write_file can change files in it. Call it once before your first edit; the branch and its session persist across turns until the change is proposed. It creates no commit and no approval.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1}},"additionalProperties":false}`),
	}}
	edit.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Repository string `json:"repository"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		if workspaces == nil || changes == nil || repository == nil || newRunID == nil {
			return nil, errors.New("repository editing is unavailable")
		}
		binding, err := workspaces.Resolve(ctx)
		if err != nil && !errors.Is(err, ErrNoWorkspace) {
			return nil, err
		}
		// An unprompted turn never joins work the owner has open. It shares a
		// thread with them (proactive output is one channel), so without this
		// a heartbeat would adopt whatever branch the owner left mid-change
		// and propose it as its own.
		if IsUnpromptedTurn(ctx) && binding.Writable && binding.Change != "" {
			change, err := changes.Load(ctx, binding.Change)
			if err != nil {
				return nil, err
			}
			if !change.Unprompted {
				return nil, ErrOwnerChangeInProgress
			}
		}
		// Editing an already-branched checkout is a no-op rather than an
		// error: the model asking twice in a thread should keep working in
		// the branch it already has, not start a second one.
		if binding.Writable {
			return json.Marshal(map[string]any{"status": "already_editing", "repository": binding.Repository, "branch": branchOfChange(ctx, changes, binding.Change), "change": binding.Change})
		}
		repositoryName := strings.TrimSpace(input.Repository)
		if repositoryName == "" {
			repositoryName = binding.Repository
		}
		if repositoryName == "" {
			return nil, errors.New("no workspace is attached: pass repository, or call workspace_open first")
		}
		configured, err := lookupRepository(ctx, store, repositoryName)
		if err != nil {
			return nil, err
		}
		adopted, err := workspaces.Adopt(ctx, configured.Name)
		if err != nil {
			return nil, err
		}
		runID := newRunID()
		branch := "eggy/" + runID
		report(ctx, progress, runID, "Creating branch "+branch)
		if err := repository.CreateBranch(ctx, adopted.Path, branch); err != nil {
			return nil, err
		}
		revision, err := repository.WorkspaceRevision(ctx, adopted.Path)
		if err != nil {
			return nil, err
		}
		if revision.Branch != branch {
			return nil, fmt.Errorf("repository created unexpected branch %q", revision.Branch)
		}
		if _, err := changes.Open(ctx, runID, configured.Name, branch, revision.Head); err != nil {
			return nil, err
		}
		if err := workspaces.MarkEditing(ctx, branch, runID); err != nil {
			return nil, err
		}
		guidance, err := repository.Inspect(ctx, adopted.Path)
		if err != nil {
			return nil, err
		}
		result := map[string]any{"status": "editing", "repository": configured.Name, "branch": branch, "change": runID}
		if guidance != "" {
			result["repository_guidance"] = guidance
		}
		return json.Marshal(result)
	}

	propose := repositoryTool{definition: ports.ToolDefinition{
		Name:        "propose_change",
		Description: "Propose the edits currently in this conversation's branched checkout as a pull request: Eggy captures the diff itself, commits, pushes, and opens (or updates) the pull request, then returns its URL. Call it once the change is complete and you have run this repository's own build/test/lint commands via terminal. Report the returned pull-request URL to the owner. You can keep editing afterwards and propose again.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string","minLength":1},"validation":{"type":"string","minLength":1},"commit_message":{"type":"string","minLength":1}},"required":["summary","validation","commit_message"],"additionalProperties":false}`),
	}}
	propose.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Summary       string `json:"summary"`
			Validation    string `json:"validation"`
			CommitMessage string `json:"commit_message"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		for field, value := range map[string]string{"summary": input.Summary, "commit_message": input.CommitMessage} {
			if strings.TrimSpace(value) == "" {
				return nil, errors.New(field + " must not be empty")
			}
		}
		if strings.TrimSpace(input.Validation) == "" {
			return nil, errors.New("validation must not be empty: describe the build/test/lint command you ran and its result")
		}
		if workspaces == nil || changes == nil || transcripts == nil || repository == nil || shipper == nil {
			return nil, errors.New("proposing a change is unavailable")
		}
		binding, err := workspaces.Resolve(ctx)
		if err != nil {
			return nil, err
		}
		if !binding.Writable || binding.Change == "" {
			return nil, ErrWorkspaceNotEditable
		}
		change, err := changes.Load(ctx, binding.Change)
		if err != nil {
			return nil, err
		}
		if IsUnpromptedTurn(ctx) && !change.Unprompted {
			return nil, ErrOwnerChangeInProgress
		}
		// Eggy re-derives the state it is about to ship rather than trusting
		// the model's account of it: the checkout must still be on the branch
		// and HEAD recorded when editing started, or something committed or
		// switched branches behind the approval chain.
		revision, err := repository.WorkspaceRevision(ctx, binding.Path)
		if err != nil {
			return nil, err
		}
		if revision.Branch != change.Branch {
			return nil, fmt.Errorf("the checkout moved from branch %q to %q; nothing was shipped", change.Branch, revision.Branch)
		}
		if revision.Head != change.BaseRevision {
			return nil, errors.New("the checkout's HEAD moved before the change was approved; nothing was shipped")
		}
		report(ctx, progress, change.ID, "Capturing diff and validation evidence")
		diff, err := repository.Diff(ctx, binding.Path)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(diff) == "" {
			return nil, errors.New("there are no changes in this workspace to propose")
		}
		if err := changes.RecordImplementation(ctx, change.ID, diff, input.Validation); err != nil {
			return nil, err
		}
		// An unprompted turn proposes; it does not present finished work. The
		// flag rides in the ship target, so it is inside every payload the
		// commit/push/pull-request approvals are bound to.
		target := ShipTarget{ChangeID: change.ID, Workspace: binding.Path, Transcript: TranscriptOf(ctx), Draft: IsUnpromptedTurn(ctx)}
		if err := transcripts.Milestone(ctx, target.Transcript, "Ready to ship"); err != nil {
			return nil, err
		}
		pr, note, err := shipper.Ship(ctx, target, change.Branch, input.CommitMessage)
		if err != nil {
			return nil, err
		}
		if note != "" {
			// The chain stopped partway (an unavailable capability or a
			// protected branch). That is blocked, and the note is the reason
			// an owner reads on the transcript.
			if err := transcripts.Milestone(ctx, target.Transcript, note); err != nil {
				return nil, err
			}
			if err := changes.SetPhase(ctx, change.ID, ports.PhaseBlocked); err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"status": "partial", "change": change.ID, "branch": change.Branch, "summary": input.Summary, "note": note})
		}
		// Rebase the session's recorded baseline onto the commit just made,
		// so a second round of edits in this same thread verifies against
		// where the branch actually is now rather than where it started.
		after, err := repository.WorkspaceRevision(ctx, binding.Path)
		if err != nil {
			return nil, err
		}
		if err := changes.Rebase(ctx, change.ID, after.Head); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{
			"status": "shipped", "change": change.ID, "branch": change.Branch,
			"summary": input.Summary, "validation": input.Validation,
			"pull_request_url": pr.URL, "pull_request_number": pr.Number,
		})
	}

	return []ports.Tool{edit, propose}
}

// branchOfChange reports the branch an already-editing thread is on, best
// effort: the tool result is more useful with it, but not wrong without it.
func branchOfChange(ctx context.Context, changes *Changes, id string) string {
	if id == "" {
		return ""
	}
	change, err := changes.Load(ctx, id)
	if err != nil {
		return ""
	}
	return change.Branch
}

func report(ctx context.Context, progress ports.ProgressReporter, runID, message string) {
	if progress == nil {
		return
	}
	progress(ctx, ports.CodingProgress{Kind: "checkpoint", Message: message, RunID: runID})
}
