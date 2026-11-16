package themes

import "testing"

func TestTheEmbeddedFileIsWellFormed(t *testing.T) {
	if err := Check(); err != nil {
		t.Fatal(err)
	}
	if All()[0].Name != "eggshell" {
		t.Fatalf("default = %q, want eggshell", All()[0].Name)
	}
}

func TestParseRefusesAThemeThatSetsAnUnknownToken(t *testing.T) {
	data := []byte(`[{"name":"a","label":"A","light":{"x":"1% 0 0"},"dark":{"x":"1% 0 0"}},{"name":"b","label":"B","dark":{"y":"1% 0 0"}}]`)
	if _, err := parse(data); err == nil {
		t.Fatal("expected refusal")
	}
}
