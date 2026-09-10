// pi: test/session-manager/migration.test.ts — v1 → v2 → v3 entry
// migrations.
package core

import (
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

func TestMigrateSessionEntries(t *testing.T) {
	t.Run("should add id/parentId to v1 entries", func(t *testing.T) {
		entries := []FileEntry{
			&SessionHeader{Type: "session", ID: "sess-1", Timestamp: "2025-01-01T00:00:00Z", Cwd: "/tmp"},
			&SessionMessageEntry{
				EntryBase: EntryBase{Type: "message", Timestamp: "2025-01-01T00:00:01Z"},
				Message:   types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1},
			},
			&SessionMessageEntry{
				EntryBase: EntryBase{Type: "message", Timestamp: "2025-01-01T00:00:02Z"},
				Message: types.AssistantMessage{
					Role:       types.RoleAssistant,
					Content:    []types.Content{types.NewTextContent("hello")},
					API:        "test",
					Provider:   "test",
					Model:      "test",
					Usage:      types.Usage{Input: 1, Output: 1, CacheRead: 0, CacheWrite: 0},
					StopReason: types.StopReasonStop,
					Timestamp:  2,
				},
			},
		}

		MigrateSessionEntries(entries)

		// Header should have version set (v3 is current after hookMessage->custom migration)
		header := entries[0].(*SessionHeader)
		if header.Version == nil || *header.Version != 3 {
			t.Fatalf("version = %v", header.Version)
		}

		// Entries should have id/parentId
		msg1 := entries[1].(SessionEntry).Base()
		msg2 := entries[2].(SessionEntry).Base()

		if len(msg1.ID) != 8 {
			t.Fatalf("msg1 id = %q", msg1.ID)
		}
		if msg1.ParentID != nil {
			t.Fatalf("msg1 parentId = %v", msg1.ParentID)
		}

		if len(msg2.ID) != 8 {
			t.Fatalf("msg2 id = %q", msg2.ID)
		}
		if msg2.ParentID == nil || *msg2.ParentID != msg1.ID {
			t.Fatalf("msg2 parentId = %v", msg2.ParentID)
		}
	})

	t.Run("should be idempotent (skip already migrated)", func(t *testing.T) {
		entries := []FileEntry{
			&SessionHeader{Type: "session", Version: intPtr(2), ID: "sess-1", Timestamp: "2025-01-01T00:00:00Z", Cwd: "/tmp"},
			&SessionMessageEntry{
				EntryBase: EntryBase{Type: "message", ID: "abc12345", ParentID: nil, Timestamp: "2025-01-01T00:00:01Z"},
				Message:   types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1},
			},
			&SessionMessageEntry{
				EntryBase: EntryBase{Type: "message", ID: "def67890", ParentID: strPtr("abc12345"), Timestamp: "2025-01-01T00:00:02Z"},
				Message: types.AssistantMessage{
					Role:       types.RoleAssistant,
					Content:    []types.Content{types.NewTextContent("hello")},
					API:        "test",
					Provider:   "test",
					Model:      "test",
					Usage:      types.Usage{Input: 1, Output: 1, CacheRead: 0, CacheWrite: 0},
					StopReason: types.StopReasonStop,
					Timestamp:  2,
				},
			},
		}

		MigrateSessionEntries(entries)

		// IDs should be unchanged
		msg1 := entries[1].(SessionEntry).Base()
		msg2 := entries[2].(SessionEntry).Base()
		if msg1.ID != "abc12345" {
			t.Fatalf("msg1 id = %q", msg1.ID)
		}
		if msg2.ID != "def67890" {
			t.Fatalf("msg2 id = %q", msg2.ID)
		}
		if msg2.ParentID == nil || *msg2.ParentID != "abc12345" {
			t.Fatalf("msg2 parentId = %v", msg2.ParentID)
		}
	})
}

func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }
