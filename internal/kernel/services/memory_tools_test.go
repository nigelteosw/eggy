package services

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nigelteosw/eggy/internal/ports"
)

type memorySearchCall struct {
	query string
	limit int
}

type fakeMemoryStore struct {
	searchText []ports.StoredMessage
	textCalls  []memorySearchCall
}

func (s *fakeMemoryStore) WriteMessage(context.Context, ports.StoredMessage) error { return nil }
func (s *fakeMemoryStore) RecentMessages(context.Context, string, int) ([]ports.StoredMessage, error) {
	return nil, nil
}
func (s *fakeMemoryStore) ResetConversation(context.Context, string, time.Time) error { return nil }
func (s *fakeMemoryStore) SearchText(_ context.Context, query string, limit int) ([]ports.StoredMessage, error) {
	s.textCalls = append(s.textCalls, memorySearchCall{query: query, limit: limit})
	return s.searchText, nil
}

func TestRecallConversationDefinitionIsStrictAndDoesNotReadHistory(t *testing.T) {
	store := &fakeMemoryStore{}
	tool := NewRecallConversationTool(store, nil)

	definition := tool.Definition()
	if definition.Name != "recall_conversation" {
		t.Fatalf("tool name = %q", definition.Name)
	}
	var schema map[string]any
	if err := json.Unmarshal(definition.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["additionalProperties"] != false || !reflect.DeepEqual(schema["required"], []any{"query"}) {
		t.Fatalf("schema = %#v", schema)
	}
	if len(store.textCalls) != 0 {
		t.Fatalf("definition changed history: store=%#v", store)
	}
}

func TestRecallConversationTextDefaultsBoundsAndRedactsResults(t *testing.T) {
	long := "active-secret " + strings.Repeat("界", 1001)
	results := make([]ports.StoredMessage, 12)
	for i := range results {
		results[i] = ports.StoredMessage{ID: int64(i + 1), Role: ports.RoleUser, Content: long, Source: "telegram", CreatedAt: time.Unix(int64(i), 0).UTC()}
	}
	store := &fakeMemoryStore{searchText: results}
	tool := NewRecallConversationTool(store, NewSecretGuard([]string{"active-secret"}))

	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"past work"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := store.textCalls, []memorySearchCall{{query: "past work", limit: 5}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("text calls = %#v, want %#v", got, want)
	}
	var output recallOutput
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if output.Notice != recallNotice || len(output.Results) != 10 {
		t.Fatalf("output = %#v", output)
	}
	if utf8.RuneCountInString(output.Results[0].Excerpt) != 1000 || strings.Contains(output.Results[0].Excerpt, "active-secret") {
		t.Fatalf("excerpt = %q", output.Results[0].Excerpt)
	}
}

func TestRecallConversationRejectsInvalidInput(t *testing.T) {
	tool := NewRecallConversationTool(&fakeMemoryStore{}, nil)
	for _, input := range []string{
		`{}`, `{"query":""}`, `{"query":"x","mode":null}`, `{"query":"x","mode":"unknown"}`, `{"query":"x","limit":null}`, `{"query":"x","limit":0}`, `{"query":"x","limit":11}`, `{"query":"x","extra":true}`,
	} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(input)); err == nil {
			t.Fatalf("input %s succeeded", input)
		}
	}
}
