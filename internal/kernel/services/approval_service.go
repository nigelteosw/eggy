package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

type ApprovalService struct {
	store ports.StateStore
	now   func() time.Time
	ttl   time.Duration
	// defaultMode is what config.yaml asked for, used only until the owner
	// chooses at runtime. A stored choice always wins: config is where the
	// deployment starts, not a standing instruction that overrides the person
	// operating it.
	defaultMode ports.ApprovalMode
}

func NewApprovalService(store ports.StateStore, now func() time.Time, ttl time.Duration, defaultMode ports.ApprovalMode) *ApprovalService {
	if !defaultMode.Valid() {
		defaultMode = ports.ModeNormal
	}
	return &ApprovalService{store: store, now: now, ttl: ttl, defaultMode: defaultMode}
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
	slices.SortFunc(pending, func(a, b approvals.Approval) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return pending, nil
}

// Mode reports how much the owner is currently being asked.
//
// The stored mode wins, the configured default fills in for state written
// before modes existed, and the retired boolean is honoured on the way past:
// an owner who left the old bypass on must not have the gate come back on
// under them because a field was renamed.
func (s *ApprovalService) Mode(ctx context.Context) (ports.ApprovalMode, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return "", err
	}
	if state.ApprovalMode.Valid() {
		return state.ApprovalMode, nil
	}
	if state.ApprovalAutoMode {
		return ports.ModeAuto, nil
	}
	return s.defaultMode, nil
}

// SetMode changes it durably. It lives here rather than on a surface because
// the gate, the approval records, and this switch are one authority: a second
// place that decided "does this need approval" would be a second answer to the
// question the gate exists to answer.
func (s *ApprovalService) SetMode(ctx context.Context, mode ports.ApprovalMode) error {
	if !mode.Valid() {
		return fmt.Errorf("approval mode %q must be strict, normal or auto", mode)
	}
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		state.ApprovalMode = mode
		// Cleared as it is superseded, so the retired field cannot come back
		// later and contradict an explicit choice.
		state.ApprovalAutoMode = false
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
