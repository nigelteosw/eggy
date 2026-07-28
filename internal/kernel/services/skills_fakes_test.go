package services

import (
	"context"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
)

// fakeShippingGateway satisfies ApprovalRequester and ports.ApprovalPolicy.
// The repo package keeps its own copy for the same reason its store fakes are
// copies: a test fake is not API worth exporting a package for.
type fakeShippingGateway struct {
	payload    any
	authorized approvals.Action
}

func (g *fakeShippingGateway) Request(_ context.Context, action approvals.Action, payload any, summary string) (approvals.Approval, error) {
	g.payload = payload
	return approvals.Approval{ID: "approval", Action: action, Summary: summary}, nil
}

func (g *fakeShippingGateway) RequestAndApprove(_ context.Context, action approvals.Action, payload any, summary string) (approvals.Approval, error) {
	g.payload = payload
	return approvals.Approval{ID: "approval", Action: action, Summary: summary, Status: approvals.Approved}, nil
}

func (g *fakeShippingGateway) Authorize(_ context.Context, action approvals.Action, payload any, id string) error {
	g.authorized = action
	return nil
}
