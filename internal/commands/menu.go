package commands

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
		panel.Text = "**Menu**\nJust talk to me, or pick a control below."
		add("Status", "status")
		add("Your settings", "settings")
		add("Help", "help")
		add("Open web panel", "web")
	case "settings":
		panel.Text = "**Your settings**\nThese are yours alone, in Telegram and on the web."
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
			add("Model · "+alias, "models")
			add("Reasoning effort · "+effort, "effort")
			add("Show thinking · "+onOff(thinking), "thinking")
			add("Check-ins · "+onOff(heartbeat), "heartbeat")
		}
		if s.Approvals != nil {
			mode, err := s.Approvals.Mode(ctx)
			if err != nil {
				return panel, err
			}
			add("When I ask · "+ModeLabel(mode), "mode")
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
		if err := report("/model effort"); err != nil {
			return panel, err
		}
		add("Default", "set:/model effort default")
		if s.AgentRuntime != nil {
			alias, err := s.AgentRuntime.SelectedModel(ctx)
			if err != nil {
				return panel, err
			}
			for _, effort := range s.AgentRuntime.ReasoningEfforts(alias) {
				add(effort, "set:/model effort "+effort)
			}
		}
		add("Back to settings", "settings")
	case "thinking", "heartbeat":
		command := "/heartbeat"
		if action == "thinking" {
			command = "/model thinking"
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
		add("Before changes", "set:/mode normal")
		add("Every time", "set:/mode strict")
		add("Never", "confirm-auto")
		add("Back to settings", "settings")
	case "confirm-auto":
		panel.Text = "**Never ask?**\nI'll change and send things for you without asking first. This applies only to your account."
		add("Never ask", "enable-auto")
		add("Keep asking", "mode")
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
		var text string
		var err error
		if alias, isModel := strings.CutPrefix(command, "/model "); isModel && slices.Contains(s.ModelAliases, alias) && s.AgentRuntime != nil {
			text = s.selectAlias(ctx, alias)
		} else if text, _, err = s.Execute(ctx, command); err != nil {
			return panel, err
		}
		panel, err = s.Control(ctx, "settings")
		if err != nil {
			return panel, err
		}
		// The tap's result is one line above the menu. Pasting the whole reply
		// above it grew the message with every tap.
		panel.Text = "✓ " + firstLine(text) + "\n\n" + panel.Text
	}
	return panel, nil
}

func onOff(value bool) string {
	if value {
		return "On"
	}
	return "Off"
}

func (s *CommandService) personalControlCommand(ctx context.Context, command string) bool {
	switch command {
	case "/mode normal", "/mode strict", "/heartbeat on", "/heartbeat off", "/model thinking on", "/model thinking off", "/model effort default", "/model default":
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
				if command == "/model effort "+effort {
					return true
				}
			}
		}
	}
	return false
}

// firstLine is the first line of a reply, trimmed, for a one-line confirmation.
func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(line)
}
