package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// WatchList is the slice of the context store the panel needs: read the watch
// list, and replace it wholesale.
//
// Wholesale, not entry at a time, because this is the owner editing a document
// they can see in full -- the same reason ReplaceDocument exists for the
// heartbeat's own rewrite. The agent's entry-addressed tools stay the right
// shape for the agent, which edits a list it cannot see.
//
// This is the one context document the panel writes. Memory and user are the
// agent's own record and are written by the turns that learn something; soul
// is edited outside Eggy. The watch list is different in kind: it is an
// instruction to Eggy, and until now the only way to give it was to ask the
// agent to write it down for itself.
type WatchList interface {
	Load(ctx context.Context) (ports.AgentContext, error)
	ReplaceDocument(ctx context.Context, document ports.ContextDocument, content string) error
}

// newWatchGetRoute hands back the list as stored, so the textarea the owner
// edits holds the same bytes the heartbeat reads -- including whatever the
// last beat annotated onto it.
func newWatchGetRoute(watch WatchList) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if watch == nil {
			writeWebError(w, http.StatusNotFound, "no watch list is available")
			return
		}
		agentContext, err := watch.Load(r.Context())
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Fields: []webField{{Label: "watch", Value: agentContext.Watch}}})
	}
}

// newWatchSetRoute replaces the list.
//
// No restart is acknowledged, unlike every config write: the heartbeat reads
// the store on each tick, so the next beat already sees this. Saving an empty
// list is allowed and is how an owner turns the heartbeat off without
// unsetting the interval -- an empty watch list is the skip the daemon
// already implements, so the acknowledgement says so rather than letting the
// owner conclude the save failed.
func newWatchSetRoute(watch WatchList) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if watch == nil {
			writeWebError(w, http.StatusNotFound, "no watch list is available")
			return
		}
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := watch.ReplaceDocument(r.Context(), ports.ContextWatch, body.Content); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		result := webResult{State: webSuccess, Title: "Saved watch list."}
		if watchIsEmpty(body.Content) {
			result.Detail = "The list is empty, so the heartbeat will skip every tick until something is on it."
		}
		writeWebResult(w, result)
	}
}

// watchIsEmpty mirrors the daemon's own emptiness rule (App.watchListIsEmpty):
// blank lines and Markdown headings are not entries. The panel must agree with
// the runtime about what counts, or it would tell the owner their heartbeat is
// armed while the daemon skips every beat.
func watchIsEmpty(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return false
	}
	return true
}
