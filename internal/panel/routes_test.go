package panel

import "testing"

// Route patterns are checked by net/http only when they are registered, and a
// conflict panics there -- at boot, after a restart, with nothing served. An
// extension's routes are registered only while it is enabled, so a collision
// with a core route stays hidden until an owner switches the extension on.
// Building the handler with every extension running is what finds it here.
// The marketplace's sources route once shared GET /api/config/marketplace with
// the generic settings section and put a deployment in a boot loop.
func TestWebHandlerRegistersEveryExtensionWithoutConflict(t *testing.T) {
	type finance struct{ FinanceService }
	type marketplace struct{ MarketplaceService }
	type agentRunner struct{ AgentRunnerService }
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("registering every extension panicked: %v", recovered)
		}
	}()
	NewWebHandler("", WebUIConfig{
		Finance:     finance{},
		Marketplace: marketplace{},
		AgentRunner: agentRunner{},
	})
}
