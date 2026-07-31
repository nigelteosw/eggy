package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeSession struct {
	calledName string
	arguments  any
	callResult *sdk.CallToolResult
	callErr    error
	tools      []*sdk.Tool
	pages      map[string]*sdk.ListToolsResult
	listErr    error
	closed     bool
	callCount  int
}

func (s *fakeSession) ListTools(_ context.Context, params *sdk.ListToolsParams) (*sdk.ListToolsResult, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	if s.pages != nil {
		return s.pages[params.Cursor], nil
	}
	return &sdk.ListToolsResult{Tools: s.tools}, nil
}

func (s *fakeSession) CallTool(_ context.Context, params *sdk.CallToolParams) (*sdk.CallToolResult, error) {
	s.callCount++
	s.calledName = params.Name
	s.arguments = params.Arguments
	return s.callResult, s.callErr
}

func (s *fakeSession) Close() error { s.closed = true; return nil }

func staticSession(session clientSession) func() clientSession {
	return func() clientSession { return session }
}

func TestRemoteToolProjectsDefinitionAndCall(t *testing.T) {
	session := &fakeSession{callResult: &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: "two projects"}},
		StructuredContent: map[string]any{"count": 2},
	}}
	tool, err := newRemoteTool("railway", &sdk.Tool{
		Name:        "list-projects",
		Description: "List projects",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false},
	}, staticSession(session), time.Second, 4096)
	if err != nil {
		t.Fatal(err)
	}
	definition := tool.Definition()
	if definition.Name != "railway__list_projects" || definition.Description != "List projects" || !bytes.Contains(definition.Schema, []byte(`"additionalProperties":false`)) {
		t.Fatalf("definition=%#v", definition)
	}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if session.calledName != "list-projects" || !bytes.Contains(out, []byte(`"count":2`)) || !bytes.Contains(out, []byte("two projects")) {
		t.Fatalf("call=%q args=%#v out=%s", session.calledName, session.arguments, out)
	}
}

func TestRemoteToolRejectsNonObjectArguments(t *testing.T) {
	session := &fakeSession{}
	tool, err := newRemoteTool("railway", &sdk.Tool{Name: "list", InputSchema: map[string]any{"type": "object"}}, staticSession(session), time.Second, 4096)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`[]`, `null`, `"text"`} {
		session.calledName = ""
		if _, err := tool.Execute(context.Background(), json.RawMessage(raw)); err == nil {
			t.Fatalf("expected %s arguments to be rejected", raw)
		}
		if session.calledName != "" {
			t.Fatalf("server called for invalid %s arguments", raw)
		}
	}
}

func TestConvertResultOmitsBinaryPayloads(t *testing.T) {
	raw := []byte("do-not-copy-this-binary-payload")
	result, err := convertResult(&sdk.CallToolResult{Content: []sdk.Content{
		&sdk.ImageContent{Data: raw, MIMEType: "image/png"},
		&sdk.AudioContent{Data: raw, MIMEType: "audio/mpeg"},
	}}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(result, raw) || bytes.Contains(result, []byte("ZG8tbm90LWNvcH")) {
		t.Fatalf("binary payload leaked into result: %s", result)
	}
	if !bytes.Contains(result, []byte(`"mime_type":"image/png"`)) || !bytes.Contains(result, []byte(`"size":31`)) {
		t.Fatalf("binary metadata missing: %s", result)
	}
}

func TestConvertResultEnforcesOutputLimit(t *testing.T) {
	_, err := convertResult(&sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "too large"}}}, 8)
	if err != ErrResultTooLarge {
		t.Fatalf("error=%v", err)
	}
}

func TestNormalizeToolName(t *testing.T) {
	if got, err := normalizeToolName("railway", "get-logs"); err != nil || got != "railway__get_logs" {
		t.Fatalf("name=%q err=%v", got, err)
	}
	if _, err := normalizeToolName("railway", "---"); err == nil {
		t.Fatal("expected empty normalized remote name to fail")
	}
	if _, err := normalizeToolName("railway", string(bytes.Repeat([]byte("x"), 128))); err == nil {
		t.Fatal("expected oversized projected name to fail")
	}
	if got, err := normalizeToolName("railway", "café"); err != nil || got != "railway__caf" {
		t.Fatalf("non-ASCII name=%q err=%v", got, err)
	}
}

// TestConvertResultCarriesTheServersErrorText is the diagnosability property:
// an MCP failure arrives as a result with IsError set and the explanation in
// its content, and a model told only "returned an error" has nothing to act on
// but a retry.
func TestConvertResultCarriesTheServersErrorText(t *testing.T) {
	result := &sdk.CallToolResult{
		IsError: true,
		Content: []sdk.Content{
			&sdk.TextContent{Text: "Request had insufficient authentication scopes."},
			&sdk.TextContent{Text: "  "},
		},
	}
	_, err := convertResult(result, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "insufficient authentication scopes") {
		t.Fatalf("err=%v", err)
	}
}

func TestConvertResultErrorFallsBackToStructuredContentThenAPlainMessage(t *testing.T) {
	structured := &sdk.CallToolResult{IsError: true, StructuredContent: map[string]any{"code": 403}}
	_, err := convertResult(structured, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err=%v", err)
	}

	silent := &sdk.CallToolResult{IsError: true}
	_, err = convertResult(silent, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "no message") {
		t.Fatalf("err=%v", err)
	}
}

// A server that answers a failure with a wall of text must not spend the
// turn's context on it.
func TestConvertResultErrorTextIsBounded(t *testing.T) {
	result := &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: strings.Repeat("x", 10_000)}}}
	_, err := convertResult(result, 1<<20)
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(err.Error()) > maxErrorTextBytes+128 {
		t.Fatalf("error length=%d", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("err=%v", err)
	}
}
