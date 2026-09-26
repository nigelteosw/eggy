package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/core/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// as returns a context acting as account id. Every private store call takes
// one; the tests below use two of them to prove that nothing crosses.
func as(id string) context.Context {
	return ports.WithPrincipal(context.Background(), ports.Principal{AccountID: id})
}

func TestAccountCannotReadOtherThread(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "eggy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "a"})
	b := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "b"})
	if _, err := db.CreateThread(a, "private-a", "web", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, found, err := db.GetThread(b, "private-a"); err != nil || found {
		t.Fatalf("cross-account lookup: found=%v err=%v", found, err)
	}
	if _, found, err := db.GetThread(a, "private-a"); err != nil || !found {
		t.Fatalf("own lookup: found=%v err=%v", found, err)
	}
}

func TestPrivateStoresRequireAPrincipal(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	now := time.Now()
	checks := map[string]func() error{
		"WriteMessage": func() error {
			return db.WriteMessage(ctx, ports.StoredMessage{ConversationID: "c", Role: "user", Content: "x", Source: "s", CreatedAt: now})
		},
		"RecentMessages":      func() error { _, err := db.RecentMessages(ctx, "c", 5); return err },
		"SearchText":          func() error { _, err := db.SearchText(ctx, "x", 5); return err },
		"ResetConversation":   func() error { return db.ResetConversation(ctx, "c", now) },
		"ConversationResetAt": func() error { _, _, err := db.ConversationResetAt(ctx, "c"); return err },
		"CreateThread":        func() error { _, err := db.CreateThread(ctx, "t", "web", now); return err },
		"ListThreads":         func() error { _, err := db.ListThreads(ctx, "web"); return err },
		"GetThread":           func() error { _, _, err := db.GetThread(ctx, "t"); return err },
		"RenameThread":        func() error { return db.RenameThread(ctx, "t", "x") },
		"DeleteThread":        func() error { return db.DeleteThread(ctx, "t") },
		"AttachWorkspace":     func() error { return db.AttachWorkspace(ctx, "t", "web", "r", "w", now) },
		"DetachWorkspace":     func() error { return db.DetachWorkspace(ctx, "t") },
		"StartTrace":          func() error { return db.StartTrace(ctx, ports.Trace{ID: "tr", StartedAt: now}) },
		"ListTraces":          func() error { _, err := db.ListTraces(ctx, 5); return err },
		"Trace":               func() error { _, _, _, err := db.Trace(ctx, "tr"); return err },
		"State.Load":          func() error { _, err := db.State().Load(ctx); return err },
		"State.Update":        func() error { _, err := db.State().Update(ctx, 0, func(*ports.State) error { return nil }); return err },
		"Schedules.List":      func() error { _, err := db.Schedules().List(ctx); return err },
		"Schedules.Create":    func() error { return db.Schedules().Create(ctx, ports.Schedule{ID: "s", Instruction: "x"}) },
		"Schedules.Get":       func() error { _, err := db.Schedules().Get(ctx, "s"); return err },
		"Schedules.Update":    func() error { return db.Schedules().Update(ctx, "s", func(*ports.Schedule) error { return nil }) },
		"Schedules.Delete":    func() error { return db.Schedules().Delete(ctx, "s") },
	}
	for name, call := range checks {
		if err := call(); !errors.Is(err, ports.ErrNoPrincipal) {
			t.Errorf("%s without a principal: err = %v, want ErrNoPrincipal", name, err)
		}
	}
}

func TestMessagesAndSearchAreScopedByAccount(t *testing.T) {
	db := newTestStore(t, 0)
	now := time.Now()
	// The same conversation ID on both sides: Telegram's fixed thread is the
	// same string for every account, and that must not merge two histories.
	for _, who := range []string{"a", "b"} {
		if err := db.WriteMessage(as(who), ports.StoredMessage{ConversationID: "owner", Role: "user", Content: "secret of " + who, Source: "telegram", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := db.RecentMessages(as("a"), "owner", 10)
	if err != nil || len(recent) != 1 || recent[0].Content != "secret of a" {
		t.Fatalf("recent=%+v err=%v", recent, err)
	}
	found, err := db.SearchText(as("b"), "secret", 10)
	if err != nil || len(found) != 1 || found[0].Content != "secret of b" {
		t.Fatalf("search=%+v err=%v", found, err)
	}
	// A reset by one account does not clear the other's window.
	if err := db.ResetConversation(as("a"), "owner", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if recent, _ := db.RecentMessages(as("a"), "owner", 10); len(recent) != 0 {
		t.Fatalf("a after reset: %+v", recent)
	}
	if recent, _ := db.RecentMessages(as("b"), "owner", 10); len(recent) != 1 {
		t.Fatalf("b after a's reset: %+v", recent)
	}
	if _, cleared, _ := db.ConversationResetAt(as("b"), "owner"); cleared {
		t.Fatal("b must not see a's reset")
	}
}

func TestThreadMutationsAreScopedByAccount(t *testing.T) {
	db := newTestStore(t, 0)
	now := time.Now()
	if _, err := db.CreateThread(as("a"), "t1", "web", now); err != nil {
		t.Fatal(err)
	}
	if err := db.RenameThread(as("b"), "t1", "hijacked"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetThreadTitle(as("b"), "t1", "hijacked"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteThread(as("b"), "t1"); err != nil {
		t.Fatal(err)
	}
	if err := db.AttachWorkspace(as("b"), "t1", "web", "repo", "/w", now); err != nil {
		t.Fatal(err)
	}
	if err := db.DetachWorkspace(as("b"), "t1"); err != nil {
		t.Fatal(err)
	}
	thread, found, err := db.GetThread(as("a"), "t1")
	if err != nil || !found {
		t.Fatalf("a's thread gone: found=%v err=%v", found, err)
	}
	if thread.Title != "" || thread.Workspace != "" {
		t.Fatalf("b changed a's thread: %+v", thread)
	}
	if thread.Owner != "a" {
		t.Fatalf("Owner = %q", thread.Owner)
	}
	// Thread IDs are global, so b's attach against a's ID was a no-op
	// rather than a second thread called t1.
	if listed, _ := db.ListThreads(as("a"), "web"); len(listed) != 1 {
		t.Fatalf("a sees %d threads", len(listed))
	}
	if listed, _ := db.ListThreads(as("b"), "web"); len(listed) != 0 {
		t.Fatalf("b sees %d threads", len(listed))
	}
	// Every account's workspace threads reach the reaper, each with its owner.
	if err := db.AttachWorkspace(as("a"), "t1", "web", "repo", "/wa", now); err != nil {
		t.Fatal(err)
	}
	if err := db.AttachWorkspace(as("b"), "t2", "web", "repo", "/wb", now); err != nil {
		t.Fatal(err)
	}
	with, err := db.ThreadsWithWorkspace(context.Background())
	if err != nil || len(with) != 2 || with[0].Owner == with[1].Owner {
		t.Fatalf("with workspace=%+v err=%v", with, err)
	}
}

func TestTracesAndSpansAreScopedByAccount(t *testing.T) {
	db := newTestStore(t, 0)
	now := time.Now()
	if err := db.StartTrace(as("a"), ports.Trace{ID: "tr", ConversationID: "c", Channel: "web", Source: "s", Kind: "owner", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendSpan(as("a"), ports.TraceSpan{TraceID: "tr", Sequence: 1, Kind: "model", Name: "m", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	// A span appended under the wrong account is dropped, as a span for a
	// pruned trace is: the trace is not there from b's point of view.
	if err := db.AppendSpan(as("b"), ports.TraceSpan{TraceID: "tr", Sequence: 2, Kind: "model", Name: "m", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteTrace(as("b"), ports.Trace{ID: "tr", Output: "forged"}); err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := db.Trace(as("b"), "tr"); err != nil || found {
		t.Fatalf("cross-account trace: found=%v err=%v", found, err)
	}
	if listed, _ := db.ListTraces(as("b"), 10); len(listed) != 0 {
		t.Fatalf("b lists %d traces", len(listed))
	}
	trace, spans, found, err := db.Trace(as("a"), "tr")
	if err != nil || !found {
		t.Fatalf("own trace: found=%v err=%v", found, err)
	}
	if trace.Output != "" || trace.Complete || len(spans) != 1 {
		t.Fatalf("b altered a's trace: %+v spans=%d", trace, len(spans))
	}
}

func TestMachineStateIsPerAccount(t *testing.T) {
	db := newTestStore(t, 0)
	state := db.State()
	if _, err := state.Update(as("a"), 0, func(next *ports.State) error {
		next.ApprovalMode = ports.ModeStrict
		next.Approvals["ap"] = approvals.Approval{ID: "ap", Status: approvals.Pending}
		next.ProcessedEvents["telegram:1"] = time.Now()
		next.ProactiveMessages = []time.Time{time.Now()}
		next.Agent.SelectedModel = "fast"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Each account has its own version counter, so b's first write is at 0.
	bState, err := state.Update(as("b"), 0, func(next *ports.State) error {
		next.ApprovalMode = ports.ModeAuto
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if bState.Version != 1 || len(bState.Approvals) != 0 || len(bState.ProcessedEvents) != 0 || len(bState.ProactiveMessages) != 0 || bState.Agent.SelectedModel != "" {
		t.Fatalf("b's state carries a's records: %#v", bState)
	}
	aState, err := state.Load(as("a"))
	if err != nil {
		t.Fatal(err)
	}
	if aState.ApprovalMode != ports.ModeStrict || aState.Version != 1 || len(aState.Approvals) != 1 {
		t.Fatalf("a's state after b's write: %#v", aState)
	}
}

func TestSchedulesAreScopedByAccount(t *testing.T) {
	db := newTestStore(t, 0)
	schedules := db.Schedules()
	if err := schedules.Create(as("a"), ports.Schedule{ID: "morning", Kind: ports.ScheduleExact, Instruction: "x", NextRun: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// The ID is global -- the scheduler claims by ID -- so b cannot reuse
	// it, but also cannot see, change or remove it.
	if err := schedules.Create(as("b"), ports.Schedule{ID: "morning", Kind: ports.ScheduleExact, Instruction: "y", NextRun: time.Now()}); err == nil {
		t.Fatal("duplicate id across accounts must fail")
	}
	if _, err := schedules.Get(as("b"), "morning"); !errors.Is(err, ports.ErrScheduleNotFound) {
		t.Fatalf("cross-account get err = %v", err)
	}
	if err := schedules.Update(as("b"), "morning", func(s *ports.Schedule) error { s.Instruction = "hijacked"; return nil }); !errors.Is(err, ports.ErrScheduleNotFound) {
		t.Fatalf("cross-account update err = %v", err)
	}
	if err := schedules.Delete(as("b"), "morning"); err != nil {
		t.Fatal(err)
	}
	if listed, _ := schedules.List(as("b")); len(listed) != 0 {
		t.Fatalf("b lists %d schedules", len(listed))
	}
	got, err := schedules.Get(as("a"), "morning")
	if err != nil || got.Instruction != "x" || got.Owner != "a" {
		t.Fatalf("a's schedule = %+v err=%v", got, err)
	}
	all, err := schedules.ListAll(context.Background())
	if err != nil || len(all) != 1 || all[0].Owner != "a" {
		t.Fatalf("ListAll = %+v err=%v", all, err)
	}
}
