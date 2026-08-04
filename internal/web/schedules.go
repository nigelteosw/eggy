package web

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ScheduleDirectory is the slice of the scheduler the panel needs: what
// exists, and removing one. Creating a schedule stays conversational --
// "every weekday at nine, check the deploy" is a sentence, and a cron
// expression field would be a worse way to say it. What the panel adds is the
// half a conversation is bad at: seeing all of them at once, and deleting the
// one that was a mistake.
type ScheduleDirectory interface {
	List(ctx context.Context) ([]ports.Schedule, error)
	Remove(ctx context.Context, id string) error
}

// newScheduleListHandler answers with the same table shape every other list
// route uses, id first, so the app renders it with the component it already
// has and the delete button has a key to send back.
func newScheduleListHandler(schedules ScheduleDirectory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		headers := []string{"id", "kind", "instruction", "expression", "next_run"}
		if schedules == nil {
			writeWebResult(w, webResult{State: webSuccess, TableHeaders: headers})
			return
		}
		all, err := schedules.List(r.Context())
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// By next run, so the list reads as a timeline. A schedule with no
		// next run sorts to the top, which is where something that has
		// stopped firing belongs.
		slices.SortFunc(all, func(a, b ports.Schedule) int { return a.NextRun.Compare(b.NextRun) })
		rows := make([][]string, 0, len(all))
		for _, schedule := range all {
			// Execution is folded into kind rather than given a column of its
			// own: "recurring reminder" is what the owner thinks they made,
			// and two columns holding "recurring" and "message" is the
			// storage shape leaking into the answer.
			kind := string(schedule.Kind)
			if schedule.Execution == ports.ScheduleExecutionMessage {
				kind += " reminder"
			}
			rows = append(rows, []string{
				schedule.ID, kind, schedule.Instruction, schedule.Expression, formatScheduleTime(schedule.NextRun),
			})
		}
		writeWebResult(w, webResult{State: webSuccess, TableHeaders: headers, TableRows: rows})
	}
}

func newScheduleDeleteHandler(schedules ScheduleDirectory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if schedules == nil {
			writeWebError(w, http.StatusNotFound, "no scheduler is running")
			return
		}
		// Removing a schedule that is already gone is not an error: the owner
		// asked for it not to run, and it will not. The same rule the cancel
		// tool follows, so the two surfaces cannot disagree about what a
		// second delete means.
		if err := schedules.Remove(r.Context(), r.PathValue("id")); err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Schedule cancelled."})
	}
}

// formatScheduleTime renders a zero time as empty rather than as year 1, so
// a schedule with no next run is blank instead of showing a date the app
// would have to know to hide.
func formatScheduleTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}
