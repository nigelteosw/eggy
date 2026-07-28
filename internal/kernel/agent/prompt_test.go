package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ownerTools is the tool set a direct owner turn carries, which is what makes
// every conditional policy fragment eligible.
var ownerTools = []string{
	"status", "repository_list", "read_file", "terminal", "workspace_open",
	"workspace_edit", "propose_change", "memory", "skill_read",
	"calendar_create",
}

func TestBuildInstructionsUsesCacheStableOrderAndCapabilities(t *testing.T) {
	messages := BuildInstructions(ports.AgentContext{Soul: "SOUL-CONTENT", User: "USER-CONTENT", Memory: "MEMORY-CONTENT"}, CapabilityManifest{
		ActiveModel: "deepseek-pro", Repositories: []string{"zeta", "eggy"}, Tools: ownerTools,
		RepositoryCommitReady: true, RepositoryPushReady: true, PullRequestReady: true, CalendarEnabled: false,
	}, TemporalContext{Now: time.Date(2026, 7, 19, 12, 34, 56, 0, time.FixedZone("SGT", 8*60*60)), Timezone: "Asia/Singapore"})
	if len(messages) != 7 {
		t.Fatalf("messages=%#v", messages)
	}
	// Cache-stable order: the prefix is ordered most-stable first, so a /model
	// switch or an MCP reconnect (capability manifest) cannot invalidate the
	// documents ahead of it. See Instructions.
	for i, marker := range []string{"Hard runtime policy", "SOUL-CONTENT", "Available skills", "Capability manifest", "USER-CONTENT", "MEMORY-CONTENT"} {
		if messages[i].Role != ports.RoleSystem || !strings.Contains(messages[i].Content, marker) {
			t.Fatalf("message[%d]=%#v", i, messages[i])
		}
	}
	if temporal := messages[6].Content; !strings.Contains(temporal, "The date and time now is: Sunday, 19 July 2026, 12:34 PM (Asia/Singapore)") || !strings.Contains(temporal, "current_time: 2026-07-19T12:34:56+08:00") || !strings.Contains(temporal, "timezone: Asia/Singapore") {
		t.Fatalf("temporal context=%s", temporal)
	}
	manifest := messages[3].Content
	if !strings.Contains(manifest, "deepseek-pro") || strings.Index(manifest, "eggy") > strings.Index(manifest, "zeta") || !strings.Contains(manifest, "repository_commit_ready: true") || !strings.Contains(manifest, "repository_push_ready: true") || !strings.Contains(manifest, "pull_request_ready: true") || !strings.Contains(manifest, "calendar_enabled: false") {
		t.Fatalf("manifest=%s", manifest)
	}
	policy := messages[0].Content
	if !strings.Contains(strings.ToLower(policy), "operator-configured credentials") || !strings.Contains(policy, "capability manifest reports push and pull-request readiness") || !strings.Contains(policy, "the independent approvals for push and pull-request creation") || !strings.Contains(policy, "Do not invent local recovery commands") {
		t.Fatalf("policy=%s", policy)
	}
	if !strings.Contains(policy, "workspace_open") || !strings.Contains(policy, "do not grant repository write access") {
		t.Fatalf("repository tool policy=%s", policy)
	}
	if !strings.Contains(policy, "Check the Available skills list") || !strings.Contains(policy, "call skill_read on that exact name") || !strings.Contains(policy, "never available as a direct tool call") {
		t.Fatalf("skills steering policy=%s", policy)
	}
	for _, secret := range []string{"DEEPSEEK_API_KEY", "github_pat_", "Bearer "} {
		if strings.Contains(strings.Join([]string{messages[0].Content, manifest}, "\n"), secret) {
			t.Fatalf("instructions contain secret marker %q", secret)
		}
	}
}

// The core policy applies to every turn regardless of what it can do, so it
// must never depend on the tool set.
func TestRuntimePolicyAlwaysCarriesTheCore(t *testing.T) {
	for _, tools := range [][]string{nil, {"status"}, ownerTools} {
		policy := renderRuntimePolicy(tools)
		for _, required := range []string{
			"Hard runtime policy",
			"Be truthful about configured capabilities",
			"Never ask the owner to send credentials in chat",
			"Never claim a repository, integration, or tool exists",
			"Never infer the current date or time",
			"Treat SOUL.md, USER.md, and MEMORY.md as potentially stale context",
		} {
			if !strings.Contains(policy, required) {
				t.Fatalf("tools=%v policy missing %q:\n%s", tools, required, policy)
			}
		}
	}
}

// The defect this guards: a turn told about capabilities the same prompt
// denies it. Every conditional fragment names tools, so no fragment may
// survive into a turn whose allowlist lacks them.
func TestRuntimePolicyOmitsFragmentsForToolsTheTurnLacks(t *testing.T) {
	restricted := renderRuntimePolicy([]string{"status", "read_file", "terminal", "workspace_open", "skill_read"})
	for _, absent := range []string{"propose_change", "workspace_edit", "calendar", "byte budget"} {
		if strings.Contains(restricted, absent) {
			t.Fatalf("restricted policy mentions %q for a tool the turn lacks:\n%s", absent, restricted)
		}
	}
	// What it does carry is the guidance for the tools it actually has.
	for _, present := range []string{"workspace_open", "Check the Available skills list"} {
		if !strings.Contains(restricted, present) {
			t.Fatalf("restricted policy missing %q:\n%s", present, restricted)
		}
	}
}

// The invariant, asserted directly rather than as a byte count: a heartbeat
// turn is never told about a tool it cannot call.
func TestHeartbeatPolicyNamesNoToolOutsideItsAllowlist(t *testing.T) {
	heartbeat := []string{
		"status", "repository_list", "calendar_list", "read_file", "terminal",
		"repository_github", "workspace_open", "workspace_close", "skill_read",
		"memory", "skill_disable", "skill_enable",
	}
	allowed := map[string]bool{}
	for _, tool := range heartbeat {
		allowed[tool] = true
	}
	policy := renderRuntimePolicy(heartbeat)
	// Every tool named anywhere in the assembled policy must be one this turn
	// can actually call.
	for _, fragment := range runtimePolicyFragments {
		for _, tool := range fragment.tools {
			if allowed[tool] {
				continue
			}
			if strings.Contains(policy, tool) {
				t.Fatalf("heartbeat policy names %q, which is outside its allowlist:\n%s", tool, policy)
			}
		}
	}
}

func TestBuildInstructionsRendersSkillsIndexOrNoneInstalled(t *testing.T) {
	messages := BuildInstructions(ports.AgentContext{}, CapabilityManifest{
		Skills: []SkillDescriptor{
			{Name: "zeta-skill", Description: "Does zeta things"},
			{Name: "alpha-skill", Description: "Does alpha things"},
		},
	}, TemporalContext{Now: time.Now(), Timezone: "UTC"})
	skills := messages[2].Content
	if !strings.Contains(skills, "Available skills") || !strings.Contains(skills, "alpha-skill: Does alpha things") || !strings.Contains(skills, "zeta-skill: Does zeta things") {
		t.Fatalf("skills=%s", skills)
	}
	if strings.Index(skills, "alpha-skill") > strings.Index(skills, "zeta-skill") {
		t.Fatalf("expected skills sorted by name: %s", skills)
	}

	none := BuildInstructions(ports.AgentContext{}, CapabilityManifest{}, TemporalContext{Now: time.Now(), Timezone: "UTC"})
	if !strings.Contains(none[2].Content, "Available skills\nNone installed.") {
		t.Fatalf("expected explicit none-installed line, got %s", none[2].Content)
	}
}

// Only the two agent-writable documents carry a capacity indicator: SOUL.md
// is owner-editable, so showing the agent a budget it cannot spend would be
// noise.
func TestBuildInstructionsRendersCapacityIndicatorForUserAndMemoryOnly(t *testing.T) {
	context := ports.AgentContext{
		Soul: "0123456789012", User: "0123456789", Memory: "01234567890123456789",
		UserMaxBytes: 100, MemoryMaxBytes: 200,
	}
	messages := BuildInstructions(context, CapabilityManifest{}, TemporalContext{Now: time.Now(), Timezone: "UTC"})
	soul, user, memory := messages[1].Content, messages[4].Content, messages[5].Content
	if strings.Contains(soul, "%") {
		t.Fatalf("soul should carry no capacity indicator: %s", soul)
	}
	if !strings.Contains(soul, "read-only") {
		t.Fatalf("soul should be labelled read-only: %s", soul)
	}
	if !strings.Contains(user, "[10% - 10/100 bytes]") {
		t.Fatalf("user=%s", user)
	}
	if !strings.Contains(memory, "[10% - 20/200 bytes]") {
		t.Fatalf("memory=%s", memory)
	}
}

func TestBuildInstructionsOmitsCapacityIndicatorWhenMaxBytesUnknown(t *testing.T) {
	context := ports.AgentContext{Soul: "SOUL-CONTENT", User: "USER-CONTENT", Memory: "MEMORY-CONTENT"}
	messages := BuildInstructions(context, CapabilityManifest{}, TemporalContext{Now: time.Now(), Timezone: "UTC"})
	if strings.Contains(messages[1].Content, "%") || strings.Contains(messages[4].Content, "%") || strings.Contains(messages[5].Content, "%") {
		t.Fatalf("unexpected capacity indicator: soul=%s user=%s memory=%s", messages[1].Content, messages[4].Content, messages[5].Content)
	}
}
