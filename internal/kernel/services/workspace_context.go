package services

import "context"

type workspaceContextKey struct{}

// withWorkspace attaches the active implementation run's workspace
// directory to ctx so tools registered once at bootstrap can resolve it
// per call, instead of being closured over one specific workspace.
func withWorkspace(ctx context.Context, workspace string) context.Context {
	return context.WithValue(ctx, workspaceContextKey{}, workspace)
}

func workspaceFromContext(ctx context.Context) (string, bool) {
	workspace, ok := ctx.Value(workspaceContextKey{}).(string)
	return workspace, ok && workspace != ""
}
