package commands

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// Control renders personal controls and delegates all writes to Execute,
// which uses the same runtime services as slash commands and the web panel.
func (s *CommandService) Control(ctx context.Context, action string) (ports.ControlPanel, error) {
	panel := ports.ControlPanel{}
	add := func(label, action string) {
		panel.Options = append(panel.Options, ports.ControlOption{Label: label, Action: action})
	}
	report := func(command string) error {
		text, _, err := s.Execute(ctx, command)
		panel.Text = text
		return err
	}
	page := 0
	if offset, ok := strings.CutPrefix(action, "models:"); ok {
		var err error
		page, err = strconv.Atoi(offset)
		if err != nil || page < 0 || page >= len(s.ModelAliases) {
			return panel, errors.New("invalid model page")
		}
		action = "models"
	}
	switch action {
	case "menu":
		panel.Text = "**Eggy menu**\nChat normally, or choose a control below."
		add("Status", "status")
		add("My settings", "settings")
		add("Help", "help")
		add("Open web panel", "web")
	case "settings":
		panel.Text = "**My settings**\nThese settings apply only to your account, across Telegram and the web."
		if s.AgentRuntime != nil {
			alias, err := s.AgentRuntime.SelectedModel(ctx)
			if err != nil {
				return panel, err
			}
			effort, err := s.AgentRuntime.ReasoningEffort(ctx)
			if err != nil {
				return panel, err
			}
			if effort == "" {
				effort = "default"
			}
			thinking, err := s.AgentRuntime.ShowThinking(ctx)
			if err != nil {
				return panel, err
			}
			heartbeat, err := s.AgentRuntime.Heartbeat(ctx)
			if err != nil {
				return panel, err
			}
			add("Model: "+alias, "models")
			add("Reasoning effort: "+effort, "effort")
			add("Thinking: "+onOff(thinking), "thinking")
			add("Heartbeat: "+onOff(heartbeat), "heartbeat")
		}
		if s.Approvals != nil {
			mode, err := s.Approvals.Mode(ctx)
			if err != nil {
				return panel, err
			}
			add("Approval mode: "+string(mode), "mode")
		}
		add("Back to menu", "menu")
	case "models":
		if s.AgentRuntime == nil {
			return panel, errors.New("model selection is unavailable")
		}
		selected, err := s.AgentRuntime.SelectedModel(ctx)
		if err != nil {
			return panel, err
		}
		panel.Text = fmt.Sprintf("**Choose your model**\nCurrent: %s\nChoose a configured alias below.", selected)
		end := min(page+8, len(s.ModelAliases))
		for _, alias := range s.ModelAliases[page:end] {
			add(alias, "set:/model "+alias)
		}
		if end < len(s.ModelAliases) {
			add("More models", fmt.Sprintf("models:%d", end))
		}
		if page > 0 {
			add("Previous models", fmt.Sprintf("models:%d", max(0, page-8)))
		}
		add("Use default model", "set:/model default")
		add("Back to settings", "settings")
	case "effort":
		if err := report("/model settings effort"); err != nil {
			return panel, err
		}
		add("Default", "set:/model settings effort default")
		if s.AgentRuntime != nil {
			alias, err := s.AgentRuntime.SelectedModel(ctx)
			if err != nil {
				return panel, err
			}
			for _, effort := range s.AgentRuntime.ReasoningEfforts(alias) {
				add(effort, "set:/model settings effort "+effort)
			}
		}
		add("Back to settings", "settings")
	case "thinking", "heartbeat":
		command := "/heartbeat"
		if action == "thinking" {
			command = "/model settings thinking"
		}
		if err := report(command); err != nil {
			return panel, err
		}
		add("On", "set:"+command+" on")
		add("Off", "set:"+command+" off")
		add("Back to settings", "settings")
	case "mode":
		if err := report("/mode"); err != nil {
			return panel, err
		}
		add("Normal — ask before writes", "set:/mode normal")
		add("Strict — ask before every tool", "set:/mode strict")
		add("Auto — no tool approvals", "confirm-auto")
		add("Back to settings", "settings")
	case "confirm-auto":
		panel.Text = "**Enable auto mode?**\nTool calls that change things will run without asking you. This applies only to your account."
		add("Enable auto mode", "enable-auto")
		add("Keep current mode", "mode")
	case "status", "help", "web":
		if err := report("/" + action); err != nil {
			return panel, err
		}
		add("Back to menu", "menu")
	default:
		command, ok := strings.CutPrefix(action, "set:")
		if action == "enable-auto" {
			command = "/mode auto"
			ok = true
		} else if ok {
			ok = s.personalControlCommand(ctx, command)
		}
		if !ok {
			return panel, errors.New("unknown personal control")
		}
		text, _, err := s.Execute(ctx, command)
		if err != nil {
			return panel, err
		}
		panel, err = s.Control(ctx, "settings")
		if err != nil {
			return panel, err
		}
		panel.Text = text + "\n\n" + panel.Text
	}
	return panel, nil
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func (s *CommandService) personalControlCommand(ctx context.Context, command string) bool {
	switch command {
	case "/mode normal", "/mode strict", "/heartbeat on", "/heartbeat off", "/model settings thinking on", "/model settings thinking off", "/model settings effort default", "/model default":
		return true
	}
	for _, alias := range s.ModelAliases {
		if command == "/model "+alias {
			return true
		}
	}
	if s.AgentRuntime != nil {
		alias, err := s.AgentRuntime.SelectedModel(ctx)
		if err == nil {
			for _, effort := range s.AgentRuntime.ReasoningEfforts(alias) {
				if command == "/model settings effort "+effort {
					return true
				}
			}
		}
	}
	return false
}
