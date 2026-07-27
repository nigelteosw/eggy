package telegram

import "testing"

func TestToTelegramHTMLEscapesPlainText(t *testing.T) {
	got := toTelegramHTML(`<ready> & "safe"`)
	want := `&lt;ready&gt; &amp; "safe"`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestToTelegramHTMLConvertsBold(t *testing.T) {
	got := toTelegramHTML("**hello** world")
	want := "<b>hello</b> world"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestToTelegramHTMLConvertsInlineCode(t *testing.T) {
	got := toTelegramHTML("use `foo()` now")
	want := "use <code>foo()</code> now"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestToTelegramHTMLEscapesInsideInlineCode(t *testing.T) {
	got := toTelegramHTML("`<x> & y`")
	want := "<code>&lt;x&gt; &amp; y</code>"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestToTelegramHTMLConvertsFencedCodeBlockWithLanguage(t *testing.T) {
	got := toTelegramHTML("```go\nfmt.Println(1)\n```")
	want := `<pre><code class="language-go">fmt.Println(1)</code></pre>`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestToTelegramHTMLConvertsFencedCodeBlockWithoutLanguage(t *testing.T) {
	got := toTelegramHTML("```\nplain\n```")
	want := `<pre><code>plain</code></pre>`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestToTelegramHTMLConvertsLink(t *testing.T) {
	got := toTelegramHTML("[Eggy](https://example.com)")
	want := `<a href="https://example.com">Eggy</a>`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestToTelegramHTMLConvertsHeadingToBold(t *testing.T) {
	got := toTelegramHTML("### Mon July 20")
	want := "<b>Mon July 20</b>"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestToTelegramHTMLRemovesHorizontalRule(t *testing.T) {
	got := toTelegramHTML("before\n---\nafter")
	want := "before\nafter"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestToTelegramHTMLRendersPipeTableRowsAsBullets(t *testing.T) {
	got := toTelegramHTML("| Event | Time |\n|-------|------|\n| Standup | 9 AM |")
	want := "• <b>Standup</b> — 9 AM"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestToTelegramHTMLRendersMultiRowTableAsOneBulletPerRow(t *testing.T) {
	got := toTelegramHTML("| Event | Time | Calendar |\n|-------|------|----------|\n| Standup | 9 AM | Work |\n| Lunch | 12 PM | Personal |")
	want := "• <b>Standup</b> — 9 AM · Work\n• <b>Lunch</b> — 12 PM · Personal"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestToTelegramHTMLHandlesCalendarStyleMessage(t *testing.T) {
	input := "### \U0001F4C5 Mon July 20\n| Event | Time | Calendar |\n|-------|------|----------|\n| Standup | 8:30 PM – 10:00 PM | Work |\n\n---\n\n### \U0001F4C5 Tue July 21\nNothing scheduled."
	got := toTelegramHTML(input)
	want := "<b>\U0001F4C5 Mon July 20</b>\n• <b>Standup</b> — 8:30 PM – 10:00 PM · Work\n\n<b>\U0001F4C5 Tue July 21</b>\nNothing scheduled."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestToTelegramHTMLHandlesMixedContent(t *testing.T) {
	got := toTelegramHTML("Status: **ok** — see `README.md` at 5 < 10")
	want := "Status: <b>ok</b> — see <code>README.md</code> at 5 &lt; 10"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
