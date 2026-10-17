package config

import (
	"slices"
	"testing"
)

func TestAddMCPServerDisabled(t *testing.T) {
	path := marketplaceConfigPath(t, "")
	if err := AddMCPServerDisabled(path, "deploy-tools-api", "https://mcp.example.com/mcp"); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	server, ok := cfg.MCP.Servers["deploy-tools-api"]
	if !ok {
		t.Fatal("server not written")
	}
	if server.Enabled || server.Auth != "oauth" || server.Transport != "streamable-http" || server.URL != "https://mcp.example.com/mcp" {
		t.Fatalf("server = %+v, want disabled oauth streamable-http", server)
	}
	if err := AddMCPServerDisabled(path, "deploy-tools-api", "https://other.example.com/mcp"); err == nil {
		t.Fatal("re-added an existing server name")
	}
}

func TestSetMCPServerApproval(t *testing.T) {
	path := marketplaceConfigPath(t, "")
	if err := AddMCPServerDisabled(path, "deploy-tools-api", "https://mcp.example.com/mcp"); err != nil {
		t.Fatal(err)
	}
	if err := SetMCPServerApproval(path, "deploy-tools-api", true, []string{"search"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	server := cfg.MCP.Servers["deploy-tools-api"]
	if !server.Enabled || !slices.Equal(server.RequireApproval, []string{"search"}) {
		t.Fatalf("server = %+v, want enabled with require_approval [search]", server)
	}
	if err := SetMCPServerApproval(path, "missing", true, nil); err == nil {
		t.Fatal("set approval on an unconfigured server")
	}
}
