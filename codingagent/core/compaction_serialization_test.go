// pi: test/compaction-serialization.test.ts — serializeConversation behavior
// (exercised through the core re-export of the shared harness implementation).
package core

import (
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
)

func TestSerializeConversationTruncatesLongToolResults(t *testing.T) {
	longContent := strings.Repeat("x", 5000)
	messages := []types.Message{
		types.ToolResultMessage{
			Role:       types.RoleToolResult,
			ToolCallID: "tc1",
			ToolName:   "read",
			Content:    []types.Content{types.NewTextContent(longContent)},
			IsError:    false,
			Timestamp:  time.Now().UnixMilli(),
		},
	}

	result := SerializeConversation(messages)

	if !strings.Contains(result, "[Tool result]:") {
		t.Fatalf("missing tool result prefix: %s", result)
	}
	if !strings.Contains(result, "[... 3000 more characters truncated]") {
		t.Fatalf("missing truncation marker: %s", result)
	}
	if strings.Contains(result, strings.Repeat("x", 3000)) {
		t.Fatal("should not contain 3000 x's")
	}
	// First 2000 chars should be present
	if !strings.Contains(result, strings.Repeat("x", 2000)) {
		t.Fatal("first 2000 chars should be present")
	}
}

func TestSerializeConversationDoesNotTruncateShortToolResults(t *testing.T) {
	shortContent := strings.Repeat("x", 1500)
	messages := []types.Message{
		types.ToolResultMessage{
			Role:       types.RoleToolResult,
			ToolCallID: "tc1",
			ToolName:   "read",
			Content:    []types.Content{types.NewTextContent(shortContent)},
			IsError:    false,
			Timestamp:  time.Now().UnixMilli(),
		},
	}

	result := SerializeConversation(messages)

	if result != "[Tool result]: "+shortContent {
		t.Fatalf("unexpected result: %s", result)
	}
	if strings.Contains(result, "truncated") {
		t.Fatal("should not contain truncation marker")
	}
}

func TestSerializeConversationDoesNotTruncateAssistantOrUserMessages(t *testing.T) {
	longText := strings.Repeat("y", 5000)
	messages := []types.Message{
		types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(longText)}},
			Timestamp: time.Now().UnixMilli(),
		},
		types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{types.NewTextContent(longText)},
			API:        "anthropic",
			Provider:   "anthropic",
			Model:      "test",
			Usage:      types.Usage{},
			StopReason: types.StopReasonStop,
			Timestamp:  time.Now().UnixMilli(),
		},
	}

	result := SerializeConversation(messages)

	if strings.Contains(result, "truncated") {
		t.Fatal("should not contain truncation marker")
	}
	if !strings.Contains(result, longText) {
		t.Fatal("long text should be present")
	}
}
