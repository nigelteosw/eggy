package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

type ApprovalService struct {
	store     ports.StateStore
	now       func() time.Time
	ttl       time.Duration
	protected map[string]bool
}

func NewApprovalService(store ports.StateStore, now func() time.Time, ttl time.Duration, protectedBranches []string) *ApprovalService {
	protected := make(map[string]bool, len(protectedBranches))
	for _, branch := range protectedBranches {
		protected[branch] = true
	}
	return &ApprovalService{store: store, now: now, ttl: ttl, protected: protected}
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
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		approval, ok := state.Approvals[id]
		if !ok || approval.Status != approvals.Pending {
			return approvals.ErrNotAuthorized
		}
		if !s.now().Before(approval.ExpiresAt) {
			approval.Status = approvals.Expired
			state.Approvals[id] = approval
			return approvals.ErrExpired
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
	return err
}

func (s *ApprovalService) Authorize(ctx context.Context, action approvals.Action, payload any, approvalID string) error {
	if action == approvals.Push {
		if branch := payloadBranch(payload); branch != "" && s.protected[branch] {
			return approvals.ErrProtectedBranch
		}
	}
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
			return approvals.ErrPayloadChanged
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

func payloadBranch(payload any) string {
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	var value struct {
		Branch string `json:"branch"`
	}
	if json.Unmarshal(data, &value) != nil {
		return ""
	}
	return value.Branch
}

func randomID() string {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		panic(errors.New("crypto/rand unavailable"))
	}
	return hex.EncodeToString(data)
}
