package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentRunnerDisabledByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AgentRunner.Enabled {
		t.Fatal("agent runner must be off until the owner turns it on")
	}
}

func TestAgentRunnerEnabledRequiresKnownModel(t *testing.T) {
	cfg := mustLoadedConfig(t)
	cfg.AgentRunner = AgentRunnerConfig{Enabled: true, Model: "nope"}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), `agent_runner.model "nope" is not a configured model alias`) {
		t.Fatalf("error = %v", err)
	}
	cfg.AgentRunner.Model = firstModelAlias(t, cfg)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("known model refused: %v", err)
	}
}

func TestAgentRunnerRejectsUnknownSkill(t *testing.T) {
	cfg := mustLoadedConfig(t)
	cfg.AgentRunner = AgentRunnerConfig{Enabled: true, Model: firstModelAlias(t, cfg), Skills: map[string]bool{"foo": true}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), `agent_runner.skills: unknown skill "foo"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestRepositoryChecksValidation(t *testing.T) {
	cases := []struct {
		name   string
		checks [][]string
		ok     bool
	}{
		{"empty argv", [][]string{{}}, false},
		{"empty program", [][]string{{"", "test"}}, false},
		{"too many", make([][]string, 9), false},
		{"valid", [][]string{{"go", "test", "./..."}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := mustLoadedConfig(t)
			for i := range tc.checks {
				if tc.checks[i] == nil {
					tc.checks[i] = []string{"go", "vet"}
				}
			}
			cfg.Repositories[0].Checks = tc.checks
			err := cfg.Validate()
			if tc.ok && err != nil {
				t.Fatalf("valid checks refused: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("invalid checks accepted")
			}
		})
	}
}

func TestSetAgentRunnerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	model := firstModelAlias(t, cfg)
	skills := map[string]bool{"brainstorming": true, "writing-plans": false}
	if err := SetAgentRunner(path, true, model, skills); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.AgentRunner.Enabled || reloaded.AgentRunner.Model != model {
		t.Fatalf("saved agent_runner = %+v", reloaded.AgentRunner)
	}
	if !reloaded.AgentRunner.Skills["brainstorming"] || reloaded.AgentRunner.Skills["writing-plans"] {
		t.Fatalf("saved skills = %v", reloaded.AgentRunner.Skills)
	}
}

func TestSetRepositoryChecksRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	name := cfg.Repositories[0].Name
	checks := [][]string{{"go", "test", "./..."}}
	if err := SetRepositoryChecks(path, name, checks); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Repositories[0].Checks) != 1 || strings.Join(reloaded.Repositories[0].Checks[0], " ") != "go test ./..." {
		t.Fatalf("saved checks = %v", reloaded.Repositories[0].Checks)
	}
	err = SetRepositoryChecks(path, "no-such-repo", checks)
	if err == nil || !strings.Contains(err.Error(), "unknown repository") {
		t.Fatalf("error = %v", err)
	}
}

// mustLoadedConfig reads the shared valid fixture through the real loader,
// so the test sees the same defaults a deployment does.
func mustLoadedConfig(t *testing.T) Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Repositories) == 0 {
		cfg.Repositories = []RepositoryConfig{{Name: "eggy", CloneURL: "https://github.com/o/eggy.git", BaseBranch: "main"}}
	}
	return cfg
}

// firstModelAlias returns one alias the fixture defines, so tests do not
// hard-code the fixture's alias names.
func firstModelAlias(t *testing.T, cfg Config) string {
	t.Helper()
	for alias := range cfg.ModelAliases {
		return alias
	}
	t.Fatal("fixture defines no model alias")
	return ""
}
