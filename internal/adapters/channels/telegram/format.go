package telegram

import (
	"regexp"
	"strings"
)

var (
	fencedCodeBlock = regexp.MustCompile("(?s)```([a-zA-Z0-9_+-]*)\\n?(.*?)```")
	markdownTable   = regexp.MustCompile(`(?m)^(\|.+\|)\r?\n(\|(?:[ \t]*:?-+:?[ \t]*\|)+)\r?\n((?:\|.*\|\r?\n?)*)`)
	inlineCode      = regexp.MustCompile("`([^`\\n]+)`")
	horizontalRule  = regexp.MustCompile(`(?m)^[ \t]*(?:-{3,}|\*{3,}|_{3,})[ \t]*\r?\n?`)
	markdownHeading = regexp.MustCompile(`(?m)^#{1,6}[ \t]+(.+)$`)
	boldText        = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	markdownLink    = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
)

// toTelegramHTML converts a small Markdown subset (bold, inline code, fenced
// code blocks, links, headings, horizontal rules, and pipe tables) into
// Telegram's HTML parse_mode dialect, escaping any other text so delivery
// never breaks on stray '&', '<', or '>'. Telegram's HTML mode has no table
// or heading elements at all: headings render as bold text, and tables
// render as one bullet per row (first cell bold, remaining cells joined
// after an em dash) rather than a monospace dump of the raw table syntax.
func toTelegramHTML(text string) string {
	var placeholders []string
	placeholder := func(html string) string {
		placeholders = append(placeholders, html)
		return "\x00" + string(rune(len(placeholders)-1)) + "\x00"
	}

	withoutFences := fencedCodeBlock.ReplaceAllStringFunc(text, func(match string) string {
		groups := fencedCodeBlock.FindStringSubmatch(match)
		language, code := groups[1], strings.TrimSuffix(groups[2], "\n")
		if language == "" {
			return placeholder("<pre><code>" + escapeHTML(code) + "</code></pre>")
		}
		return placeholder(`<pre><code class="language-` + language + `">` + escapeHTML(code) + "</code></pre>")
	})

	withoutTables := markdownTable.ReplaceAllStringFunc(withoutFences, func(match string) string {
		groups := markdownTable.FindStringSubmatch(match)
		return renderTableRowsAsBullets(groups[3])
	})

	withoutInlineCode := inlineCode.ReplaceAllStringFunc(withoutTables, func(match string) string {
		content := inlineCode.FindStringSubmatch(match)[1]
		return placeholder("<code>" + escapeHTML(content) + "</code>")
	})

	withoutRules := horizontalRule.ReplaceAllString(withoutInlineCode, "")
	withoutHeadings := markdownHeading.ReplaceAllString(withoutRules, "**$1**")

	escaped := escapeHTML(withoutHeadings)
	withLinks := markdownLink.ReplaceAllString(escaped, `<a href="$2">$1</a>`)
	withBold := boldText.ReplaceAllString(withLinks, "<b>$1</b>")

	result := withBold
	for i, html := range placeholders {
		result = strings.ReplaceAll(result, "\x00"+string(rune(i))+"\x00", html)
	}
	return result
}

// renderTableRowsAsBullets turns each data row of a pipe table into a
// bullet line instead of dumping the raw table syntax into a monospace
// block. The result still contains raw markdown (e.g. "**cell**") so it
// flows through the same escape/bold/link pass as the rest of the message.
// The header row is dropped: for typical chat-shaped tables (an item name
// followed by a few details) each row is self-descriptive without it.
func renderTableRowsAsBullets(rows string) string {
	rows = strings.TrimRight(rows, "\r\n")
	if rows == "" {
		return ""
	}
	var lines []string
	for _, row := range strings.Split(rows, "\n") {
		cells := splitTableRow(row)
		if len(cells) == 0 {
			continue
		}
		line := "• **" + cells[0] + "**"
		if len(cells) > 1 {
			line += " — " + strings.Join(cells[1:], " · ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func splitTableRow(row string) []string {
	trimmed := strings.TrimSpace(row)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cells = append(cells, strings.TrimSpace(part))
	}
	return cells
}

func escapeHTML(text string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(text)
}
