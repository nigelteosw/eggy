package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerfileHasNoCLICodingAgentAndKeepsGoToolchainForValidation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	dockerfile := string(body)

	for _, unwanted := range []string{"codex", "claude", "CODEX", "CLAUDE"} {
		if strings.Contains(dockerfile, unwanted) {
			t.Fatalf("Dockerfile still references a CLI coding agent (%q): %s", unwanted, dockerfile)
		}
	}
	if !strings.Contains(dockerfile, "COPY --from=builder /usr/local/go /usr/local/go") {
		t.Fatalf("Dockerfile missing Go toolchain needed by the terminal tool's build/test validation: %s", dockerfile)
	}
}

func TestReadmeDoesNotDocumentCLICodingAgentSetup(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	readme := string(body)

	for _, unwanted := range []string{"claude setup-token", "CLAUDE_CODE_OAUTH_TOKEN", "/coding_agent", "codex login", "CODEX_HOME"} {
		if strings.Contains(readme, unwanted) {
			t.Fatalf("README.md still documents a CLI coding agent (%q)", unwanted)
		}
	}
	if !strings.Contains(readme, "read_file") || !strings.Contains(readme, "terminal") {
		t.Fatalf("README.md should document the native implementation tools: %s", readme)
	}
}
