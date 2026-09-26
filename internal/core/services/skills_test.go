package services

import (
	"context"
	"errors"
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
		summaries = append(summaries, ports.SkillSummary{Name: skill.Name, Description: skill.Description})
	}
	return summaries, nil
}

func (s *fakeSkillsStore) Read(_ context.Context, name string) (ports.Skill, error) {
	skill, ok := s.skills[name]
	if !ok {
		return ports.Skill{}, errors.New("not found")
	}
	return skill, nil
}

func (s *fakeSkillsStore) Write(_ context.Context, name, description, body string) error {
	s.skills[name] = ports.Skill{Name: name, Description: description, Body: body}
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
