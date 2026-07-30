package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

type stubWorkspaces struct {
	binding WorkspaceBinding
	err     error
}

func (s stubWorkspaces) Resolve(context.Context) (WorkspaceBinding, error) {
	return s.binding, s.err
}

func primitivesByName(tools []ports.Tool) map[string]ports.Tool {
	byName := map[string]ports.Tool{}
	for _, tool := range tools {
		byName[tool.Definition().Name] = tool
	}
	return byName
}

func TestReadFileRequiresAnAttachedWorkspace(t *testing.T) {
	byName := primitivesByName(NewPrimitiveTools(stubWorkspaces{err: ErrNoWorkspace}, &fakeRepositoryReader{content: "line one\n"}))
	if _, err := byName["read_file"].Execute(context.Background(), json.RawMessage(`{"path":"main.go"}`)); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("err=%v, want ErrNoWorkspace", err)
	}
}

func TestReadFileTakesNoRepositoryArgument(t *testing.T) {
	byName := primitivesByName(NewPrimitiveTools(stubWorkspaces{binding: WorkspaceBinding{Path: t.TempDir()}}, &fakeRepositoryReader{}))
	schema := string(byName["read_file"].Definition().Schema)
	if strings.Contains(schema, `"repository"`) {
		t.Fatalf("read_file resolves its workspace from thread state: %s", schema)
	}
}

func TestReadFileReadsFromTheResolvedWorkspace(t *testing.T) {
	byName := primitivesByName(NewPrimitiveTools(stubWorkspaces{binding: WorkspaceBinding{Path: "/tmp/run-1"}}, &fakeRepositoryReader{content: "line one\n"}))
	result, err := byName["read_file"].Execute(context.Background(), json.RawMessage(`{"path":"main.go"}`))
	if err != nil || !strings.Contains(string(result), "line one") {
		t.Fatalf("result=%s err=%v", result, err)
	}
}
