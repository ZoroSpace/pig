package core

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

// Ports pi v1.0.4's --tools/--exclude-tools pattern semantics
// (agent-session-mcp.test.ts + 5109-exclude-tools regression): entries may
// be exact names or `*` patterns, matching a tool activates it, and an
// explicitly empty allowlist still exposes nothing.
func TestToolNamePatternsFilterAndActivate(t *testing.T) {
	createFileSession := func(t *testing.T, opts *CreateAgentSessionOptions) *AgentSession {
		t.Helper()
		tempDir := t.TempDir()
		agentDir := filepath.Join(tempDir, "agent")
		sessionManager, err := InMemorySession(tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		opts.Cwd = tempDir
		opts.AgentDir = agentDir
		opts.SessionManager = sessionManager
		opts.SettingsManager = CreateSettingsManager(tempDir, agentDir, nil)
		result, err := CreateAgentSession(context.Background(), opts)
		if err != nil {
			t.Fatal(err)
		}
		return result.Session
	}

	t.Run("allowlist pattern activates matching tools only", func(t *testing.T) {
		session := createFileSession(t, &CreateAgentSessionOptions{Tools: []string{"rea*", "writ*"}})
		defer session.Dispose()
		got := session.GetActiveToolNames()
		slices.Sort(got)
		if want := []string{"read", "write"}; !slices.Equal(got, want) {
			t.Fatalf("active tools = %v, want %v", got, want)
		}
	})

	t.Run("exclude pattern removes matching tools", func(t *testing.T) {
		session := createFileSession(t, &CreateAgentSessionOptions{ExcludeTools: []string{"ba*sh", "*it"}})
		defer session.Dispose()
		got := session.GetActiveToolNames()
		slices.Sort(got)
		if want := []string{"read", "write"}; !slices.Equal(got, want) {
			t.Fatalf("active tools = %v, want %v", got, want)
		}
	})

	t.Run("empty allowlist exposes nothing", func(t *testing.T) {
		session := createFileSession(t, &CreateAgentSessionOptions{Tools: []string{}})
		defer session.Dispose()
		if got := session.GetActiveToolNames(); len(got) != 0 {
			t.Fatalf("active tools = %v, want none", got)
		}
	})
}
