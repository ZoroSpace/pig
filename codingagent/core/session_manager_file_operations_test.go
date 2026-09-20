// pi: test/session-manager/file-operations.test.ts — loadEntriesFromFile,
// header scan bounds, findMostRecentSession, flat dir listing, corrupted
// setSessionFile recovery.
package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent"
)

const headerScanLimitBytes = 1024 * 1024

func writeSessionHeader(t *testing.T, file string, cwd string, id string, prefix string) {
	t.Helper()
	header, err := mustJSONLine(map[string]any{
		"type": "session", "version": 3, "id": id,
		"timestamp": "2025-01-01T00:00:00Z", "cwd": cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(prefix+header+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEntriesFromFile(t *testing.T) {
	tempDir := newTempDir(t)

	t.Run("adds a newline after an unterminated valid record", func(t *testing.T) {
		file := filepath.Join(tempDir, "unterminated.jsonl")
		header, err0 := mustJSONLine(map[string]any{
			"type": "session", "id": "abc", "timestamp": "2025-01-01T00:00:00Z", "cwd": "/tmp",
		})
		if err0 != nil {
			t.Fatal(err0)
		}
		message := `{"type":"message","id":"1","parentId":null,"timestamp":"2025-01-01T00:00:01Z","message":{"role":"user","content":"hi","timestamp":1}}`
		content := header + "\n" + message
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		if entries := LoadEntriesFromFile(file); len(entries) != 2 {
			t.Fatalf("entries = %d", len(entries))
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != content+"\n" {
			t.Fatalf("file was not repaired: %q", string(data))
		}
	})

	t.Run("adds a newline after an unterminated malformed final fragment", func(t *testing.T) {
		file := filepath.Join(tempDir, "malformed-tail.jsonl")
		header, err0 := mustJSONLine(map[string]any{
			"type": "session", "id": "abc", "timestamp": "2025-01-01T00:00:00Z", "cwd": "/tmp",
		})
		if err0 != nil {
			t.Fatal(err0)
		}
		content := header + "\n" + `{"type":"message"`
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		if entries := LoadEntriesFromFile(file); len(entries) != 1 {
			t.Fatalf("entries = %d", len(entries))
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != content+"\n" {
			t.Fatalf("file was not repaired: %q", string(data))
		}
	})

	t.Run("does not modify an unterminated non-session file", func(t *testing.T) {
		file := filepath.Join(tempDir, "invalid.jsonl")
		content := `{"type":"message","id":"1"}`
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		if entries := LoadEntriesFromFile(file); len(entries) != 0 {
			t.Fatalf("entries = %d", len(entries))
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != content {
			t.Fatalf("file was modified: %q", string(data))
		}
	})

	t.Run("returns empty array for non-existent file", func(t *testing.T) {
		entries := LoadEntriesFromFile(filepath.Join(tempDir, "nonexistent.jsonl"))
		if len(entries) != 0 {
			t.Fatalf("entries = %d", len(entries))
		}
	})

	t.Run("returns empty array for empty file", func(t *testing.T) {
		file := filepath.Join(tempDir, "empty.jsonl")
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if entries := LoadEntriesFromFile(file); len(entries) != 0 {
			t.Fatalf("entries = %d", len(entries))
		}
	})

	t.Run("returns empty array for file without valid session header", func(t *testing.T) {
		file := filepath.Join(tempDir, "no-header.jsonl")
		if err := os.WriteFile(file, []byte(`{"type":"message","id":"1"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if entries := LoadEntriesFromFile(file); len(entries) != 0 {
			t.Fatalf("entries = %d", len(entries))
		}
	})

	t.Run("returns empty array for malformed JSON", func(t *testing.T) {
		file := filepath.Join(tempDir, "malformed.jsonl")
		if err := os.WriteFile(file, []byte("not json\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if entries := LoadEntriesFromFile(file); len(entries) != 0 {
			t.Fatalf("entries = %d", len(entries))
		}
	})

	t.Run("loads valid session file", func(t *testing.T) {
		file := filepath.Join(tempDir, "valid.jsonl")
		content := `{"type":"session","id":"abc","timestamp":"2025-01-01T00:00:00Z","cwd":"/tmp"}` + "\n" +
			`{"type":"message","id":"1","parentId":null,"timestamp":"2025-01-01T00:00:01Z","message":{"role":"user","content":"hi","timestamp":1}}` + "\n"
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		entries := LoadEntriesFromFile(file)
		if len(entries) != 2 {
			t.Fatalf("entries = %d", len(entries))
		}
		if entries[0].EntryType() != "session" || entries[1].EntryType() != "message" {
			t.Fatalf("types = %q %q", entries[0].EntryType(), entries[1].EntryType())
		}
	})

	t.Run("skips malformed lines but keeps valid ones", func(t *testing.T) {
		file := filepath.Join(tempDir, "mixed.jsonl")
		content := `{"type":"session","id":"abc","timestamp":"2025-01-01T00:00:00Z","cwd":"/tmp"}` + "\n" +
			"not valid json\n" +
			`{"type":"message","id":"1","parentId":null,"timestamp":"2025-01-01T00:00:01Z","message":{"role":"user","content":"hi","timestamp":1}}` + "\n"
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if entries := LoadEntriesFromFile(file); len(entries) != 2 {
			t.Fatalf("entries = %d", len(entries))
		}
	})

	t.Run("reads cwd from a session with leading blank lines", func(t *testing.T) {
		file := filepath.Join(tempDir, "leading-blank.jsonl")
		storedCwd := filepath.Join(tempDir, "stored-project")
		writeSessionHeader(t, file, storedCwd, "leading-blank", "\n  \n")
		sessionManager, err := OpenSession(file, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if sessionManager.GetSessionID() != "leading-blank" || sessionManager.GetCwd() != storedCwd {
			t.Fatalf("id = %q cwd = %q", sessionManager.GetSessionID(), sessionManager.GetCwd())
		}
	})

	t.Run("reads cwd from a session with leading malformed lines", func(t *testing.T) {
		file := filepath.Join(tempDir, "leading-malformed.jsonl")
		storedCwd := filepath.Join(tempDir, "stored-project")
		writeSessionHeader(t, file, storedCwd, "leading-malformed", "not json\n{broken json\n")
		sessionManager, err := OpenSession(file, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if sessionManager.GetSessionID() != "leading-malformed" || sessionManager.GetCwd() != storedCwd {
			t.Fatalf("id = %q cwd = %q", sessionManager.GetSessionID(), sessionManager.GetCwd())
		}
	})

	t.Run("reads cwd from a session with a multi-buffer header", func(t *testing.T) {
		file := filepath.Join(tempDir, "multi-buffer.jsonl")
		storedCwd := filepath.Join(tempDir, "stored-project")
		writeSessionHeader(t, file, storedCwd, strings.Repeat("a", 8192), "")
		sessionManager, err := OpenSession(file, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if sessionManager.GetSessionID() != strings.Repeat("a", 8192) || sessionManager.GetCwd() != storedCwd {
			t.Fatalf("cwd = %q", sessionManager.GetCwd())
		}
	})

	t.Run("opens compatible sessions beyond the discovery scan limit", func(t *testing.T) {
		storedCwd := filepath.Join(tempDir, "stored-project")
		overrideCwd := filepath.Join(tempDir, "override-project")
		cases := []struct {
			name   string
			id     string
			prefix string
		}{
			{"large-header", strings.Repeat("a", headerScanLimitBytes+1), ""},
			{"large-prefix", "large-prefix", strings.Repeat("x", headerScanLimitBytes+1) + "\n"},
		}

		for _, c := range cases {
			file := filepath.Join(tempDir, c.name+".jsonl")
			writeSessionHeader(t, file, storedCwd, c.id, c.prefix)
			for _, cwdOverride := range []*string{nil, &overrideCwd} {
				sessionManager, err := OpenSession(file, tempDir, cwdOverride)
				if err != nil {
					t.Fatalf("%s: %v", c.name, err)
				}
				if sessionManager.GetSessionID() != c.id {
					t.Fatalf("%s: id = %q", c.name, sessionManager.GetSessionID())
				}
				wantCwd := storedCwd
				if cwdOverride != nil {
					wantCwd = *cwdOverride
				}
				if sessionManager.GetCwd() != wantCwd {
					t.Fatalf("%s: cwd = %q, want %q", c.name, sessionManager.GetCwd(), wantCwd)
				}
			}
		}
	})

	t.Run("opens session files larger than Node's max string length", func(t *testing.T) {
		if testing.Short() {
			t.Skip("writes a ~512MB sparse file")
		}
		file := filepath.Join(tempDir, "large.jsonl")
		header, err := mustJSONLine(map[string]any{
			"type": "session", "version": 3, "id": "abc",
			"timestamp": "2025-01-01T00:00:00Z", "cwd": "/tmp",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(header+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		f, err := os.OpenFile(file, os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		const maxStringLength = (1 << 29) - 24
		const stride = 16 * 1024 * 1024
		for offset := stride; offset <= maxStringLength+stride; offset += stride {
			if _, err := f.WriteAt([]byte("\n"), int64(offset)); err != nil {
				f.Close()
				t.Fatal(err)
			}
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}

		message := `{"type":"message","id":"1","parentId":null,"timestamp":"2025-01-01T00:00:01Z","message":{"role":"user","content":"hi","timestamp":1}}` + "\n"
		f, err = os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(message); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}

		sessionManager, err := OpenSession(file, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if sessionManager.GetSessionID() != "abc" {
			t.Fatalf("id = %q", sessionManager.GetSessionID())
		}
		if len(sessionManager.GetEntries()) != 1 {
			t.Fatalf("entries = %d", len(sessionManager.GetEntries()))
		}
		ctx := sessionManager.BuildSessionContext()
		if len(ctx.Messages) != 1 {
			t.Fatalf("messages = %d", len(ctx.Messages))
		}
		if um, ok := ctx.Messages[0].(types.UserMessage); !ok || um.Content.String != "hi" {
			t.Fatalf("message = %#v", ctx.Messages[0])
		}
	})
}

func TestFindMostRecentSession(t *testing.T) {
	tempDir := newTempDir(t)

	t.Run("returns null for empty directory", func(t *testing.T) {
		if got := FindMostRecentSession(tempDir, ""); got != nil {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("returns null for non-existent directory", func(t *testing.T) {
		if got := FindMostRecentSession(filepath.Join(tempDir, "nonexistent"), ""); got != nil {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("ignores non-jsonl files", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(tempDir, "file.txt"), []byte("hello"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tempDir, "file.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := FindMostRecentSession(tempDir, ""); got != nil {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("ignores jsonl files without valid session header", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(tempDir, "invalid.jsonl"), []byte(`{"type":"message"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := FindMostRecentSession(tempDir, ""); got != nil {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("returns single valid session file", func(t *testing.T) {
		file := filepath.Join(tempDir, "session.jsonl")
		if err := os.WriteFile(file, []byte(`{"type":"session","id":"abc","timestamp":"2025-01-01T00:00:00Z","cwd":"/tmp"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := FindMostRecentSession(tempDir, ""); got == nil || *got != file {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("returns most recently modified session", func(t *testing.T) {
		file1 := filepath.Join(tempDir, "older.jsonl")
		file2 := filepath.Join(tempDir, "newer.jsonl")

		if err := os.WriteFile(file1, []byte(`{"type":"session","id":"old","timestamp":"2025-01-01T00:00:00Z","cwd":"/tmp"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Small delay to ensure different mtime
		time.Sleep(10 * time.Millisecond)
		if err := os.WriteFile(file2, []byte(`{"type":"session","id":"new","timestamp":"2025-01-01T00:00:00Z","cwd":"/tmp"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		if got := FindMostRecentSession(tempDir, ""); got == nil || *got != file2 {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("skips invalid files and returns valid one", func(t *testing.T) {
		invalid := filepath.Join(tempDir, "invalid2.jsonl")
		valid := filepath.Join(tempDir, "valid2.jsonl")

		if err := os.WriteFile(invalid, []byte(`{"type":"not-session"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
		if err := os.WriteFile(valid, []byte(`{"type":"session","id":"abc","timestamp":"2025-01-01T00:00:00Z","cwd":"/tmp"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		if got := FindMostRecentSession(tempDir, ""); got == nil || *got != valid {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("skips oversized corrupt files and returns a valid session", func(t *testing.T) {
		invalid := filepath.Join(tempDir, "oversized.jsonl")
		valid := filepath.Join(tempDir, "valid3.jsonl")
		if err := os.WriteFile(invalid, []byte(strings.Repeat("x", headerScanLimitBytes+1)), 0o644); err != nil {
			t.Fatal(err)
		}
		// Ensure distinct mtimes (the sibling "skips invalid files" case
		// sleeps for the same reason; ext4 nanosecond stamps can still tie
		// under scheduler jitter).
		time.Sleep(10 * time.Millisecond)
		if err := os.WriteFile(valid, []byte(`{"type":"session","id":"abc","timestamp":"2025-01-01T00:00:00Z","cwd":"/tmp"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		if got := FindMostRecentSession(tempDir, ""); got == nil || *got != valid {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("filters most recent session by cwd", func(t *testing.T) {
		projectA := filepath.Join(tempDir, "project-a")
		projectB := filepath.Join(tempDir, "project-b")
		fileA := filepath.Join(tempDir, "a.jsonl")
		fileB := filepath.Join(tempDir, "b.jsonl")

		headerA, err := mustJSONLine(map[string]any{
			"type": "session", "id": "a", "timestamp": "2025-01-01T00:00:00Z", "cwd": projectA,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fileA, []byte(headerA+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
		headerB, err := mustJSONLine(map[string]any{
			"type": "session", "id": "b", "timestamp": "2025-01-01T00:00:00Z", "cwd": projectB,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fileB, []byte(headerB+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		if got := FindMostRecentSession(tempDir, projectA); got == nil || *got != fileA {
			t.Fatalf("got %v", got)
		}
		if got := FindMostRecentSession(tempDir, projectB); got == nil || *got != fileB {
			t.Fatalf("got %v", got)
		}
	})
}

func TestSessionManagerCustomFlatSessionDirectory(t *testing.T) {
	tempDir := newTempDir(t)
	projectA := filepath.Join(tempDir, "project-a")
	projectB := filepath.Join(tempDir, "project-b")
	for _, dir := range []string{projectA, projectB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	createPersistedSession := func(cwd string, label string) string {
		session, err := CreateSessionManager(cwd, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendMessage(types.UserMessage{
			Role: types.RoleUser, Content: types.UserContent{String: label}, Timestamp: timeNowMillis(),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendMessage(types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{types.NewTextContent("reply to " + label)},
			API:        "anthropic-messages",
			Provider:   "anthropic",
			Model:      "test",
			Usage:      testUsage(),
			StopReason: types.StopReasonStop,
			Timestamp:  timeNowMillis(),
		}); err != nil {
			t.Fatal(err)
		}
		sessionFile := session.GetSessionFile()
		if sessionFile == "" {
			t.Fatal("Expected persisted session file")
		}
		return sessionFile
	}

	t.Run("scopes current-folder APIs by cwd while listing all flat sessions", func(t *testing.T) {
		sessionA := createPersistedSession(projectA, "from A")
		time.Sleep(10 * time.Millisecond)
		sessionB := createPersistedSession(projectB, "from B")

		currentA := ListSessions(context.Background(), projectA, tempDir, nil)
		var pathsA []string
		for _, s := range currentA {
			pathsA = append(pathsA, s.Path)
		}
		if !reflect.DeepEqual(pathsA, []string{sessionA}) {
			t.Fatalf("pathsA = %#v", pathsA)
		}

		all := ListAllSessions(context.Background(), tempDir, nil)
		var pathsAll []string
		for _, s := range all {
			pathsAll = append(pathsAll, s.Path)
		}
		sort.Strings(pathsAll)
		want := []string{sessionA, sessionB}
		sort.Strings(want)
		if !reflect.DeepEqual(pathsAll, want) {
			t.Fatalf("pathsAll = %#v, want %v", pathsAll, want)
		}

		continuedA, err := ContinueRecentSession(projectA, tempDir)
		if err != nil {
			t.Fatal(err)
		}
		if continuedA.GetSessionFile() != sessionA {
			t.Fatalf("continued = %q, want %q", continuedA.GetSessionFile(), sessionA)
		}
	})
}

func TestSessionManagerSetSessionFileWithCorruptedFiles(t *testing.T) {
	tempDir := newTempDir(t)

	t.Run("truncates and rewrites empty file with valid header", func(t *testing.T) {
		emptyFile := filepath.Join(tempDir, "empty.jsonl")
		if err := os.WriteFile(emptyFile, nil, 0o644); err != nil {
			t.Fatal(err)
		}

		sm, err := OpenSession(emptyFile, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}

		// Should have created a new session with valid header
		if sm.GetSessionID() == "" || sm.GetHeader() == nil || sm.GetHeader().Type != "session" {
			t.Fatalf("header = %#v", sm.GetHeader())
		}

		// File should now contain a valid header
		content, err := os.ReadFile(emptyFile)
		if err != nil {
			t.Fatal(err)
		}
		lines := splitNonEmpty(string(content))
		if len(lines) != 1 {
			t.Fatalf("lines = %d", len(lines))
		}
		var header map[string]any
		if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
			t.Fatal(err)
		}
		if header["type"] != "session" || header["id"] != sm.GetSessionID() {
			t.Fatalf("header = %#v", header)
		}
	})

	t.Run("errors and preserves non-empty file without valid header", func(t *testing.T) {
		noHeaderFile := filepath.Join(tempDir, "no-header.jsonl")
		originalContent := `{"type":"message","id":"abc","parentId":"orphaned","timestamp":"2025-01-01T00:00:00Z","message":{"role":"assistant","content":"test"}}` + "\n"
		if err := os.WriteFile(noHeaderFile, []byte(originalContent), 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := OpenSession(noHeaderFile, tempDir, nil)
		if err == nil || err.Error() != "Session file is not a valid "+codingagent.AppName+" session: "+noHeaderFile {
			t.Fatalf("err = %v", err)
		}
		content, _ := os.ReadFile(noHeaderFile)
		if string(content) != originalContent {
			t.Fatalf("content = %q", content)
		}
	})

	t.Run("errors and preserves non-session JSONL files", func(t *testing.T) {
		nonSessionFile := filepath.Join(tempDir, "not-a-session.log")
		originalContent := `{"type":"event","data":"not a session"}` + "\n"
		if err := os.WriteFile(nonSessionFile, []byte(originalContent), 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := OpenSession(nonSessionFile, tempDir, nil)
		if err == nil || err.Error() != "Session file is not a valid "+codingagent.AppName+" session: "+nonSessionFile {
			t.Fatalf("err = %v", err)
		}
		content, _ := os.ReadFile(nonSessionFile)
		if string(content) != originalContent {
			t.Fatalf("content = %q", content)
		}
	})

	t.Run("preserves explicit session file path when recovering from corrupted file", func(t *testing.T) {
		explicitPath := filepath.Join(tempDir, "my-session.jsonl")
		if err := os.WriteFile(explicitPath, nil, 0o644); err != nil {
			t.Fatal(err)
		}

		sm, err := OpenSession(explicitPath, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}

		// The session file path should be preserved
		if sm.GetSessionFile() != explicitPath {
			t.Fatalf("session file = %q", sm.GetSessionFile())
		}
	})

	t.Run("subsequent loads of initialized empty file work correctly", func(t *testing.T) {
		emptyFile := filepath.Join(tempDir, "empty2.jsonl")
		if err := os.WriteFile(emptyFile, nil, 0o644); err != nil {
			t.Fatal(err)
		}

		sm1, err := OpenSession(emptyFile, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		sessionID := sm1.GetSessionID()

		sm2, err := OpenSession(emptyFile, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if sm2.GetSessionID() != sessionID || sm2.GetHeader().Type != "session" {
			t.Fatalf("sm2 = %q %#v", sm2.GetSessionID(), sm2.GetHeader())
		}
	})
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// mustJSONLine marshals a value to a single JSON line.
func mustJSONLine(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
