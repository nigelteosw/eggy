package events

import "testing"

func TestPromptPrefixesAReplyWithTheFullQuotedPassage(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Message
		want string
	}{
		{"no quote", Message{Text: "hello"}, "hello"},
		{"blank quote is no quote", Message{Text: "hello", Quote: &Quote{Text: "  "}}, "hello"},
		{"someone else's words", Message{Text: "why?", Quote: &Quote{Text: "Board on a quarterly basis"}},
			"[Replying to: \"Board on a quarterly basis\"]\n\nwhy?"},
		{"Eggy's own words", Message{Text: "why?", Quote: &Quote{Text: "line one\nline two", OwnMessage: true}},
			"[Replying to your previous message: \"line one\nline two\"]\n\nwhy?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Prompt(); got != tc.want {
				t.Fatalf("Prompt()=%q, want %q", got, tc.want)
			}
		})
	}
}
