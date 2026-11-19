package config

// TerminalConfig switches the terminal extension on. It is startup
// configuration like finance: a change applies on restart, and disabled the
// extension starts no herdr-core, mounts no route and draws no tab.
type TerminalConfig struct {
	Enabled bool `yaml:"enabled"`
}

// SetTerminalEnabled saves the terminal section under the config lock, with
// the same validation as every other write.
func SetTerminalEnabled(path string, enabled bool) error {
	return mutate(path, func(cfg *Config) error {
		cfg.Terminal.Enabled = enabled
		return nil
	})
}
