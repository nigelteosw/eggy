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

func TestRecallConversationDefinitionIsStrictAndDoesNotReadHistory(t *testing.T) {
	store := &fakeMemoryStore{}
	tool := NewRecallConversationTool(store, nil, nil)

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
	if len(store.textCalls) != 0 || len(store.similarCalls) != 0 || len(store.pendingLimits) != 0 {
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
	tool := NewRecallConversationTool(store, nil, NewSecretGuard([]string{"active-secret"}))

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

func TestRecallConversationSemanticEmbedsQueryAndRejectsMissingEmbedder(t *testing.T) {
	store := &fakeMemoryStore{searchSimilar: []ports.StoredMessage{{ID: 7, Content: "matching"}}}
	tool := NewRecallConversationTool(store, &fakeEmbedder{vectors: map[string][]float32{"semantic query": {1, 2}}}, nil)

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"semantic query","mode":"semantic","limit":10}`)); err != nil {
		t.Fatal(err)
	}
	if got, want := store.similarCalls, []memorySimilarCall{{embedding: []float32{1, 2}, limit: 10}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("similar calls = %#v, want %#v", got, want)
	}
	withoutEmbedder := NewRecallConversationTool(store, nil, nil)
	if _, err := withoutEmbedder.Execute(context.Background(), json.RawMessage(`{"query":"semantic query","mode":"semantic"}`)); err == nil || !strings.Contains(err.Error(), "semantic recall unavailable") {
		t.Fatalf("missing embedder error = %v", err)
	}
}

func TestRecallConversationRejectsInvalidInput(t *testing.T) {
	tool := NewRecallConversationTool(&fakeMemoryStore{}, nil, nil)
	for _, input := range []string{
		`{}`, `{"query":""}`, `{"query":"x","mode":null}`, `{"query":"x","mode":"unknown"}`, `{"query":"x","limit":null}`, `{"query":"x","limit":0}`, `{"query":"x","limit":11}`, `{"query":"x","extra":true}`,
	} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(input)); err == nil {
			t.Fatalf("input %s succeeded", input)
		}
	}
}
