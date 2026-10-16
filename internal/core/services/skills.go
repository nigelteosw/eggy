package services

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"regexp"
	"slices"

	"github.com/nigelteosw/eggy/internal/ports"
)

// skillNamePattern mirrors the adapter's own validation (see
// internal/skills.ValidateName). Duplicated here, like
// repositoryNamePattern in repositories.go, because the core stays
// adapter-agnostic and cannot import internal/skills directly; the
// adapter re-validates independently before touching disk.
var skillNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// SkillsService has two faces. The model's face (Enabled, Show) sees only
// usable, enabled skills and is budgeted. The owner's face (List, Inspect,
// Write, SetDisabled, Delete) sees everything, disabled and invalid included.
type SkillsService struct {
	store ports.SkillsStore
}

func NewSkillsService(store ports.SkillsStore) *SkillsService { return &SkillsService{store: store} }

// List is the owner's inventory: every skill file, including disabled and
// invalid ones. It is not what the model sees.
func (s *SkillsService) List(ctx context.Context) ([]ports.SkillSummary, error) {
	return s.store.List(ctx)
}

// Enabled is the index the model sees each turn: usable, enabled skills in
// name order, cut at the index budget. Disabled and invalid entries are
// removed before the budget is applied, so they cannot push active skills out.
func (s *SkillsService) Enabled(ctx context.Context) ([]ports.SkillSummary, error) {
	all, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	// Sort before budgeting so which skills survive the cut never depends on
	// the store's iteration order.
	slices.SortFunc(all, func(a, b ports.SkillSummary) int { return cmp.Compare(a.Name, b.Name) })
	enabled := make([]ports.SkillSummary, 0, len(all))
	indexBytes := 0
	for _, summary := range all {
		if summary.Disabled || summary.Invalid != "" {
			continue
		}
		cost := len(summary.Name) + len(summary.Description) + 2
		if indexBytes+cost > ports.SkillIndexBudget {
			slog.Warn("skill index is at capacity, skipping skill", "skill", summary.Name, "limit", ports.SkillIndexBudget)
			continue
		}
		indexBytes += cost
		enabled = append(enabled, summary)
	}
	return enabled, nil
}

// Show is the model's read, used by skill_read. A disabled skill is refused
// here, so disabling takes effect on the next read, not only the next index.
func (s *SkillsService) Show(ctx context.Context, name string) (ports.Skill, error) {
	skill, err := s.Inspect(ctx, name)
	if err != nil {
		return ports.Skill{}, err
	}
	if skill.Disabled {
		return ports.Skill{}, errors.New("skill " + name + " is disabled")
	}
	return skill, nil
}

// ReadFile is the model's read of one reference file of a directory skill.
// It shares Show's refusal: a disabled skill's files are as unreadable as its
// body, so disabling takes effect on the next read.
func (s *SkillsService) ReadFile(ctx context.Context, name, file string) (string, error) {
	if _, err := s.Show(ctx, name); err != nil {
		return "", err
	}
	return s.store.ReadFile(ctx, name, file)
}

// WriteBundle writes a whole skill, reference files included, after checking
// its name the same way the single-file writes do.
func (s *SkillsService) WriteBundle(ctx context.Context, bundle ports.SkillBundle) error {
	if err := validateSkillName(bundle.Name); err != nil {
		return err
	}
	return s.store.WriteBundle(ctx, bundle)
}

// Inspect reads one skill for the owner, disabled included.
func (s *SkillsService) Inspect(ctx context.Context, name string) (ports.Skill, error) {
	if err := validateSkillName(name); err != nil {
		return ports.Skill{}, err
	}
	return s.store.Read(ctx, name)
}

func (s *SkillsService) Write(ctx context.Context, name, description, body string) error {
	if err := validateSkillName(name); err != nil {
		return err
	}
	return s.store.Write(ctx, name, description, body)
}

func (s *SkillsService) SetDisabled(ctx context.Context, name string, disabled bool) error {
	if err := validateSkillName(name); err != nil {
		return err
	}
	return s.store.SetDisabled(ctx, name, disabled)
}

func (s *SkillsService) Delete(ctx context.Context, name string) error {
	if err := validateSkillName(name); err != nil {
		return err
	}
	return s.store.Delete(ctx, name)
}

func validateSkillName(name string) error {
	if !skillNamePattern.MatchString(name) {
		return errors.New("skill name must be 1-64 lowercase letters, digits, or hyphens")
	}
	return nil
}
