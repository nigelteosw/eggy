package web

import (
	"context"
	"net/http"
	"time"

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
