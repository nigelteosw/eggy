package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// fakeAgentSwitch mirrors services.AgentRuntime's contract closely enough to
// exercise the routes: aliases are fixed at construction, and an effort that
// the selected model does not support is not reported.
type fakeAgentSwitch struct {
	aliases      []string
	efforts      map[string][]string
	model        string
	effort       string
	hideThinking bool
	heartbeat    bool
}

func (f *fakeAgentSwitch) Heartbeat(context.Context) (bool, error) { return f.heartbeat, nil }

func (f *fakeAgentSwitch) SetHeartbeat(_ context.Context, on bool) error {
	f.heartbeat = on
	return nil
}

func (f *fakeAgentSwitch) ShowThinking(context.Context) (bool, error) { return !f.hideThinking, nil }

func (f *fakeAgentSwitch) SetShowThinking(_ context.Context, show bool) error {
	f.hideThinking = !show
	return nil
}

func (f *fakeAgentSwitch) Aliases() []string { return slices.Clone(f.aliases) }

func (f *fakeAgentSwitch) SelectedModel(context.Context) (string, error) { return f.model, nil }

func (f *fakeAgentSwitch) SelectModel(_ context.Context, alias string) error {
	if alias != "" && !slices.Contains(f.aliases, alias) {
		return errors.New("model alias " + alias + " is not configured")
	}
	f.model = alias
	return nil
}

func (f *fakeAgentSwitch) ReasoningEfforts(alias string) []string { return f.efforts[alias] }

func (f *fakeAgentSwitch) ReasoningEffort(context.Context) (string, error) {
	if !slices.Contains(f.efforts[f.model], f.effort) {
		return "", nil
	}
	return f.effort, nil
}

func (f *fakeAgentSwitch) SelectReasoningEffort(_ context.Context, effort string) error {
	if !slices.Contains(f.efforts[f.model], effort) {
		return errors.New("model " + f.model + " does not support effort " + effort)
	}
	f.effort = effort
	return nil
}

func agentCall(t *testing.T, handler http.Handler, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		attachSession(request, cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeSelection(t *testing.T, response *httptest.ResponseRecorder) agentSelection {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded agentSelection
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// The composer draws its model and effort controls from this one read, so it
// has to carry every alias the owner may pick, the one in force, and the effort
// levels that model -- not some other model -- accepts.
func TestWebAgentReportsWhatTheComposerCanOffer(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	agent := &fakeAgentSwitch{
		aliases: []string{"fast", "thinker"},
		efforts: map[string][]string{"thinker": {"low", "medium", "high"}},
		model:   "thinker",
		effort:  "medium",
	}
	webConfig := testWebConfig(t, now)
	webConfig.Agent = agent
	webConfig.ApprovalMode = &fakeApprovalMode{mode: ports.ModeStrict}
	handler := NewWebHandler("", webConfig)
	cookie := webLoginCookie(t, handler)

	got := decodeSelection(t, agentCall(t, handler, cookie, http.MethodGet, "/api/agent", ""))
	if !slices.Equal(got.Models, []string{"fast", "thinker"}) || got.Model != "thinker" {
		t.Fatalf("models=%#v model=%q", got.Models, got.Model)
	}
	if !slices.Equal(got.Efforts, []string{"low", "medium", "high"}) || got.Effort != "medium" {
		t.Fatalf("efforts=%#v effort=%q", got.Efforts, got.Effort)
	}
	// The approval mode rides along so all three controls in the composer row
	// settle from one response rather than at different times.
	if got.Approval != string(ports.ModeStrict) {
		t.Fatalf("approval=%q", got.Approval)
	}
}

// Switching to a model with no effort levels must not leave the composer still
// offering the old ones: the write answers with the state it produced, and the
// runtime is the only thing that knows the stored effort no longer applies.
func TestWebAgentModelWriteAnswersWithTheResultingState(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	agent := &fakeAgentSwitch{
		aliases: []string{"fast", "thinker"},
		efforts: map[string][]string{"thinker": {"low", "high"}},
		model:   "thinker",
		effort:  "high",
	}
	webConfig := testWebConfig(t, now)
	webConfig.Agent = agent
	webConfig.ApprovalMode = &fakeApprovalMode{mode: ports.ModeStrict}
	handler := NewWebHandler("", webConfig)
	cookie := webLoginCookie(t, handler)

	got := decodeSelection(t, agentCall(t, handler, cookie, http.MethodPost, "/api/agent/model", "model=fast"))
	if got.Model != "fast" || len(got.Efforts) != 0 || got.Effort != "" {
		t.Fatalf("selection=%#v", got)
	}
	// Every route here answers with the whole selection, approval mode
	// included. The composer replaces its state with this response, so a write
	// that reported only what it changed would blank the controls it left
	// alone -- which is exactly what picking an effort did to the approval
	// chip before this was one shape.
	if got.Approval != string(ports.ModeStrict) {
		t.Fatalf("a model write dropped the approval mode: %#v", got)
	}
	// An unconfigured alias is refused rather than resolved to something that
	// works, and the refusal leaves the selection alone.
	if response := agentCall(t, handler, cookie, http.MethodPost, "/api/agent/model", "model=nope"); response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if agent.model != "fast" {
		t.Fatalf("a refused alias still changed the selection: %q", agent.model)
	}
}

func TestWebAgentEffortRejectsALevelTheModelDoesNotSupport(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	agent := &fakeAgentSwitch{
		aliases: []string{"thinker"},
		efforts: map[string][]string{"thinker": {"low", "high"}},
		model:   "thinker",
	}
	webConfig := testWebConfig(t, now)
	webConfig.Agent = agent
	webConfig.ApprovalMode = &fakeApprovalMode{mode: ports.ModeNormal}
	handler := NewWebHandler("", webConfig)
	cookie := webLoginCookie(t, handler)

	got := decodeSelection(t, agentCall(t, handler, cookie, http.MethodPost, "/api/agent/effort", "effort=high"))
	if got.Effort != "high" {
		t.Fatalf("effort=%q", got.Effort)
	}
	if got.Approval != string(ports.ModeNormal) {
		t.Fatalf("an effort write dropped the approval mode: %#v", got)
	}
	if response := agentCall(t, handler, cookie, http.MethodPost, "/api/agent/effort", "effort=max"); response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if agent.effort != "high" {
		t.Fatalf("a refused level still changed the effort: %q", agent.effort)
	}
}

// Model selection changes what every subsequent turn runs on and costs, so it
// is owner-only like every other write in this panel.
func TestWebAgentRoutesRequireASession(t *testing.T) {
	webConfig := testWebConfig(t, time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC))
	webConfig.Agent = &fakeAgentSwitch{aliases: []string{"fast"}, model: "fast"}
	handler := NewWebHandler("", webConfig)
	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/agent", ""},
		{http.MethodPost, "/api/agent/model", "model=fast"},
		{http.MethodPost, "/api/agent/effort", "effort=low"},
	} {
		if response := agentCall(t, handler, nil, route.method, route.path, route.body); response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status=%d", route.method, route.path, response.Code)
		}
	}
}

// With no model backend wired the composer must be told so, rather than being
// handed an empty list it would draw as "no models configured".
func TestWebAgentWithoutARuntimeIsAbsentRatherThanEmpty(t *testing.T) {
	webConfig := testWebConfig(t, time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC))
	handler := NewWebHandler("", webConfig)
	cookie := webLoginCookie(t, handler)
	if response := agentCall(t, handler, cookie, http.MethodGet, "/api/agent", ""); response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

// scopedAgentSwitch records settings per principal, standing in for the
// runtime's per-account state, so a route that let a body name the account
// would be caught writing the other one's row.
type scopedAgentSwitch struct {
	models    map[string]string
	thinking  map[string]bool
	heartbeat map[string]bool
}

func (s *scopedAgentSwitch) Heartbeat(ctx context.Context) (bool, error) {
	p, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return false, err
	}
	return s.heartbeat[p.AccountID], nil
}

func (s *scopedAgentSwitch) SetHeartbeat(ctx context.Context, on bool) error {
	p, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return err
	}
	s.heartbeat[p.AccountID] = on
	return nil
}

func (s *scopedAgentSwitch) Aliases() []string { return []string{"shared-default", "other"} }
func (s *scopedAgentSwitch) SelectedModel(ctx context.Context) (string, error) {
	p, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return "", err
	}
	if m := s.models[p.AccountID]; m != "" {
		return m, nil
	}
	return "shared-default", nil
}
func (s *scopedAgentSwitch) SelectModel(ctx context.Context, alias string) error {
	p, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return err
	}
	s.models[p.AccountID] = alias
	return nil
}
func (s *scopedAgentSwitch) ReasoningEfforts(string) []string                    { return nil }
func (s *scopedAgentSwitch) ReasoningEffort(context.Context) (string, error)     { return "", nil }
func (s *scopedAgentSwitch) SelectReasoningEffort(context.Context, string) error { return nil }
func (s *scopedAgentSwitch) ShowThinking(ctx context.Context) (bool, error) {
	p, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return false, err
	}
	hidden, ok := s.thinking[p.AccountID]
	return !ok || !hidden, nil
}
func (s *scopedAgentSwitch) SetShowThinking(ctx context.Context, show bool) error {
	p, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return err
	}
	s.thinking[p.AccountID] = !show
	return nil
}

func TestPersonalAgentRoutesActOnlyAsTheSessionAccount(t *testing.T) {
	now := time.Now().UTC()
	cfg, db, _ := accountWebConfig(t, now)
	agent := &scopedAgentSwitch{models: map[string]string{}, thinking: map[string]bool{}, heartbeat: map[string]bool{}}
	cfg.Agent = agent
	handler := NewWebHandler("", cfg)
	nigel, nigelCSRF := signIn(t, handler, db, "nigel", now)
	partner, partnerCSRF := signIn(t, handler, db, "partner", now)

	// A body naming another account is not a way to write their row: the
	// form route ignores the field, the JSON route refuses it.
	response := authenticatedJSON(handler, nigel, nigelCSRF, http.MethodPost, "/api/agent/model", "")
	_ = response
	form := httptest.NewRequest(http.MethodPost, "/api/agent/model", strings.NewReader("model=other&account_id=partner"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	attachSession(form, nigel)
	formResponse := httptest.NewRecorder()
	handler.ServeHTTP(formResponse, form)
	if formResponse.Code != http.StatusOK {
		t.Fatalf("model status=%d body=%s", formResponse.Code, formResponse.Body.String())
	}
	if agent.models["nigel"] != "other" || agent.models["partner"] != "" {
		t.Fatalf("models=%v", agent.models)
	}
	if response := authenticatedJSON(handler, nigel, nigelCSRF, http.MethodPost, "/api/agent/thinking", `{"show":false,"account_id":"partner"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("thinking with account_id status=%d", response.Code)
	}
	if response := authenticatedJSON(handler, nigel, nigelCSRF, http.MethodPost, "/api/agent/thinking", `{"show":false}`); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"show_thinking":false`) {
		t.Fatalf("thinking status=%d body=%s", response.Code, response.Body.String())
	}
	if response := authenticatedJSON(handler, nigel, nigelCSRF, http.MethodPost, "/api/agent/heartbeat", `{"on":true}`); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"heartbeat":true`) {
		t.Fatalf("heartbeat status=%d body=%s", response.Code, response.Body.String())
	}
	// partner still sees the defaults, heartbeat off included.
	if response := authenticatedJSON(handler, partner, partnerCSRF, http.MethodGet, "/api/agent", ""); !strings.Contains(response.Body.String(), `"heartbeat":false`) {
		t.Fatalf("partner's heartbeat=%s", response.Body.String())
	}
	response = authenticatedJSON(handler, partner, partnerCSRF, http.MethodGet, "/api/agent", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"model":"shared-default"`) || !strings.Contains(response.Body.String(), `"show_thinking":true`) {
		t.Fatalf("partner's view=%s", response.Body.String())
	}
}
