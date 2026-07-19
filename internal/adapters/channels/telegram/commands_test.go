package telegram

import (
	"strings"
	"testing"
)

func TestCommandRegistryIncludesEveryOperationalShortcutWithADescription(t *testing.T) {
	names := map[string]bool{}
	for _, command := range Commands() {
		if command.Description == "" {
			t.Fatalf("command %q has no description", command.Name)
		}
		names[command.Name] = true
	}
	for _, want := range []string{"status", "repositories", "runs", "stop", "schedules", "memory", "model", "coding_agent", "usage", "new", "calendar_auth"} {
		if !names[want] {
			t.Fatalf("command %q missing from Commands(): %v", want, names)
		}
	}
}

func TestCodingCommandDescriptionsAreProviderNeutral(t *testing.T) {
	for _, command := range Commands() {
		if (command.Name == "runs" || command.Name == "stop") && strings.Contains(strings.ToLower(command.Description), "codex") {
			t.Fatalf("command %q has provider-specific description %q", command.Name, command.Description)
		}
	}
}
