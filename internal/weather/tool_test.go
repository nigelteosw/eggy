package weather

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestToolIsReadOnlyArgumentFreeAndReadsTheSharedReport(t *testing.T) {
	u := newUpstream(t)
	area, key := "Bedok", ""
	c := &clock{at: time.Date(2026, 11, 17, 14, 0, 0, 0, singapore)}
	service := newService(u, &area, &key, c)
	tool := NewTool(service)

	definition := tool.Definition()
	if definition.Name != ToolName || !definition.Effect.ReadOnly {
		t.Fatalf("definition = %+v", definition)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(definition.Schema, &schema); err != nil || len(schema.Properties) != 0 {
		t.Fatalf("schema takes arguments: %s", definition.Schema)
	}

	raw, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Area  string `json:"area"`
		Today struct {
			High int `json:"high"`
		} `json:"today"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Area != "Bedok" || out.Today.High != 33 {
		t.Fatalf("output = %s", raw)
	}
	// Home loading right after the tool call is served the same report.
	if _, err := service.Report(context.Background(), false); err != nil || u.calls.Load() != 5 {
		t.Fatalf("the tool and Home did not share one fetch: calls=%d", u.calls.Load())
	}
}
