package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// version6Schema is the machine-state schema exactly as the previous release
// wrote it, before any table carried an account. Kept verbatim so the
// upgrade is exercised against what is actually on disk in an old home,
// not against a reconstruction of it.
const version6Schema = `
CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT, conversation_id TEXT NOT NULL DEFAULT 'owner',
    role TEXT NOT NULL, content TEXT NOT NULL, source TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE VIRTUAL TABLE messages_fts USING fts5(content, content='messages', content_rowid='id');
CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
END;
CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT, channel TEXT NOT NULL, created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL, workspace TEXT, workspace_repository TEXT, workspace_branch TEXT, workspace_session TEXT);
CREATE TABLE conversation_resets (conversation_id TEXT PRIMARY KEY, cleared_at INTEGER NOT NULL);
CREATE TABLE traces (id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, session TEXT NOT NULL DEFAULT '',
    channel TEXT NOT NULL, source TEXT NOT NULL, kind TEXT NOT NULL, model TEXT NOT NULL, effort TEXT NOT NULL,
    input TEXT NOT NULL, output TEXT NOT NULL, error TEXT NOT NULL, usage TEXT NOT NULL,
    started_at INTEGER NOT NULL, duration_ns INTEGER NOT NULL, complete INTEGER NOT NULL);
CREATE TABLE trace_spans (id INTEGER PRIMARY KEY AUTOINCREMENT, trace_id TEXT NOT NULL, sequence INTEGER NOT NULL,
    kind TEXT NOT NULL, name TEXT NOT NULL, call_id TEXT NOT NULL, request TEXT NOT NULL, response TEXT NOT NULL,
    error TEXT NOT NULL, usage TEXT NOT NULL, started_at INTEGER NOT NULL, duration_ns INTEGER NOT NULL);
CREATE TABLE schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE imports (name TEXT PRIMARY KEY, imported_at INTEGER NOT NULL, records INTEGER NOT NULL DEFAULT 0);
CREATE TABLE machine_state (id INTEGER PRIMARY KEY CHECK (id = 1), version INTEGER NOT NULL,
    approval_mode TEXT NOT NULL DEFAULT '', approval_auto_mode INTEGER NOT NULL DEFAULT 0,
    agent TEXT NOT NULL DEFAULT '{}', repositories TEXT NOT NULL DEFAULT '{}');
CREATE TABLE approvals (id TEXT PRIMARY KEY, record TEXT NOT NULL);
CREATE TABLE processed_events (id TEXT PRIMARY KEY, seen_at TEXT NOT NULL);
CREATE TABLE proactive_messages (id INTEGER PRIMARY KEY AUTOINCREMENT, sent_at TEXT NOT NULL);
CREATE TABLE schedules (id TEXT PRIMARY KEY, kind TEXT NOT NULL, execution TEXT NOT NULL DEFAULT '',
    instruction TEXT NOT NULL, expression TEXT NOT NULL DEFAULT '', next_run TEXT NOT NULL DEFAULT '',
    last_run TEXT NOT NULL DEFAULT '', pending_run TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 0);
CREATE TABLE auth_records (section TEXT NOT NULL, key TEXT NOT NULL, record TEXT NOT NULL, PRIMARY KEY (section, key));
INSERT INTO schema_meta VALUES ('machine_state_version', '6');
INSERT INTO messages (conversation_id, role, content, source, created_at) VALUES ('owner', 'user', 'remember the aardvark', 'telegram', 10);
INSERT INTO messages (conversation_id, role, content, source, created_at) VALUES ('web-1', 'assistant', 'noted', 'web', 20);
INSERT INTO threads VALUES ('web-1', 'Aardvarks', 'web', 5, 20, '/data/runs/w1', 'eggy', 'main', 'sess');
INSERT INTO conversation_resets VALUES ('owner', 3);
INSERT INTO traces VALUES ('tr1', 'owner', '', 'telegram', 'telegram', 'owner', 'm', '', 'prompt', 'reply', '', '{}', 10, 5, 1);
INSERT INTO trace_spans (trace_id, sequence, kind, name, call_id, request, response, error, usage, started_at, duration_ns)
    VALUES ('tr1', 1, 'model', 'm', '', 'req', 'res', '', '{}', 10, 1);
INSERT INTO machine_state VALUES (1, 12, 'strict', 0, '{"selected_model":"deepseek-pro"}', '{"eggy":{"Name":"eggy","CloneURL":"https://x/eggy.git"}}');
INSERT INTO approvals VALUES ('a1', '{"id":"a1","action":"tool_call","status":"pending"}');
INSERT INTO processed_events VALUES ('telegram:99', '2026-09-06T10:00:00Z');
INSERT INTO proactive_messages (sent_at) VALUES ('2026-09-06T09:00:00Z');
INSERT INTO schedules VALUES ('morning', 'recurring', 'agent', 'status', '0 9 * * *', '2026-09-07T09:00:00Z', '', '', 1);
INSERT INTO auth_records VALUES ('google', 'workspace', '{"version":1,"ciphertext":"sealed-google"}');
INSERT INTO auth_records VALUES ('mcp', 'railway', '{"version":1,"ciphertext":"sealed-mcp"}');
`

func writeVersion6Database(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "eggy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(version6Schema); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMigrateAccountsAssignsEveryLegacyRecordToTheNamedAccount(t *testing.T) {
	path := writeVersion6Database(t)
	store, err := Open(path)
	if err != nil {
		t.Fatalf("opening a version-6 home: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	// Until the mapping is applied nothing is readable by anyone: the rows
	// are unowned, not shared.
	if recent, err := store.RecentMessages(as("nigel"), "owner", 10); err != nil || len(recent) != 0 {
		t.Fatalf("before migration: recent=%+v err=%v", recent, err)
	}
	if store.HasUnownedRecords(ctx) != true {
		t.Fatal("unowned legacy records must be reported before the mapping runs")
	}
	if err := store.MigrateAccounts(ctx, "nigel", false); err != nil {
		t.Fatal(err)
	}
	if store.HasUnownedRecords(ctx) {
		t.Fatal("nothing should be unowned after the mapping")
	}

	nigel, other := as("nigel"), as("partner")
	recent, err := store.RecentMessages(nigel, "owner", 10)
	if err != nil || len(recent) != 1 || recent[0].Content != "remember the aardvark" {
		t.Fatalf("recent=%+v err=%v", recent, err)
	}
	if found, _ := store.SearchText(nigel, "aardvark", 10); len(found) != 1 {
		t.Fatalf("fts after migration: %+v", found)
	}
	if found, _ := store.SearchText(other, "aardvark", 10); len(found) != 0 {
		t.Fatalf("other account sees legacy fts hit: %+v", found)
	}
	thread, found, err := store.GetThread(nigel, "web-1")
	if err != nil || !found || thread.Title != "Aardvarks" || thread.Workspace != "/data/runs/w1" || thread.Owner != "nigel" {
		t.Fatalf("thread=%+v found=%v err=%v", thread, found, err)
	}
	if _, found, _ := store.GetThread(other, "web-1"); found {
		t.Fatal("other account sees legacy thread")
	}
	if at, cleared, _ := store.ConversationResetAt(nigel, "owner"); !cleared || at.UnixNano() != 3 {
		t.Fatalf("reset=%v cleared=%v", at, cleared)
	}
	trace, spans, found, err := store.Trace(nigel, "tr1")
	if err != nil || !found || trace.Input != "prompt" || len(spans) != 1 {
		t.Fatalf("trace=%+v spans=%d found=%v err=%v", trace, len(spans), found, err)
	}
	if _, _, found, _ := store.Trace(other, "tr1"); found {
		t.Fatal("other account sees legacy trace")
	}
	state, err := store.State().Load(nigel)
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != 12 || state.ApprovalMode != ports.ModeStrict || state.Agent.SelectedModel != "deepseek-pro" || state.Repositories["eggy"].CloneURL != "https://x/eggy.git" {
		t.Fatalf("state=%#v", state)
	}
	if _, ok := state.Approvals["a1"]; !ok {
		t.Fatalf("legacy approval kept for the single owner: %#v", state.Approvals)
	}
	if _, ok := state.ProcessedEvents["telegram:99"]; !ok {
		t.Fatalf("dedup window lost: %#v", state.ProcessedEvents)
	}
	if len(state.ProactiveMessages) != 1 {
		t.Fatalf("proactive=%v", state.ProactiveMessages)
	}
	otherState, err := store.State().Load(other)
	if err != nil || otherState.Version != 0 || otherState.ApprovalMode != "" || len(otherState.ProcessedEvents) != 0 {
		t.Fatalf("other state=%#v err=%v", otherState, err)
	}
	schedules, err := store.Schedules().List(nigel)
	if err != nil || len(schedules) != 1 || schedules[0].Owner != "nigel" {
		t.Fatalf("schedules=%+v err=%v", schedules, err)
	}
	if schedules, _ := store.Schedules().List(other); len(schedules) != 0 {
		t.Fatalf("other account sees legacy schedule: %+v", schedules)
	}
	// Grants are shared and untouched, byte for byte.
	for _, record := range [][2]string{{"google", "workspace"}, {"mcp", "railway"}} {
		grant, err := store.Auth().Read(record[0], record[1])
		if err != nil || !strings.Contains(string(grant), "sealed-") {
			t.Fatalf("grant %s/%s = %s err=%v", record[0], record[1], grant, err)
		}
	}
	var version string
	if err := store.db.QueryRow(`SELECT value FROM schema_meta WHERE key = ?`, machineStateVersionKey).Scan(&version); err != nil || version != "7" {
		t.Fatalf("version=%q err=%v", version, err)
	}
}

func TestMigrateAccountsInvalidatesPendingApprovalsOnConversion(t *testing.T) {
	store, err := Open(writeVersion6Database(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.MigrateAccounts(context.Background(), "nigel", true); err != nil {
		t.Fatal(err)
	}
	state, err := store.State().Load(as("nigel"))
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Approvals) != 0 {
		t.Fatalf("legacy approvals survived conversion: %#v", state.Approvals)
	}
	if _, ok := state.ProcessedEvents["telegram:99"]; !ok {
		t.Fatal("dedup window must survive conversion so a replayed update is not delivered again")
	}
}

func TestMigrateAccountsIsIdempotentAndFollowsARename(t *testing.T) {
	store, err := Open(writeVersion6Database(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	// A legacy boot: the single owner is named by its Telegram number.
	if err := store.MigrateAccounts(ctx, "42", false); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateAccounts(ctx, "42", false); err != nil {
		t.Fatalf("same mapping again: %v", err)
	}
	if recorded, _ := store.LegacyAccount(ctx); recorded != "42" {
		t.Fatalf("recorded=%q", recorded)
	}
	// Conversion names the same person "nigel": everything "42" owned,
	// including what it wrote after the first migration, moves across.
	if err := store.WriteMessage(as("42"), ports.StoredMessage{ConversationID: "owner", Role: "user", Content: "after", Source: "telegram", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateAccounts(ctx, "nigel", true); err != nil {
		t.Fatal(err)
	}
	if recent, _ := store.RecentMessages(as("nigel"), "owner", 10); len(recent) != 2 {
		t.Fatalf("nigel's history after rename: %+v", recent)
	}
	if recent, _ := store.RecentMessages(as("42"), "owner", 10); len(recent) != 0 {
		t.Fatalf("42 still owns records: %+v", recent)
	}
	if recorded, _ := store.LegacyAccount(ctx); recorded != "nigel" {
		t.Fatalf("recorded=%q", recorded)
	}
	if state, _ := store.State().Load(as("nigel")); state.Version != 12 || state.ApprovalMode != ports.ModeStrict {
		t.Fatalf("state after rename=%#v", state)
	}
}

func TestFreshDatabaseNeedsNoMigration(t *testing.T) {
	store := newTestStore(t, 0)
	ctx := context.Background()
	if store.HasUnownedRecords(ctx) {
		t.Fatal("fresh database reports unowned records")
	}
	if err := store.MigrateAccounts(ctx, "nigel", true); err != nil {
		t.Fatalf("mapping on a fresh database: %v", err)
	}
	// Nothing was mapped, so nothing is recorded and another owner is fine.
	if err := store.MigrateAccounts(ctx, "partner", true); err != nil {
		t.Fatalf("second owner on a fresh database: %v", err)
	}
}

func TestOlderBinaryRefusesTheUpgradedDatabase(t *testing.T) {
	store := newTestStore(t, 0)
	var version string
	if err := store.db.QueryRow(`SELECT value FROM schema_meta WHERE key = ?`, machineStateVersionKey).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != "7" {
		t.Fatalf("fresh database stamped %q, want 7 so a version-6 binary refuses it", version)
	}
}
