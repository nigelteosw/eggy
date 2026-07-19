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
// or heading elements at all, so tables render as a monospace block and
// headings render as bold text rather than being dropped or shown as raw
// markdown syntax.
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
		header, rows := groups[1], strings.TrimRight(groups[3], "\r\n")
		content := header
		if rows != "" {
			content += "\n" + rows
		}
		return placeholder("<pre>" + escapeHTML(content) + "</pre>")
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

func escapeHTML(text string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(text)
}
