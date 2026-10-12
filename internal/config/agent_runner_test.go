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

func TestAddAndRemoveRepositoryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := RepositoryConfig{Name: "widgets", CloneURL: "https://github.com/acme/widgets.git"}
	if err := AddRepository(path, repo); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range reloaded.Repositories {
		if r.Name == "widgets" {
			found = r.CloneURL == repo.CloneURL && r.BaseBranch == "main"
		}
	}
	if !found {
		t.Fatalf("added repository not saved with main as base: %+v", reloaded.Repositories)
	}
	if err := AddRepository(path, repo); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate add: %v", err)
	}
	if err := RemoveRepository(path, "widgets"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRepository(path, "widgets"); err == nil || !strings.Contains(err.Error(), "unknown repository") {
		t.Fatalf("removing twice: %v", err)
	}
}

func TestAddRepositoryRefusesUnsafeEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]RepositoryConfig{
		"ssh url":         {Name: "a", CloneURL: "git@github.com:acme/a.git"},
		"other host":      {Name: "b", CloneURL: "https://gitlab.com/acme/b.git"},
		"embedded creds":  {Name: "c", CloneURL: "https://user:pw@github.com/acme/c.git"},
		"name with slash": {Name: "d/e", CloneURL: "https://github.com/acme/d.git"},
		"empty name":      {Name: "", CloneURL: "https://github.com/acme/f.git"},
	}
	for label, repo := range cases {
		if err := AddRepository(path, repo); err == nil {
			t.Errorf("%s was accepted", label)
		}
	}
}
