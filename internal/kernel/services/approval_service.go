package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

type ApprovalService struct {
	store ports.StateStore
	now   func() time.Time
	ttl   time.Duration
}

func NewApprovalService(store ports.StateStore, now func() time.Time, ttl time.Duration) *ApprovalService {
	return &ApprovalService{store: store, now: now, ttl: ttl}
}

func (s *ApprovalService) Request(ctx context.Context, action approvals.Action, payload any, summary string) (approvals.Approval, error) {
	canonical, digest, err := canonicalPayload(payload)
	if err != nil {
		return approvals.Approval{}, err
	}
	now := s.now()
	approval := approvals.Approval{
		ID: randomID(), Action: action, PayloadDigest: digest, Payload: canonical, Summary: summary,
		Status: approvals.Pending, CreatedAt: now, ExpiresAt: now.Add(s.ttl),
		Destination: destination.FromContext(ctx),
	}
	state, err := s.store.Load(ctx)
	if err != nil {
		return approvals.Approval{}, err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		if state.Approvals == nil {
			state.Approvals = map[string]approvals.Approval{}
		}
		state.Approvals[approval.ID] = approval
		return nil
	})
	return approval, err
}

func (s *ApprovalService) Decide(ctx context.Context, id string, approved bool) error {
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	// The expiry transition is committed and the error returned afterwards,
	// rather than returned from the mutation. A store discards the whole
	// update when the mutation errors, so marking the record Expired and
	// returning ErrExpired in the same breath threw the mark away: the
	// approval stayed Pending, deciding it failed again next time, and the
	// count of "approvals waiting" could only ever grow. Nothing else can
	// retire one, since Decide is the only path that checks the window.
	expired := false
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		approval, ok := state.Approvals[id]
		if !ok || approval.Status != approvals.Pending {
			return approvals.ErrNotAuthorized
		}
		if !s.now().Before(approval.ExpiresAt) {
			approval.Status = approvals.Expired
			state.Approvals[id] = approval
			expired = true
			return nil
		}
		if approved {
			approval.Status = approvals.Approved
		} else {
			approval.Status = approvals.Rejected
		}
		approval.DecidedAt = s.now()
		state.Approvals[id] = approval
		return nil
	})
	if err != nil {
		return err
	}
	if expired {
		return approvals.ErrExpired
	}
	return nil
}

// Pending is every approval still awaiting a decision, oldest first.
//
// Expired ones are included rather than filtered out. An approval whose
// window has closed still sits in state as Pending -- that is what the status
// tool counts -- so hiding it here would leave the owner staring at a count
// they cannot reconcile with any list. Reporting it and letting the caller
// see ExpiresAt is what makes a stale approval explicable instead of a
// phantom.
func (s *ApprovalService) Pending(ctx context.Context) ([]approvals.Approval, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	pending := make([]approvals.Approval, 0, len(state.Approvals))
	for _, approval := range state.Approvals {
		if approval.Status == approvals.Pending {
			pending = append(pending, approval)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].CreatedAt.Before(pending[j].CreatedAt) })
	return pending, nil
}

// AutoApprove reports whether approval-gated tool calls currently run without
// asking the owner.
func (s *ApprovalService) AutoApprove(ctx context.Context) (bool, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return false, err
	}
	return state.ApprovalAutoMode, nil
}

// SetAutoApprove turns the bypass on or off. It lives here rather than on a
// surface because the gate, the approval records, and this switch are one
// authority: a second place that decided "does this need approval" would be a
// second answer to the question the gate exists to answer.
func (s *ApprovalService) SetAutoApprove(ctx context.Context, auto bool) error {
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		state.ApprovalAutoMode = auto
		return nil
	})
	return err
}

// Invalidate marks a pending approval unusable without changing approvals
// that were already decided or consumed.
func (s *ApprovalService) Invalidate(ctx context.Context, id string) error {
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		approval, ok := state.Approvals[id]
		if !ok || approval.Status != approvals.Pending {
			return approvals.ErrNotAuthorized
		}
		approval.Status, approval.DecidedAt = approvals.Invalidated, s.now()
		state.Approvals[id] = approval
		return nil
	})
	return err
}

func (s *ApprovalService) Authorize(ctx context.Context, action approvals.Action, payload any, approvalID string) error {
	_, digest, err := canonicalPayload(payload)
	if err != nil {
		return err
	}
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		approval, ok := state.Approvals[approvalID]
		if !ok || approval.Action != action || approval.Status != approvals.Approved {
			return approvals.ErrNotAuthorized
		}
		if !s.now().Before(approval.ExpiresAt) {
			approval.Status = approvals.Expired
			state.Approvals[approvalID] = approval
			return approvals.ErrExpired
		}
		if approval.PayloadDigest != digest {
			return approvals.ErrPayloadMismatch
		}
		approval.Status = approvals.Used
		state.Approvals[approvalID] = approval
		return nil
	})
	return err
}

func canonicalPayload(payload any) (json.RawMessage, string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("encode approval payload: %w", err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, "", fmt.Errorf("canonicalize approval payload: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(sum[:]), nil
}

func randomID() string {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		panic(errors.New("crypto/rand unavailable"))
	}
	return hex.EncodeToString(data)
}
