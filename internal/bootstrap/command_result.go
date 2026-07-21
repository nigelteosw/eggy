package bootstrap

import (
	"strings"
	"text/tabwriter"
)

// ResultState classifies a CommandResult so renderers can add a lightweight,
// colour-free marker (CLI output must stay readable when piped, and Telegram
// markdown has no colour concept either).
type ResultState string

const (
	ResultSuccess ResultState = "success"
	ResultInfo    ResultState = "info"
	ResultWarning ResultState = "warning"
	ResultError   ResultState = "error"
	ResultHelp    ResultState = "help"
)

// ResultField is a single label/value pair, e.g. "Active model: deepseek-pro".
type ResultField struct {
	Label string
	Value string
}

// CommandResult is the structured response every catalog command handler
// produces. Two renderers turn the same value into surface-appropriate
// output: RenderPlainText for the CLI, and RenderMarkdown for Telegram, whose
// transport already converts a small Markdown subset into safe HTML on
// delivery (see internal/adapters/channels/telegram/format.go).
type CommandResult struct {
	State ResultState
	// Title is the one-line headline: what happened, or what is being shown.
	Title string
	// Detail is an optional explanatory paragraph giving context beyond the
	// title (e.g. why an action is unavailable, or what a section means).
	Detail string
	// Fields render as "Label: value" lines, in order.
	Fields []ResultField
	// TableHeaders/TableRows render a list of like-shaped items (repositories,
	// runs, schedules, ...). Telegram has no table element, so its renderer
	// turns each row into a bullet line; the CLI renders an aligned table.
	TableHeaders []string
	TableRows    [][]string
	// Lines is a freeform bullet list for content that isn't tabular (prompt
	// names, help text lines).
	Lines []string
	// Next lists canonical follow-up commands the owner can run next, e.g.
	// "/repositories add <name> <clone_url>".
	Next []string
}

func (r CommandResult) titleWithMarker() string {
	switch r.State {
	case ResultError:
		return "Error: " + r.Title
	case ResultWarning:
		return "Warning: " + r.Title
	default:
		return r.Title
	}
}

// RenderPlainText renders r as clean plain text for the CLI: no ANSI colour,
// no Markdown syntax, readable when redirected to a file or piped.
func (r CommandResult) RenderPlainText() string {
	var sections []string
	if r.Title != "" {
		sections = append(sections, r.titleWithMarker())
	}
	if r.Detail != "" {
		sections = append(sections, r.Detail)
	}
	if len(r.Fields) > 0 {
		lines := make([]string, 0, len(r.Fields))
		for _, field := range r.Fields {
			lines = append(lines, field.Label+": "+field.Value)
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	if len(r.TableRows) > 0 {
		sections = append(sections, renderPlainTable(r.TableHeaders, r.TableRows))
	}
	if len(r.Lines) > 0 {
		lines := make([]string, 0, len(r.Lines))
		for _, line := range r.Lines {
			lines = append(lines, "- "+line)
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	if len(r.Next) > 0 {
		sections = append(sections, "Next: "+strings.Join(r.Next, ", "))
	}
	return strings.Join(sections, "\n\n")
}

func renderPlainTable(headers []string, rows [][]string) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)
	if len(headers) > 0 {
		w.Write([]byte(strings.Join(headers, "\t") + "\n"))
	}
	for _, row := range rows {
		w.Write([]byte(strings.Join(row, "\t") + "\n"))
	}
	w.Flush()
	return strings.TrimRight(b.String(), "\n")
}

// RenderMarkdown renders r as the small Markdown subset that Telegram's
// delivery path (toTelegramHTML) converts to safe HTML. It must never be sent
// anywhere except through that path, since it deliberately leaves raw "**"
// and "|" table syntax in place for that converter to escape and translate.
func (r CommandResult) RenderMarkdown() string {
	var sections []string
	hasBody := r.Detail != "" || len(r.Fields) > 0 || len(r.TableRows) > 0 || len(r.Lines) > 0 || len(r.Next) > 0
	if r.Title != "" {
		title := r.titleWithMarker()
		if hasBody || r.State == ResultError || r.State == ResultWarning {
			title = "**" + title + "**"
		}
		sections = append(sections, title)
	}
	if r.Detail != "" {
		sections = append(sections, r.Detail)
	}
	if len(r.Fields) > 0 {
		lines := make([]string, 0, len(r.Fields))
		for _, field := range r.Fields {
			lines = append(lines, "**"+field.Label+":** "+field.Value)
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	if len(r.TableRows) > 0 {
		sections = append(sections, renderMarkdownTable(r.TableHeaders, r.TableRows))
	}
	if len(r.Lines) > 0 {
		lines := make([]string, 0, len(r.Lines))
		for _, line := range r.Lines {
			lines = append(lines, "- "+line)
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	if len(r.Next) > 0 {
		commands := make([]string, 0, len(r.Next))
		for _, next := range r.Next {
			commands = append(commands, "`"+next+"`")
		}
		sections = append(sections, "Next: "+strings.Join(commands, ", "))
	}
	return strings.Join(sections, "\n\n")
}

// renderMarkdownTable emits a pipe table. Telegram's toTelegramHTML has no
// table element, so its converter turns each data row into a bullet line
// (first cell bold, remaining cells joined after an em dash) and drops the
// header; the header is still included here so any other Markdown consumer
// sees a complete table.
func renderMarkdownTable(headers []string, rows [][]string) string {
	if len(headers) == 0 && len(rows) > 0 {
		headers = make([]string, len(rows[0]))
	}
	var b strings.Builder
	b.WriteString("|" + strings.Join(headers, "|") + "|\n")
	separators := make([]string, len(headers))
	for i := range separators {
		separators[i] = "---"
	}
	b.WriteString("|" + strings.Join(separators, "|") + "|\n")
	for _, row := range rows {
		b.WriteString("|" + strings.Join(row, "|") + "|\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
