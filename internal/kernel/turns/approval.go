package turns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/kernel/events"
)

// Approval executes what an owner's approve tap authorized, or reports the
// rejection. The destination is taken from the approval itself, so the
// outcome reaches the surface the approval was issued on.
func (s *Service) Approval(ctx context.Context, decision events.ApprovalDecision) error {
	preState, err := s.Store.Load(ctx)
	if err != nil {
		return err
	}
	ctx = destination.With(ctx, preState.Approvals[decision.ApprovalID].Destination)
	if err := s.Approvals.Decide(ctx, decision.ApprovalID, decision.Approved); err != nil {
		return s.deliverApprovalFailure(ctx, decision.MessageID, err)
	}
	if !decision.Approved {
		return s.Presenter.DeliverOutcome(ctx, decision.MessageID, "Action rejected.")
	}
	state, err := s.Store.Load(ctx)
	if err != nil {
		return s.deliverApprovalFailure(ctx, decision.MessageID, err)
	}
	approval := state.Approvals[decision.ApprovalID]
	executor, ok := s.Executors[approval.Action]
	if !ok {
		return s.deliverApprovalFailure(ctx, decision.MessageID, errors.New("unknown approval action"))
	}
	result, err := executor.ExecuteApproved(ctx, approval)
	if err != nil {
		return s.deliverApprovalFailure(ctx, decision.MessageID, err)
	}
	return s.Presenter.DeliverOutcome(ctx, decision.MessageID, approvalOutcomeText(result))
}

// approvalOutcomeText renders what the owner reads after an approve tap.
//
// A tool that can say what it just did says it in its own result, under
// "summary" -- in the file whoever added the action was already editing, the
// same reason a tool declares its own effect there rather than in a table here.
// Anything else falls back to the record itself, which is right for an MCP tool
// this repository cannot write a sentence for, and wrong enough for the tools
// it can that they should carry a summary.
func approvalOutcomeText(result any) string {
	text, isText := result.(string)
	if !isText {
		return fmt.Sprintf("Approved action completed: %v", result)
	}
	var payload struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err == nil && strings.TrimSpace(payload.Summary) != "" {
		return "Done. " + payload.Summary
	}
	return "Approved action completed: " + text
}

// deliverApprovalFailure tells the owner an approve/reject tap didn't go
// through, instead of leaving execErr to only reach the server log. Without
// this, a tap that produces no visible outcome at all is indistinguishable
// from a broken button, and the owner has no way to learn what actually
// failed. Still returns execErr so the failure remains logged server-side.
func (s *Service) deliverApprovalFailure(ctx context.Context, messageID string, execErr error) error {
	if deliverErr := s.Presenter.DeliverOutcome(ctx, messageID, fmt.Sprintf("Action failed: %v", execErr)); deliverErr != nil {
		return errors.Join(execErr, deliverErr)
	}
	return execErr
}
