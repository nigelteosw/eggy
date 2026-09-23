package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// AgentSwitch is the model and reasoning-effort selection behind /model, read
// and written by the chat composer. It is the same authority the command
// writes: a picker that kept its own idea of the active alias would disagree
// with the phone the moment either was used.
type AgentSwitch interface {
	Aliases() []string
	SelectedModel(ctx context.Context) (string, error)
	SelectModel(ctx context.Context, alias string) error
	ReasoningEfforts(alias string) []string
	ReasoningEffort(ctx context.Context) (string, error)
	SelectReasoningEffort(ctx context.Context, effort string) error
	ShowThinking(ctx context.Context) (bool, error)
	SetShowThinking(ctx context.Context, show bool) error
	Heartbeat(ctx context.Context) (bool, error)
	SetHeartbeat(ctx context.Context, on bool) error
}

// agentSelection is what the composer renders: every alias it may offer, the
// one in force, and the effort levels that alias supports. Efforts are the
// selected model's, not the union across models, because a level one model
// accepts is a level another rejects -- offering the union would put a
// rejection behind a control the panel drew as available.
type agentSelection struct {
	Models       []string `json:"models"`
	Model        string   `json:"model"`
	Efforts      []string `json:"efforts"`
	Effort       string   `json:"effort"`
	ShowThinking bool     `json:"show_thinking"`
	Heartbeat    bool     `json:"heartbeat"`
	Approval     string   `json:"approval_mode,omitempty"`
}

// newAgentHandler answers the composer's one read. It folds in the approval
// mode because the composer shows all three controls in one row and a second
// round trip for one string would only make them settle at different times.
func newAgentHandler(agent AgentSwitch, gate ApprovalModeSwitch) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if agent == nil {
			writeWebError(w, http.StatusNotFound, "model selection is unavailable")
			return
		}
		respondAgentSelection(w, r, agent, gate)
	}
}

// newAgentModelHandler sets the active alias. An empty alias means "default",
// the same word /model accepts, so clearing the selection stays possible from
// the panel without a second route for it.
func newAgentModelHandler(agent AgentSwitch, gate ApprovalModeSwitch) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if agent == nil {
			writeWebError(w, http.StatusNotFound, "model selection is unavailable")
			return
		}
		alias := strings.TrimSpace(r.FormValue("model"))
		if alias == "default" {
			alias = ""
		}
		if err := agent.SelectModel(r.Context(), alias); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		respondAgentSelection(w, r, agent, gate)
	}
}

// newAgentEffortHandler sets the reasoning effort for whichever model is
// selected. The runtime rejects a level that model does not support, and that
// rejection is reported rather than smoothed over: the panel's option list came
// from the same runtime, so a mismatch means the two are out of step and the
// owner should see it.
func newAgentEffortHandler(agent AgentSwitch, gate ApprovalModeSwitch) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if agent == nil {
			writeWebError(w, http.StatusNotFound, "model selection is unavailable")
			return
		}
		effort := strings.TrimSpace(r.FormValue("effort"))
		if err := agent.SelectReasoningEffort(r.Context(), effort); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		respondAgentSelection(w, r, agent, gate)
	}
}

// newAgentThinkingHandler sets whether the model's reasoning is delivered as
// a separate message, the panel's half of /model thinking. Like every
// setting here it is the calling account's own; the body names no account
// and a body that tries to is refused by the strict decoder.
func newAgentThinkingHandler(agent AgentSwitch, gate ApprovalModeSwitch) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if agent == nil {
			writeWebError(w, http.StatusNotFound, "model selection is unavailable")
			return
		}
		var input struct {
			Show bool `json:"show"`
		}
		if err := decodeAuthBody(r, &input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := agent.SetShowThinking(r.Context(), input.Show); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		respondAgentSelection(w, r, agent, gate)
	}
}

// newAgentHeartbeatHandler switches the calling account's own check-ins, the
// panel's half of /heartbeat. Off until the person turns it on.
func newAgentHeartbeatHandler(agent AgentSwitch, gate ApprovalModeSwitch) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if agent == nil {
			writeWebError(w, http.StatusNotFound, "model selection is unavailable")
			return
		}
		var input struct {
			On bool `json:"on"`
		}
		if err := decodeAuthBody(r, &input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := agent.SetHeartbeat(r.Context(), input.On); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		respondAgentSelection(w, r, agent, gate)
	}
}

// respondAgentSelection answers every route here with the whole selection, so
// the composer re-renders from the runtime rather than from what it assumed a
// write did. It is one shape for all three because the composer replaces its
// state wholesale: a write that answered with a partial selection would blank
// the controls it did not mention -- switching models really can invalidate
// the stored effort, and only the runtime knows when it has.
func respondAgentSelection(w http.ResponseWriter, r *http.Request, agent AgentSwitch, gate ApprovalModeSwitch) {
	selection, err := readAgentSelection(r.Context(), agent)
	if err != nil {
		writeWebError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if gate != nil {
		mode, err := gate.Mode(r.Context())
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		selection.Approval = string(mode)
	}
	writeJSON(w, selection)
}

func readAgentSelection(ctx context.Context, agent AgentSwitch) (agentSelection, error) {
	model, err := agent.SelectedModel(ctx)
	if err != nil {
		return agentSelection{}, err
	}
	effort, err := agent.ReasoningEffort(ctx)
	if err != nil {
		return agentSelection{}, err
	}
	models := agent.Aliases()
	if models == nil {
		models = []string{}
	}
	efforts := agent.ReasoningEfforts(model)
	if efforts == nil {
		efforts = []string{}
	}
	show, err := agent.ShowThinking(ctx)
	if err != nil {
		return agentSelection{}, err
	}
	heartbeat, err := agent.Heartbeat(ctx)
	if err != nil {
		return agentSelection{}, err
	}
	return agentSelection{Models: models, Model: model, Efforts: efforts, Effort: effort, ShowThinking: show, Heartbeat: heartbeat}, nil
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}
