package services

import (
	"context"
	"errors"
	"regexp"

	"github.com/nigelteosw/eggy/internal/ports"
)

// skillNamePattern mirrors the adapter's own validation (see
// plugins/skills.ValidateName). Duplicated here, like
// repositoryNamePattern in repositories.go, because the kernel stays
// adapter-agnostic and cannot import plugins/skills directly; the
// adapter re-validates independently before touching disk.
var skillNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

type SkillsService struct {
	store ports.SkillsStore
}

func NewSkillsService(store ports.SkillsStore) *SkillsService { return &SkillsService{store: store} }

func (s *SkillsService) List(ctx context.Context) ([]ports.SkillSummary, error) {
	return s.store.List(ctx)
}

func (s *SkillsService) Enabled(ctx context.Context) ([]ports.SkillSummary, error) {
	return s.List(ctx)
}

func (s *SkillsService) Show(ctx context.Context, name string) (ports.Skill, error) {
	if err := validateSkillName(name); err != nil {
		return ports.Skill{}, err
	}
	return s.store.Read(ctx, name)
}

func validateSkillName(name string) error {
	if !skillNamePattern.MatchString(name) {
		return errors.New("skill name must be 1-64 lowercase letters, digits, or hyphens")
	}
	return nil
}
