// pi: test/session-manager/save-entry.test.ts — custom entries ride the
// tree and are skipped by context building.
package core

import (
	"reflect"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

func TestSessionManagerSaveCustomEntry(t *testing.T) {
	t.Run("saves custom entries and includes them in tree traversal", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		// Save a message
		msgID, err := session.AppendMessage(userMsg("hello"))
		mustAppend(t, msgID, err)

		// Save a custom entry
		customID, err := session.AppendCustomEntry("my_data", map[string]any{"foo": "bar"})
		mustAppend(t, customID, err)

		// Save another message
		msg2ID, err := session.AppendMessage(types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{types.NewTextContent("hi")},
			API:        "anthropic-messages",
			Provider:   "anthropic",
			Model:      "test",
			Usage:      testUsage(),
			StopReason: types.StopReasonStop,
			Timestamp:  2,
		})
		mustAppend(t, msg2ID, err)

		// Custom entry should be in entries
		entries := session.GetEntries()
		if len(entries) != 3 {
			t.Fatalf("expected 3 entries, got %d", len(entries))
		}

		var customEntry *CustomEntry
		for _, entry := range entries {
			if e, ok := entry.(*CustomEntry); ok {
				customEntry = e
			}
		}
		if customEntry == nil {
			t.Fatal("custom entry missing")
		}
		if customEntry.CustomType != "my_data" {
			t.Fatalf("customType = %q", customEntry.CustomType)
		}
		if !reflect.DeepEqual(customEntry.Data, map[string]any{"foo": "bar"}) {
			t.Fatalf("data = %#v", customEntry.Data)
		}
		if customEntry.Base().ID != customID || *customEntry.Base().ParentID != msgID {
			t.Fatalf("custom entry = %#v", customEntry)
		}

		// Tree structure should be correct
		path := session.GetBranch("")
		if len(path) != 3 || path[0].Base().ID != msgID || path[1].Base().ID != customID || path[2].Base().ID != msg2ID {
			t.Fatalf("path = %#v", path)
		}

		// buildSessionContext should work (custom entries skipped in messages)
		ctx := session.BuildSessionContext()
		if len(ctx.Messages) != 2 { // only message entries
			t.Fatalf("expected 2 messages, got %d", len(ctx.Messages))
		}
	})
}
