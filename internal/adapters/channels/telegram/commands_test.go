package telegram

import "testing"

func TestCommandsIncludesEveryOperationalShortcutWithADescription(t *testing.T) {
	names := map[string]bool{}
	for _, command := range Commands() {
		if command.Description == "" {
			t.Fatalf("command %q has no description", command.Name)
		}
		names[command.Name] = true
	}
	for _, want := range []string{"status", "repositories", "runs", "stop", "schedules", "memory", "model", "usage", "new", "calendar_auth"} {
		if !names[want] {
			t.Fatalf("command %q missing from Commands(): %v", want, names)
		}
	}
}
