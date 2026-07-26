package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

// ErrNoWorkspace is returned by a primitive tool when neither an
// implementation run nor the calling thread has a workspace attached.
var ErrNoWorkspace = errors.New("no workspace is attached to this session: call workspace_open with a configured repository first")

// ErrWorkspaceReadOnly is returned by the write primitives when the
// session's workspace is an inspection checkout rather than an
// implementation run's branch. It is a *result*, not an absence: patch and
// write_file stay in the model's tool list either way, so the model learns
// why the edit was refused instead of silently losing the capability.
var ErrWorkspaceReadOnly = errors.New("this workspace is read-only: it is an inspection checkout with no branch, so edits cannot be shipped. Use repository_modify to make changes")

// WorkspaceBinding is the checkout a session's primitive tools act on.
type WorkspaceBinding struct {
	Repository string
	Path       string
	// Writable is true only for an implementation run's branched checkout.
	Writable bool
}

// WorkspaceSessions owns the mapping from a conversation thread to the
// checkout its primitive tools read and write. It is the single source of
// workspace truth for read_file, terminal, patch, and write_file: the
// primitives never take a repository argument and never clone per call.
//
// Resolution order is run first, then thread: while an implementation run
// is executing, its own branched workspace wins, so the run's tools act on
// the branch being shipped rather than on whatever the thread happened to
// have open.
type WorkspaceSessions struct {
	store    ports.StateStore
	runner   ports.Runner
	checkout ports.RepositoryCheckout
	newID    func() string

	mu    sync.RWMutex
	bound map[string]WorkspaceBinding
}

func NewWorkspaceSessions(store ports.StateStore, runner ports.Runner, checkout ports.RepositoryCheckout, newID func() string) *WorkspaceSessions {
	return &WorkspaceSessions{store: store, runner: runner, checkout: checkout, newID: newID, bound: map[string]WorkspaceBinding{}}
}

// Resolve returns the workspace the current turn's primitives act on.
func (s *WorkspaceSessions) Resolve(ctx context.Context) (WorkspaceBinding, error) {
	if workspace, ok := workspaceFromContext(ctx); ok {
		return WorkspaceBinding{Path: workspace, Writable: true}, nil
	}
	s.mu.RLock()
	binding, ok := s.bound[destination.FromContext(ctx).ConversationID()]
	s.mu.RUnlock()
	if !ok {
		return WorkspaceBinding{}, ErrNoWorkspace
	}
	return binding, nil
}

// Open clones repositoryName into a checkout attached to the calling
// thread and keeps it until workspace_close, so successive greps and reads
// accumulate against one checkout instead of paying a clone per call.
func (s *WorkspaceSessions) Open(ctx context.Context, repositoryName string) (WorkspaceBinding, error) {
	if s.runner == nil || s.checkout == nil || s.newID == nil {
		return WorkspaceBinding{}, errors.New("workspaces are unavailable")
	}
	repository, err := lookupRepository(ctx, s.store, repositoryName)
	if err != nil {
		return WorkspaceBinding{}, err
	}
	conversationID := destination.FromContext(ctx).ConversationID()
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
	binding := WorkspaceBinding{Repository: repository.Name, Path: path}
	s.mu.Lock()
	s.bound[conversationID] = binding
	s.mu.Unlock()
	return binding, nil
}

// Close destroys the calling thread's checkout, if it has one. Closing a
// thread with no workspace open is not an error.
func (s *WorkspaceSessions) Close(ctx context.Context) error {
	conversationID := destination.FromContext(ctx).ConversationID()
	s.mu.Lock()
	binding, ok := s.bound[conversationID]
	delete(s.bound, conversationID)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return s.runner.Destroy(ctx, binding.Path)
}

// CloseAll destroys every attached checkout, for process shutdown.
func (s *WorkspaceSessions) CloseAll(ctx context.Context) error {
	s.mu.Lock()
	bound := s.bound
	s.bound = map[string]WorkspaceBinding{}
	s.mu.Unlock()
	var firstErr error
	for _, binding := range bound {
		if err := s.runner.Destroy(ctx, binding.Path); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Tools returns the thread-scoped workspace lifecycle tools. They are
// ordinary non-primitive tools: they attach and detach the checkout the
// primitives resolve, and never read or write its contents themselves.
func (s *WorkspaceSessions) Tools() []ports.Tool {
	open := repositoryTool{definition: ports.ToolDefinition{
		Name:        "workspace_open",
		Description: "Attach a read-only checkout of a configured repository to this conversation so read_file and terminal can explore it. The checkout persists across turns until workspace_close; it creates no branch, commit, or approval.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1}},"required":["repository"],"additionalProperties":false}`),
	}}
	open.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Repository string `json:"repository"`
		}
		if err := decodeStrict(raw, &input); err != nil {
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
		if err := decodeStrict(raw, &struct{}{}); err != nil {
			return nil, err
		}
		if err := s.Close(ctx); err != nil {
			return nil, fmt.Errorf("close workspace: %w", err)
		}
		return json.Marshal(map[string]string{"status": "closed"})
	}

	return []ports.Tool{open, closeTool}
}
