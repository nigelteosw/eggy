package marketplace

import (
	"strings"
	"testing"
)

// memTree builds a Tree from path -> content pairs. Tests use it instead of
// on-disk fixtures so each case states its whole repository inline.
func memTree(files map[string]string) Tree {
	tree := Tree{Files: map[string][]byte{}, Skipped: map[string]int64{}}
	for name, body := range files {
		tree.Files[name] = []byte(body)
	}
	return tree
}

func itemByName(items []Item, name string) (Item, bool) {
	for _, item := range items {
		if item.Name == name {
			return item, true
		}
	}
	return Item{}, false
}

func TestCatalogClaudeMarketplace(t *testing.T) {
	tree := memTree(map[string]string{
		".claude-plugin/marketplace.json": `{"name":"team","plugins":[
			{"name":"fmt","source":"./plugins/fmt","description":"Formats"},
			{"name":"gh","source":{"source":"github","repo":"o/gh","ref":"v1"},"description":"GH"},
			{"name":"sub","source":{"source":"git-subdir","url":"https://github.com/o/mono.git","path":"tools/sub","sha":"abc"}},
			{"name":"npmthing","source":{"source":"npm","package":"x"}},
			{"name":"other","source":{"source":"url","url":"https://example.com/x.git"}}
		]}`,
	})
	items, err := catalogItems(Source{Name: "team", Kind: KindClaudeMarketplace, Repo: "o/team"}, tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("got %d items, want 5: %+v", len(items), items)
	}
	fmtItem, _ := itemByName(items, "fmt")
	if fmtItem.Kind != "plugin" || fmtItem.Root != "plugins/fmt" || fmtItem.Description != "Formats" || fmtItem.Unsupported != "" {
		t.Fatalf("relative plugin = %+v", fmtItem)
	}
	gh, _ := itemByName(items, "gh")
	if gh.External == nil || gh.External.Repo != "o/gh" || gh.External.Ref != "v1" {
		t.Fatalf("github plugin = %+v", gh)
	}
	sub, _ := itemByName(items, "sub")
	if sub.External == nil || sub.External.Repo != "o/mono" || sub.External.Path != "tools/sub" || sub.External.SHA != "abc" {
		t.Fatalf("git-subdir plugin = %+v", sub)
	}
	npm, _ := itemByName(items, "npmthing")
	if !strings.Contains(npm.Unsupported, "npm") {
		t.Fatalf("npm source not unsupported: %+v", npm)
	}
	other, _ := itemByName(items, "other")
	if !strings.Contains(other.Unsupported, "url") {
		t.Fatalf("url source not unsupported: %+v", other)
	}
}

func TestCatalogSkillsRepo(t *testing.T) {
	tree := memTree(map[string]string{
		"README.md":               "# skills",
		"skills/pdf/SKILL.md":     "---\nname: pdf\ndescription: Fill PDF forms\n---\n\nbody\n",
		"skills/pdf/reference.md": "reference",
		"skills/docx/SKILL.md":    "---\nname: docx\ndescription: Edit Word files\n---\n\nbody\n",
		"skills/notes.md":         "not a skill directory",
	})
	items, err := catalogItems(Source{Name: "anthropic", Kind: KindSkillsRepo, Repo: "anthropics/skills"}, tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2: %+v", len(items), items)
	}
	if items[0].Name != "docx" || items[1].Name != "pdf" {
		t.Fatalf("items not sorted by name: %+v", items)
	}
	pdf := items[1]
	if pdf.Kind != "skill" || pdf.Root != "skills/pdf" || pdf.Description != "Fill PDF forms" {
		t.Fatalf("pdf item = %+v", pdf)
	}
}

func TestCatalogRefusesDotDot(t *testing.T) {
	tree := memTree(map[string]string{
		".claude-plugin/marketplace.json": `{"name":"team","plugins":[{"name":"x","source":"./../x"}]}`,
	})
	items, err := catalogItems(Source{Name: "team", Kind: KindClaudeMarketplace, Repo: "o/team"}, tree)
	if err != nil {
		t.Fatal(err)
	}
	x, ok := itemByName(items, "x")
	if !ok || !strings.Contains(x.Unsupported, "leaves the repository") {
		t.Fatalf("dot-dot source not refused: %+v", items)
	}

	_, err = catalogItems(Source{Name: "s", Kind: KindSkillsRepo, Repo: "o/s", Path: "../x"}, memTree(nil))
	if err == nil {
		t.Fatal("skills_repo path with .. accepted")
	}
}

func TestCatalogMalformedJSON(t *testing.T) {
	tree := memTree(map[string]string{
		".claude-plugin/marketplace.json": `{not json`,
	})
	_, err := catalogItems(Source{Name: "team", Kind: KindClaudeMarketplace, Repo: "o/team"}, tree)
	if err == nil || !strings.Contains(err.Error(), ".claude-plugin/marketplace.json") {
		t.Fatalf("err = %v, want one naming the file", err)
	}
}
