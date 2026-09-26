package services

import "context"

type selectedModelKey struct{}

// WithSelectedModel stamps the reasoning-model alias a turn resolved onto its
// ctx alongside the destination and other per-turn state.
// flag. A tool that needs to record which model did the work reads it from
// here rather than taking a model reader in its constructor and asking again:
// the turn has already resolved the alias it is actually running on, and a
// second lookup mid-turn could answer differently if /model landed in between.
func WithSelectedModel(ctx context.Context, alias string) context.Context {
	return context.WithValue(ctx, selectedModelKey{}, alias)
}

// SelectedModelFromContext reports the alias the running turn resolved, or ""
// outside a turn. Empty is the honest answer rather than a fallback to the
// current selection: a caller with no turn has no model to attribute work to,
// and /runs show reports an unrecorded alias as such.
func SelectedModelFromContext(ctx context.Context) string {
	alias, _ := ctx.Value(selectedModelKey{}).(string)
	return alias
}
