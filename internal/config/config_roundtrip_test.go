package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConfigMarshalsUnderItsYAMLKeysNotItsGoNames(t *testing.T) {
	cfg, _, err := loadText(t, validConfig(), testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// models is the one field whose Go name (ModelAliases) differs from its
	// YAML key, so it is the one a dropped tag would silently rename.
	for _, key := range []string{"models:", "providers:", "agent:", "server:", "data_dir:"} {
		if !strings.Contains(string(body), key) {
			t.Fatalf("marshaled config is missing %q:\n%s", key, body)
		}
	}
	if strings.Contains(string(body), "modelaliases:") {
		t.Fatalf("ModelAliases marshaled under its Go name:\n%s", body)
	}
	// And it must load back.
	if _, _, err := loadText(t, string(body), testSecrets()); err != nil {
		t.Fatalf("marshaled config does not reload: %v\n%s", err, body)
	}
}
