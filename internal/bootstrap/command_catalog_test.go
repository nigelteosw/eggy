package bootstrap

import (
	"reflect"
	"testing"
)

func testCatalogIndex() map[string]CatalogEntry {
	return buildCatalogIndex([]CatalogEntry{
		{Path: "status"},
		{Path: "repositories"},
		{Path: "repositories add"},
		{Path: "repositories remove"},
		{Path: "notes"},
		{Path: "notes set"},
		{Path: "model"},
		{Path: "model effort"},
		{Path: "model default"},
		{Path: "config"},
		{Path: "config set"},
		{Path: "config set model"},
		{Path: "stop"},
		{Path: "continue"},
	})
}

func TestParseTelegramInputMatchesLongestRegisteredPath(t *testing.T) {
	index := testCatalogIndex()
	req, ok := ParseTelegramInput(index, "/config set model alias=x provider=y model=z")
	if !ok {
		t.Fatal("expected match")
	}
	if !reflect.DeepEqual(req.Path, []string{"config", "set", "model"}) {
		t.Fatalf("path=%v", req.Path)
	}
	want := map[string]string{"alias": "x", "provider": "y", "model": "z"}
	if !reflect.DeepEqual(req.Named, want) {
		t.Fatalf("named=%v", req.Named)
	}
}

func TestParseTelegramInputFallsBackToShorterPathOnUnknownSubcommand(t *testing.T) {
	index := testCatalogIndex()
	req, ok := ParseTelegramInput(index, "/repositories bogus")
	if !ok {
		t.Fatal("expected the bare repositories entry to match")
	}
	if !reflect.DeepEqual(req.Path, []string{"repositories"}) || !reflect.DeepEqual(req.Args, []string{"bogus"}) {
		t.Fatalf("path=%v args=%v", req.Path, req.Args)
	}
}

func TestParseTelegramInputRejectsNonCommandText(t *testing.T) {
	if _, ok := ParseTelegramInput(testCatalogIndex(), "hello there"); ok {
		t.Fatal("plain text should not match a command")
	}
}

func TestParseTelegramInputRejectsUnknownTopLevelCommand(t *testing.T) {
	if _, ok := ParseTelegramInput(testCatalogIndex(), "/unknown"); ok {
		t.Fatal("unregistered command should not match")
	}
}

func TestParseTelegramInputPreservesOriginalSpacingInTrailingContent(t *testing.T) {
	index := testCatalogIndex()
	req, ok := ParseTelegramInput(index, "/notes set reviewer  Be   blunt.")
	if !ok {
		t.Fatal("expected match")
	}
	if req.Tail != "reviewer  Be   blunt." {
		t.Fatalf("tail=%q", req.Tail)
	}
}

func TestParseCLIArgsNormalizesDoubleDashFlagsToNamed(t *testing.T) {
	index := testCatalogIndex()
	req, ok := ParseCLIArgs(index, []string{"config", "set", "model", "--alias=x", "--provider", "y", "--model=z"})
	if !ok {
		t.Fatal("expected match")
	}
	if !reflect.DeepEqual(req.Path, []string{"config", "set", "model"}) {
		t.Fatalf("path=%v", req.Path)
	}
	want := map[string]string{"alias": "x", "provider": "y", "model": "z"}
	if !reflect.DeepEqual(req.Named, want) {
		t.Fatalf("named=%v", req.Named)
	}
}

func TestParseCLIArgsAndTelegramProduceEquivalentRequestsForNamedArgs(t *testing.T) {
	index := testCatalogIndex()
	telegramReq, ok := ParseTelegramInput(index, "/config set model alias=deepseek-pro provider=deepseek model=deepseek-v4-pro")
	if !ok {
		t.Fatal("expected telegram match")
	}
	cliReq, ok := ParseCLIArgs(index, []string{"config", "set", "model", "--alias=deepseek-pro", "--provider=deepseek", "--model=deepseek-v4-pro"})
	if !ok {
		t.Fatal("expected cli match")
	}
	if !reflect.DeepEqual(telegramReq.Path, cliReq.Path) {
		t.Fatalf("path mismatch telegram=%v cli=%v", telegramReq.Path, cliReq.Path)
	}
	if !reflect.DeepEqual(telegramReq.Named, cliReq.Named) {
		t.Fatalf("named mismatch telegram=%v cli=%v", telegramReq.Named, cliReq.Named)
	}
}

func TestParseCLIArgsAndTelegramProduceEquivalentRequestsForPositionalArgs(t *testing.T) {
	index := testCatalogIndex()
	telegramReq, ok := ParseTelegramInput(index, "/repositories add eggy https://github.com/nigelteosw/eggy.git main")
	if !ok {
		t.Fatal("expected telegram match")
	}
	cliReq, ok := ParseCLIArgs(index, []string{"repositories", "add", "eggy", "https://github.com/nigelteosw/eggy.git", "main"})
	if !ok {
		t.Fatal("expected cli match")
	}
	if !reflect.DeepEqual(telegramReq.Path, cliReq.Path) || !reflect.DeepEqual(telegramReq.Args, cliReq.Args) {
		t.Fatalf("telegram=%+v cli=%+v", telegramReq, cliReq)
	}
}

func TestParseCLIArgsRejectsUnknownCommand(t *testing.T) {
	if _, ok := ParseCLIArgs(testCatalogIndex(), []string{"unknown"}); ok {
		t.Fatal("unregistered command should not match")
	}
}

func TestMatchCatalogEntryPrefersLongestPath(t *testing.T) {
	index := testCatalogIndex()
	entry, rest, ok := matchCatalogEntry(index, []string{"model", "effort", "high"})
	if !ok || entry.Path != "model effort" || !reflect.DeepEqual(rest, []string{"high"}) {
		t.Fatalf("entry=%+v rest=%v ok=%v", entry, rest, ok)
	}
	entry, rest, ok = matchCatalogEntry(index, []string{"model", "openrouter-pro"})
	if !ok || entry.Path != "model" || !reflect.DeepEqual(rest, []string{"openrouter-pro"}) {
		t.Fatalf("entry=%+v rest=%v ok=%v", entry, rest, ok)
	}
}
