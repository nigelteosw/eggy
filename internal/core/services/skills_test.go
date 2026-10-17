package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

type fakeSkillsStore struct {
	skills map[string]ports.Skill
	// refs holds reference files per directory skill: name -> file -> content.
	refs map[string]map[string]string
}

func newFakeSkillsStore() *fakeSkillsStore {
	return &fakeSkillsStore{skills: map[string]ports.Skill{}, refs: map[string]map[string]string{}}
}

func (s *fakeSkillsStore) List(context.Context) ([]ports.SkillSummary, error) {
	summaries := make([]ports.SkillSummary, 0, len(s.skills))
	for _, skill := range s.skills {
		summaries = append(summaries, ports.SkillSummary{Name: skill.Name, Description: skill.Description, Disabled: skill.Disabled, Source: skill.Source})
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
	if files := s.refs[name]; len(files) > 0 {
		skill.Files = slices.Sorted(maps.Keys(files))
	}
	return skill, nil
}

func (s *fakeSkillsStore) Write(_ context.Context, name, description, body string) error {
	disabled := s.skills[name].Disabled
	s.skills[name] = ports.Skill{Name: name, Description: description, Body: body, Disabled: disabled}
	return nil
}

func (s *fakeSkillsStore) ReadFile(_ context.Context, name, file string) (string, error) {
	content, ok := s.refs[name][file]
	if !ok {
		return "", fmt.Errorf("fake has no reference file %q of skill %q", file, name)
	}
	return content, nil
}

func (s *fakeSkillsStore) WriteBundle(_ context.Context, b ports.SkillBundle) error {
	if _, exists := s.skills[b.Name]; exists && !b.Replace {
		return ports.ErrSkillExists
	}
	s.skills[b.Name] = ports.Skill{Name: b.Name, Description: b.Description, Body: b.Body, Source: b.Source}
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
	for _, summary := range enabled {
		if summary.Disabled || summary.Invalid != "" {
			t.Fatalf("model index includes %#v", summary)
		}
	}
	if used := indexBytes(enabled); used > ports.SkillIndexBudget {
		t.Fatalf("index uses %d bytes, over budget", used)
	}
	if len(enabled) == 0 || enabled[0].Name != "b-on-00" {
		t.Fatalf("enabled=%#v", enabled)
	}
}

// indexBytes is what the model receives for a set of entries: each rendered
// line plus its newline, with the name-only hint standing in for an omitted
// description.
func indexBytes(enabled []ports.SkillSummary) int {
	total := 0
	for _, s := range enabled {
		if s.Description == "" {
			total += len(s.Name) + 2 + len(ports.SkillNameOnlyHint) + 1
			continue
		}
		total += len(s.Name) + 2 + len(s.Description) + 1
	}
	return total
}

func TestIndexKeepsEveryNameBeforeAnyDescription(t *testing.T) {
	store := newFakeSkillsStore()
	long := strings.Repeat("d", 400)
	for i := range 200 {
		name := fmt.Sprintf("skill-%03d", i)
		store.skills[name] = ports.Skill{Name: name, Description: long, Body: "x"}
	}
	enabled, err := NewSkillsService(store).Enabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 200 {
		t.Fatalf("kept %d of 200 names", len(enabled))
	}
	if used := indexBytes(enabled); used > ports.SkillIndexBudget {
		t.Fatalf("index uses %d bytes, over budget", used)
	}
	described := 0
	for _, s := range enabled {
		if s.Description != "" {
			described++
		}
	}
	if described == 0 || described == 200 {
		t.Fatalf("expected some descriptions kept and some shed, got %d of 200", described)
	}
}

func TestIndexHandPlacedDescriptionsFirst(t *testing.T) {
	store := newFakeSkillsStore()
	long := strings.Repeat("d", 9000)
	store.skills["aa"] = ports.Skill{Name: "aa", Description: long, Body: "x", Source: "mk/aa"}
	store.skills["zz"] = ports.Skill{Name: "zz", Description: long, Body: "x"}
	enabled, err := NewSkillsService(store).Enabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ports.SkillSummary{}
	for _, s := range enabled {
		byName[s.Name] = s
	}
	if byName["zz"].Description != long {
		t.Fatalf("hand-placed zz lost its description")
	}
	if byName["aa"].Description != "" {
		t.Fatalf("installed aa kept its description over hand-placed zz")
	}
}

func TestIndexDropsInstalledNamesFirstWhenNamesOverflow(t *testing.T) {
	store := newFakeSkillsStore()
	for i := range 300 {
		name := fmt.Sprintf("inst-%03d", i)
		store.skills[name] = ports.Skill{Name: name, Description: "does a thing", Body: "x", Source: "mk/" + name}
	}
	for i := range 40 {
		name := fmt.Sprintf("hand-%03d", i)
		store.skills[name] = ports.Skill{Name: name, Description: "does a thing", Body: "x"}
	}
	enabled, err := NewSkillsService(store).Enabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, s := range enabled {
		kept[s.Name] = true
	}
	for i := range 40 {
		if name := fmt.Sprintf("hand-%03d", i); !kept[name] {
			t.Fatalf("hand-placed %s dropped while installed names remained", name)
		}
	}
	// 340 names at 49 bytes each is 16660 against a 16384 budget: six installed
	// names (294 bytes) must go, the six greatest, and inst-293 must stay.
	for i := 294; i < 300; i++ {
		if name := fmt.Sprintf("inst-%03d", i); kept[name] {
			t.Fatalf("installed %s kept; greatest installed names should drop first", name)
		}
	}
	if !kept["inst-293"] {
		t.Fatalf("inst-293 dropped; only the six greatest installed names should go")
	}
	if used := indexBytes(enabled); used > ports.SkillIndexBudget {
		t.Fatalf("index uses %d bytes, over budget", used)
	}
}

// reorderedSkillsStore lists a fixed sequence, so determinism can be checked
// against a store that returns the same skills in a different order.
type reorderedSkillsStore struct {
	*fakeSkillsStore
	order []ports.SkillSummary
}

func (s *reorderedSkillsStore) List(context.Context) ([]ports.SkillSummary, error) {
	return slices.Clone(s.order), nil
}

func TestIndexIsDeterministic(t *testing.T) {
	var summaries []ports.SkillSummary
	for i := range 120 {
		name := fmt.Sprintf("s-%03d", i)
		summary := ports.SkillSummary{Name: name, Description: strings.Repeat("x", 100+i*7)}
		if i%3 == 0 {
			summary.Source = "mk/" + name
		}
		summaries = append(summaries, summary)
	}
	reversed := slices.Clone(summaries)
	slices.Reverse(reversed)

	forward, err := NewSkillsService(&reorderedSkillsStore{fakeSkillsStore: newFakeSkillsStore(), order: summaries}).Enabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	backward, err := NewSkillsService(&reorderedSkillsStore{fakeSkillsStore: newFakeSkillsStore(), order: reversed}).Enabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(forward, backward) {
		t.Fatal("index depends on store order")
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

func TestSkillReadListsFiles(t *testing.T) {
	store := newFakeSkillsStore()
	store.skills["pdf"] = ports.Skill{Name: "pdf", Description: "does pdf", Body: "steps"}
	store.refs["pdf"] = map[string]string{"reference.md": "details"}
	tools := NewSkillTools(NewSkillsService(store))

	out, err := tools[0].Execute(context.Background(), []byte(`{"name":"pdf"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Files, []string{"reference.md"}) {
		t.Fatalf("files=%v output=%s", got.Files, out)
	}
}

func TestSkillReadFile(t *testing.T) {
	store := newFakeSkillsStore()
	store.skills["pdf"] = ports.Skill{Name: "pdf", Description: "does pdf", Body: "steps"}
	store.refs["pdf"] = map[string]string{"reference.md": "details"}
	tools := NewSkillTools(NewSkillsService(store))

	out, err := tools[0].Execute(context.Background(), []byte(`{"name":"pdf","file":"reference.md"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Name    string `json:"name"`
		File    string `json:"file"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "pdf" || got.File != "reference.md" || got.Content != "details" {
		t.Fatalf("output=%s", out)
	}
}

func TestSkillReadFileRefusedWhenDisabled(t *testing.T) {
	store := newFakeSkillsStore()
	store.skills["pdf"] = ports.Skill{Name: "pdf", Description: "does pdf", Body: "steps", Disabled: true}
	store.refs["pdf"] = map[string]string{"reference.md": "details"}
	service := NewSkillsService(store)

	if _, err := service.ReadFile(context.Background(), "pdf", "reference.md"); err == nil {
		t.Fatal("service returned a reference file of a disabled skill")
	}
	out, err := NewSkillTools(service)[0].Execute(context.Background(), []byte(`{"name":"pdf","file":"reference.md"}`))
	if err == nil {
		t.Fatalf("skill_read returned a reference file of a disabled skill: %s", out)
	}
}

func TestSkillReadStillReadOnly(t *testing.T) {
	def := NewSkillTools(NewSkillsService(newFakeSkillsStore()))[0].Definition()
	if !reflect.DeepEqual(def.Effect, ports.ReadOnlyTool()) {
		t.Fatalf("effect=%#v", def.Effect)
	}
	want := `{"type":"object","properties":{"name":{"type":"string","minLength":1},"file":{"type":"string","minLength":1}},"required":["name"],"additionalProperties":false}`
	if string(def.Schema) != want {
		t.Fatalf("schema=%s", def.Schema)
	}
}

func TestSkillServicePassthroughsValidateNames(t *testing.T) {
	store := newFakeSkillsStore()
	service := NewSkillsService(store)
	ctx := context.Background()

	if _, err := service.ReadFile(ctx, "../escape", "reference.md"); err == nil {
		t.Fatal("expected invalid name to be refused by ReadFile")
	}
	if err := service.WriteBundle(ctx, ports.SkillBundle{Name: "Bad Name", Description: "d", Body: "b"}); err == nil {
		t.Fatal("expected invalid name to be refused by WriteBundle")
	}
	if len(store.skills) != 0 {
		t.Fatalf("store changed on invalid names: %#v", store.skills)
	}

	if err := service.WriteBundle(ctx, ports.SkillBundle{Name: "fine", Description: "Use when fine", Body: "steps"}); err != nil {
		t.Fatal(err)
	}
	if err := service.WriteBundle(ctx, ports.SkillBundle{Name: "fine", Description: "Use when fine", Body: "steps"}); !errors.Is(err, ports.ErrSkillExists) {
		t.Fatalf("second create err=%v, want ErrSkillExists", err)
	}
}
