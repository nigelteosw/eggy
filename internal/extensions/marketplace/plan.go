package marketplace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Limits the skills store enforces on write. The plan applies them first, so
// an install that the store would refuse is shown as skipped, not attempted.
const (
	maxSkillFileBytes  = 32 << 10
	maxReferenceBytes  = 64 << 10
	maxSkillDirBytes   = 256 << 10
	maxSkillDirFiles   = 64
	maxDescriptionSize = 1024
)

// unsupportedDirs are plugin components Eggy does not offer. They are listed in
// the review, so the owner sees what was left out.
var unsupportedDirs = map[string]bool{
	"commands": true, "agents": true, "hooks": true, "bin": true, "monitors": true,
	"themes": true, "output-styles": true, "workflows": true,
}

// mcpNamePattern is the config's rule for an MCP server name, applied here so a
// server the config would refuse is skipped in the review.
var mcpNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func plannedMCP(plan *Plan, name string) bool {
	for _, server := range plan.MCP {
		if server.Name == name {
			return true
		}
	}
	return false
}

var skillNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// PlanInput is everything a plan depends on besides the fetched tree. Renames,
// SkillExists, MCPExists and Replacing decide conflicts, so they are part of
// what the owner approves.
type PlanInput struct {
	Source      Source
	Repo        string // repository the item's files came from
	SHA         string
	CatalogSHA  string // the catalog commit the item was read from
	Item        Item
	Renames     map[string]string // upstream skill name -> chosen name
	SkillExists func(name string) bool
	MCPExists   func(name string) bool
	Replacing   map[string]bool // "skill/<name>" and "mcp_server/<name>" this install already owns (update)
}

// PlannedSkill is one skill the install would write.
type PlannedSkill struct {
	Name, UpstreamName, Description, Body, Hash string
	Extra                                       map[string]any
	Files                                       map[string]string
	Warnings                                    []string
}

// PlannedMCP is one remote MCP server the install would add, disabled.
type PlannedMCP struct{ Name, URL, Hash string }

// Skipped is something in the item that the install will not write, and why.
type Skipped struct{ Path, Reason string }

// Plan is the reviewed content of one install or update. Digest covers the
// parts that would be written; warnings and skipped entries are for display.
type Plan struct {
	Source, Repo, SHA, Item string
	CatalogSHA              string
	Skills                  []PlannedSkill
	MCP                     []PlannedMCP
	Skipped                 []Skipped
	Conflicts               []string // upstream names that need a rename
	IndexBytes              int      // bytes the skills add to the index
	Digest                  string   // hex sha256 of the canonical plan
}

// buildPlan turns one item in one fetched tree into a plan. It does no I/O.
func buildPlan(in PlanInput, tree Tree) (Plan, error) {
	plan := Plan{Source: in.Source.Name, Repo: in.Repo, SHA: in.SHA, CatalogSHA: in.CatalogSHA, Item: in.Item.Name}
	root := in.Item.Root

	var dirs []string
	if in.Item.Kind == "skill" {
		dirs = []string{root}
	} else {
		if err := planPluginComponents(&plan, in, tree, root); err != nil {
			return Plan{}, err
		}
		dirs = pluginSkillDirs(tree, root)
	}

	seen := map[string]bool{}
	for _, dir := range dirs {
		planSkill(&plan, in, tree, dir, seen)
	}

	sort.Slice(plan.Skills, func(i, j int) bool { return plan.Skills[i].Name < plan.Skills[j].Name })
	sort.Slice(plan.MCP, func(i, j int) bool { return plan.MCP[i].Name < plan.MCP[j].Name })
	sort.Slice(plan.Skipped, func(i, j int) bool { return plan.Skipped[i].Path < plan.Skipped[j].Path })
	sort.Strings(plan.Conflicts)
	for _, skill := range plan.Skills {
		plan.IndexBytes += len(skill.Name) + 2 + len(skill.Description) + 1
	}
	digest, err := planDigest(plan)
	if err != nil {
		return Plan{}, err
	}
	plan.Digest = digest
	return plan, nil
}

// planPluginComponents records what a plugin brings that Eggy does not install,
// and the remote MCP servers it declares.
func planPluginComponents(plan *Plan, in PlanInput, tree Tree, root string) error {
	for file := range tree.Files {
		if rel, ok := under(root, file); ok && unsupportedComponent(rel) {
			plan.Skipped = append(plan.Skipped, Skipped{Path: file, Reason: "not supported in Eggy"})
		}
	}
	for file := range tree.Skipped {
		if rel, ok := under(root, file); ok && unsupportedComponent(rel) {
			plan.Skipped = append(plan.Skipped, Skipped{Path: file, Reason: "not supported in Eggy"})
		}
	}

	manifest := path.Join(root, ".claude-plugin", "plugin.json")
	if raw, ok := tree.Files[manifest]; ok {
		var m struct {
			UserConfig json.RawMessage `json:"userConfig"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("%s: %w", manifest, err)
		}
		if len(m.UserConfig) > 0 {
			plan.Skipped = append(plan.Skipped, Skipped{Path: manifest + "#userConfig", Reason: "not supported in Eggy"})
		}
	}

	if err := planMCP(plan, in, tree, path.Join(root, ".mcp.json"), false); err != nil {
		return err
	}
	return planMCP(plan, in, tree, manifest, true)
}

// unsupportedComponent reports whether rel, relative to the plugin root, is a
// plugin component Eggy does not offer.
func unsupportedComponent(rel string) bool {
	first := strings.SplitN(rel, "/", 2)[0]
	return unsupportedDirs[first] || rel == ".lsp.json"
}

// planMCP reads one MCP config file. A remote server with no headers and no
// variables becomes a disabled entry; anything else is skipped with a reason.
func planMCP(plan *Plan, in PlanInput, tree Tree, file string, fromManifest bool) error {
	raw, ok := tree.Files[file]
	if !ok {
		return nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	servers := doc
	if inner, ok := doc["mcpServers"]; ok {
		servers = map[string]json.RawMessage{}
		if err := json.Unmarshal(inner, &servers); err != nil {
			return fmt.Errorf("%s: mcpServers: %w", file, err)
		}
	} else if fromManifest {
		return nil
	}
	for server, entryRaw := range servers {
		var entry struct {
			URL     string          `json:"url"`
			Command string          `json:"command"`
			Headers json.RawMessage `json:"headers"`
		}
		skipPath := file + "#" + server
		if err := json.Unmarshal(entryRaw, &entry); err != nil {
			plan.Skipped = append(plan.Skipped, Skipped{Path: skipPath, Reason: "unreadable server entry"})
			continue
		}
		switch {
		case entry.Command != "":
			plan.Skipped = append(plan.Skipped, Skipped{Path: skipPath, Reason: "runs a downloaded program"})
		case entry.URL == "":
			plan.Skipped = append(plan.Skipped, Skipped{Path: skipPath, Reason: "not a remote server"})
		case (len(entry.Headers) > 0 && string(entry.Headers) != "null") || strings.Contains(string(entryRaw), "${"):
			plan.Skipped = append(plan.Skipped, Skipped{Path: skipPath, Reason: "needs configuration Eggy cannot infer"})
		default:
			// Everything the config would refuse is skipped here with its reason,
			// so an approved install cannot fail on it later. A taken name is
			// skipped too: renames apply to skills, so it cannot be cleared.
			name := in.Item.Name + "-" + server
			switch {
			case !mcpNamePattern.MatchString(name):
				plan.Skipped = append(plan.Skipped, Skipped{Path: skipPath, Reason: "server name is not valid in Eggy"})
			case !strings.HasPrefix(entry.URL, "https://"):
				plan.Skipped = append(plan.Skipped, Skipped{Path: skipPath, Reason: "only https servers are supported"})
			case plannedMCP(plan, name):
				plan.Skipped = append(plan.Skipped, Skipped{Path: skipPath, Reason: "server is declared more than once"})
			case in.MCPExists != nil && in.MCPExists(name) && !in.Replacing["mcp_server/"+name]:
				plan.Skipped = append(plan.Skipped, Skipped{Path: skipPath, Reason: "a server with this name is already configured"})
			default:
				plan.MCP = append(plan.MCP, PlannedMCP{Name: name, URL: entry.URL, Hash: mcpHash(entry.URL)})
			}
		}
	}
	return nil
}

// pluginSkillDirs lists the skill directories a plugin carries: each direct
// child of skills/ that holds a SKILL.md, and the plugin root itself when it is
// a single skill.
func pluginSkillDirs(tree Tree, root string) []string {
	base := path.Join(root, "skills")
	var dirs []string
	for file := range tree.Files {
		if path.Base(file) != "SKILL.md" {
			continue
		}
		dir := path.Dir(file)
		if path.Dir(dir) == base {
			dirs = append(dirs, dir)
		}
	}
	if _, ok := tree.Files[path.Join(root, "SKILL.md")]; ok {
		dirs = append(dirs, root)
	}
	sort.Strings(dirs)
	return dirs
}

// planSkill checks one skill directory against the store's limits and the
// name rules, then records it as planned, skipped, or conflicting.
func planSkill(plan *Plan, in PlanInput, tree Tree, dir string, seen map[string]bool) {
	skillFile := path.Join(dir, "SKILL.md")
	raw, ok := tree.Files[skillFile]
	if !ok {
		return
	}
	text := normalizeText(raw)
	if len(text) > maxSkillFileBytes {
		plan.Skipped = append(plan.Skipped, Skipped{Path: skillFile, Reason: "SKILL.md exceeds 32 KiB"})
		return
	}
	front, body, err := splitSkill(text)
	if err != nil {
		plan.Skipped = append(plan.Skipped, Skipped{Path: skillFile, Reason: "malformed frontmatter: " + err.Error()})
		return
	}
	upstream, _ := front["name"].(string)
	description, _ := front["description"].(string)
	description = strings.TrimSpace(description)
	if upstream == "" {
		plan.Skipped = append(plan.Skipped, Skipped{Path: skillFile, Reason: "frontmatter has no name"})
		return
	}
	if description == "" {
		plan.Skipped = append(plan.Skipped, Skipped{Path: skillFile, Reason: "frontmatter has no description"})
		return
	}
	if body == "" {
		plan.Skipped = append(plan.Skipped, Skipped{Path: skillFile, Reason: "SKILL.md has no instructions"})
		return
	}
	if len(description) > maxDescriptionSize {
		plan.Skipped = append(plan.Skipped, Skipped{Path: skillFile, Reason: fmt.Sprintf("description exceeds %d bytes", maxDescriptionSize)})
		return
	}

	name := upstream
	if renamed, ok := in.Renames[upstream]; ok {
		name = renamed
	}
	if !skillNamePattern.MatchString(name) || seen[name] {
		plan.Conflicts = append(plan.Conflicts, upstream)
		return
	}
	if in.SkillExists != nil && in.SkillExists(name) && !in.Replacing["skill/"+name] {
		plan.Conflicts = append(plan.Conflicts, upstream)
		return
	}
	seen[name] = true

	files, skipped, tooBig := skillFiles(tree, dir)
	if tooBig != "" {
		plan.Skipped = append(plan.Skipped, Skipped{Path: dir, Reason: tooBig})
		delete(seen, name)
		return
	}
	plan.Skipped = append(plan.Skipped, skipped...)

	var warnings []string
	for _, s := range skipped {
		rel := strings.TrimPrefix(s.Path, dir+"/")
		if strings.Contains(body, rel) {
			warnings = append(warnings, "this skill refers to files Eggy will not install and may not work")
			break
		}
	}

	extra := map[string]any{}
	for k, v := range front {
		switch k {
		case "name", "description", "source", "disabled":
		default:
			extra[k] = v
		}
	}
	// The digest is JSON. Frontmatter JSON cannot hold, such as a map with
	// number keys, would fail the whole item, so that one skill is skipped.
	if _, err := json.Marshal(extra); err != nil {
		plan.Skipped = append(plan.Skipped, Skipped{Path: skillFile, Reason: "frontmatter cannot be stored"})
		delete(seen, name)
		return
	}
	plan.Skills = append(plan.Skills, PlannedSkill{
		Name:         name,
		UpstreamName: upstream,
		Description:  description,
		Body:         body,
		Hash:         skillHash(description, body, files),
		Extra:        extra,
		Files:        files,
		Warnings:     warnings,
	})
}

// skillFiles returns the reference files of one skill directory, the skipped
// files, and a reason if the directory is over a limit.
func skillFiles(tree Tree, dir string) (map[string]string, []Skipped, string) {
	files := map[string]string{}
	var skipped []Skipped
	total := 0
	if raw, ok := tree.Files[path.Join(dir, "SKILL.md")]; ok {
		total += len(normalizeText(raw))
	}
	for file, raw := range tree.Files {
		rel, ok := under(dir, file)
		if !ok || rel == "SKILL.md" {
			continue
		}
		text := normalizeText(raw)
		if !isTextPath(rel) {
			skipped = append(skipped, Skipped{Path: file, Reason: "Eggy never runs skill files"})
			continue
		}
		if len(text) > maxReferenceBytes {
			skipped = append(skipped, Skipped{Path: file, Reason: "reference exceeds 64 KiB"})
			continue
		}
		files[rel] = text
		total += len(text)
	}
	for file := range tree.Skipped {
		rel, ok := under(dir, file)
		if !ok {
			continue
		}
		reason := "Eggy never runs skill files"
		if isTextPath(rel) {
			reason = "reference exceeds 64 KiB"
		}
		skipped = append(skipped, Skipped{Path: file, Reason: reason})
	}
	if len(files)+1 > maxSkillDirFiles {
		return nil, nil, fmt.Sprintf("skill directory exceeds %d files", maxSkillDirFiles)
	}
	if total > maxSkillDirBytes {
		return nil, nil, "skill directory exceeds 256 KiB"
	}
	return files, skipped, ""
}

// splitSkill separates a SKILL.md into its frontmatter map and trimmed body.
func splitSkill(text string) (map[string]any, string, error) {
	if !strings.HasPrefix(text, "---\n") {
		return nil, "", fmt.Errorf("missing frontmatter")
	}
	rest := text[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, "", fmt.Errorf("unterminated frontmatter")
	}
	front := map[string]any{}
	if err := yaml.Unmarshal([]byte(rest[:end]), &front); err != nil {
		return nil, "", err
	}
	body := strings.TrimSpace(rest[end+len("\n---"):])
	return front, body, nil
}

// normalizeText strips a byte-order mark and converts CRLF line endings, so the
// same skill has the same bytes, and the same digest, on any platform.
func normalizeText(raw []byte) string {
	text := strings.TrimPrefix(string(raw), "\ufeff")
	return strings.ReplaceAll(text, "\r\n", "\n")
}

// under returns file's path relative to dir, if file lies inside dir.
func under(dir, file string) (string, bool) {
	if dir == "." || dir == "" {
		return file, true
	}
	return strings.CutPrefix(file, dir+"/")
}

func skillHash(description, body string, files map[string]string) string {
	raw, _ := json.Marshal(struct {
		Description string            `json:"description"`
		Body        string            `json:"body"`
		Files       map[string]string `json:"files"`
	}{description, body, files})
	return sha256Hex(raw)
}

func mcpHash(url string) string {
	return sha256Hex([]byte(url))
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type digestDoc struct {
	Source     string        `json:"source"`
	Repo       string        `json:"repo"`
	SHA        string        `json:"sha"`
	CatalogSHA string        `json:"catalog_sha"`
	Item       string        `json:"item"`
	Skills     []digestSkill `json:"skills"`
	MCP        []digestMCP   `json:"mcp"`
}

type digestSkill struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Body        string            `json:"body"`
	Files       map[string]string `json:"files"`
	Extra       map[string]any    `json:"extra"`
}

type digestMCP struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// planDigest hashes what would be written, in a fixed order. Warnings and
// skipped entries are left out: they change the display, not the write.
func planDigest(plan Plan) (string, error) {
	doc := digestDoc{Source: plan.Source, Repo: plan.Repo, SHA: plan.SHA, CatalogSHA: plan.CatalogSHA, Item: plan.Item}
	for _, s := range plan.Skills {
		doc.Skills = append(doc.Skills, digestSkill{Name: s.Name, Description: s.Description, Body: s.Body, Files: s.Files, Extra: s.Extra})
	}
	for _, m := range plan.MCP {
		doc.MCP = append(doc.MCP, digestMCP{Name: m.Name, URL: m.URL})
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return sha256Hex(raw), nil
}
