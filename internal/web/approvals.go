package web

import (
	"context"
	"net/http"
	"time"

	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
)

// ApprovalDirectory is the read half of approvals as the panel sees it.
// Deciding one is not here on purpose: POST /api/chat/approve already exists
// and enqueues the decision through the same event path a Telegram tap takes,
// so the panel gets a second view onto one mechanism rather than a second way
// to approve something.
type ApprovalDirectory interface {
	Pending(ctx context.Context) ([]approvals.Approval, error)
}

// AutoModeSwitch is the approval gate's on/off state. Unlike deciding an
// approval, this is a setting rather than an event, so the panel writes it
// directly -- through the same approval authority /auto writes, which is what
// keeps the two surfaces from holding different answers.
type AutoModeSwitch interface {
	AutoApprove(ctx context.Context) (bool, error)
	SetAutoApprove(ctx context.Context, auto bool) error
}

// newAutoModeHandler reads the switch on GET and flips it on POST. POST takes
// no body: it is a toggle, matching /auto, so the panel and the chat command
// are the same gesture and neither can set a state the other cannot express.
func newAutoModeHandler(gate AutoModeSwitch, toggle bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gate == nil {
			writeWebError(w, http.StatusNotFound, "approvals are unavailable")
			return
		}
		auto, err := gate.AutoApprove(r.Context())
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if toggle {
			auto = !auto
			if err := gate.SetAutoApprove(r.Context(), auto); err != nil {
				writeWebError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeWebResult(w, webResult{
			State: webSuccess, Title: commands.AutoModeMessage(auto),
			Fields: []webField{{Label: "auto_mode", Value: autoModeValue(auto)}},
		})
	}
}

func autoModeValue(auto bool) string {
	if auto {
		return "enabled"
	}
	return "disabled"
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
