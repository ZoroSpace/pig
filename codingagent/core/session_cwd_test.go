// pi: test/session-cwd.test.ts — stored session cwd validation. The third
// pi test (createAgentSessionRuntime interaction) is deferred with the
// agent-session-runtime batch; pig ports the two SessionManager-level cases.
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sessionCwdTempDir(t *testing.T, name string) string {
	t.Helper()
	return t.TempDir()
}

func writeSessionFile(t *testing.T, path string, cwd string) {
	t.Helper()
	header := map[string]any{
		"type":      "session",
		"version":   3,
		"id":        "session-id",
		"timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		"cwd":       cwd,
	}
	content, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSessionCwdHandling(t *testing.T) {
	t.Run("detects missing session cwd from persisted sessions", func(t *testing.T) {
		fallbackCwd := sessionCwdTempDir(t, "fallback")
		missingCwd := filepath.Join(fallbackCwd, "does-not-exist")
		sessionDir := sessionCwdTempDir(t, "session-dir")
		sessionFile := filepath.Join(sessionDir, "session.jsonl")
		writeSessionFile(t, sessionFile, missingCwd)

		sessionManager, err := OpenSession(sessionFile, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		issue := GetMissingSessionCwdIssue(sessionManager, fallbackCwd)
		if issue == nil {
			t.Fatal("expected issue")
		}
		if issue.SessionFile != sessionManager.GetSessionFile() || issue.SessionCwd != missingCwd || issue.FallbackCwd != fallbackCwd {
			t.Fatalf("issue = %+v", issue)
		}
	})

	t.Run("supports overriding the effective cwd when opening a session", func(t *testing.T) {
		fallbackCwd := sessionCwdTempDir(t, "fallback")
		missingCwd := filepath.Join(fallbackCwd, "does-not-exist")
		sessionDir := sessionCwdTempDir(t, "session-dir")
		sessionFile := filepath.Join(sessionDir, "session.jsonl")
		writeSessionFile(t, sessionFile, missingCwd)

		sessionManager, err := OpenSession(sessionFile, "", &fallbackCwd)
		if err != nil {
			t.Fatal(err)
		}
		if sessionManager.GetCwd() != fallbackCwd {
			t.Fatalf("cwd = %q", sessionManager.GetCwd())
		}
		if issue := GetMissingSessionCwdIssue(sessionManager, fallbackCwd); issue != nil {
			t.Fatalf("unexpected issue: %+v", issue)
		}
	})
}
