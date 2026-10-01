// pi: test/session-manager/file-operations.test.ts "SessionManager session
// file creation" describe (#10000) — a session file is created on the first
// user message, not the first assistant response.
package core

import (
	"os"
	"strings"
	"testing"
)

// readSessionFileRoles reads the JSONL type of every line (pi:
// readSessionFileRoles in test/utilities.ts).
func readSessionFileRoles(t *testing.T, file string) []string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var roles []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		roles = append(roles, jsonLineType(t, line))
	}
	return roles
}

func jsonLineType(t *testing.T, line string) string {
	t.Helper()
	const typeKey = `"type":"`
	idx := strings.Index(line, typeKey)
	if idx == -1 {
		t.Fatalf("line has no type field: %s", line)
	}
	rest := line[idx+len(typeKey):]
	end := strings.Index(rest, `"`)
	if end == -1 {
		t.Fatalf("unterminated type field: %s", line)
	}
	return rest[:end]
}

func TestSessionFileCreatedAtFirstUserMessage(t *testing.T) {
	tempDir := newTempDir(t)

	t.Run("does not create a file for a session with only setup entries", func(t *testing.T) {
		session, err := CreateSessionManager(tempDir, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendModelChange("anthropic", "claude-sonnet-4-5"); err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendThinkingLevelChange("off"); err != nil {
			t.Fatal(err)
		}

		if _, err := os.Stat(session.GetSessionFile()); !os.IsNotExist(err) {
			t.Fatalf("expected no session file, got err=%v", err)
		}
	})

	// #10000: the first prompt must survive a first turn that never produces
	// an assistant message.
	t.Run("creates the file when the first user message is appended", func(t *testing.T) {
		session, err := CreateSessionManager(tempDir, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendModelChange("anthropic", "claude-sonnet-4-5"); err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendMessage(userMsg("first question")); err != nil {
			t.Fatal(err)
		}

		file := session.GetSessionFile()
		roles := readSessionFileRoles(t, file)
		if len(roles) != 3 || roles[0] != "session" || roles[1] != "model_change" || roles[2] != "message" {
			t.Fatalf("unexpected roles: %v", roles)
		}
		// The user message must be on disk before any assistant response.
		if !strings.Contains(readFileT(t, file), "first question") {
			t.Fatalf("user message not persisted to %s", file)
		}
		reopened, err := OpenSession(file, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		messages := reopened.BuildSessionContext().Messages
		if len(messages) != 1 {
			t.Fatalf("expected 1 message after reopen, got %d", len(messages))
		}
	})

	t.Run("appends later entries to the file without rewriting earlier ones", func(t *testing.T) {
		session, err := CreateSessionManager(tempDir, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendMessage(userMsg("first question")); err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendCustomEntry("preset-state", map[string]any{"name": "plan"}); err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendMessage(assistantMsg("first answer")); err != nil {
			t.Fatal(err)
		}

		roles := readSessionFileRoles(t, session.GetSessionFile())
		if len(roles) != 4 || roles[0] != "session" || roles[1] != "message" || roles[2] != "custom" || roles[3] != "message" {
			t.Fatalf("unexpected roles: %v", roles)
		}
	})
}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
