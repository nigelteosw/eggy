package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/home"
)

func newHomeFiles(t *testing.T) (*HomeFiles, home.Layout) {
	t.Helper()
	layout := home.At(t.TempDir())
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	return NewHomeFiles(layout), layout
}

// validConfigYAML is the smallest config.yaml that survives Validate, used to
// prove the write path accepts a real edit rather than only rejecting bad
// ones.
func validConfigYAML(layout home.Layout) string {
	return strings.Join([]string{
		"server:",
		"  listen: \":8080\"",
		"  public_base_url: \"https://eggy.example.com\"",
		"  telegram_webhook_path: \"/webhooks/telegram\"",
		"data_dir: \"" + layout.Root + "\"",
		"owner:",
		"  id: \"1\"",
		"agent:",
		"  default_model: \"deepseek-pro\"",
		"providers:",
		"  deepseek:",
		"    adapter: \"openai_compatible\"",
		"    base_url: \"https://api.deepseek.com\"",
		"    api_key_env: \"DEEPSEEK_API_KEY\"",
		"models:",
		"  deepseek-pro:",
		"    provider: \"deepseek\"",
		"    model: \"deepseek-v4-pro\"",
		"",
	}, "\n")
}

func TestListCoversTheOwnerFacingHomeAndNothingElse(t *testing.T) {
	files, layout := newHomeFiles(t)
	if err := os.WriteFile(layout.Memory(), []byte("# memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.State(), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	listing, err := files.List()
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]FileAccess{}
	for _, file := range listing {
		paths[file.Path] = file.Access
	}
	for _, want := range []string{"config.yaml", "SOUL.md", "HEARTBEAT.md", "memories/MEMORY.md", ".env", "auth.json"} {
		if _, ok := paths[want]; !ok {
			t.Fatalf("%s missing from listing: %#v", want, paths)
		}
	}
	// state.json, eggy.db, runs/, changes/, and sessions/ are Eggy's own
	// bookkeeping and must never appear.
	if _, ok := paths["state.json"]; ok {
		t.Fatal("state.json is exposed in the listing")
	}
	if access := paths[".env"]; access != AccessSecret {
		t.Fatalf(".env access=%q, want secret", access)
	}
	if access := paths["memories/MEMORY.md"]; access != AccessEdit {
		t.Fatalf("MEMORY.md access=%q, want edit", access)
	}
}

// TestSecretFilesAreListedButNeverServed is the security boundary: an owner
// can see .env exists and when it changed, but no HTTP session ever reads
// the credentials inside it.
func TestSecretFilesAreListedButNeverServed(t *testing.T) {
	files, layout := newHomeFiles(t)
	if err := os.WriteFile(layout.Env(), []byte("DEEPSEEK_API_KEY=super-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := files.Read(".env"); !errors.Is(err, ErrFileForbidden) {
		t.Fatalf("err=%v, want ErrFileForbidden", err)
	}
	if err := files.Write(".env", "DEEPSEEK_API_KEY=x"); !errors.Is(err, ErrFileForbidden) {
		t.Fatalf("err=%v, want ErrFileForbidden", err)
	}
	// The file on disk must be exactly as it was.
	body, err := os.ReadFile(layout.Env())
	if err != nil || string(body) != "DEEPSEEK_API_KEY=super-secret" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestLogsAreReadableButNotWritable(t *testing.T) {
	files, layout := newHomeFiles(t)
	if err := os.WriteFile(filepath.Join(layout.Logs(), GatewayLogName), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	content, access, err := files.Read("logs/" + GatewayLogName)
	if err != nil || content != "hello" || access != AccessRead {
		t.Fatalf("content=%q access=%q err=%v", content, access, err)
	}
	if err := files.Write("logs/"+GatewayLogName, "tampered"); !errors.Is(err, ErrFileReadOnly) {
		t.Fatalf("err=%v, want ErrFileReadOnly", err)
	}
}

// TestPathsOutsideTheAllowlistAreRefused covers traversal and any home file
// that simply is not exposed.
func TestPathsOutsideTheAllowlistAreRefused(t *testing.T) {
	files, _ := newHomeFiles(t)
	for _, path := range []string{
		"../../etc/passwd", "memories/../../.env", "/etc/passwd", "state.json",
		"eggy.db", "runs/anything", "memories", "", "./config.yaml",
		"skills/evil.sh", "cron/../auth.json",
	} {
		if _, _, err := files.Read(path); err == nil {
			t.Fatalf("read %q was allowed", path)
		}
		if err := files.Write(path, "x"); err == nil {
			t.Fatalf("write %q was allowed", path)
		}
	}
}

func TestWriteEditableFileRoundTrips(t *testing.T) {
	files, layout := newHomeFiles(t)
	if err := files.Write("SOUL.md", "# Eggy Soul\n\nBe useful.\n"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(layout.Soul())
	if err != nil || string(body) != "# Eggy Soul\n\nBe useful.\n" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	if info, _ := os.Stat(layout.Soul()); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v, want 0600", info.Mode().Perm())
	}
	content, access, err := files.Read("SOUL.md")
	if err != nil || access != AccessEdit || !strings.Contains(content, "Be useful.") {
		t.Fatalf("content=%q access=%q err=%v", content, access, err)
	}
}

// TestMissingEditableFileReadsAsEmpty lets the UI offer to create a file
// Eggy has not written yet, instead of showing an error.
func TestMissingEditableFileReadsAsEmpty(t *testing.T) {
	files, _ := newHomeFiles(t)
	content, access, err := files.Read("HEARTBEAT.md")
	if err != nil || content != "" || access != AccessEdit {
		t.Fatalf("content=%q access=%q err=%v", content, access, err)
	}
}

// TestInvalidConfigIsRefusedBeforeItLands is the guarantee that matters most
// for raw YAML editing: the UI cannot leave behind a home Eggy will not boot
// from.
func TestInvalidConfigIsRefusedBeforeItLands(t *testing.T) {
	files, layout := newHomeFiles(t)
	original := validConfigYAML(layout)
	if err := files.Write("config.yaml", original); err != nil {
		t.Fatal(err)
	}
	for name, broken := range map[string]string{
		"not yaml":       "server: [unclosed\n",
		"unknown key":    original + "nonsense_key: true\n",
		"fails Validate": strings.Replace(original, "  default_model: \"deepseek-pro\"", "  default_model: \"missing-alias\"", 1),
	} {
		if err := files.Write("config.yaml", broken); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	// The good config must still be on disk, untouched by the failures.
	body, err := os.ReadFile(layout.Config())
	if err != nil || string(body) != original {
		t.Fatalf("config was damaged: %q err=%v", body, err)
	}
}

func TestCronJobsMustBeValidYAML(t *testing.T) {
	files, _ := newHomeFiles(t)
	if err := files.Write("cron/abc123.yaml", "instruction: check the oven\nenabled: true\n"); err != nil {
		t.Fatal(err)
	}
	if err := files.Write("cron/abc123.yaml", "instruction: [unclosed\n"); err == nil {
		t.Fatal("expected invalid YAML to be refused")
	}
}

func TestOversizedContentIsRefused(t *testing.T) {
	files, _ := newHomeFiles(t)
	if err := files.Write("SOUL.md", strings.Repeat("x", maxEditableBytes+1)); err == nil {
		t.Fatal("expected oversized content to be refused")
	}
}
