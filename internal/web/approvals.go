package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// ApprovalDirectory is the read half of approvals as the panel sees it.
// Deciding one is not here on purpose: POST /api/chat/approve already exists
// and enqueues the decision through the same event path a Telegram tap takes,
// so the panel gets a second view onto one mechanism rather than a second way
// to approve something.
type ApprovalDirectory interface {
	Pending(ctx context.Context) ([]approvals.Approval, error)
}

// ApprovalModeSwitch is which of the three modes is in force. Unlike deciding
// an approval, this is a setting rather than an event, so the panel writes it
// directly -- through the same approval authority /mode writes, which is what
// keeps the two surfaces from holding different answers.
type ApprovalModeSwitch interface {
	Mode(ctx context.Context) (ports.ApprovalMode, error)
	SetMode(ctx context.Context, mode ports.ApprovalMode) error
}

// newApprovalModeHandler reads the mode on GET and sets it on POST.
//
// POST names the mode it wants rather than advancing to the next one. A toggle
// was the right shape for two states; with three, "next" is a way to land in
// auto without having asked for it, and a panel and a phone advancing from
// different starting points would disagree about where they ended up.
func newApprovalModeHandler(gate ApprovalModeSwitch, set bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gate == nil {
			writeWebError(w, http.StatusNotFound, "approvals are unavailable")
			return
		}
		mode, err := gate.Mode(r.Context())
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if set {
			requested := ports.ApprovalMode(strings.ToLower(strings.TrimSpace(r.FormValue("mode"))))
			if !requested.Valid() {
				writeWebError(w, http.StatusBadRequest, "mode must be strict, normal or auto")
				return
			}
			if err := gate.SetMode(r.Context(), requested); err != nil {
				writeWebError(w, http.StatusInternalServerError, err.Error())
				return
			}
			mode = requested
		}
		writeWebResult(w, webResult{
			State: webSuccess, Title: commands.ModeMessage(mode),
			Fields: []webField{{Label: "approval_mode", Value: string(mode)}},
		})
	}
}

// newApprovalListHandler answers with the same table shape the other list
// routes use, id first so the approve and reject buttons have a key.
//
// An approval past its window is reported as expired rather than hidden. It
// stays Pending in state, which is what the status tool counts, so hiding it
// would leave the owner with a count matching nothing they can see -- the
// exact confusion that "1 approval waiting" and no way to inspect it caused.
func newApprovalListHandler(directory ApprovalDirectory, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		headers := []string{"id", "action", "summary", "state", "requested"}
		if directory == nil {
			writeWebResult(w, webResult{State: webSuccess, TableHeaders: headers})
			return
		}
		pending, err := directory.Pending(r.Context())
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rows := make([][]string, 0, len(pending))
		for _, approval := range pending {
			state := "waiting"
			if !now().Before(approval.ExpiresAt) {
				state = "expired"
			}
			rows = append(rows, []string{
				approval.ID, string(approval.Action), approval.Summary, state,
				approval.CreatedAt.UTC().Format(time.RFC3339),
			})
		}
		writeWebResult(w, webResult{State: webSuccess, TableHeaders: headers, TableRows: rows})
	}
}
