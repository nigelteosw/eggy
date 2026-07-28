package services

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
)

// mustMarshal is duplicated in the repo package's tests for the same reason
// its store fakes are: test helpers are not API.
func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type fakePolicy struct {
	actions  []approvals.Action
	payloads []any
	ids      []string
	err      error
}

func (p *fakePolicy) Authorize(_ context.Context, action approvals.Action, payload any, id string) error {
	p.actions = append(p.actions, action)
	p.payloads = append(p.payloads, payload)
	p.ids = append(p.ids, id)
	return p.err
}

func (p *fakePolicy) RequestAndApprove(_ context.Context, action approvals.Action, payload any, summary string) (approvals.Approval, error) {
	return approvals.Approval{ID: "approval", Action: action, Summary: summary, Status: approvals.Approved}, nil
}

// contains is a substring check kept local to the package's tests.
func contains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}

// webThread builds a ctx addressed to a web thread. Duplicated in the repo
// package's tests for the same reason as the store fakes.
func webThread(id string) context.Context {
	return destination.With(context.Background(), destination.Destination{Kind: destination.Web, ThreadID: id})
}
