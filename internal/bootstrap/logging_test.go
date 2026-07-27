package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/home"
)

// TestLoggerRedactsSecretsOnDisk is the reason logs live in the home at all:
// a token that reaches a log line through an error string must not be
// readable in gateway.log afterwards.
func TestLoggerRedactsSecretsOnDisk(t *testing.T) {
	layout := home.At(t.TempDir())
	secrets := Secrets{
		TelegramBotToken: "telegram-token-value",
		GitHubToken:      "github-token-value",
		ProviderAPIKeys:  map[string]string{"deepseek": "deepseek-key-value"},
	}
	logger, closer, err := NewLogger(layout, secrets)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("calling provider", "url", "https://api.deepseek.com?key=deepseek-key-value")
	logger.Error("push failed", "error", "remote rejected token github-token-value")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}

	gateway := readLog(t, layout, GatewayLogName)
	errorsLog := readLog(t, layout, ErrorsLogName)
	for _, secret := range []string{"deepseek-key-value", "github-token-value", "telegram-token-value"} {
		if strings.Contains(gateway, secret) {
			t.Fatalf("gateway.log leaked %q:\n%s", secret, gateway)
		}
		if strings.Contains(errorsLog, secret) {
			t.Fatalf("errors.log leaked %q:\n%s", secret, errorsLog)
		}
	}
	if !strings.Contains(gateway, "[redacted]") {
		t.Fatalf("nothing was redacted:\n%s", gateway)
	}
}

// TestErrorsLogHoldsOnlyErrors proves errors.log is the short file an
// operator can actually scan, not a second copy of everything.
func TestErrorsLogHoldsOnlyErrors(t *testing.T) {
	layout := home.At(t.TempDir())
	logger, closer, err := NewLogger(layout, Secrets{})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("routine startup")
	logger.Error("something broke")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	gateway := readLog(t, layout, GatewayLogName)
	if !strings.Contains(gateway, "routine startup") || !strings.Contains(gateway, "something broke") {
		t.Fatalf("gateway.log is missing records:\n%s", gateway)
	}
	errorsLog := readLog(t, layout, ErrorsLogName)
	if strings.Contains(errorsLog, "routine startup") {
		t.Fatalf("errors.log captured a non-error:\n%s", errorsLog)
	}
	if !strings.Contains(errorsLog, "something broke") {
		t.Fatalf("errors.log is missing the error:\n%s", errorsLog)
	}
}

func TestLogFilesAreOwnerOnly(t *testing.T) {
	layout := home.At(t.TempDir())
	_, closer, err := NewLogger(layout, Secrets{})
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	for _, name := range []string{GatewayLogName, ErrorsLogName} {
		info, err := os.Stat(filepath.Join(layout.Logs(), name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode=%v err=%v", name, info.Mode().Perm(), err)
		}
	}
}

// TestSecretValuesSkipsEmptyStrings guards the redactor's worst failure
// mode: an empty secret would match between every byte of every line.
func TestSecretValuesSkipsEmptyStrings(t *testing.T) {
	values := Secrets{GitHubToken: "real", GoogleClientID: "", ProviderAPIKeys: map[string]string{"a": " "}}.Values()
	if len(values) != 1 || values[0] != "real" {
		t.Fatalf("values=%#v", values)
	}
}

// TestRedactorPrefersTheLongestSecret proves a secret that contains another
// is removed whole, rather than leaving its tail behind.
func TestRedactorPrefersTheLongestSecret(t *testing.T) {
	var sink strings.Builder
	writer := newRedactor(&sink, []string{"abc", "abcdef"})
	if _, err := writer.Write([]byte("token=abcdef\n")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sink.String(), "def") {
		t.Fatalf("partial redaction left a suffix: %q", sink.String())
	}
}

func readLog(t *testing.T, layout home.Layout, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(layout.Logs(), name))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
