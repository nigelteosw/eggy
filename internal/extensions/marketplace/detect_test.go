package marketplace

import "testing"

func TestDetectLayout(t *testing.T) {
	files := func(paths ...string) Tree {
		tree := Tree{Files: map[string][]byte{}}
		for _, p := range paths {
			tree.Files[p] = nil
		}
		return tree
	}
	cases := []struct {
		name       string
		tree       Tree
		kind, path string
		wantErr    bool
	}{
		{"catalog wins", files(claudeCatalogPath, "skills/pdf/SKILL.md"), KindClaudeMarketplace, "", false},
		{"skills folder", files("skills/pdf/SKILL.md", "other/x/SKILL.md", "other/y/SKILL.md"), KindSkillsRepo, "skills", false},
		{"busiest folder", files("a/one/SKILL.md", "b/one/SKILL.md", "b/two/SKILL.md"), KindSkillsRepo, "b", false},
		{"hidden and root ignored", files(".claude/skills/x/SKILL.md", "x/SKILL.md"), "", "", true},
		{"nothing", files("README.md"), "", "", true},
	}
	for _, c := range cases {
		kind, p, err := detectLayout(c.tree)
		if (err != nil) != c.wantErr || kind != c.kind || p != c.path {
			t.Errorf("%s: got %q %q %v", c.name, kind, p, err)
		}
	}
}
