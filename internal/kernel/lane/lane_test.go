package lane

import "testing"

func TestDetect(t *testing.T) {
	tests := []struct {
		name string
		text string
		want Lane
	}{
		{name: "implement design", text: "Implement the approved design", want: Implementation},
		{name: "implement endpoint", text: "Can you implement a new endpoint?", want: Implementation},
		{name: "refactor module", text: "Refactor the authentication module", want: Implementation},
		{name: "create PR", text: "Create a PR for these changes", want: Implementation},
		{name: "create pull request", text: "Create a pull request please", want: Implementation},
		{name: "open MR", text: "Open an MR for this branch", want: Implementation},
		{name: "commit this", text: "Commit this change", want: Implementation},
		{name: "fix broken test", text: "Fix the broken test", want: Implementation},
		{name: "fix code", text: "Fix the code in handler.go", want: Implementation},
		{name: "fix bug", text: "There's a bug in the auth module, fix it", want: Implementation},
		{name: "change function", text: "Change the function signature", want: Implementation},
		{name: "add feature", text: "Add a new feature to the API", want: Implementation},
		{name: "add endpoint", text: "Add an endpoint for user profiles", want: Implementation},
		{name: "remove deprecated", text: "Remove the deprecated endpoint", want: Implementation},
		{name: "modify file", text: "Modify the config file", want: Implementation},
		{name: "update handler", text: "Update the error handler", want: Implementation},
		{name: "rewrite module", text: "Rewrite the payment module", want: Implementation},
		{name: "patch vulnerability", text: "Patch the vulnerability in auth.go", want: Implementation},
		{name: "fix test", text: "Fix the failing unit test", want: Implementation},
		{name: "diagnose and fix", text: "Diagnose the failing test and fix it", want: Implementation},
		{name: "explain auth", text: "Explain how webhook authentication works", want: Assistant},
		{name: "review design", text: "Review this design and report problems", want: Assistant},
		{name: "diagnose test", text: "Diagnose the failing test", want: Assistant},
		{name: "investigate leak", text: "Investigate the memory leak", want: Assistant},
		{name: "what framework", text: "What testing framework does eggy use?", want: Assistant},
		{name: "how dispatcher", text: "How does the dispatcher work?", want: Assistant},
		{name: "list repos", text: "List all repositories", want: Assistant},
		{name: "show status", text: "Show me the status", want: Assistant},
		{name: "look at TODO", text: "looking at the TODO.md, what would be a good thing to implement next?", want: Assistant},
		{name: "check calendar", text: "what events do i have on my calendar?", want: Assistant},
		{name: "fix it", text: "Fix it", want: Assistant},
		{name: "change something", text: "Can you change something?", want: Assistant},
		{name: "add that", text: "Add that please", want: Assistant},
		{name: "remove it", text: "Remove it", want: Assistant},
		{name: "add calendar", text: "Add a meeting to my calendar", want: Assistant},
		{name: "fix schedule", text: "Fix my schedule for tomorrow", want: Assistant},
		{name: "update reminder", text: "Update the reminder time", want: Assistant},
		{name: "empty", text: "", want: Assistant},
		{name: "whitespace", text: "   ", want: Assistant},
		{name: "hello", text: "Hello!", want: Assistant},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Detect(tt.text)
			if got != tt.want {
				t.Errorf("Detect(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
