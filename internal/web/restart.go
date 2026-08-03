package web

import (
	"net/http"

	"github.com/nigelteosw/eggy/internal/commands"
)

// restartToApply is the one detail line every config write here ends with.
// Adapters are built once at startup, so a saved section is stored but not
// yet live; naming both ways to finish the job keeps the notice from being a
// dead end on whichever surface the owner is already holding.
const restartToApply = "Restart Eggy for this to take effect: use Advanced -> Restart, or send /restart in chat."

// newRestartHandler is the panel's half of /restart. It is a POST with no
// body for the same reason /restart takes no argument: there is one thing to
// ask for, and the answer says what happened. The decision itself -- including
// the pre-flight that refuses a config which would not load -- lives in
// internal/commands, so the button and the command cannot disagree about when
// a restart is safe.
func newRestartHandler(restarter commands.Restarter, configPath string, getenv func(string) string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		message, restarting := commands.Restart(restarter, configPath, getenv)
		if !restarting {
			// A rejected restart is the useful one: the body is why the
			// config would not have started, which is what the owner edits
			// against.
			writeWebError(w, http.StatusBadRequest, message)
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Restarting.", Detail: message})
	}
}
