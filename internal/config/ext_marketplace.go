package config

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// MarketplaceConfig turns the marketplace extension on and names the catalogs
// it reads. It is startup configuration like finance: a change applies on
// restart, and an absent or disabled section costs nothing.
type MarketplaceConfig struct {
	Enabled bool                `yaml:"enabled"`
	Sources []MarketplaceSource `yaml:"sources,omitempty"`
}

// MarketplaceSource is one catalog the owner reads from GitHub. Kind is
// "claude_marketplace" or "skills_repo". The extension owns these values; they
// are repeated here because config may not import the extension.
type MarketplaceSource struct {
	Name string `yaml:"name"`
	Kind string `yaml:"kind"`
	Repo string `yaml:"repo"`
	Path string `yaml:"path,omitempty"`
	Ref  string `yaml:"ref,omitempty"`
}

var (
	marketplaceNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	marketplaceRepoPattern = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
)

func (c Config) validateMarketplace() error {
	seen := map[string]bool{}
	for _, source := range c.Marketplace.Sources {
		if !marketplaceNamePattern.MatchString(source.Name) {
			return fmt.Errorf("marketplace source name %q must be 1-64 lowercase letters, digits, or hyphens", source.Name)
		}
		if seen[source.Name] {
			return fmt.Errorf("marketplace source %q is listed twice", source.Name)
		}
		seen[source.Name] = true
		if source.Kind != "claude_marketplace" && source.Kind != "skills_repo" {
			return fmt.Errorf("marketplace source %q has kind %q; want claude_marketplace or skills_repo", source.Name, source.Kind)
		}
		if !marketplaceRepoPattern.MatchString(source.Repo) {
			return fmt.Errorf("marketplace source %q repo %q must be owner/name", source.Name, source.Repo)
		}
		if source.Path != "" && (strings.HasPrefix(source.Path, "/") || strings.Contains(path.Clean(source.Path), "..")) {
			return fmt.Errorf("marketplace source %q path %q must be relative and stay inside the repository", source.Name, source.Path)
		}
		if strings.ContainsAny(source.Ref, " \t\r\n") {
			return fmt.Errorf("marketplace source %q ref %q contains whitespace", source.Name, source.Ref)
		}
	}
	return nil
}

// SetMarketplace saves the marketplace section under the config lock, with the
// same validation as every other write. A refused save leaves the file alone.
func SetMarketplace(path string, enabled bool, sources []MarketplaceSource) error {
	return mutate(path, func(cfg *Config) error {
		cfg.Marketplace.Enabled = enabled
		cfg.Marketplace.Sources = sources
		return nil
	})
}

// GetMarketplaceSources reads the configured catalogs, for the settings card.
func GetMarketplaceSources(path string) ([]MarketplaceSource, error) {
	cfg, err := LoadDocument(path)
	if err != nil {
		return nil, err
	}
	return cfg.Marketplace.Sources, nil
}

// SetMarketplaceSources replaces the source list and leaves enabled as it is,
// inside the one locked write, so saving sources can never revert a concurrent
// change to marketplace.enabled.
func SetMarketplaceSources(path string, sources []MarketplaceSource) error {
	return mutate(path, func(cfg *Config) error {
		cfg.Marketplace.Sources = sources
		return nil
	})
}
