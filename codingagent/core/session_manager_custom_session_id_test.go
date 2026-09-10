// pi: test/session-manager/custom-session-id.test.ts — custom session ids,
// UUIDv7 generation, fork headers.
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
)

var uuidV7Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

var sessionFileNameRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-\d{3}Z_(.+)\.jsonl$`)

func TestSessionManagerNewSessionWithCustomID(t *testing.T) {
	t.Run("uses the provided id instead of generating one", func(t *testing.T) {
		session, _ := InMemorySession("", nil)
		if _, err := session.NewSession(&NewSessionOptions{ID: lo.ToPtr("my-custom-id")}); err != nil {
			t.Fatal(err)
		}
		if session.GetSessionID() != "my-custom-id" {
			t.Fatalf("id = %q", session.GetSessionID())
		}
	})

	t.Run("uses the provided id when creating an in-memory session", func(t *testing.T) {
		session, err := InMemorySession("", &NewSessionOptions{ID: lo.ToPtr("memory-session-id")})
		if err != nil {
			t.Fatal(err)
		}
		if session.GetSessionID() != "memory-session-id" {
			t.Fatalf("id = %q", session.GetSessionID())
		}
		if session.GetHeader().ID != "memory-session-id" {
			t.Fatalf("header id = %q", session.GetHeader().ID)
		}
		if session.GetSessionFile() != "" {
			t.Fatalf("session file = %q", session.GetSessionFile())
		}
	})

	t.Run("allows alphanumeric session ids with interior punctuation", func(t *testing.T) {
		session, _ := InMemorySession("", nil)
		if _, err := session.NewSession(&NewSessionOptions{ID: lo.ToPtr("abc-123_def.456")}); err != nil {
			t.Fatal(err)
		}
		if session.GetSessionID() != "abc-123_def.456" {
			t.Fatalf("id = %q", session.GetSessionID())
		}
	})

	t.Run("rejects invalid custom session ids", func(t *testing.T) {
		invalidIDs := []string{"", "-abc", "abc-", "_abc", "abc_", ".abc", "abc.", "abc/def", `abc\def`, "abc def"}

		for _, id := range invalidIDs {
			session, _ := InMemorySession("", nil)
			_, err := session.NewSession(&NewSessionOptions{ID: lo.ToPtr(id)})
			if err == nil || !strings.Contains(err.Error(), "Session id must be non-empty, contain only alphanumeric characters") {
				t.Fatalf("id %q: err = %v", id, err)
			}
		}
	})

	t.Run("generates a UUIDv7 id when no id is provided", func(t *testing.T) {
		session, _ := InMemorySession("", nil)
		if _, err := session.NewSession(nil); err != nil {
			t.Fatal(err)
		}
		id := session.GetSessionID()
		if id == "" || !uuidV7Re.MatchString(id) {
			t.Fatalf("id = %q", id)
		}
	})

	t.Run("generates a UUIDv7 id when options is provided without id", func(t *testing.T) {
		session, _ := InMemorySession("", nil)
		if _, err := session.NewSession(&NewSessionOptions{ParentSession: lo.ToPtr("parent.jsonl")}); err != nil {
			t.Fatal(err)
		}
		id := session.GetSessionID()
		if id == "" || !uuidV7Re.MatchString(id) {
			t.Fatalf("id = %q", id)
		}
	})

	t.Run("includes the custom id in the session header", func(t *testing.T) {
		session, _ := InMemorySession("", nil)
		if _, err := session.NewSession(&NewSessionOptions{ID: lo.ToPtr("header-test-id")}); err != nil {
			t.Fatal(err)
		}

		header := session.GetHeader()
		if header == nil || header.ID != "header-test-id" {
			t.Fatalf("header = %#v", header)
		}
	})

	t.Run("generates a UUIDv7 id when constructed without an explicit id", func(t *testing.T) {
		session, _ := InMemorySession("", nil)
		id := session.GetSessionID()
		if id == "" || !uuidV7Re.MatchString(id) {
			t.Fatalf("id = %q", id)
		}
		if session.GetHeader().ID != session.GetSessionID() {
			t.Fatal("header id mismatch")
		}
	})

	t.Run("uses the provided id when creating a persisted session", func(t *testing.T) {
		tempDir := newTempDir(t)
		session, err := CreateSessionManager(tempDir, tempDir, &NewSessionOptions{ID: lo.ToPtr("created-session-id")})
		if err != nil {
			t.Fatal(err)
		}

		if session.GetSessionID() != "created-session-id" {
			t.Fatalf("id = %q", session.GetSessionID())
		}
		if session.GetHeader().ID != "created-session-id" {
			t.Fatalf("header id = %q", session.GetHeader().ID)
		}
		sessionFile := session.GetSessionFile()
		if sessionFile == "" || !strings.Contains(sessionFile, "created-session-id") {
			t.Fatalf("session file = %q", sessionFile)
		}
		base := filepath.Base(sessionFile)
		m := sessionFileNameRe.FindStringSubmatch(base)
		if m == nil || m[1] != "created-session-id" {
			t.Fatalf("file name = %q", base)
		}
		if _, err := os.Stat(sessionFile); !os.IsNotExist(err) {
			t.Fatalf("expected file to not exist yet: %v", err)
		}
	})

	t.Run("generates a UUIDv7 id when creating a branched session", func(t *testing.T) {
		session, _ := InMemorySession("", nil)
		firstID, err2 := session.AppendMessage(types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{Parts: []types.Content{types.NewTextContent("hello")}},
			Timestamp: timeNowMillis(),
		})
		mustAppend(t, firstID, err2)

		if _, err := session.CreateBranchedSession(firstID); err != nil {
			t.Fatal(err)
		}

		id := session.GetSessionID()
		if !uuidV7Re.MatchString(id) {
			t.Fatalf("id = %q", id)
		}
		if session.GetHeader().ID != session.GetSessionID() {
			t.Fatal("header id mismatch")
		}
	})

	t.Run("generates a UUIDv7 id when forking from another session file", func(t *testing.T) {
		tempDir := newTempDir(t)
		sourcePath := filepath.Join(tempDir, "source.jsonl")
		content := mustJSONLines(
			map[string]any{
				"type": "session", "version": 3, "id": "legacy-session-id",
				"timestamp": sessionTimestamp(), "cwd": tempDir,
			},
			map[string]any{
				"type": "message", "id": "entry-1", "parentId": nil,
				"timestamp": sessionTimestamp(),
				"message": map[string]any{
					"role":    "assistant",
					"content": []any{map[string]any{"type": "text", "text": "hello"}},
					"api":     "openai-responses", "provider": "openai", "model": "gpt-5.4",
					"usage": map[string]any{
						"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0,
						"totalTokens": 0,
						"cost":        map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0},
					},
					"stopReason": "stop",
					"timestamp":  timeNowMillis(),
				},
			},
		)
		if err := os.WriteFile(sourcePath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		forked, err := ForkSession(sourcePath, tempDir, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		header := forked.GetHeader()
		if header == nil || !uuidV7Re.MatchString(header.ID) {
			t.Fatalf("header = %#v", header)
		}
		if *header.ParentSession != sourcePath {
			t.Fatalf("parentSession = %q", *header.ParentSession)
		}
	})

	t.Run("uses the provided id when forking from another session file", func(t *testing.T) {
		tempDir := newTempDir(t)
		sourcePath := filepath.Join(tempDir, "source.jsonl")
		content := mustJSONLines(map[string]any{
			"type": "session", "version": 3, "id": "source-session-id",
			"timestamp": sessionTimestamp(), "cwd": tempDir,
		})
		if err := os.WriteFile(sourcePath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		forked, err := ForkSession(sourcePath, tempDir, tempDir, &NewSessionOptions{ID: lo.ToPtr("forked-session-id")})
		if err != nil {
			t.Fatal(err)
		}
		header := forked.GetHeader()
		if header == nil || header.ID != "forked-session-id" {
			t.Fatalf("header = %#v", header)
		}
		if *header.ParentSession != sourcePath {
			t.Fatalf("parentSession = %q", *header.ParentSession)
		}
		sessionFile := forked.GetSessionFile()
		if sessionFile == "" || !strings.Contains(sessionFile, "forked-session-id") {
			t.Fatalf("session file = %q", sessionFile)
		}
		base := filepath.Base(sessionFile)
		m := sessionFileNameRe.FindStringSubmatch(base)
		if m == nil || m[1] != "forked-session-id" {
			t.Fatalf("file name = %q", base)
		}
	})
}

// mustJSONLines renders values as newline-delimited JSON (pi: the JS
// template-literal array joins in the custom-session-id tests).
func mustJSONLines(values ...map[string]any) string {
	lines := make([]string, 0, len(values))
	for _, v := range values {
		data, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}
		lines = append(lines, string(data))
	}
	return strings.Join(lines, "\n") + "\n"
}
