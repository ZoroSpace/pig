// Ported from pi's test/sdk-session-manager.test.ts, adapted to pig's builtin
// provider set: pig ships the completions-family subset only (Phase 2
// decision), so the anthropic model literal becomes an openrouter model.
package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

func sdkSessionTestDirs(t *testing.T) (tempDir, cwd, agentDir string) {
	t.Helper()
	tempDir = t.TempDir()
	cwd = filepath.Join(tempDir, "project")
	agentDir = filepath.Join(tempDir, "agent")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return tempDir, cwd, agentDir
}

func sdkSessionTestModel() *types.Model {
	return &types.Model{
		ID:            "claude-sonnet-4-5",
		Name:          "Claude Sonnet 4.5",
		API:           ai.ApiOpenAICompletions,
		Provider:      "openrouter",
		BaseURL:       "http://localhost:0",
		Reasoning:     false,
		ContextWindow: 200000,
		MaxTokens:     64000,
		Input:         []ai.ModelInput{ai.ModelInputText},
	}
}

func TestCreateAgentSessionSessionManagerDefaults(t *testing.T) {
	t.Run("uses agentDir for the default persisted session path", func(t *testing.T) {
		_, cwd, agentDir := sdkSessionTestDirs(t)
		model := sdkSessionTestModel()

		result, err := CreateAgentSession(context.Background(), &CreateAgentSessionOptions{
			Cwd:      cwd,
			AgentDir: agentDir,
			Model:    model,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer result.Session.Dispose()

		safePath := "--" + strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(strings.TrimLeft(cwd, "/\\"), ""), ":", "-"), string(filepath.Separator), "-") + "--"
		if !strings.HasPrefix(safePath, "--") || safePath == "----" {
			// The replacement above mirrors pi's regex chain; verify it is
			// non-trivial for this cwd.
			t.Fatalf("safePath derivation collapsed: %q from %q", safePath, cwd)
		}
		expectedSessionDir := filepath.Join(agentDir, "sessions", "--"+strings.NewReplacer("/", "-", ":", "-").Replace(strings.TrimLeft(strings.ReplaceAll(cwd, "\\", "/"), "/"))+"--")
		sessionDir := result.Session.SessionManager.GetSessionDir()
		sessionFile := result.Session.SessionManager.GetSessionFile()

		// pi asserts exact equality; pig normalizes the expected path through
		// GetDefaultSessionDir's own derivation to avoid duplicating its
		// separator handling, and additionally checks the prefix shape.
		if sessionDir != expectedSessionDir {
			t.Fatalf("session dir = %q, want %q", sessionDir, expectedSessionDir)
		}
		prefix := expectedSessionDir + string(filepath.Separator)
		if sessionFile == "" || !strings.HasPrefix(sessionFile, prefix) {
			t.Fatalf("session file = %q, want prefix %q", sessionFile, prefix)
		}
		_ = safePath
		_ = agentDir
	})

	t.Run("keeps an explicit sessionManager override", func(t *testing.T) {
		_, cwd, agentDir := sdkSessionTestDirs(t)
		model := sdkSessionTestModel()

		sessionManager, err := InMemorySession(cwd, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, err := CreateAgentSession(context.Background(), &CreateAgentSessionOptions{
			Cwd:            cwd,
			AgentDir:       agentDir,
			Model:          model,
			SessionManager: sessionManager,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer result.Session.Dispose()

		if result.Session.SessionManager != sessionManager {
			t.Fatal("expected the explicit session manager to be used")
		}
		if result.Session.SessionManager.IsPersisted() {
			t.Fatal("expected in-memory session to not be persisted")
		}
	})

	t.Run("derives cwd from an explicit sessionManager when cwd is omitted", func(t *testing.T) {
		tempDir, _, agentDir := sdkSessionTestDirs(t)
		model := sdkSessionTestModel()

		sessionCwd := filepath.Join(tempDir, "session-project")
		if err := os.MkdirAll(sessionCwd, 0o755); err != nil {
			t.Fatal(err)
		}
		sessionManager, err := InMemorySession(sessionCwd, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, err := CreateAgentSession(context.Background(), &CreateAgentSessionOptions{
			AgentDir:       agentDir,
			Model:          model,
			SessionManager: sessionManager,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer result.Session.Dispose()

		if result.Session.SessionManager != sessionManager {
			t.Fatal("expected the explicit session manager to be used")
		}
		if got := result.Session.SystemPrompt(); !strings.Contains(got, "<cwd>\n"+sessionCwd+"\n</cwd>") {
			t.Fatalf("system prompt missing cwd line:\n%s", got)
		}

		var bashTool *agent.AgentTool
		for i, tool := range result.Session.Agent.State().Tools {
			if tool.Name == "bash" {
				bashTool = &result.Session.Agent.State().Tools[i]
				break
			}
		}
		if bashTool == nil {
			t.Fatal("bash tool not found")
		}
		result2, err := bashTool.Execute(context.Background(), "test", map[string]any{"command": "pwd"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
		for _, item := range result2.Content {
			if text, ok := item.(*types.TextContent); ok {
				output.WriteString(text.Text)
			}
		}
		trimmed := strings.TrimSpace(output.String())
		resolved, err := filepath.EvalSymlinks(trimmed)
		if err != nil {
			t.Fatal(err)
		}
		want, err := filepath.EvalSymlinks(sessionCwd)
		if err != nil {
			t.Fatal(err)
		}
		if resolved != want {
			t.Fatalf("bash pwd = %q, want %q", resolved, want)
		}
	})

	t.Run("exposes current session state to the built-in bash tool", func(t *testing.T) {
		_, cwd, agentDir := sdkSessionTestDirs(t)
		model := sdkSessionTestModel()

		result, err := CreateAgentSession(context.Background(), &CreateAgentSessionOptions{
			Cwd:           cwd,
			AgentDir:      agentDir,
			Model:         model,
			ThinkingLevel: "high",
		})
		if err != nil {
			t.Fatal(err)
		}
		defer result.Session.Dispose()

		if result.Session.SessionFile() == "" {
			t.Fatal("expected a session file")
		}
		if got := result.Session.SystemPrompt(); !strings.Contains(got, "You can inspect PI_* environment variables for current model and session details.") {
			t.Fatalf("system prompt missing PI_* line:\n%s", got)
		}

		var bashTool *agent.AgentTool
		for i, tool := range result.Session.Agent.State().Tools {
			if tool.Name == "bash" {
				bashTool = &result.Session.Agent.State().Tools[i]
				break
			}
		}
		if bashTool == nil {
			t.Fatal("bash tool not found")
		}
		toolResult, err := bashTool.Execute(context.Background(), "test", map[string]any{
			"command": `printf '%s\n' "$PI_SESSION_ID" "$PI_SESSION_FILE" "$PI_PROVIDER" "$PI_MODEL" "$PI_REASONING_LEVEL"`,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
		for _, item := range toolResult.Content {
			if text, ok := item.(*types.TextContent); ok {
				output.WriteString(text.Text)
			}
		}
		lines := strings.Split(strings.TrimSpace(output.String()), "\n")
		want := []string{
			result.Session.SessionID(),
			result.Session.SessionFile(),
			string(model.Provider),
			model.ID,
			string(result.Session.ThinkingLevel()),
		}
		if len(lines) != len(want) {
			t.Fatalf("env output lines = %v, want %v", lines, want)
		}
		for i := range want {
			if lines[i] != want[i] {
				t.Fatalf("line %d = %q, want %q", i, lines[i], want[i])
			}
		}
	})
}
