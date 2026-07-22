package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
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

func TestSkillsRequestWriteStagesAndExecuteApprovedPersists(t *testing.T) {
	store := newFakeSkillsStore()
	stateStore := newMemoryStore()
	gateway := &fakeShippingGateway{}
	service := NewSkillsService(store, stateStore, gateway, gateway, nil)

	approval, err := service.RequestWrite(context.Background(), "fix-flaky-tests", "Use when a test intermittently fails", "1. Rerun with -count=10")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := gateway.payload.(skillWritePayload); !ok {
		t.Fatalf("payload=%#v", gateway.payload)
	}
	if _, err := store.Read(context.Background(), "fix-flaky-tests"); err == nil {
		t.Fatal("skill must not be written before approval")
	}

	approval.Payload = mustMarshal(t, gateway.payload)
	result, err := service.ExecuteApproved(context.Background(), approval)
	if err != nil {
		t.Fatal(err)
	}
	if result != "fix-flaky-tests" {
		t.Fatalf("result=%#v", result)
	}
	skill, err := store.Read(context.Background(), "fix-flaky-tests")
	if err != nil || skill.Description != "Use when a test intermittently fails" {
		t.Fatalf("skill=%#v err=%v", skill, err)
	}
	if gateway.authorized != approvals.SkillWrite {
		t.Fatalf("gateway=%#v", gateway)
	}
}

func TestSkillsExecuteApprovedRequiresAuthorization(t *testing.T) {
	store := newFakeSkillsStore()
	stateStore := newMemoryStore()
	policy := &fakePolicy{err: approvals.ErrExpired}
	service := NewSkillsService(store, stateStore, &fakeShippingGateway{}, policy, nil)

	approval := approvals.Approval{ID: "approval-1", Action: approvals.SkillWrite, Payload: mustMarshal(t, skillWritePayload{Name: "x", Description: "d", Body: "b"})}
	if _, err := service.ExecuteApproved(context.Background(), approval); !errors.Is(err, approvals.ErrExpired) {
		t.Fatalf("error=%v", err)
	}
	if _, err := store.Read(context.Background(), "x"); err == nil {
		t.Fatal("skill persisted despite failed authorization")
	}
}

func TestSkillsRequestWriteRejectsInvalidNameAndSecretContent(t *testing.T) {
	store := newFakeSkillsStore()
	stateStore := newMemoryStore()
	gateway := &fakeShippingGateway{}
	service := NewSkillsService(store, stateStore, gateway, gateway, nil)

	if _, err := service.RequestWrite(context.Background(), "Bad Name", "description", "body"); err == nil {
		t.Fatal("expected invalid name rejection")
	}
	if _, err := service.RequestWrite(context.Background(), "valid-name", "description", "api_key: ghp_abcdef1234567890"); err == nil {
		t.Fatal("expected secret-content rejection")
	}
}

func TestSkillsDeleteRequiresApproval(t *testing.T) {
	store := newFakeSkillsStore()
	store.skills["obsolete"] = ports.Skill{Name: "obsolete", Description: "old", Body: "old body"}
	stateStore := newMemoryStore()
	gateway := &fakeShippingGateway{}
	service := NewSkillsService(store, stateStore, gateway, gateway, nil)

	approval, err := service.RequestDelete(context.Background(), "obsolete")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(context.Background(), "obsolete"); err != nil {
		t.Fatal("skill must survive until approval executes")
	}
	approval.Payload = mustMarshal(t, gateway.payload)
	if _, err := service.ExecuteApproved(context.Background(), approval); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(context.Background(), "obsolete"); err == nil {
		t.Fatal("expected skill to be deleted")
	}

	if _, err := service.RequestDelete(context.Background(), "missing"); err == nil {
		t.Fatal("expected error requesting deletion of an unknown skill")
	}
}

func TestSkillsDisableEnableAreImmediateAndFilterEnabled(t *testing.T) {
	store := newFakeSkillsStore()
	store.skills["a"] = ports.Skill{Name: "a", Description: "does a"}
	store.skills["b"] = ports.Skill{Name: "b", Description: "does b"}
	stateStore := newMemoryStore()
	service := NewSkillsService(store, stateStore, &fakeShippingGateway{}, &fakeShippingGateway{}, nil)

	if err := service.Disable(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	all, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var aDisabled bool
	for _, summary := range all {
		if summary.Name == "a" {
			aDisabled = summary.Disabled
		}
	}
	if !aDisabled {
		t.Fatalf("expected a to be disabled: %#v", all)
	}

	enabled, err := service.Enabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 1 || enabled[0].Name != "b" {
		t.Fatalf("enabled=%#v", enabled)
	}

	if err := service.Enable(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	enabled, err = service.Enabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 2 {
		t.Fatalf("expected both skills enabled again, got %#v", enabled)
	}

	if err := service.Disable(context.Background(), "missing"); err == nil {
		t.Fatal("expected error disabling an unknown skill")
	}
}

func TestSkillToolsReadAndToggle(t *testing.T) {
	store := newFakeSkillsStore()
	store.skills["a"] = ports.Skill{Name: "a", Description: "does a", Body: "steps"}
	stateStore := newMemoryStore()
	service := NewSkillsService(store, stateStore, &fakeShippingGateway{}, &fakeShippingGateway{}, nil)
	tools := NewSkillTools(service)
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}

	read := tools[0]
	out, err := read.Execute(context.Background(), []byte(`{"name":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "steps") || !strings.Contains(string(out), "does a") {
		t.Fatalf("output=%s", out)
	}

	disable := tools[1]
	if disable.Definition().Name != "skill_disable" {
		t.Fatalf("unexpected tool order: %s", disable.Definition().Name)
	}
	if _, err := disable.Execute(context.Background(), []byte(`{"name":"a"}`)); err != nil {
		t.Fatal(err)
	}
	enabled, err := service.Enabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 0 {
		t.Fatalf("expected no enabled skills after disable tool call, got %#v", enabled)
	}

	enable := tools[2]
	if enable.Definition().Name != "skill_enable" {
		t.Fatalf("unexpected tool order: %s", enable.Definition().Name)
	}
	if _, err := enable.Execute(context.Background(), []byte(`{"name":"a"}`)); err != nil {
		t.Fatal(err)
	}
	enabled, err = service.Enabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 1 {
		t.Fatalf("expected skill re-enabled, got %#v", enabled)
	}
}
