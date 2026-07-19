package services

import (
	"context"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestHeartbeatPolicyEnforcesQuietHoursIntervalsAndWeeklyLimit(t *testing.T) {
	location := time.FixedZone("SGT", 8*60*60)
	policy, err := NewHeartbeatPolicy("22:00", "07:00", location, 2*time.Hour, 2)
	if err != nil {
		t.Fatal(err)
	}
	state := ports.State{}
	if policy.CanSend(state, time.Date(2026, 7, 19, 23, 0, 0, 0, location)) {
		t.Fatal("sent inside quiet hours")
	}
	first := time.Date(2026, 7, 19, 9, 0, 0, 0, location)
	if !policy.CanSend(state, first) {
		t.Fatal("first daytime message denied")
	}
	state.ProactiveMessages = []time.Time{first}
	if policy.CanSend(state, first.Add(time.Hour)) {
		t.Fatal("minimum interval ignored")
	}
	second := first.Add(3 * time.Hour)
	if !policy.CanSend(state, second) {
		t.Fatal("second message denied")
	}
	state.ProactiveMessages = append(state.ProactiveMessages, second)
	if policy.CanSend(state, second.Add(3*time.Hour)) {
		t.Fatal("weekly limit ignored")
	}
	if !policy.CanSend(state, second.Add(8*24*time.Hour)) {
		t.Fatal("old weekly messages were not pruned")
	}
}

func TestHeartbeatRecordsOnlyAllowedProactiveMessage(t *testing.T) {
	store := newMemoryStore()
	location := time.UTC
	policy, _ := NewHeartbeatPolicy("22:00", "07:00", location, time.Hour, 3)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, location)
	if err := policy.Record(context.Background(), store, now); err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load(context.Background())
	if len(state.ProactiveMessages) != 1 || !state.ProactiveMessages[0].Equal(now) {
		t.Fatalf("messages=%v", state.ProactiveMessages)
	}
	if err := policy.Record(context.Background(), store, now.Add(30*time.Minute)); err == nil {
		t.Fatal("throttled record succeeded")
	}
}

func TestHeartbeatCannotBypassProtectedActions(t *testing.T) {
	for _, action := range []string{"commit", "push", "create_pull_request", "calendar_create", "calendar_update", "calendar_delete"} {
		if HeartbeatActionAllowed(action) {
			t.Fatalf("heartbeat action %q allowed", action)
		}
	}
	if !HeartbeatActionAllowed("calendar_read") || !HeartbeatActionAllowed("status") {
		t.Fatal("safe heartbeat action denied")
	}
}
