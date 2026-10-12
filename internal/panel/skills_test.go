package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type fakeSkillsAdmin struct {
	skills map[string]ports.Skill
}

func newFakeSkillsAdmin() *fakeSkillsAdmin {
	return &fakeSkillsAdmin{skills: map[string]ports.Skill{}}
}

func (f *fakeSkillsAdmin) List(context.Context) ([]ports.SkillSummary, error) {
	out := []ports.SkillSummary{}
	for _, skill := range f.skills {
		out = append(out, ports.SkillSummary{Name: skill.Name, Description: skill.Description, Disabled: skill.Disabled})
	}
	return out, nil
}

func (f *fakeSkillsAdmin) Inspect(_ context.Context, name string) (ports.Skill, error) {
	skill, ok := f.skills[name]
	if !ok {
		return ports.Skill{}, fmt.Errorf("%w: %q", ports.ErrSkillNotFound, name)
	}
	return skill, nil
}

func (f *fakeSkillsAdmin) Write(_ context.Context, name, description, body string) error {
	if name == "bad name" {
		return errors.New("skill name must be 1-64 lowercase letters, digits, or hyphens")
	}
	disabled := f.skills[name].Disabled
	f.skills[name] = ports.Skill{Name: name, Description: description, Body: body, Disabled: disabled}
	return nil
}

func (f *fakeSkillsAdmin) SetDisabled(_ context.Context, name string, disabled bool) error {
	skill, ok := f.skills[name]
	if !ok {
		return fmt.Errorf("%w: %q", ports.ErrSkillNotFound, name)
	}
	skill.Disabled = disabled
	f.skills[name] = skill
	return nil
}

func (f *fakeSkillsAdmin) Delete(_ context.Context, name string) error {
	if _, ok := f.skills[name]; !ok {
		return fmt.Errorf("%w: %q", ports.ErrSkillNotFound, name)
	}
	delete(f.skills, name)
	return nil
}

func skillRequest(t *testing.T, handler http.Handler, method, path, body, name string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if name != "" {
		req.SetPathValue("name", name)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestSkillsRoutesCreateListInspectUpdateDelete(t *testing.T) {
	admin := newFakeSkillsAdmin()
	create := skillsCreateRoute(admin)
	list := skillsListRoute(admin)
	get := skillsGetRoute(admin)
	update := skillsUpdateRoute(admin)
	remove := skillsDeleteRoute(admin)

	rec := skillRequest(t, create, http.MethodPost, "/api/skills", `{"name":"brief","description":"Use when briefing","body":"steps"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body)
	}
	rec = skillRequest(t, create, http.MethodPost, "/api/skills", `{"name":"brief","description":"again","body":"steps"}`, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate create status=%d", rec.Code)
	}
	rec = skillRequest(t, create, http.MethodPost, "/api/skills", `{"name":"bad name","description":"x","body":"y"}`, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid create status=%d", rec.Code)
	}

	rec = skillRequest(t, list, http.MethodGet, "/api/skills", "", "")
	var rows []ports.SkillSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].Name != "brief" {
		t.Fatalf("list rows=%#v err=%v body=%s", rows, err, rec.Body)
	}

	rec = skillRequest(t, get, http.MethodGet, "/api/skills/brief", "", "brief")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"body":"steps"`) {
		t.Fatalf("get status=%d body=%s", rec.Code, rec.Body)
	}
	rec = skillRequest(t, get, http.MethodGet, "/api/skills/none", "", "none")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get missing status=%d", rec.Code)
	}

	rec = skillRequest(t, update, http.MethodPut, "/api/skills/brief", `{"description":"Use when briefing, revised","body":"new steps","disabled":true}`, "brief")
	if rec.Code != http.StatusOK || !admin.skills["brief"].Disabled || admin.skills["brief"].Body != "new steps" {
		t.Fatalf("update status=%d skill=%#v", rec.Code, admin.skills["brief"])
	}
	rec = skillRequest(t, update, http.MethodPut, "/api/skills/none", `{"description":"x","body":"y"}`, "none")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("update missing status=%d", rec.Code)
	}

	rec = skillRequest(t, remove, http.MethodDelete, "/api/skills/brief", "", "brief")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status=%d", rec.Code)
	}
	rec = skillRequest(t, remove, http.MethodDelete, "/api/skills/brief", "", "brief")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing status=%d", rec.Code)
	}
}

func TestSkillsRoutesCapRequestBodies(t *testing.T) {
	admin := newFakeSkillsAdmin()
	huge := `{"name":"big","description":"x","body":"` + strings.Repeat("a", skillRequestMaxBytes) + `"}`
	rec := skillRequest(t, skillsCreateRoute(admin), http.MethodPost, "/api/skills", huge, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized body status=%d", rec.Code)
	}
	if len(admin.skills) != 0 {
		t.Fatal("oversized body was written")
	}
}

func TestWebSkillsRoutesRequireSessionAndCSRF(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := testWebConfig(t, now)
	admin := newFakeSkillsAdmin()
	admin.skills["brief"] = ports.Skill{Name: "brief", Description: "Use when briefing", Body: "steps"}
	config.Skills = admin
	handler := NewWebHandler("", config)

	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/api/skills", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list status=%d", anonymous.Code)
	}

	// A signed-in session still needs the CSRF header on a write.
	cookie := webLoginCookie(t, handler)
	write := httptest.NewRequest(http.MethodDelete, "/api/skills/brief", nil)
	write.AddCookie(cookie)
	write.Header.Set("Origin", "https://eggy.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, write)
	if response.Code == http.StatusNoContent {
		t.Fatal("delete without CSRF token succeeded")
	}
	if _, ok := admin.skills["brief"]; !ok {
		t.Fatal("skill deleted without CSRF token")
	}
}
