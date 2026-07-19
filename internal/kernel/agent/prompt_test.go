package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestBuildInstructionsUsesFixedTrustOrderAndCapabilities(t *testing.T) {
	messages := BuildInstructions(ports.AgentContext{Soul: "SOUL-CONTENT", User: "USER-CONTENT", Memory: "MEMORY-CONTENT"}, CapabilityManifest{
		ActiveModel: "deepseek-pro", Repositories: []string{"zeta", "eggy"}, Tools: []string{"status", "repository_list"}, CodexReady: true, CalendarEnabled: false,
	}, TemporalContext{Now: time.Date(2026, 7, 19, 12, 34, 56, 0, time.FixedZone("SGT", 8*60*60)), Timezone: "Asia/Singapore"})
	if len(messages) != 6 {
		t.Fatalf("messages=%#v", messages)
	}
	for i, marker := range []string{"Hard runtime policy", "Capability manifest", "SOUL-CONTENT", "USER-CONTENT", "MEMORY-CONTENT"} {
		if messages[i].Role != ports.RoleSystem || !strings.Contains(messages[i].Content, marker) {
			t.Fatalf("message[%d]=%#v", i, messages[i])
		}
	}
	if temporal := messages[5].Content; !strings.Contains(temporal, "current_time: 2026-07-19T12:34:56+08:00") || !strings.Contains(temporal, "timezone: Asia/Singapore") {
		t.Fatalf("temporal context=%s", temporal)
	}
	manifest := messages[1].Content
	if !strings.Contains(manifest, "deepseek-pro") || strings.Index(manifest, "eggy") > strings.Index(manifest, "zeta") || !strings.Contains(manifest, "codex_ready: true") || !strings.Contains(manifest, "calendar_enabled: false") {
		t.Fatalf("manifest=%s", manifest)
	}
	for _, secret := range []string{"DEEPSEEK_API_KEY", "github_pat_", "Bearer "} {
		if strings.Contains(strings.Join([]string{messages[0].Content, manifest}, "\n"), secret) {
			t.Fatalf("instructions contain secret marker %q", secret)
		}
	}
}
