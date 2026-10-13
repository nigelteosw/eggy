package agent

import (
	"strings"
	"testing"
)

// A reply that says Eggy searched, found, or pulled something has to rest on a
// tool result from this turn. Without a line saying so, the policy let a
// listing of "recent openings I found" stand with no search behind it.
func TestHardPolicyBindsResearchClaimsToToolResults(t *testing.T) {
	for _, phrase := range []string{"searched", "found", "filing"} {
		if !strings.Contains(coreRuntimePolicy, phrase) {
			t.Fatalf("hard runtime policy does not mention %q:\n%s", phrase, coreRuntimePolicy)
		}
	}
	if !strings.Contains(coreRuntimePolicy, "Never describe research as done") {
		t.Fatalf("hard runtime policy does not forbid describing unrun research as done:\n%s", coreRuntimePolicy)
	}
}
