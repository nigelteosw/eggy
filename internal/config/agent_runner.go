package config

import (
	"fmt"
	"slices"
	"strings"
)

// AgentRunnerConfig switches the agent runner extension on and names the model
// its coding turns use. It is startup configuration, like finance: a change
// applies on restart, because an unconfigured extension registers no tool,
// mounts no route and runs no worker.
type AgentRunnerConfig struct {
	// Enabled is a plain bool: absent and false mean the same thing.
	Enabled bool `yaml:"enabled"`
	// Model is one of the models: aliases. Coding turns always use it, and it
	// is independent of the account's /model choice.
	Model string `yaml:"model,omitempty"`
	// Skills toggles each bundled skill named in AgentRunnerSkills.
	Skills map[string]bool `yaml:"skills,omitempty"`
}

// AgentRunnerSkills is the one list of skills the agent runner bundles. A
// skill the owner names here and not in this list is a typo, so it is refused.
var AgentRunnerSkills = []string{"brainstorming", "writing-plans", "verification-before-completion"}

// maxRepositoryChecks bounds how many check commands one repository may list.
const maxRepositoryChecks = 8

// validateAgentRunner checks the section's shape always, and the model only
// when the extension is on: a disabled runner may keep a stale model name
// without that stopping the deployment from starting.
func (c Config) validateAgentRunner() error {
	for skill := range c.AgentRunner.Skills {
		if !slices.Contains(AgentRunnerSkills, skill) {
			return fmt.Errorf("agent_runner.skills: unknown skill %q", skill)
		}
	}
	if c.AgentRunner.Enabled {
		if _, ok := c.ModelAliases[c.AgentRunner.Model]; !ok {
			return fmt.Errorf("agent_runner.model %q is not a configured model alias", c.AgentRunner.Model)
		}
	}
	for _, repo := range c.Repositories {
		if err := validateRepositoryChecks(repo.Name, repo.Checks); err != nil {
			return err
		}
	}
	return nil
}

// validateRepositoryChecks refuses a check the runner could not execute: an
// empty argv has no program, and an empty program name is not one either.
func validateRepositoryChecks(repository string, checks [][]string) error {
	if len(checks) > maxRepositoryChecks {
		return fmt.Errorf("repositories %q: at most %d checks", repository, maxRepositoryChecks)
	}
	for i, argv := range checks {
		if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
			return fmt.Errorf("repositories %q: checks[%d] must be a command with a program name", repository, i)
		}
	}
	return nil
}

// SetAgentRunner saves the agent_runner section under the config lock, with
// the same validation every other write gets.
func SetAgentRunner(path string, enabled bool, model string, skills map[string]bool) error {
	return mutate(path, func(cfg *Config) error {
		cfg.AgentRunner.Enabled = enabled
		cfg.AgentRunner.Model = model
		cfg.AgentRunner.Skills = skills
		return nil
	})
}

// SetRepositoryChecks replaces one allowed repository's check list. nil or
// empty clears it. A name that is not in repositories: is refused, because a
// check on a repository the owner never allowed is not a check anyone runs.
func SetRepositoryChecks(path, repository string, checks [][]string) error {
	return mutate(path, func(cfg *Config) error {
		for i := range cfg.Repositories {
			if cfg.Repositories[i].Name == repository {
				cfg.Repositories[i].Checks = checks
				return nil
			}
		}
		return fmt.Errorf("unknown repository %q", repository)
	})
}
