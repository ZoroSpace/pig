package mcp

import (
	"os"
	"strings"
	"testing"
)

func TestParseConfigValid(t *testing.T) {
	config, diags := ParseConfigBytes("test.json", []byte(`{
		"mcpServers": {
			"fs": {"command": "npx", "args": ["-y", "fs-mcp"], "lifecycle": "lazy"},
			"web": {"url": "https://example.com/mcp", "headers": {"Authorization": "Bearer x"}, "toolPrefix": "web_"}
		}
	}`))
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if len(config.MCPServers) != 2 {
		t.Fatalf("want 2 servers, got %d", len(config.MCPServers))
	}
	if config.MCPServers["fs"].Args[0] != "-y" {
		t.Fatalf("args not preserved: %+v", config.MCPServers["fs"])
	}
	if config.MCPServers["web"].Headers["Authorization"] != "Bearer x" {
		t.Fatalf("headers not preserved: %+v", config.MCPServers["web"])
	}
}

func TestParseConfigEmptyServers(t *testing.T) {
	config, diags := ParseConfigBytes("test.json", []byte(`{}`))
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if !config.IsEmpty() {
		t.Fatalf("want empty config, got %+v", config)
	}
}

func TestParseConfigInvalidJSON(t *testing.T) {
	config, diags := ParseConfigBytes("test.json", []byte(`{"mcpServers":`))
	if config != nil {
		t.Fatalf("want nil config for invalid JSON")
	}
	if len(diags) != 1 || diags[0].Severity != "error" || !strings.Contains(diags[0].Message, "invalid JSON") {
		t.Fatalf("want single error diagnostic, got %+v", diags)
	}
}

func TestParseConfigValidationErrors(t *testing.T) {
	config, diags := ParseConfigBytes("test.json", []byte(`{
		"mcpServers": {
			"both": {"command": "x", "url": "https://x"},
			"neither": {"args": ["y"]},
			"badLifecycle": {"command": "x", "lifecycle": "zombie"},
			"good": {"command": "x"}
		}
	}`))
	if len(diags) != 3 {
		t.Fatalf("want 3 error diagnostics, got %+v", diags)
	}
	for _, diag := range diags {
		if diag.Severity != "error" || diag.Path != "test.json" {
			t.Fatalf("bad diagnostic: %+v", diag)
		}
	}
	if len(config.MCPServers) != 1 {
		t.Fatalf("invalid servers must be dropped, got %+v", config.MCPServers)
	}
	if _, ok := config.MCPServers["good"]; !ok {
		t.Fatalf("valid server must survive, got %+v", config.MCPServers)
	}
}

func TestLoadConfigFilesLaterWins(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, content string) string {
		path := dir + "/" + name
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	projectRoot := writeFile("root.json", `{"mcpServers": {
		"fs": {"command": "old"},
		"web": {"url": "https://old.example.com/mcp"}
	}}`)
	owner := writeFile("owner.json", `{"mcpServers": {
		"fs": {"command": "new", "args": ["a"]},
		"extra": {"command": "extra"}
	}}`)

	config, diags, err := LoadConfigFiles([]string{projectRoot, owner})
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if config.MCPServers["fs"].Command != "new" || len(config.MCPServers["fs"].Args) != 1 {
		t.Fatalf("later file must win whole-entry, got %+v", config.MCPServers["fs"])
	}
	if config.MCPServers["web"].URL != "https://old.example.com/mcp" {
		t.Fatalf("unrelated server must survive merge, got %+v", config.MCPServers["web"])
	}
	if _, ok := config.MCPServers["extra"]; !ok {
		t.Fatalf("extra server missing: %+v", config.MCPServers)
	}
}

func TestLoadConfigFilesMissingFileOK(t *testing.T) {
	config, diags, err := LoadConfigFiles([]string{"/nonexistent/mcp.json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 0 || !config.IsEmpty() {
		t.Fatalf("missing file must be a silent empty config, got diags=%+v config=%+v", diags, config)
	}
}

func TestSortedServerNames(t *testing.T) {
	config, _ := ParseConfigBytes("t", []byte(`{"mcpServers": {"zebra": {"command": "a"}, "alpha": {"command": "b"}}}`))
	names := config.SortedServerNames()
	if len(names) != 2 || names[0] != "alpha" || names[1] != "zebra" {
		t.Fatalf("want sorted names, got %v", names)
	}
}
