package services

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// skillNamePattern mirrors the adapter's own validation (see
// internal/adapters/skills.ValidateName). Duplicated here, like
// repositoryNamePattern in repositories.go, because the kernel stays
// adapter-agnostic and cannot import internal/adapters/skills directly; the
// adapter re-validates independently before touching disk.
var skillNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

type SkillsService struct {
	store     ports.SkillsStore
	state     ports.StateStore
	requester ApprovalRequester
	policy    ports.ApprovalPolicy
	guard     *SecretGuard
}

func NewSkillsService(store ports.SkillsStore, state ports.StateStore, requester ApprovalRequester, policy ports.ApprovalPolicy, guard *SecretGuard) *SkillsService {
	if guard == nil {
		guard = NewSecretGuard(nil)
	}
	return &SkillsService{store: store, state: state, requester: requester, policy: policy, guard: guard}
}

type skillWritePayload struct {
	Name        string
	Description string
	Body        string
}

type skillDeletePayload struct {
	Name string
}

// List returns every installed skill, including disabled ones, annotated
// against the current disabled set.
func (s *SkillsService) List(ctx context.Context) ([]ports.SkillSummary, error) {
	summaries, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	state, err := s.state.Load(ctx)
	if err != nil {
		return nil, err
	}
	for i := range summaries {
		if state.DisabledSkills[summaries[i].Name] {
			summaries[i].Disabled = true
		}
	}
	return summaries, nil
}

// Enabled returns only the skills the current disabled set does not
// exclude — the compact index the agent is steered to consult every turn.
func (s *SkillsService) Enabled(ctx context.Context) ([]ports.SkillSummary, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	enabled := make([]ports.SkillSummary, 0, len(all))
	for _, summary := range all {
		if !summary.Disabled {
			enabled = append(enabled, summary)
		}
	}
	return enabled, nil
}

func (s *SkillsService) Show(ctx context.Context, name string) (ports.Skill, error) {
	if err := validateSkillName(name); err != nil {
		return ports.Skill{}, err
	}
	return s.store.Read(ctx, name)
}

// RequestWrite stages a create-or-update proposal for owner approval; it
// never writes the skill file itself. A skill's body is instructions that
// steer later tool calls, so — unlike memory/user/soul edits — it always
// goes through ApprovalService, whether proposed by the agent or the owner.
// See ExecuteApproved.
func (s *SkillsService) RequestWrite(ctx context.Context, name, description, body string) (approvals.Approval, error) {
	if err := validateSkillName(name); err != nil {
		return approvals.Approval{}, err
	}
	description = strings.TrimSpace(description)
	body = strings.TrimSpace(body)
	if description == "" {
		return approvals.Approval{}, errors.New("description is required")
	}
	if body == "" {
		return approvals.Approval{}, errors.New("content is required")
	}
	if err := s.guard.Validate("skill_description", description); err != nil {
		return approvals.Approval{}, err
	}
	if err := s.guard.Validate("skill_content", body); err != nil {
		return approvals.Approval{}, err
	}
	verb := "Add"
	if _, err := s.store.Read(ctx, name); err == nil {
		verb = "Update"
	}
	payload := skillWritePayload{Name: name, Description: description, Body: body}
	return s.requester.Request(ctx, approvals.SkillWrite, payload, verb+" skill "+name)
}

// RequestDelete stages a removal proposal for owner approval.
func (s *SkillsService) RequestDelete(ctx context.Context, name string) (approvals.Approval, error) {
	if err := validateSkillName(name); err != nil {
		return approvals.Approval{}, err
	}
	if _, err := s.store.Read(ctx, name); err != nil {
		return approvals.Approval{}, err
	}
	return s.requester.Request(ctx, approvals.SkillDelete, skillDeletePayload{Name: name}, "Remove skill "+name)
}

// ExecuteApproved performs the write or delete, but only after the
// independent approval policy authorizes this exact payload. It implements
// ApprovalExecutor.
func (s *SkillsService) ExecuteApproved(ctx context.Context, approval approvals.Approval) (any, error) {
	switch approval.Action {
	case approvals.SkillWrite:
		var payload skillWritePayload
		if err := json.Unmarshal(approval.Payload, &payload); err != nil {
			return nil, err
		}
		if err := s.policy.Authorize(ctx, approvals.SkillWrite, payload, approval.ID); err != nil {
			return nil, err
		}
		if err := s.store.Write(ctx, payload.Name, payload.Description, payload.Body); err != nil {
			return nil, err
		}
		return payload.Name, nil
	case approvals.SkillDelete:
		var payload skillDeletePayload
		if err := json.Unmarshal(approval.Payload, &payload); err != nil {
			return nil, err
		}
		if err := s.policy.Authorize(ctx, approvals.SkillDelete, payload, approval.ID); err != nil {
			return nil, err
		}
		if err := s.store.Delete(ctx, payload.Name); err != nil {
			return nil, err
		}
		return payload.Name, nil
	default:
		return nil, errors.New("approval is not a skills action")
	}
}

// Disable and Enable toggle whether a skill is surfaced to the agent.
// Unlike RequestWrite/RequestDelete, neither touches the skill's content —
// disabling only removes it from the steering index, reversibly — so
// neither carries an approval gate.
func (s *SkillsService) Disable(ctx context.Context, name string) error {
	if err := validateSkillName(name); err != nil {
		return err
	}
	if _, err := s.store.Read(ctx, name); err != nil {
		return err
	}
	state, err := s.state.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.state.Update(ctx, state.Version, func(state *ports.State) error {
		if state.DisabledSkills == nil {
			state.DisabledSkills = map[string]bool{}
		}
		state.DisabledSkills[name] = true
		return nil
	})
	return err
}

func (s *SkillsService) Enable(ctx context.Context, name string) error {
	if err := validateSkillName(name); err != nil {
		return err
	}
	state, err := s.state.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.state.Update(ctx, state.Version, func(state *ports.State) error {
		delete(state.DisabledSkills, name)
		return nil
	})
	return err
}

func validateSkillName(name string) error {
	if !skillNamePattern.MatchString(name) {
		return errors.New("skill name must be 1-64 lowercase letters, digits, or hyphens")
	}
	return nil
}
