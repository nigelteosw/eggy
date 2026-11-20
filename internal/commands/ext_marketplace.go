package commands

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

// pluginUsage lists every /plugin form. The words follow Claude Code's
// /plugin so a person who knows one knows the other.
const pluginUsage = "Usage:\n/plugin marketplace add <owner/repo> [skills [path]]\n/plugin marketplace list\n/plugin marketplace remove <name>\n/plugin browse <marketplace>\n/plugin install <item>[@<marketplace>]\n/plugin show <code> <skill>\n/plugin approve <code>\n/plugin list\n/plugin uninstall <id>"

// pluginReviewTTL is how long a review card's code stays valid. The install
// re-checks the digest at the reviewed commit either way; the expiry only keeps
// a forgotten card from being approved days later.
const pluginReviewTTL = 30 * time.Minute

// Marketplace is the marketplace extension as /plugin sees it. The commands
// package declares it rather than importing the extension; bootstrap adapts the
// service to it. Install is the same digest-checked write the panel's review
// calls: one install path, two views onto it.
type Marketplace interface {
	Sources(ctx context.Context) ([]MarketplaceSourceStatus, error)
	Items(ctx context.Context, source string) ([]MarketplaceItem, error)
	Plan(ctx context.Context, source, item string) (MarketplacePlan, error)
	Install(ctx context.Context, source, item, sha, digest string) error
	Installs(ctx context.Context) ([]ports.MarketplaceInstall, error)
	Remove(ctx context.Context, id string) error
}

// MarketplaceSourceStatus is one configured catalog and the commit it names now.
type MarketplaceSourceStatus struct {
	Name, Repo, SHA, Error string
}

// MarketplaceItem is one installable entry in a catalog.
type MarketplaceItem struct {
	Name, Description, Kind, Unsupported string
}

// MarketplacePlan is what an install would write, bound to one commit by Digest.
type MarketplacePlan struct {
	Source, Repo, SHA, Item, Digest string
	Skills                          []MarketplacePlanSkill
	MCP                             []struct{ Name, URL string }
	Skipped                         []string
	Conflicts                       []string
	IndexBytes                      int
}

// MarketplacePlanSkill is one skill a plan would write, body included so the
// owner can read it from chat before approving.
type MarketplacePlanSkill struct {
	Name, Description, Body string
	Files                   int
	Warnings                []string
}

// pendingPlugin is one review card awaiting /plugin approve.
type pendingPlugin struct {
	account string
	plan    MarketplacePlan
	expires time.Time
}

// pluginReviews holds review cards by code. It is in memory on purpose: a
// restart drops them, and the owner asks for the card again.
type pluginReviews struct {
	mu    sync.Mutex
	cards map[string]pendingPlugin
}

var errReviewNotFound = errors.New("no review with that code; run /plugin install again")

func (r *pluginReviews) put(account string, plan MarketplacePlan, now time.Time) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	code := hex.EncodeToString(b[:])
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cards == nil {
		r.cards = map[string]pendingPlugin{}
	}
	for c, card := range r.cards {
		if now.After(card.expires) {
			delete(r.cards, c)
		}
	}
	r.cards[code] = pendingPlugin{account: account, plan: plan, expires: now.Add(pluginReviewTTL)}
	return code, nil
}

// get returns the card only to the account it was made for, so one person's
// code cannot approve an install from another person's chat.
func (r *pluginReviews) get(account, code string, now time.Time, take bool) (MarketplacePlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	card, ok := r.cards[strings.ToLower(code)]
	if !ok || card.account != account || now.After(card.expires) {
		return MarketplacePlan{}, errReviewNotFound
	}
	if take {
		delete(r.cards, strings.ToLower(code))
	}
	return card.plan, nil
}

// pluginCommand is /plugin, in Telegram and in web chat alike. Installing is
// two steps: install shows the review card, and approve, with the card's code,
// writes exactly what it showed. Discord has no decision surface, so it can
// look but not approve.
func (s *CommandService) pluginCommand(ctx context.Context, args []string) (string, bool, error) {
	if s.Marketplace == nil {
		return "The marketplace is off. Turn it on in Customize → Extensions on the web panel (/web), then /restart.", true, nil
	}
	if len(args) == 0 {
		return pluginUsage, true, nil
	}
	switch args[0] {
	case "marketplace":
		return s.pluginMarketplace(ctx, args[1:])
	case "browse":
		if len(args) != 2 {
			return pluginUsage, true, nil
		}
		return s.pluginBrowse(ctx, args[1])
	case "install":
		if len(args) != 2 {
			return pluginUsage, true, nil
		}
		return s.pluginInstall(ctx, args[1])
	case "show":
		if len(args) != 3 {
			return pluginUsage, true, nil
		}
		return s.pluginShow(ctx, args[1], args[2])
	case "approve":
		if len(args) != 2 {
			return pluginUsage, true, nil
		}
		return s.pluginApprove(ctx, args[1])
	case "list":
		if len(args) != 1 {
			return pluginUsage, true, nil
		}
		return s.pluginList(ctx)
	case "uninstall", "remove":
		if len(args) != 2 {
			return pluginUsage, true, nil
		}
		if err := s.Marketplace.Remove(ctx, args[1]); err != nil {
			return err.Error(), true, nil
		}
		return "Uninstalled " + args[1] + ". Any skill you had edited is kept as your own.", true, nil
	default:
		return pluginUsage, true, nil
	}
}

func (s *CommandService) pluginMarketplace(ctx context.Context, args []string) (string, bool, error) {
	if len(args) == 0 {
		return pluginUsage, true, nil
	}
	switch args[0] {
	case "list":
		statuses, err := s.Marketplace.Sources(ctx)
		if err != nil {
			return err.Error(), true, nil
		}
		if len(statuses) == 0 {
			return "No marketplaces yet. Add one: /plugin marketplace add anthropics/skills", true, nil
		}
		var lines []string
		for _, st := range statuses {
			line := st.Name + " · " + st.Repo
			if st.Error != "" {
				line += " · " + st.Error
			} else if st.SHA != "" {
				line += " @ " + shortSHA(st.SHA)
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n") + "\n\nNext: /plugin browse <name>", true, nil
	case "add":
		if len(args) < 2 || len(args) > 4 || (len(args) > 2 && args[2] != "skills") {
			return pluginUsage, true, nil
		}
		source := config.MarketplaceSource{Kind: "claude_marketplace", Repo: strings.TrimSuffix(strings.TrimPrefix(args[1], "https://github.com/"), ".git")}
		if len(args) > 2 {
			source.Kind = "skills_repo"
			source.Path = "skills"
			if len(args) == 4 {
				source.Path = args[3]
			}
		}
		return s.pluginAddSource(source)
	case "remove":
		if len(args) != 2 {
			return pluginUsage, true, nil
		}
		sources, err := config.GetMarketplaceSources(s.ConfigPath)
		if err != nil {
			return "", true, err
		}
		kept := sources[:0]
		for _, src := range sources {
			if src.Name != args[1] {
				kept = append(kept, src)
			}
		}
		if len(kept) == len(sources) {
			return "No marketplace named " + args[1] + ". See /plugin marketplace list.", true, nil
		}
		if err := config.SetMarketplaceSources(s.ConfigPath, kept); err != nil {
			return err.Error(), true, nil
		}
		return "Removed marketplace " + args[1] + ". What you installed from it stays installed.", true, nil
	default:
		return pluginUsage, true, nil
	}
}

var sourceNameUnsafe = regexp.MustCompile(`[^a-z0-9-]+`)

// pluginAddSource writes one source through the config authority, the same
// list the panel's Sources card edits. Sources are read on every call, so it
// is usable at once, without a restart.
func (s *CommandService) pluginAddSource(source config.MarketplaceSource) (string, bool, error) {
	owner, repo, ok := strings.Cut(source.Repo, "/")
	if !ok || owner == "" || repo == "" {
		return "Name the repository as owner/repo, e.g. /plugin marketplace add anthropics/skills", true, nil
	}
	sources, err := config.GetMarketplaceSources(s.ConfigPath)
	if err != nil {
		return "", true, err
	}
	taken := map[string]bool{}
	for _, src := range sources {
		if strings.EqualFold(src.Repo, source.Repo) && src.Path == source.Path {
			return fmt.Sprintf("%s is already added as %s. Next: /plugin browse %s", source.Repo, src.Name, src.Name), true, nil
		}
		taken[src.Name] = true
	}
	source.Name = sourceName(repo)
	if taken[source.Name] {
		source.Name = sourceName(owner + "-" + repo)
	}
	if err := config.SetMarketplaceSources(s.ConfigPath, append(sources, source)); err != nil {
		return err.Error(), true, nil
	}
	reply := fmt.Sprintf("Added marketplace %s (%s). Next: /plugin browse %s", source.Name, source.Repo, source.Name)
	if source.Kind == "claude_marketplace" {
		reply += "\nIf it has no .claude-plugin/marketplace.json, remove it and add it as a skills repository: /plugin marketplace add " + source.Repo + " skills [path]"
	}
	return reply, true, nil
}

func sourceName(raw string) string {
	name := strings.Trim(sourceNameUnsafe.ReplaceAllString(strings.ToLower(raw), "-"), "-")
	if len(name) > 64 {
		name = strings.Trim(name[:64], "-")
	}
	return name
}

func (s *CommandService) pluginBrowse(ctx context.Context, source string) (string, bool, error) {
	items, err := s.Marketplace.Items(ctx, source)
	if err != nil {
		return err.Error(), true, nil
	}
	if len(items) == 0 {
		return source + " lists nothing installable.", true, nil
	}
	lines := []string{source + ":"}
	for _, item := range items {
		line := "• " + item.Name
		if item.Description != "" {
			line += " — " + clip(item.Description, 120)
		}
		if item.Unsupported != "" {
			line += " (can't install: " + item.Unsupported + ")"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n") + "\n\nNext: /plugin install <item>@" + source, true, nil
}

// pluginInstall builds the plan and shows it. Nothing is written here.
func (s *CommandService) pluginInstall(ctx context.Context, target string) (string, bool, error) {
	principal, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return "", true, err
	}
	item, source, _ := strings.Cut(target, "@")
	if source == "" {
		source, err = s.findPluginSource(ctx, item)
		if err != nil {
			return err.Error(), true, nil
		}
	}
	plan, err := s.Marketplace.Plan(ctx, source, item)
	if err != nil {
		return err.Error(), true, nil
	}
	if len(plan.Conflicts) > 0 {
		return fmt.Sprintf("Can't install %s from chat: %s already exist here. Rename them in the review on the web panel (/web → Customize → Marketplace).", item, strings.Join(plan.Conflicts, ", ")), true, nil
	}
	if len(plan.Skills) == 0 && len(plan.MCP) == 0 {
		return item + " has nothing Eggy can install." + skippedLines(plan.Skipped), true, nil
	}
	code, err := s.pluginReviews.put(principal.AccountID, plan, s.Now())
	if err != nil {
		return "", true, err
	}
	return reviewCard(plan, code), true, nil
}

// findPluginSource resolves an item named without @ when exactly one
// marketplace offers it.
func (s *CommandService) findPluginSource(ctx context.Context, item string) (string, error) {
	statuses, err := s.Marketplace.Sources(ctx)
	if err != nil {
		return "", err
	}
	var found []string
	for _, st := range statuses {
		if st.Error != "" {
			continue
		}
		items, err := s.Marketplace.Items(ctx, st.Name)
		if err != nil {
			continue
		}
		for _, it := range items {
			if it.Name == item {
				found = append(found, st.Name)
			}
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no marketplace offers %s. See /plugin marketplace list", item)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("%s is in %s; say which: /plugin install %s@<marketplace>", item, strings.Join(found, " and "), item)
	}
}

func reviewCard(plan MarketplacePlan, code string) string {
	lines := []string{fmt.Sprintf("Review: %s from %s", plan.Item, plan.Source), fmt.Sprintf("%s @ %s", plan.Repo, shortSHA(plan.SHA)), ""}
	if len(plan.Skills) > 0 {
		lines = append(lines, fmt.Sprintf("Skills (%d):", len(plan.Skills)))
		for _, sk := range plan.Skills {
			line := "• " + sk.Name
			if sk.Description != "" {
				line += " — " + clip(sk.Description, 160)
			}
			if sk.Files > 0 {
				line += fmt.Sprintf(" [+%d reference files]", sk.Files)
			}
			lines = append(lines, line)
			for _, w := range sk.Warnings {
				lines = append(lines, "  ⚠ "+w)
			}
		}
	}
	if len(plan.MCP) > 0 {
		lines = append(lines, "", "MCP servers (added switched off, enabled only after review on the Connectors page):")
		for _, m := range plan.MCP {
			lines = append(lines, "• "+m.Name+" — "+m.URL)
		}
	}
	if extra := skippedLines(plan.Skipped); extra != "" {
		lines = append(lines, strings.TrimPrefix(extra, "\n"))
	}
	if plan.IndexBytes > 0 {
		lines = append(lines, "", fmt.Sprintf("Adds about %.1f KB to every turn's skill list.", float64(plan.IndexBytes)/1024))
	}
	lines = append(lines, "",
		"Skills become instructions I follow. Read one in full: /plugin show "+code+" <skill>",
		"Install exactly this: /plugin approve "+code+" (valid 30 minutes)")
	return strings.Join(lines, "\n")
}

func skippedLines(skipped []string) string {
	if len(skipped) == 0 {
		return ""
	}
	return "\n\nSkipped:\n• " + strings.Join(skipped, "\n• ")
}

func (s *CommandService) pluginShow(ctx context.Context, code, skill string) (string, bool, error) {
	principal, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return "", true, err
	}
	plan, err := s.pluginReviews.get(principal.AccountID, code, s.Now(), false)
	if err != nil {
		return err.Error(), true, nil
	}
	for _, sk := range plan.Skills {
		if sk.Name == skill {
			return fmt.Sprintf("%s — %s\n\n%s", sk.Name, sk.Description, sk.Body), true, nil
		}
	}
	return "That review has no skill named " + skill + ".", true, nil
}

// pluginApprove writes the reviewed plan. The service rebuilds it at the
// reviewed commit and refuses if the digest differs, so what lands is exactly
// what the card showed.
func (s *CommandService) pluginApprove(ctx context.Context, code string) (string, bool, error) {
	if destination.FromContext(ctx).Kind == destination.Discord {
		return "Installs can't be approved from Discord. Approve from Telegram or the web.", true, nil
	}
	principal, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return "", true, err
	}
	plan, err := s.pluginReviews.get(principal.AccountID, code, s.Now(), true)
	if err != nil {
		return err.Error(), true, nil
	}
	if err := s.Marketplace.Install(ctx, plan.Source, plan.Item, plan.SHA, plan.Digest); err != nil {
		return "Not installed: " + err.Error(), true, nil
	}
	reply := fmt.Sprintf("Installed %s from %s.", plan.Item, plan.Source)
	if len(plan.Skills) > 0 {
		reply += " Its skills are available from your next message."
	}
	if len(plan.MCP) > 0 {
		reply += " Its MCP servers are off until you review them on the Connectors page."
	}
	return reply, true, nil
}

func (s *CommandService) pluginList(ctx context.Context) (string, bool, error) {
	installs, err := s.Marketplace.Installs(ctx)
	if err != nil {
		return err.Error(), true, nil
	}
	if len(installs) == 0 {
		return "Nothing installed from a marketplace yet. Next: /plugin marketplace list", true, nil
	}
	var lines []string
	for _, in := range installs {
		line := fmt.Sprintf("%s · %s@%s @ %s", in.ID, in.Item, in.SourceName, shortSHA(in.CommitSHA))
		if in.Disabled {
			line += " · off"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n") + "\n\nUninstall: /plugin uninstall <id>", true, nil
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
