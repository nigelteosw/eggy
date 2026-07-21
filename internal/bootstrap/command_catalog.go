package bootstrap

import (
	"context"
	"strings"
)

// CommandRequest is the single validated shape both Telegram's "/command
// key=value ..." grammar and the CLI's "eggy command --flag=value ..."
// grammar parse into. Every catalog handler reads from this instead of
// surface-specific token indexing, so both surfaces run the same logic.
type CommandRequest struct {
	// Path is the catalog dispatch path matched from the leading tokens, e.g.
	// []string{"repositories", "add"}.
	Path []string
	// Args holds every remaining token after Path, in original order,
	// including tokens that look like key=value pairs.
	Args []string
	// Named holds the subset of Args that parsed as key=value, canonicalized
	// so CLI's "--alias=x" / "--alias x" and Telegram's "alias=x" produce an
	// identical map.
	Named map[string]string
	// Tail is the raw remaining text after the Path tokens: the original
	// substring for Telegram (so internal spacing/punctuation in free-form
	// content survives), or Args re-joined with single spaces for the CLI
	// (whose shell already normalized inter-token whitespace). Handlers that
	// take trailing free text (prompt content, a continue instruction) read
	// this instead of Args.
	Tail string
}

// CommandHandler executes one catalog entry against a parsed request.
type CommandHandler func(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error)

// Example pairs the canonical Telegram and CLI invocation for one catalog
// entry, shown in /help, eggy help, and malformed-input responses.
type Example struct {
	Telegram string
	CLI      string
}

// CatalogEntry is the one source of truth for a command's name, description,
// examples, and handler. Telegram autocomplete, /help, eggy help, and
// malformed-command responses are all generated from this list so the three
// surfaces cannot drift apart.
type CatalogEntry struct {
	// Path is this entry's dispatch path. Top-level commands have a single
	// element, e.g. []string{"status"}; subcommands add elements, e.g.
	// []string{"repositories", "add"}.
	Path string
	// Summary is a one-line description. Only top-level entries (single-word
	// Path) appear in Telegram's autocomplete list and the bare /help output;
	// subcommand entries still use Summary for /help <command>.
	Summary string
	// Examples shows canonical usage in both grammars. The first example is
	// used in malformed-input help.
	Examples []Example
	Handler  CommandHandler
}

func (e CatalogEntry) pathTokens() []string { return strings.Fields(e.Path) }

func buildCatalogIndex(entries []CatalogEntry) map[string]CatalogEntry {
	index := make(map[string]CatalogEntry, len(entries))
	for _, entry := range entries {
		index[entry.Path] = entry
	}
	return index
}

// matchCatalogEntry finds the longest registered path that prefixes tokens,
// so "config set model alias=x" matches the "config set model" entry rather
// than stopping at "config" or "config set". It returns the matched entry and
// the tokens remaining after the path.
func matchCatalogEntry(index map[string]CatalogEntry, tokens []string) (CatalogEntry, []string, bool) {
	limit := len(tokens)
	if limit > 3 {
		limit = 3
	}
	for length := limit; length >= 1; length-- {
		key := strings.Join(tokens[:length], " ")
		if entry, ok := index[key]; ok {
			return entry, tokens[length:], true
		}
	}
	return CatalogEntry{}, nil, false
}

// parseNamedArgs splits args into positional tokens and a canonical
// key=value map. Both grammars funnel into this once the CLI has normalized
// "--flag=value"/"--flag value" into bare "flag=value" tokens (see
// normalizeCLIArgs), so it is the only place key=value tokens are recognized.
func parseNamedArgs(args []string) (positional []string, named map[string]string) {
	named = make(map[string]string, len(args))
	for _, arg := range args {
		if key, value, ok := strings.Cut(arg, "="); ok && key != "" {
			named[key] = value
			continue
		}
		positional = append(positional, arg)
	}
	return positional, named
}

// ParseTelegramInput parses a raw Telegram message into a CommandRequest.
// matched is false when the text isn't a "/command" at all, or the leading
// token doesn't correspond to any registered top-level command; callers
// should treat that as "not a command" and fall through to the model.
func ParseTelegramInput(index map[string]CatalogEntry, input string) (CommandRequest, bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return CommandRequest{}, false
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return CommandRequest{}, false
	}
	fields[0] = strings.TrimPrefix(fields[0], "/")
	entry, rest, ok := matchCatalogEntry(index, fields)
	if !ok {
		return CommandRequest{}, false
	}
	_, named := parseNamedArgs(rest)
	tail := trailingSubstring(trimmed, entry.pathTokens())
	return CommandRequest{Path: entry.pathTokens(), Args: rest, Named: named, Tail: tail}, true
}

// trailingSubstring returns the original substring of trimmedInput after its
// first len(pathTokens) whitespace-separated tokens, preserving whatever
// internal spacing or punctuation the owner typed (important for free-form
// content like prompt text or a continue instruction).
func trailingSubstring(trimmedInput string, pathTokens []string) string {
	rest := trimmedInput
	for range pathTokens {
		rest = strings.TrimSpace(rest)
		fields := strings.SplitN(rest, " ", 2)
		if len(fields) < 2 {
			return ""
		}
		rest = fields[1]
	}
	return strings.TrimSpace(rest)
}

// ParseCLIArgs parses "eggy <command> [args...]" arguments (already
// shell-split, without the "eggy" program name or global flags) into a
// CommandRequest using the same catalog and the same canonical key=value
// shape ParseTelegramInput produces, normalizing "--flag=value" and
// "--flag value" into bare "flag=value" tokens first.
func ParseCLIArgs(index map[string]CatalogEntry, args []string) (CommandRequest, bool) {
	if len(args) == 0 {
		return CommandRequest{}, false
	}
	entry, rest, ok := matchCatalogEntry(index, args)
	if !ok {
		return CommandRequest{}, false
	}
	normalized := normalizeCLIArgs(rest)
	_, named := parseNamedArgs(normalized)
	return CommandRequest{Path: entry.pathTokens(), Args: normalized, Named: named, Tail: strings.Join(normalized, " ")}, true
}

// normalizeCLIArgs rewrites conventional CLI flags into the same bare
// "key=value" shape Telegram's grammar already uses natively, so a shared
// handler never has to know which surface called it. Both "--flag=value" and
// "--flag value" are accepted; the flag's hyphenated CLI spelling (e.g.
// "--base-url", "--reasoning-efforts") is converted to Telegram's
// underscored key (base_url, reasoning_efforts) since both grammars funnel
// into the same handlers looking up the same canonical key.
func normalizeCLIArgs(tokens []string) []string {
	result := make([]string, 0, len(tokens))
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if !strings.HasPrefix(token, "--") {
			result = append(result, token)
			continue
		}
		flag := strings.TrimPrefix(token, "--")
		if key, value, ok := strings.Cut(flag, "="); ok {
			result = append(result, strings.ReplaceAll(key, "-", "_")+"="+value)
			continue
		}
		if i+1 < len(tokens) {
			result = append(result, strings.ReplaceAll(flag, "-", "_")+"="+tokens[i+1])
			i++
			continue
		}
		result = append(result, strings.ReplaceAll(flag, "-", "_")+"=")
	}
	return result
}
