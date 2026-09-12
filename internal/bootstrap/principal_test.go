package bootstrap

import (
	"context"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ownerCtx acts as the test deployment's one account ("42"), for tests that
// reach the private stores directly rather than through the dispatcher or
// the session guard that would otherwise put the principal there.
func ownerCtx() context.Context {
	return ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "42"})
}
