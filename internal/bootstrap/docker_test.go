package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerfilePinsClaudeCodeAndConfiguresDataDir(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	dockerfile := string(body)

	if !strings.Contains(dockerfile, "ARG CLAUDE_CODE_VERSION=2.1.215") {
		t.Fatalf("Dockerfile missing pinned Claude Code version: %s", dockerfile)
	}
	if !strings.Contains(dockerfile, "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}") {
		t.Fatalf("Dockerfile missing Claude Code package install: %s", dockerfile)
	}
	if !strings.Contains(dockerfile, "/data/claude") {
		t.Fatalf("Dockerfile missing /data/claude directory: %s", dockerfile)
	}
	if !strings.Contains(dockerfile, "CLAUDE_CONFIG_DIR") {
		t.Fatalf("Dockerfile missing CLAUDE_CONFIG_DIR: %s", dockerfile)
	}
	if strings.Contains(dockerfile, "CLAUDE_CODE_OAUTH_TOKEN=") {
		t.Fatalf("Dockerfile must not embed a CLAUDE_CODE_OAUTH_TOKEN value: %s", dockerfile)
	}
}

func TestReadmeDocumentsClaudeCodeRailwaySetup(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	readme := string(body)

	for _, want := range []string{"claude setup-token", "CLAUDE_CODE_OAUTH_TOKEN", "/coding_agent"} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README.md missing %q", want)
		}
	}
	if strings.Contains(readme, "CLAUDE_CODE_OAUTH_TOKEN=") {
		t.Fatalf("README.md must not embed a CLAUDE_CODE_OAUTH_TOKEN value: %s", readme)
	}
}
