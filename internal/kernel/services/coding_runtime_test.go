package services

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/nigelteosw/eggy/internal/adapters/state/jsonfile"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestCodingAgentRuntimeSelectsAndPersistsAliases(t *testing.T) {
	ctx := context.Background()
	store := jsonfile.Open(t.TempDir() + "/state.json")
	agents := map[string]ports.CodingAgent{
		"zeta":  &runtimeFakeCodingAgent{},
		"alpha": &runtimeFakeCodingAgent{},
	}
	runtime, err := NewCodingAgentRuntime(store, "alpha", agents)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := runtime.Selected(ctx); err != nil || got != "alpha" {
		t.Fatalf("selected=%q err=%v", got, err)
	}
	aliases := runtime.Aliases()
	if want := []string{"alpha", "zeta"}; !reflect.DeepEqual(aliases, want) {
		t.Fatalf("aliases=%v want=%v", aliases, want)
	}
	aliases[0] = "changed"
	if got := runtime.Aliases(); !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Fatalf("Aliases returned internal slice: %v", got)
	}

	if err := runtime.Select(ctx, "zeta"); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewCodingAgentRuntime(store, "alpha", agents)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := restarted.Selected(ctx); err != nil || got != "zeta" {
		t.Fatalf("persisted selected=%q err=%v", got, err)
	}

	if err := restarted.Select(ctx, "missing"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unknown alias error=%v", err)
	}
	if got, _ := restarted.Selected(ctx); got != "zeta" {
		t.Fatalf("unknown selection changed selected alias to %q", got)
	}

	if err := restarted.Select(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := restarted.Selected(ctx); err != nil || got != "alpha" {
		t.Fatalf("selected after reset=%q err=%v", got, err)
	}
}

func TestCodingAgentRuntimeDelegatesToSelectedAgent(t *testing.T) {
	wanted := ports.CodingResult{Summary: "done", ChangedFiles: []string{"main.go"}}
	var received ports.CodingRequest
	selected := &runtimeFakeCodingAgent{run: func(_ context.Context, request ports.CodingRequest, progress func(ports.CodingProgress)) (ports.CodingResult, error) {
		received = request
		progress(ports.CodingProgress{Kind: "message", RunID: request.RunID, Message: "working"})
		return wanted, nil
	}}
	runtime, err := NewCodingAgentRuntime(jsonfile.Open(t.TempDir()+"/state.json"), "first", map[string]ports.CodingAgent{
		"first":  &runtimeFakeCodingAgent{},
		"second": selected,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Select(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	request := ports.CodingRequest{RunID: "run-1", Workspace: "/workspace", Instruction: "change it"}
	var progress []ports.CodingProgress
	got, err := runtime.Run(context.Background(), request, func(event ports.CodingProgress) { progress = append(progress, event) })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, wanted) || !reflect.DeepEqual(received, request) || len(progress) != 1 || progress[0].Message != "working" {
		t.Fatalf("result=%#v request=%#v progress=%#v", got, received, progress)
	}
}

func TestCodingAgentRuntimeInterruptsAgentThatStartedRun(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	first := &runtimeFakeCodingAgent{run: func(context.Context, ports.CodingRequest, func(ports.CodingProgress)) (ports.CodingResult, error) {
		close(started)
		<-release
		return ports.CodingResult{}, nil
	}}
	second := &runtimeFakeCodingAgent{}
	runtime, err := NewCodingAgentRuntime(jsonfile.Open(t.TempDir()+"/state.json"), "first", map[string]ports.CodingAgent{
		"first": first, "second": second,
	})
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() {
		_, err := runtime.Run(context.Background(), ports.CodingRequest{RunID: "run-1"}, nil)
		runDone <- err
	}()
	<-started
	if err := runtime.Select(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Interrupt("run-1"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if got := first.interruptedRuns(); !reflect.DeepEqual(got, []string{"run-1"}) {
		t.Fatalf("first interrupts=%v", got)
	}
	if got := second.interruptedRuns(); len(got) != 0 {
		t.Fatalf("second interrupts=%v", got)
	}
}

func TestCodingAgentRuntimeRejectsDuplicateActiveRunID(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	first := &runtimeFakeCodingAgent{run: func(context.Context, ports.CodingRequest, func(ports.CodingProgress)) (ports.CodingResult, error) {
		close(started)
		<-release
		return ports.CodingResult{}, nil
	}}
	secondDelegated := false
	second := &runtimeFakeCodingAgent{run: func(context.Context, ports.CodingRequest, func(ports.CodingProgress)) (ports.CodingResult, error) {
		secondDelegated = true
		return ports.CodingResult{}, nil
	}}
	runtime, err := NewCodingAgentRuntime(jsonfile.Open(t.TempDir()+"/state.json"), "first", map[string]ports.CodingAgent{
		"first": first, "second": second,
	})
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() {
		_, err := runtime.Run(context.Background(), ports.CodingRequest{RunID: "duplicate"}, nil)
		runDone <- err
	}()
	<-started
	if err := runtime.Select(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Run(context.Background(), ports.CodingRequest{RunID: "duplicate"}, nil); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("duplicate run error=%v", err)
	}
	if secondDelegated {
		t.Fatal("duplicate run delegated to newly selected agent")
	}
	if err := runtime.Interrupt("duplicate"); err != nil {
		t.Fatalf("interrupt original run: %v", err)
	}
	close(release)
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if got := first.interruptedRuns(); !reflect.DeepEqual(got, []string{"duplicate"}) {
		t.Fatalf("first interrupts=%v", got)
	}
	if got := second.interruptedRuns(); len(got) != 0 {
		t.Fatalf("second interrupts=%v", got)
	}
}

func TestCodingAgentRuntimeRetriesStateVersionConflict(t *testing.T) {
	store := &runtimeConflictStore{conflictsRemaining: 1}
	runtime, err := NewCodingAgentRuntime(store, "first", map[string]ports.CodingAgent{
		"first": &runtimeFakeCodingAgent{}, "second": &runtimeFakeCodingAgent{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Select(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.Selected(context.Background()); err != nil || got != "second" {
		t.Fatalf("selected=%q err=%v", got, err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.updateCalls != 2 {
		t.Fatalf("update calls=%d want=2", store.updateCalls)
	}
}

type runtimeConflictStore struct {
	mu                 sync.Mutex
	state              ports.State
	conflictsRemaining int
	updateCalls        int
}

func (s *runtimeConflictStore) Load(context.Context) (ports.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, nil
}

func (s *runtimeConflictStore) Update(_ context.Context, expected uint64, mutate func(*ports.State) error) (ports.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateCalls++
	if s.conflictsRemaining > 0 {
		s.conflictsRemaining--
		return ports.State{}, ports.ErrStateVersionConflict
	}
	if s.state.Version != expected {
		return ports.State{}, ports.ErrStateVersionConflict
	}
	if err := mutate(&s.state); err != nil {
		return ports.State{}, err
	}
	s.state.Version++
	return s.state, nil
}

type runtimeFakeCodingAgent struct {
	run        func(context.Context, ports.CodingRequest, func(ports.CodingProgress)) (ports.CodingResult, error)
	mu         sync.Mutex
	interrupts []string
}

func (a *runtimeFakeCodingAgent) Run(ctx context.Context, request ports.CodingRequest, progress func(ports.CodingProgress)) (ports.CodingResult, error) {
	if a.run != nil {
		return a.run(ctx, request, progress)
	}
	return ports.CodingResult{}, nil
}

func (a *runtimeFakeCodingAgent) Interrupt(runID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.interrupts = append(a.interrupts, runID)
	return nil
}

func (a *runtimeFakeCodingAgent) interruptedRuns() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.interrupts...)
}
