package marketplace

import (
	"strings"
	"testing"
)

func planInput(item Item) PlanInput {
	return PlanInput{
		Source:      Source{Name: "team", Kind: KindClaudeMarketplace, Repo: "o/team"},
		Repo:        "o/deploy",
		SHA:         "abc123",
		Item:        item,
		SkillExists: func(string) bool { return false },
		MCPExists:   func(string) bool { return false },
	}
}

func deployPlugin() (Item, Tree) {
	item := Item{Name: "deploy-tools", Kind: "plugin", Root: "plugins/deploy"}
	tree := memTree(map[string]string{
		"plugins/deploy/.mcp.json":               `{"mcpServers":{"api":{"url":"https://mcp.example.com/mcp"},"local":{"command":"node","args":["server.js"]}}}`,
		"plugins/deploy/skills/pdf/SKILL.md":     "---\nname: pdf\ndescription: Fill PDF forms\n---\n\nRun scripts/fill.py to fill forms.\n",
		"plugins/deploy/skills/pdf/reference.md": "reference text",
		"plugins/deploy/skills/docx/SKILL.md":    "---\nname: docx\ndescription: Edit Word files\n---\n\nbody\n",
		"plugins/deploy/hooks/hooks.json":        `{}`,
		"plugins/deploy/bin/tool":                "binary",
	})
	tree.Skipped["plugins/deploy/skills/pdf/scripts/fill.py"] = 12
	tree.Skipped["plugins/deploy/bin/tool"] = 6
	return item, tree
}

func TestPlanMapsPluginComponents(t *testing.T) {
	item, tree := deployPlugin()
	plan, err := buildPlan(planInput(item), tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Skills) != 2 || plan.Skills[0].Name != "docx" || plan.Skills[1].Name != "pdf" {
		t.Fatalf("skills = %+v, want docx and pdf", plan.Skills)
	}
	if len(plan.MCP) != 1 || plan.MCP[0].Name != "deploy-tools-api" || plan.MCP[0].URL != "https://mcp.example.com/mcp" {
		t.Fatalf("mcp = %+v, want deploy-tools-api over http", plan.MCP)
	}
	reasons := map[string]string{}
	for _, s := range plan.Skipped {
		reasons[s.Path] = s.Reason
	}
	if !strings.Contains(reasons["plugins/deploy/.mcp.json#local"], "downloaded program") {
		t.Fatalf("stdio server reason = %q", reasons["plugins/deploy/.mcp.json#local"])
	}
	if !strings.Contains(reasons["plugins/deploy/hooks/hooks.json"], "not supported") {
		t.Fatalf("hooks reason = %q", reasons["plugins/deploy/hooks/hooks.json"])
	}
	if !strings.Contains(reasons["plugins/deploy/bin/tool"], "not supported") {
		t.Fatalf("bin reason = %q", reasons["plugins/deploy/bin/tool"])
	}
	if !strings.Contains(reasons["plugins/deploy/skills/pdf/scripts/fill.py"], "never runs") {
		t.Fatalf("script reason = %q", reasons["plugins/deploy/skills/pdf/scripts/fill.py"])
	}
}

func TestPlanNormalizesCRLFAndBOM(t *testing.T) {
	item := Item{Name: "p", Kind: "skill", Root: "skills/pdf"}
	tree := memTree(map[string]string{
		"skills/pdf/SKILL.md": "\ufeff---\r\nname: pdf\r\ndescription: Fill\r\n---\r\n\r\nbody\r\n",
	})
	plan, err := buildPlan(planInput(item), tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Skills) != 1 {
		t.Fatalf("skills = %+v", plan.Skills)
	}
	got := plan.Skills[0]
	if got.Description != "Fill" || got.Body != "body" {
		t.Fatalf("normalized skill = %+v", got)
	}
}

func TestPlanSkipsLongDescription(t *testing.T) {
	item := Item{Name: "p", Kind: "skill", Root: "skills/long"}
	tree := memTree(map[string]string{
		"skills/long/SKILL.md": "---\nname: long\ndescription: " + strings.Repeat("d", 1025) + "\n---\n\nbody\n",
	})
	plan, err := buildPlan(planInput(item), tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Skills) != 0 {
		t.Fatalf("over-long skill planned: %+v", plan.Skills)
	}
	if len(plan.Skipped) != 1 || !strings.Contains(plan.Skipped[0].Reason, "1024") {
		t.Fatalf("skipped = %+v, want a reason naming 1024 bytes", plan.Skipped)
	}
}

func TestPlanInvalidNameIsConflict(t *testing.T) {
	item := Item{Name: "p", Kind: "skill", Root: "skills/tools"}
	tree := memTree(map[string]string{
		"skills/tools/SKILL.md": "---\nname: PDF Tools\ndescription: Fill\n---\n\nbody\n",
	})
	plan, err := buildPlan(planInput(item), tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0] != "PDF Tools" {
		t.Fatalf("conflicts = %v, want PDF Tools", plan.Conflicts)
	}

	in := planInput(item)
	in.Renames = map[string]string{"PDF Tools": "pdf-tools"}
	plan, err = buildPlan(in, tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 0 || len(plan.Skills) != 1 || plan.Skills[0].Name != "pdf-tools" {
		t.Fatalf("renamed plan = %+v, conflicts %v", plan.Skills, plan.Conflicts)
	}
}

func TestPlanExistingNameIsConflict(t *testing.T) {
	item := Item{Name: "p", Kind: "skill", Root: "skills/pdf"}
	tree := memTree(map[string]string{
		"skills/pdf/SKILL.md": "---\nname: pdf\ndescription: Fill\n---\n\nbody\n",
	})
	in := planInput(item)
	in.SkillExists = func(name string) bool { return name == "pdf" }
	plan, err := buildPlan(in, tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0] != "pdf" {
		t.Fatalf("conflicts = %v, want pdf", plan.Conflicts)
	}

	in.Replacing = map[string]bool{"skill/pdf": true}
	plan, err = buildPlan(in, tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 0 {
		t.Fatalf("owned name conflicts = %v, want none", plan.Conflicts)
	}
}

func TestPlanWarnsOnSkippedReference(t *testing.T) {
	item := Item{Name: "p", Kind: "skill", Root: "skills/pdf"}
	tree := memTree(map[string]string{
		"skills/pdf/SKILL.md": "---\nname: pdf\ndescription: Fill\n---\n\nRun scripts/fill.py first.\n",
	})
	tree.Skipped["skills/pdf/scripts/fill.py"] = 9
	plan, err := buildPlan(planInput(item), tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Skills) != 1 {
		t.Fatalf("skills = %+v", plan.Skills)
	}
	found := false
	for _, w := range plan.Skills[0].Warnings {
		if strings.Contains(w, "may not work") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no reference warning: %v", plan.Skills[0].Warnings)
	}
}

func TestPlanDigestStable(t *testing.T) {
	item, tree := deployPlugin()
	a, err := buildPlan(planInput(item), tree)
	if err != nil {
		t.Fatal(err)
	}
	b, err := buildPlan(planInput(item), tree)
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest == "" || a.Digest != b.Digest {
		t.Fatalf("digests differ: %q vs %q", a.Digest, b.Digest)
	}

	tree.Files["plugins/deploy/skills/docx/SKILL.md"] = []byte("---\nname: docx\ndescription: Edit Word files\n---\n\nbody changed\n")
	c, err := buildPlan(planInput(item), tree)
	if err != nil {
		t.Fatal(err)
	}
	if c.Digest == a.Digest {
		t.Fatal("one changed body byte left the digest unchanged")
	}
}

func TestPlanDigestCoversRenames(t *testing.T) {
	item := Item{Name: "p", Kind: "skill", Root: "skills/tools"}
	tree := memTree(map[string]string{
		"skills/tools/SKILL.md": "---\nname: PDF Tools\ndescription: Fill\n---\n\nbody\n",
	})
	in := planInput(item)
	in.Renames = map[string]string{"PDF Tools": "pdf-tools"}
	a, err := buildPlan(in, tree)
	if err != nil {
		t.Fatal(err)
	}
	in.Renames = map[string]string{"PDF Tools": "pdf-two"}
	b, err := buildPlan(in, tree)
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest == b.Digest {
		t.Fatal("different renames produced the same digest")
	}
}

func TestPlanSkipsMCPServersTheConfigWouldRefuse(t *testing.T) {
	item := Item{Name: "deploy-tools", Kind: "plugin", Root: "plugins/deploy"}
	tree := memTree(map[string]string{
		"plugins/deploy/.mcp.json": `{"mcpServers":{
			"local":{"url":"http://localhost:3000/mcp"},
			"bad name":{"url":"https://mcp.example.com/a"},
			"dup":{"url":"https://mcp.example.com/b"},
			"taken":{"url":"https://mcp.example.com/c"}
		}}`,
		"plugins/deploy/.claude-plugin/plugin.json": `{"mcpServers":{"dup":{"url":"https://mcp.example.com/b2"}}}`,
	})
	in := planInput(item)
	in.MCPExists = func(name string) bool { return name == "deploy-tools-taken" }
	plan, err := buildPlan(in, tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 0 {
		t.Fatalf("conflicts = %v; an MCP name cannot be renamed, so it must be skipped", plan.Conflicts)
	}
	if len(plan.MCP) != 1 || plan.MCP[0].Name != "deploy-tools-dup" {
		t.Fatalf("mcp = %+v, want only the first dup", plan.MCP)
	}
	reasons := map[string]string{}
	for _, s := range plan.Skipped {
		reasons[s.Path] = s.Reason
	}
	for path, want := range map[string]string{
		"plugins/deploy/.mcp.json#local":                "https",
		"plugins/deploy/.mcp.json#bad name":             "not valid",
		"plugins/deploy/.mcp.json#taken":                "already configured",
		"plugins/deploy/.claude-plugin/plugin.json#dup": "more than once",
	} {
		if !strings.Contains(reasons[path], want) {
			t.Fatalf("%s reason = %q, want it to mention %q", path, reasons[path], want)
		}
	}
}

func TestPlanSkipsASkillWithNoInstructions(t *testing.T) {
	item := Item{Name: "p", Kind: "skill", Root: "skills/empty"}
	tree := memTree(map[string]string{"skills/empty/SKILL.md": "---\nname: empty\ndescription: Nothing here\n---\n\n"})
	plan, err := buildPlan(planInput(item), tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Skills) != 0 || len(plan.Skipped) != 1 || !strings.Contains(plan.Skipped[0].Reason, "no instructions") {
		t.Fatalf("plan = %+v, want the empty skill skipped", plan)
	}
}

func TestPlanSkipsFrontmatterThatCannotBeStored(t *testing.T) {
	item := Item{Name: "p", Kind: "plugin", Root: "plugins/deploy"}
	tree := memTree(map[string]string{
		"plugins/deploy/skills/odd/SKILL.md":  "---\nname: odd\ndescription: Odd\nmetadata:\n  1: a\n---\n\nbody\n",
		"plugins/deploy/skills/fine/SKILL.md": "---\nname: fine\ndescription: Fine\n---\n\nbody\n",
	})
	plan, err := buildPlan(planInput(item), tree)
	if err != nil {
		t.Fatalf("one unstorable skill failed the whole item: %v", err)
	}
	if len(plan.Skills) != 1 || plan.Skills[0].Name != "fine" {
		t.Fatalf("skills = %+v, want only fine", plan.Skills)
	}
}
