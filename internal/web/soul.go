package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// newSoulGetRoute hands back SOUL.md as a turn would read it: the owner's
// file, or Eggy's built-in soul when there is none.
func newSoulGetRoute(documents ContextDocuments) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if documents == nil {
			writeWebError(w, http.StatusNotFound, "SOUL.md is unavailable")
			return
		}
		agentContext, err := documents.Load(r.Context())
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Fields: []webField{{Label: "soul", Value: agentContext.Soul}}})
	}
}

// newSoulSetRoute replaces SOUL.md. Like the watch list it needs no restart:
// every turn loads the soul afresh. Saving nothing is the reset -- the store
// reads a blank soul as the built-in one -- and says so, so an owner who
// cleared the box does not conclude Eggy now has no identity.
func newSoulSetRoute(documents ContextDocuments) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if documents == nil {
			writeWebError(w, http.StatusNotFound, "SOUL.md is unavailable")
			return
		}
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := documents.ReplaceDocument(r.Context(), ports.ContextSoul, body.Content); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		result := webResult{State: webSuccess, Title: "Saved soul."}
		if strings.TrimSpace(body.Content) == "" {
			result.Detail = "Reset to Eggy's built-in soul."
		}
		writeWebResult(w, result)
	}
}
