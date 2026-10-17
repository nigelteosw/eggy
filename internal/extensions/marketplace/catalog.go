package marketplace

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	KindClaudeMarketplace = "claude_marketplace"
	KindSkillsRepo        = "skills_repo"

	claudeCatalogPath = ".claude-plugin/marketplace.json"
	defaultSkillsPath = "skills"
)

// Source is one catalog the owner has configured.
type Source struct {
	Name, Kind, Repo, Path, Ref string
}

// RepoRef is a repository an item's files live in, when they are not in the
// catalog's own repository.
type RepoRef struct {
	Repo, Path, Ref, SHA string
}

// Item is one installable thing a catalog offers. Root is the item's directory
// inside the catalog's tree; External is set instead when the item lives in
// another GitHub repository; Unsupported says why the item cannot be installed.
type Item struct {
	Name, Description, Kind, Root string
	External                      *RepoRef
	Unsupported                   string
}

type claudeCatalog struct {
	Name    string         `json:"name"`
	Plugins []claudePlugin `json:"plugins"`
}

type claudePlugin struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Source      json.RawMessage `json:"source"`
}

type claudeSourceObject struct {
	Source string `json:"source"`
	Repo   string `json:"repo"`
	URL    string `json:"url"`
	Path   string `json:"path"`
	Ref    string `json:"ref"`
	SHA    string `json:"sha"`
}

// catalogItems lists what one source offers at one snapshot of its tree. An
// unsupported entry is listed with a reason rather than dropped, so the owner
// can see it; only a catalog that cannot be parsed at all is an error.
func catalogItems(src Source, tree Tree) ([]Item, error) {
	switch src.Kind {
	case KindClaudeMarketplace:
		return claudeItems(tree)
	case KindSkillsRepo:
		return skillsRepoItems(src.Path, tree)
	default:
		return nil, fmt.Errorf("unknown catalog kind %q", src.Kind)
	}
}

func claudeItems(tree Tree) ([]Item, error) {
	raw, ok := tree.Files[claudeCatalogPath]
	if !ok {
		return nil, fmt.Errorf("no %s in the repository", claudeCatalogPath)
	}
	var catalog claudeCatalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return nil, fmt.Errorf("%s: %w", claudeCatalogPath, err)
	}
	items := make([]Item, 0, len(catalog.Plugins))
	for _, plugin := range catalog.Plugins {
		item := Item{Name: plugin.Name, Description: plugin.Description, Kind: "plugin"}
		applyClaudeSource(&item, plugin.Source)
		items = append(items, item)
	}
	return items, nil
}

// applyClaudeSource fills in where a plugin's files come from. A string is a
// path inside the catalog's repository; an object names another source.
func applyClaudeSource(item *Item, raw json.RawMessage) {
	if len(raw) == 0 {
		item.Unsupported = "no source"
		return
	}
	if raw[0] == '"' {
		var rel string
		if err := json.Unmarshal(raw, &rel); err != nil {
			item.Unsupported = "unreadable source"
			return
		}
		root, ok := relativeRoot(rel)
		if !ok {
			item.Unsupported = "path leaves the repository: " + rel
			return
		}
		item.Root = root
		return
	}
	var obj claudeSourceObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		item.Unsupported = "unreadable source"
		return
	}
	switch obj.Source {
	case "github":
		item.External = &RepoRef{Repo: obj.Repo, Ref: obj.Ref, SHA: obj.SHA}
	case "git-subdir":
		repo, ok := githubRepo(obj.URL)
		if !ok {
			item.Unsupported = "unsupported source: git-subdir URL " + obj.URL
			return
		}
		item.External = &RepoRef{Repo: repo, Path: obj.Path, Ref: obj.Ref, SHA: obj.SHA}
	default:
		item.Unsupported = "unsupported source: " + obj.Source
	}
}

// relativeRoot cleans a plugin path given relative to the repository root. A
// path that climbs out of the repository is refused.
func relativeRoot(rel string) (string, bool) {
	cleaned := path.Clean(strings.TrimPrefix(rel, "./"))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", false
	}
	return cleaned, true
}

// githubRepo accepts "owner/repo" or a github.com HTTPS URL, with or without a
// trailing ".git". Anything else is not a GitHub repository this package can fetch.
func githubRepo(url string) (string, bool) {
	trimmed := strings.TrimSuffix(url, ".git")
	if rest, ok := strings.CutPrefix(trimmed, "https://github.com/"); ok {
		trimmed = rest
	} else if strings.Contains(trimmed, "://") || strings.Contains(trimmed, ":") {
		return "", false
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

func skillsRepoItems(skillsPath string, tree Tree) ([]Item, error) {
	base := skillsPath
	if base == "" {
		base = defaultSkillsPath
	}
	if strings.Contains(base, "..") {
		return nil, fmt.Errorf("skills path %q leaves the repository", base)
	}
	base = path.Clean(base)
	prefix := base + "/"

	var items []Item
	for file, content := range tree.Files {
		rest, ok := strings.CutPrefix(file, prefix)
		if !ok {
			continue
		}
		dir, leaf, ok := strings.Cut(rest, "/")
		if !ok || leaf != "SKILL.md" || strings.Contains(dir, "/") {
			continue
		}
		item := Item{Name: dir, Kind: "skill", Root: base + "/" + dir}
		desc, err := frontmatterDescription(content)
		if err != nil {
			item.Unsupported = "malformed SKILL.md frontmatter: " + err.Error()
		}
		item.Description = desc
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

// frontmatterDescription reads only the description from a SKILL.md header.
func frontmatterDescription(content []byte) (string, error) {
	text := string(content)
	if !strings.HasPrefix(text, "---\n") {
		return "", fmt.Errorf("missing frontmatter")
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return "", fmt.Errorf("unterminated frontmatter")
	}
	var front struct {
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(text[4:4+end]), &front); err != nil {
		return "", err
	}
	return front.Description, nil
}
