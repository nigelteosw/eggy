package telegram

import (
	"regexp"
	"strings"
)

var (
	fencedCodeBlock = regexp.MustCompile("(?s)```([a-zA-Z0-9_+-]*)\\n?(.*?)```")
	inlineCode      = regexp.MustCompile("`([^`\\n]+)`")
	boldText        = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	markdownLink    = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
)

// toTelegramHTML converts a small Markdown subset (bold, inline code, fenced
// code blocks, links) into Telegram's HTML parse_mode dialect, escaping any
// other text so delivery never breaks on stray '&', '<', or '>'.
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

	withoutInlineCode := inlineCode.ReplaceAllStringFunc(withoutFences, func(match string) string {
		content := inlineCode.FindStringSubmatch(match)[1]
		return placeholder("<code>" + escapeHTML(content) + "</code>")
	})

	escaped := escapeHTML(withoutInlineCode)
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
