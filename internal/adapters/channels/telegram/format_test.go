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

func TestToTelegramHTMLHandlesMixedContent(t *testing.T) {
	got := toTelegramHTML("Status: **ok** — see `README.md` at 5 < 10")
	want := "Status: <b>ok</b> — see <code>README.md</code> at 5 &lt; 10"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
