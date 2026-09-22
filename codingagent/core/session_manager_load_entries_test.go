// pi: test/session-manager/load-entries.test.ts — SessionManager.inMemory
// with preloaded entries (v0.85.1: entries held outside the filesystem load
// directly instead of starting a fresh session).
package core

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/samber/lo"
)

var uuidV7Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// asFileEntries widens typed entries to the raw file-entry surface (TS is
// structurally covariant; Go slices need an explicit conversion).
func asFileEntries(entries []SessionEntry) []FileEntry {
	out := make([]FileEntry, len(entries))
	for i, entry := range entries {
		out[i] = entry
	}
	return out
}

// storedEntries builds entries in a throwaway in-memory session (pi:
// storedEntries).
func storedEntries(t *testing.T, build func(source *SessionManager)) []SessionEntry {
	t.Helper()
	source, err := InMemorySession("/project", nil)
	if err != nil {
		t.Fatal(err)
	}
	build(source)
	return source.GetEntries()
}

func TestLoadEntriesInMemory(t *testing.T) {
	t.Run("adopts entries verbatim", func(t *testing.T) {
		entries := storedEntries(t, func(source *SessionManager) {
			if _, err := source.AppendMessage(userMsg("hello")); err != nil {
				t.Fatal(err)
			}
			if _, err := source.AppendModelChange("anthropic", "claude-opus-4-5"); err != nil {
				t.Fatal(err)
			}
			if _, err := source.AppendMessage(userMsg("again")); err != nil {
				t.Fatal(err)
			}
		})

		session, err := InMemorySession("/project", nil, asFileEntries(entries))
		if err != nil {
			t.Fatal(err)
		}
		loaded := session.GetEntries()
		if len(loaded) != len(entries) {
			t.Fatalf("entries = %d, want %d", len(loaded), len(entries))
		}
		for i := range entries {
			if loaded[i].Base().ID != entries[i].Base().ID {
				t.Fatalf("entry %d id = %q, want %q", i, loaded[i].Base().ID, entries[i].Base().ID)
			}
		}
	})

	t.Run("keeps the loaded leaf so appends continue the conversation", func(t *testing.T) {
		entries := storedEntries(t, func(source *SessionManager) {
			if _, err := source.AppendMessage(userMsg("hello")); err != nil {
				t.Fatal(err)
			}
			if _, err := source.AppendMessage(userMsg("again")); err != nil {
				t.Fatal(err)
			}
		})
		lastID := entries[len(entries)-1].Base().ID

		session, err := InMemorySession("/project", nil, asFileEntries(entries))
		if err != nil {
			t.Fatal(err)
		}
		appendedID, err := session.AppendMessage(userMsg("continued"))
		if err != nil {
			t.Fatal(err)
		}

		if got := session.GetLeafID(); got == nil || *got != appendedID {
			t.Fatalf("leafId = %v, want %q", got, appendedID)
		}
		if entry := session.GetEntry(appendedID); entry.Base().ParentID == nil || *entry.Base().ParentID != lastID {
			t.Fatalf("parentId = %v, want %q", entry.Base().ParentID, lastID)
		}
	})

	t.Run("never mints an id that collides with a loaded entry", func(t *testing.T) {
		entries := storedEntries(t, func(source *SessionManager) {
			for i := 0; i < 50; i++ {
				if _, err := source.AppendMessage(userMsg("message")); err != nil {
					t.Fatal(err)
				}
			}
		})

		session, err := InMemorySession("/project", nil, asFileEntries(entries))
		if err != nil {
			t.Fatal(err)
		}
		appendedID, err := session.AppendMessage(userMsg("continued"))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Base().ID == appendedID {
				t.Fatalf("appended id %q collides with a loaded entry", appendedID)
			}
		}
	})

	t.Run("rebuilds the branch structure rather than a flat chain", func(t *testing.T) {
		entries := storedEntries(t, func(source *SessionManager) {
			firstID, err := source.AppendMessage(userMsg("hello"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.AppendMessage(userMsg("abandoned")); err != nil {
				t.Fatal(err)
			}
			if err := source.Branch(firstID); err != nil {
				t.Fatal(err)
			}
			if _, err := source.AppendMessage(userMsg("kept")); err != nil {
				t.Fatal(err)
			}
		})

		session, err := InMemorySession("/project", nil, asFileEntries(entries))
		if err != nil {
			t.Fatal(err)
		}
		roots := session.GetTree()
		if len(roots) != 1 {
			t.Fatalf("roots = %d, want 1", len(roots))
		}
		if len(roots[0].Children) != 2 {
			t.Fatalf("root children = %d, want 2", len(roots[0].Children))
		}
	})

	t.Run("rebuilds labels", func(t *testing.T) {
		var labelledID string
		entries := storedEntries(t, func(source *SessionManager) {
			id, err := source.AppendMessage(userMsg("hello"))
			if err != nil {
				t.Fatal(err)
			}
			labelledID = id
			if _, err := source.AppendLabelChange(labelledID, lo.ToPtr("checkpoint")); err != nil {
				t.Fatal(err)
			}
		})

		session, err := InMemorySession("/project", nil, asFileEntries(entries))
		if err != nil {
			t.Fatal(err)
		}
		if label, ok := session.GetLabel(labelledID); !ok || label != "checkpoint" {
			t.Fatalf("label = %q, %v", label, ok)
		}
	})

	t.Run("resolves a compaction against the entry it was written against", func(t *testing.T) {
		var keptID string
		entries := storedEntries(t, func(source *SessionManager) {
			if _, err := source.AppendMessage(userMsg("dropped")); err != nil {
				t.Fatal(err)
			}
			id, err := source.AppendMessage(userMsg("kept"))
			if err != nil {
				t.Fatal(err)
			}
			keptID = id
			if _, err := source.AppendCompaction("summary so far", lo.ToPtr(keptID), 1000, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
		})

		session, err := InMemorySession("/project", nil, asFileEntries(entries))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range session.BuildContextEntries() {
			if entry.Base().ID == keptID {
				found = true
			}
		}
		if !found {
			t.Fatal("kept entry missing from context entries")
		}
	})

	t.Run("creates a header from the options when the entries carry none", func(t *testing.T) {
		entries := storedEntries(t, func(source *SessionManager) {
			if _, err := source.AppendMessage(userMsg("hello")); err != nil {
				t.Fatal(err)
			}
		})

		session, err := InMemorySession("/project", &NewSessionOptions{ID: lo.ToPtr("restored-session")}, asFileEntries(entries))
		if err != nil {
			t.Fatal(err)
		}

		if got := session.GetSessionID(); got != "restored-session" {
			t.Fatalf("sessionId = %q", got)
		}
		if header := session.GetHeader(); header == nil || header.ID != "restored-session" {
			t.Fatalf("header = %+v", session.GetHeader())
		} else if header.Cwd != "/project" {
			t.Fatalf("header.cwd = %q", header.Cwd)
		}
	})

	t.Run("generates a session id when the options carry none", func(t *testing.T) {
		entries := storedEntries(t, func(source *SessionManager) {
			if _, err := source.AppendMessage(userMsg("hello")); err != nil {
				t.Fatal(err)
			}
		})

		session, err := InMemorySession("/project", nil, asFileEntries(entries))
		if err != nil {
			t.Fatal(err)
		}
		if !uuidV7Pattern.MatchString(session.GetSessionID()) {
			t.Fatalf("sessionId = %q", session.GetSessionID())
		}
		if header := session.GetHeader(); header == nil || header.ID != session.GetSessionID() {
			t.Fatalf("header = %+v", session.GetHeader())
		}
	})

	t.Run("stays off the filesystem", func(t *testing.T) {
		entries := storedEntries(t, func(source *SessionManager) {
			if _, err := source.AppendMessage(userMsg("hello")); err != nil {
				t.Fatal(err)
			}
		})

		session, err := InMemorySession("/project", nil, asFileEntries(entries))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendMessage(userMsg("continued")); err != nil {
			t.Fatal(err)
		}
		if got := session.GetSessionFile(); got != "" {
			t.Fatalf("sessionFile = %q", got)
		}
		if session.IsPersisted() {
			t.Fatal("expected a non-persisted session")
		}
	})

	t.Run("starts an empty session when the entries are empty", func(t *testing.T) {
		session, err := InMemorySession("/project", &NewSessionOptions{ID: lo.ToPtr("empty-session")}, []FileEntry{})
		if err != nil {
			t.Fatal(err)
		}
		if got := session.GetSessionID(); got != "empty-session" {
			t.Fatalf("sessionId = %q", got)
		}
		if len(session.GetEntries()) != 0 {
			t.Fatalf("entries = %d", len(session.GetEntries()))
		}
		if session.GetLeafID() != nil {
			t.Fatalf("leafId = %v", session.GetLeafID())
		}
	})

	t.Run("takes the session identity from a header among the entries", func(t *testing.T) {
		body := storedEntries(t, func(source *SessionManager) {
			if _, err := source.AppendMessage(userMsg("hello")); err != nil {
				t.Fatal(err)
			}
		})
		entries := append([]FileEntry{
			&SessionHeader{Type: "session", Version: lo.ToPtr(3), ID: "stored-session", Timestamp: "2026-01-01T00:00:00Z", Cwd: "/stored"},
		}, asFileEntries(body)...)

		session, err := InMemorySession("/project", &NewSessionOptions{ID: lo.ToPtr("ignored")}, entries)
		if err != nil {
			t.Fatal(err)
		}
		if got := session.GetSessionID(); got != "stored-session" {
			t.Fatalf("sessionId = %q", got)
		}
		if header := session.GetHeader(); header == nil || header.Cwd != "/stored" {
			t.Fatalf("header = %+v", session.GetHeader())
		}
	})

	t.Run("migrates entries restored with an older header", func(t *testing.T) {
		hookRaw := json.RawMessage(`{"role":"hookMessage","content":"from a hook","timestamp":1}`)
		entries := []FileEntry{
			&SessionHeader{Type: "session", Version: lo.ToPtr(2), ID: "v2-session", Timestamp: "2026-01-01T00:00:00Z", Cwd: "/project"},
			&SessionMessageEntry{
				EntryBase: EntryBase{Type: "message", ID: "abc12345", Timestamp: "2026-01-01T00:00:01Z"},
				Message:   &untypedMessage{Role: "hookMessage", Raw: hookRaw},
			},
		}

		session, err := InMemorySession("/project", nil, entries)
		if err != nil {
			t.Fatal(err)
		}
		header := session.GetHeader()
		if header == nil || header.Version == nil || *header.Version != 3 {
			t.Fatalf("header = %+v", header)
		}
		restored, ok := session.GetEntries()[0].(*SessionMessageEntry)
		if !ok {
			t.Fatalf("entry type = %T", session.GetEntries()[0])
		}
		if restored.Message.MessageRole() != "custom" {
			t.Fatalf("role = %q", restored.Message.MessageRole())
		}
		if restored.Base().ID != "abc12345" {
			t.Fatalf("id = %q", restored.Base().ID)
		}
	})

	t.Run("adopts headerless entries as current-version without migrating them", func(t *testing.T) {
		entries := []FileEntry{
			&SessionMessageEntry{
				EntryBase: EntryBase{Type: "message", ID: "abc12345", Timestamp: "2026-01-01T00:00:01Z"},
				Message:   &untypedMessage{Role: "hookMessage", Raw: json.RawMessage(`{"role":"hookMessage","content":"from a hook","timestamp":1}`)},
			},
		}

		session, err := InMemorySession("/project", nil, entries)
		if err != nil {
			t.Fatal(err)
		}
		restored, ok := session.GetEntries()[0].(*SessionMessageEntry)
		if !ok {
			t.Fatalf("entry type = %T", session.GetEntries()[0])
		}
		if !strings.Contains(string(restored.Message.MessageRole()), "hookMessage") {
			t.Fatalf("role = %q", restored.Message.MessageRole())
		}
	})
}
