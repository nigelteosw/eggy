package agent

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

// scriptedTool answers with a fixed body per call, so a test can plant one
// early finding and then bury it under noise.
type scriptedTool struct {
	name    string
	replies []string
	calls   int
}

func (t *scriptedTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: t.name, Schema: json.RawMessage(`{"type":"object"}`)}
}

func (t *scriptedTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	reply := ""
	if t.calls < len(t.replies) {
		reply = t.replies[t.calls]
	}
	t.calls++
	return json.RawMessage(reply), nil
}

// callStep is one scripted model turn that calls a tool.
func callStep(id, tool, arguments string) ports.ModelResponse {
	return ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{
		{ID: id, Name: tool, Arguments: json.RawMessage(arguments)},
	}}}
}

func lastRequest(t *testing.T, model *queuedModel) []ports.Message {
	t.Helper()
	if len(model.requests) == 0 {
		t.Fatal("model was never called")
	}
	return model.requests[len(model.requests)-1].Messages
}

func windowText(messages []ports.Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, message.Content)
		for _, call := range message.ToolCalls {
			parts = append(parts, call.Name, string(call.Arguments))
		}
	}
	return strings.Join(parts, "\n")
}

// A finding from an early step must survive compaction. "Used lookup" would
// tell the model it once looked something up while discarding the answer,
// which is the only reason the step happened.
func TestCompactionKeepsEarlyFindingsAfterFolding(t *testing.T) {
	tool := &scriptedTool{name: "lookup", replies: []string{`{"deploy_token":"tok-98217"}`}}
	responses := []ports.ModelResponse{callStep("1", "lookup", `{"key":"deploy_token"}`)}
	for i := 0; i < 8; i++ {
		responses = append(responses, callStep(fmt.Sprintf("n%d", i), "lookup", `{"key":"noise"}`))
	}
	responses = append(responses, ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, Content: "done"}})
	model := &queuedModel{responses: responses}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}},
		StaticTools{tool}, ContextPolicy{RecentSteps: 2})

	if _, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "find the token"}, nil, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	window := windowText(lastRequest(t, model))
	if !strings.Contains(window, "tok-98217") {
		t.Fatalf("the early finding was compacted away:\n%s", window)
	}
	if !strings.Contains(window, `"key":"deploy_token"`) {
		t.Fatalf("the arguments that produced the finding were lost:\n%s", window)
	}
	if !strings.Contains(window, "untrusted") {
		t.Fatalf("quoted tool output was not marked as untrusted data:\n%s", window)
	}
}

// A correction that arrives after the checkpoint is already full must still
// reach the model, and verbatim: a paraphrased correction is a correction
// lost, and a summary that stopped recording when full would drop exactly the
// newest instruction.
func TestCompactionPreservesSteeringAfterTheSummaryFills(t *testing.T) {
	filler := strings.Repeat("x", 4000)
	tool := &scriptedTool{name: "lookup"}
	responses := make([]ports.ModelResponse, 0, 20)
	for i := 0; i < 18; i++ {
		responses = append(responses, callStep(fmt.Sprintf("s%d", i), "lookup", `{}`))
		tool.replies = append(tool.replies, `{"body":"`+filler+`"}`)
	}
	responses = append(responses, ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, Content: "done"}})
	model := &queuedModel{responses: responses}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}},
		StaticTools{tool}, ContextPolicy{RecentSteps: 2})

	steps := 0
	if _, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "start"}, nil, RunOptions{
		PendingInput: func() []ports.Message {
			steps++
			if steps != 12 {
				return nil
			}
			return []ports.Message{{Role: ports.RoleUser, Content: "stop touching production"}}
		},
	}); err != nil {
		t.Fatal(err)
	}
	live := lastRequest(t, model)
	occurrences := strings.Count(windowText(live), "stop touching production")
	if occurrences != 1 {
		t.Fatalf("steering appears %d times, want exactly 1:\n%s", occurrences, windowText(live))
	}
	for _, message := range live {
		if message.Role == ports.RoleUser && message.Content == "stop touching production" {
			return
		}
	}
	t.Fatalf("the correction survived only as a summary line, not as the owner's message:\n%s", windowText(live))
}

// One oversized tool result must not blow the request budget, and must not
// vanish either: the checkpoint keeps a bounded excerpt that says how much it
// left out.
func TestCompactionBoundsAnOversizedToolResult(t *testing.T) {
	huge := strings.Repeat("y", 200000)
	tool := &scriptedTool{name: "read_file", replies: []string{`{"content":"` + huge + `"}`, `{"content":"small"}`}}
	model := &queuedModel{responses: []ports.ModelResponse{
		callStep("1", "read_file", `{"path":"big.log"}`),
		callStep("2", "read_file", `{"path":"small.log"}`),
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "done"}},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}},
		StaticTools{tool}, ContextPolicy{RequestChars: 60000, ReserveChars: 4000, OutputExcerptChars: 512})

	if _, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "read them"}, nil, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	live := lastRequest(t, model)
	if chars := MessageChars(live); chars > 56000 {
		t.Fatalf("request carried %d characters against a 56000 allowance", chars)
	}
	window := windowText(live)
	if !strings.Contains(window, "characters shown") {
		t.Fatalf("the oversized result was dropped without saying so:\n%s", window)
	}
}

// Input the turn may not compact away is reported rather than sent: a request
// the provider will reject or silently truncate is worse than an error.
func TestCompactionReportsMandatoryInputThatCannotFit(t *testing.T) {
	history := []ports.Message{{Role: ports.RoleUser, Content: strings.Repeat("h", 40000)}}
	model := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{Role: ports.RoleAssistant, Content: "hi"}}}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}},
		StaticTools{&scriptedTool{name: "lookup"}}, ContextPolicy{RequestChars: 20000, ReserveChars: 4000})

	_, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "go"}, history, RunOptions{})
	if !errors.Is(err, ErrContextTooLarge) {
		t.Fatalf("err=%v, want ErrContextTooLarge", err)
	}
	if len(model.requests) != 0 {
		t.Fatalf("an unsendable request was sent anyway: %d calls", len(model.requests))
	}
}

// Attachments count by what the model is billed for them, not by file size:
// an image is one page image, and a PDF is one per page.
func TestMessageCharsCountsAttachmentsByPage(t *testing.T) {
	image := ports.ContentPart{Type: ports.ModalityImage, MediaType: "image/jpeg", Data: make([]byte, 500<<10)}
	message := ports.Message{Role: ports.RoleUser, Content: "look", Parts: []ports.ContentPart{image}}
	if chars := MessageChars([]ports.Message{message}); chars != 4+pageChars {
		t.Fatalf("image chars=%d, want %d", chars, 4+pageChars)
	}
	pdf := ports.ContentPart{Type: ports.ModalityFile, MediaType: "application/pdf", Data: testPDF(3, 500<<10)}
	if chars := partChars(pdf); chars != 3*pageChars {
		t.Fatalf("pdf chars=%d, want %d", chars, 3*pageChars)
	}
	unknown := ports.ContentPart{Type: ports.ModalityAudio, Data: make([]byte, 1024)}
	if chars := partChars(unknown); chars != 1024 {
		t.Fatalf("unestimated part chars=%d, want its size", chars)
	}
}

// PDF 1.5 packs page objects into compressed object streams; pages found
// there count too, and a document with none found is still counted by size.
func TestPDFPagesReadsObjectStreams(t *testing.T) {
	var packed bytes.Buffer
	writer := zlib.NewWriter(&packed)
	writer.Write([]byte("<</Type /Pages /Count 2>> <</Type/Page/Parent 1 0 R>> <</Type /Page>>"))
	writer.Close()
	data := append([]byte("%PDF-1.7\n4 0 obj <</Type /ObjStm /N 3 /Length 99>>\nstream\r\n"), packed.Bytes()...)
	data = append(data, "\nendstream endobj <</Type /Page>>"...)
	if pages := pdfPages(data); pages != 3 {
		t.Fatalf("pages=%d, want 3", pages)
	}
	if pages := pdfPages(make([]byte, 10*assumedPageBytes)); pages != 10 {
		t.Fatalf("unparseable pages=%d, want 10 from size", pages)
	}
	if pages := pdfPages([]byte("%PDF")); pages != 1 {
		t.Fatalf("tiny pages=%d, want at least 1", pages)
	}
}

// A short PDF made large by embedded fonts and images reaches the model: the
// budget refused a 500 KB, three-page document when it counted bytes.
func TestLoopSendsALargeShortPDF(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{Role: ports.RoleAssistant, Content: "read it"}}}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}},
		StaticTools{&scriptedTool{name: "lookup"}}, ContextPolicy{})
	pdf := ports.ContentPart{Type: ports.ModalityFile, MediaType: "application/pdf", Data: testPDF(3, 600<<10)}

	if _, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "summarize", Parts: []ports.ContentPart{pdf}}, nil, RunOptions{}); err != nil {
		t.Fatalf("err=%v, want the PDF sent", err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("requests=%d, want 1", len(model.requests))
	}
}

// testPDF is a PDF with the given page objects, padded to size with the kind
// of binary a real document carries.
func testPDF(pages, size int) []byte {
	data := []byte("%PDF-1.4\n1 0 obj <</Type /Pages /Count " + fmt.Sprint(pages) + ">> endobj\n")
	for i := 0; i < pages; i++ {
		data = append(data, "<</Type /Page /Parent 1 0 R>>\n"...)
	}
	for len(data) < size {
		data = append(data, 0xff)
	}
	return data
}

// The tool catalog is part of the request, so it is part of the budget.
func TestDefinitionCharsCountsTheCatalog(t *testing.T) {
	definitions := []ports.ToolDefinition{{Name: "ab", Description: "cd", Schema: json.RawMessage(`{}`)}}
	if chars := DefinitionChars(definitions); chars != 6 {
		t.Fatalf("chars=%d, want 6", chars)
	}
}

// A full summary drops its oldest entries, never the newest: the checkpoint
// exists to carry what the turn is working from right now.
func TestAppendSummaryKeepsTheNewestEntries(t *testing.T) {
	summary := ""
	for i := 0; i < 200; i++ {
		summary = AppendSummary(summary, fmt.Sprintf("entry %d %s", i, strings.Repeat("z", 500)))
	}
	if !strings.Contains(summary, "entry 199 ") {
		t.Fatal("the newest entry was dropped")
	}
	if strings.Contains(summary, "entry 0 ") {
		t.Fatal("the oldest entry was kept past the limit")
	}
}
