package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"log/slog"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

// ErrNoWorkspace is returned by a primitive tool when neither an
// implementation run nor the calling thread has a workspace attached.
var ErrNoWorkspace = errors.New("no workspace is attached to this session: call workspace_open with a configured repository first")

// ErrWorkspaceReadOnly is returned by the write primitives when the
// thread's workspace is still an inspection checkout with no branch. It is
// a *result*, not an absence: patch and write_file stay in the model's tool
// list either way, so the model learns why the edit was refused instead of
// silently losing the capability.
var ErrWorkspaceReadOnly = errors.New("this workspace is read-only: it is an inspection checkout with no branch, so edits cannot be shipped. Call workspace_edit first to branch it")

// WorkspaceBinding is the checkout a session's primitive tools act on.
type WorkspaceBinding struct {
	Repository string
	Path       string
	// Writable is true once workspace_edit has branched the checkout.
	Writable bool
	// Change is the Change these edits belong to, empty until the checkout
	// is branched. propose_change ships it.
	Change string
}

// WorkspaceSessions owns the mapping from a conversation thread to the
// checkout its primitive tools read and write. It is the single source of
// workspace truth for read_file, terminal, patch, and write_file: the
// primitives never take a repository argument and never clone per call.
//
// The binding lives on ports.ThreadStore rather than in memory, so an open
// workspace survives a restart: exploration accumulates across a deploy
// instead of being reaped on shutdown. Recover reconciles those records
// against what actually exists on disk at boot.
//
// There is exactly one resolution source: the calling thread. An
// implementation run does not bring its own checkout — it branches the
// thread's, so inspect -> edit -> discuss is one continuous transcript
// against one directory, with no lane transition and no re-clone.
type WorkspaceSessions struct {
	store    ports.StateStore
	threads  ports.ThreadStore
	runner   ports.Runner
	checkout ports.RepositoryCheckout
	newID    func() string
	now      func() time.Time
	logger   *slog.Logger
}

func NewWorkspaceSessions(store ports.StateStore, threads ports.ThreadStore, runner ports.Runner, checkout ports.RepositoryCheckout, newID func() string, now func() time.Time, logger *slog.Logger) *WorkspaceSessions {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &WorkspaceSessions{store: store, threads: threads, runner: runner, checkout: checkout, newID: newID, now: now, logger: logger}
}

// Resolve returns the workspace the current turn's primitives act on.
func (s *WorkspaceSessions) Resolve(ctx context.Context) (WorkspaceBinding, error) {
	if s.threads == nil {
		return WorkspaceBinding{}, ErrNoWorkspace
	}
	thread, found, err := s.threads.GetThread(ctx, destination.FromContext(ctx).ConversationID())
	if err != nil {
		return WorkspaceBinding{}, err
	}
	if !found || thread.Workspace == "" {
		return WorkspaceBinding{}, ErrNoWorkspace
	}
	return bindingOf(thread), nil
}

func bindingOf(thread ports.Thread) WorkspaceBinding {
	return WorkspaceBinding{Repository: thread.WorkspaceRepository, Path: thread.Workspace, Writable: thread.WorkspaceBranch != "", Change: thread.ChangeID}
}

// Adopt returns the checkout editing should happen in. When the thread
// already has repositoryName open, that same checkout is reused — the edits
// land in what the owner was just reading, instead of paying for a second
// clone of the same repository. Any other state (no workspace, or one for a
// different repository) opens a fresh checkout.
func (s *WorkspaceSessions) Adopt(ctx context.Context, repositoryName string) (WorkspaceBinding, error) {
	if s.threads != nil {
		thread, found, err := s.threads.GetThread(ctx, destination.FromContext(ctx).ConversationID())
		if err != nil {
			return WorkspaceBinding{}, err
		}
		// Only a clean inspection clone is adopted: a checkout already on a
		// previous session's branch would carry that session's uncommitted
		// work into this one's diff.
		if found && thread.Workspace != "" && thread.WorkspaceRepository == repositoryName && thread.WorkspaceBranch == "" {
			return bindingOf(thread), nil
		}
	}
	return s.Open(ctx, repositoryName)
}

// MarkEditing records the branch created in the thread's checkout and the
// change its edits belong to, which is what the write primitives resolve as
// writable. Passing an empty branch demotes the checkout back to an
// inspection clone.
func (s *WorkspaceSessions) MarkEditing(ctx context.Context, branch, changeID string) error {
	if s.threads == nil {
		return ErrNoWorkspace
	}
	return s.threads.SetWorkspaceEdit(ctx, destination.FromContext(ctx).ConversationID(), branch, changeID)
}

// Open clones repositoryName into a checkout attached to the calling
// thread and keeps it until workspace_close, so successive greps and reads
// accumulate against one checkout instead of paying a clone per call.
func (s *WorkspaceSessions) Open(ctx context.Context, repositoryName string) (WorkspaceBinding, error) {
	if s.runner == nil || s.checkout == nil || s.newID == nil || s.threads == nil {
		return WorkspaceBinding{}, errors.New("workspaces are unavailable")
	}
	repository, err := lookupRepository(ctx, s.store, repositoryName)
	if err != nil {
		return WorkspaceBinding{}, err
	}
	dest := destination.FromContext(ctx)
	if err := s.Close(ctx); err != nil {
		return WorkspaceBinding{}, err
	}
	path, err := s.runner.Create(ctx, "workspace-"+s.newID())
	if err != nil {
		return WorkspaceBinding{}, err
	}
	if err := s.checkout.Clone(ctx, repository, path); err != nil {
		_ = s.runner.Destroy(context.WithoutCancel(ctx), path)
		return WorkspaceBinding{}, err
	}
	if err := s.threads.AttachWorkspace(ctx, dest.ConversationID(), dest.Kind, repository.Name, path, s.now()); err != nil {
		// The record is the source of truth; an unrecorded checkout would
		// be an orphan no reaper could ever find.
		_ = s.runner.Destroy(context.WithoutCancel(ctx), path)
		return WorkspaceBinding{}, err
	}
	return WorkspaceBinding{Repository: repository.Name, Path: path}, nil
}

// Close destroys the calling thread's checkout, if it has one. Closing a
// thread with no workspace open is not an error.
func (s *WorkspaceSessions) Close(ctx context.Context) error {
	if s.threads == nil {
		return nil
	}
	return s.closeThread(ctx, destination.FromContext(ctx).ConversationID())
}

// closeThread detaches the record before destroying the directory: if the
// destroy fails, the thread is still correctly reported as having no
// workspace rather than pointing at a checkout that may be half-removed.
func (s *WorkspaceSessions) closeThread(ctx context.Context, threadID string) error {
	thread, found, err := s.threads.GetThread(ctx, threadID)
	if err != nil {
		return err
	}
	if !found || thread.Workspace == "" {
		return nil
	}
	if err := s.threads.DetachWorkspace(ctx, threadID); err != nil {
		return err
	}
	return s.runner.Destroy(ctx, thread.Workspace)
}

// Recover reconciles the durable thread -> checkout bindings against what
// is actually on disk at boot. A record whose directory is gone (a wiped
// volume, a manual cleanup) is dropped rather than resurrected, so a
// primitive never resolves onto a path that no longer exists. Records whose
// directory survived are left attached: that is the point of persisting
// them. Returns how many stale bindings were dropped.
func (s *WorkspaceSessions) Recover(ctx context.Context) (int, error) {
	if s.threads == nil {
		return 0, nil
	}
	attached, err := s.threads.ThreadsWithWorkspace(ctx)
	if err != nil {
		return 0, err
	}
	probe, canProbe := s.runner.(ports.WorkspaceProbe)
	dropped := 0
	for _, thread := range attached {
		exists := false
		if canProbe {
			exists, err = probe.Exists(ctx, thread.Workspace)
			if err != nil {
				return dropped, err
			}
		}
		// Without a probe, a binding cannot be verified, so it cannot be
		// trusted: drop it and make the thread re-open explicitly.
		if exists {
			continue
		}
		if err := s.threads.DetachWorkspace(ctx, thread.ID); err != nil {
			return dropped, err
		}
		s.logger.Info("dropped stale thread workspace binding", "thread_id", thread.ID, "workspace", thread.Workspace, "repository", thread.WorkspaceRepository)
		dropped++
	}
	return dropped, nil
}

// CleanupIdle destroys the checkout of every thread whose last activity is
// older than cutoff. Making workspaces durable is what makes this
// necessary: nothing else would ever reap a thread the owner simply
// stopped talking to. Returns how many were reaped.
func (s *WorkspaceSessions) CleanupIdle(ctx context.Context, cutoff time.Time) (int, error) {
	if s.threads == nil {
		return 0, nil
	}
	attached, err := s.threads.ThreadsWithWorkspace(ctx)
	if err != nil {
		return 0, err
	}
	reaped := 0
	for _, thread := range attached {
		if !thread.UpdatedAt.Before(cutoff) {
			continue
		}
		if err := s.closeThread(ctx, thread.ID); err != nil {
			return reaped, err
		}
		s.logger.Info("reaped idle thread workspace", "thread_id", thread.ID, "workspace", thread.Workspace, "idle_since", thread.UpdatedAt)
		reaped++
	}
	return reaped, nil
}

// Tools returns the thread-scoped workspace lifecycle tools. They are
// ordinary non-primitive tools: they attach and detach the checkout the
// primitives resolve, and never read or write its contents themselves.
func (s *WorkspaceSessions) Tools() []ports.Tool {
	open := repositoryTool{definition: ports.ToolDefinition{
		Name:        "workspace_open",
		Description: "Attach a read-only checkout of a configured repository to this conversation so read_file and terminal can explore it. The checkout persists across turns and across restarts until workspace_close; it creates no branch, commit, or approval. Call workspace_edit afterwards when you need to change files.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1}},"required":["repository"],"additionalProperties":false}`),
	}}
	open.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Repository string `json:"repository"`
		}
		if err := services.DecodeToolInput(raw, &input); err != nil {
			return nil, err
		}
		binding, err := s.Open(ctx, input.Repository)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"status": "open", "repository": binding.Repository, "writable": binding.Writable})
	}

	closeTool := repositoryTool{definition: ports.ToolDefinition{
		Name:        "workspace_close",
		Description: "Detach and destroy this conversation's read-only checkout. Call it when repository exploration is finished.",
		Schema:      json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}}
	closeTool.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		if err := services.DecodeToolInput(raw, &struct{}{}); err != nil {
			return nil, err
		}
		if err := s.Close(ctx); err != nil {
			return nil, fmt.Errorf("close workspace: %w", err)
		}
		return json.Marshal(map[string]string{"status": "closed"})
	}

	return []ports.Tool{open, closeTool}
}
