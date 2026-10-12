package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

type fakeSkillsStore struct {
	skills map[string]ports.Skill
}

func newFakeSkillsStore() *fakeSkillsStore {
	return &fakeSkillsStore{skills: map[string]ports.Skill{}}
}

func (s *fakeSkillsStore) List(context.Context) ([]ports.SkillSummary, error) {
	summaries := make([]ports.SkillSummary, 0, len(s.skills))
	for _, skill := range s.skills {
		summaries = append(summaries, ports.SkillSummary{Name: skill.Name, Description: skill.Description, Disabled: skill.Disabled})
	}
	return summaries, nil
}

func (s *fakeSkillsStore) SetDisabled(_ context.Context, name string, disabled bool) error {
	skill, ok := s.skills[name]
	if !ok {
		return errors.New("not found")
	}
	skill.Disabled = disabled
	s.skills[name] = skill
	return nil
}

func (s *fakeSkillsStore) Read(_ context.Context, name string) (ports.Skill, error) {
	skill, ok := s.skills[name]
	if !ok {
		return ports.Skill{}, errors.New("not found")
	}
	return skill, nil
}

func (s *fakeSkillsStore) Write(_ context.Context, name, description, body string) error {
	disabled := s.skills[name].Disabled
	s.skills[name] = ports.Skill{Name: name, Description: description, Body: body, Disabled: disabled}
	return nil
}

func (s *fakeSkillsStore) Delete(_ context.Context, name string) error {
	if _, ok := s.skills[name]; !ok {
		return errors.New("not found")
	}
	delete(s.skills, name)
	return nil
}

func TestSkillToolsReadReviewedFiles(t *testing.T) {
	store := newFakeSkillsStore()
	store.skills["a"] = ports.Skill{Name: "a", Description: "does a", Body: "steps"}
	service := NewSkillsService(store)
	tools := NewSkillTools(service)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	read := tools[0]
	out, err := read.Execute(context.Background(), []byte(`{"name":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "steps") || !strings.Contains(string(out), "does a") {
		t.Fatalf("output=%s", out)
	}

}

func TestEnabledOmitsDisabledAndInvalidAndBudgetsOnlyEnabledSkills(t *testing.T) {
	store := newFakeSkillsStore()
	service := NewSkillsService(store)
	ctx := context.Background()
	long := strings.Repeat("d", 1024)
	// Disabled skills sort before the enabled ones, so if they consumed index
	// budget the enabled skills would be the ones cut.
	for i := range 40 {
		name := fmt.Sprintf("a-off-%02d", i)
		store.skills[name] = ports.Skill{Name: name, Description: long, Body: "x", Disabled: true}
	}
	for i := range 40 {
		name := fmt.Sprintf("b-on-%02d", i)
		store.skills[name] = ports.Skill{Name: name, Description: long, Body: "x"}
	}

	enabled, err := service.Enabled(ctx)
	if err != nil {
		t.Fatal(err)
	}
	used := 0
	for _, summary := range enabled {
		if summary.Disabled || summary.Invalid != "" {
			t.Fatalf("model index includes %#v", summary)
		}
		used += len(summary.Name) + len(summary.Description) + 2
	}
	if used > ports.SkillIndexBudget {
		t.Fatalf("index uses %d bytes, over budget", used)
	}
	if len(enabled) == 0 || enabled[0].Name != "b-on-00" {
		t.Fatalf("enabled=%#v", enabled)
	}
}

func TestShowRefusesDisabledSkillButInspectSeesIt(t *testing.T) {
	store := newFakeSkillsStore()
	store.skills["off"] = ports.Skill{Name: "off", Description: "does off", Body: "steps", Disabled: true}
	service := NewSkillsService(store)
	ctx := context.Background()

	if _, err := service.Show(ctx, "off"); err == nil {
		t.Fatal("model read returned a disabled skill")
	}
	skill, err := service.Inspect(ctx, "off")
	if err != nil || !skill.Disabled {
		t.Fatalf("inspect skill=%#v err=%v", skill, err)
	}

	out, err := NewSkillTools(service)[0].Execute(ctx, []byte(`{"name":"off"}`))
	if err == nil {
		t.Fatalf("skill_read returned disabled content: %s", out)
	}
}

func TestAdminMutationsValidateNamesBeforeTouchingTheStore(t *testing.T) {
	store := newFakeSkillsStore()
	service := NewSkillsService(store)
	ctx := context.Background()

	if err := service.Write(ctx, "../escape", "desc", "body"); err == nil {
		t.Fatal("expected invalid name to be refused")
	}
	if err := service.SetDisabled(ctx, "Bad Name", true); err == nil {
		t.Fatal("expected invalid name to be refused")
	}
	if err := service.Delete(ctx, "a/b"); err == nil {
		t.Fatal("expected invalid name to be refused")
	}
	if len(store.skills) != 0 {
		t.Fatalf("store changed on invalid names: %#v", store.skills)
	}

	if err := service.Write(ctx, "fine", "Use when fine", "steps"); err != nil {
		t.Fatal(err)
	}
	if err := service.SetDisabled(ctx, "fine", true); err != nil {
		t.Fatal(err)
	}
	skill, err := service.Inspect(ctx, "fine")
	if err != nil || !skill.Disabled {
		t.Fatalf("skill=%#v err=%v", skill, err)
	}
	if err := service.Delete(ctx, "fine"); err != nil {
		t.Fatal(err)
	}
}
