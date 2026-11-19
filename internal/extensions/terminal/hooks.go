package terminal

import (
	"context"
	"encoding/base64"
	"encoding/json"
)

// Claude Code does not speak herdr-core's Program Status Protocol (OSC 7501)
// itself, so a terminal could never say it is waiting on its owner. These
// hooks make it: each prints one status record to the terminal it runs in,
// and herdr-core stores it. Nothing reads the screen.
//
// They are written once, into a home that has no Claude Code settings yet, and
// never again: after that the file is the owner's to edit or delete.
func statusHookSettings() ([]byte, error) {
	hook := func(state string) []map[string]any {
		return []map[string]any{{"hooks": []map[string]any{{
			"type":    "command",
			"command": `printf '\033]7501;state=` + state + `:app=claude\033\\' > /dev/tty 2>/dev/null || true`,
		}}}}
	}
	return json.MarshalIndent(map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": hook("working"),
			"Notification":     hook("blocked"),
			"Stop":             hook("done"),
		},
	}, "", "  ")
}

// installStatusHooks writes the settings through the core, so the file is the
// sandbox user's, in its own home, written by a confined process.
func installStatusHooks(ctx context.Context, c *client) error {
	settings, err := statusHookSettings()
	if err != nil {
		return err
	}
	return c.call(ctx, "machine.exec", map[string]any{
		"argv": []string{"sh", "-c", `[ -e "$HOME/.claude/settings.json" ] || { mkdir -p "$HOME/.claude" && cat > "$HOME/.claude/settings.json"; }`},
		// The core takes input as base64.
		"input": base64.StdEncoding.EncodeToString(settings),
	}, nil)
}
