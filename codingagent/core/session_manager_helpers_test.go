// Shared test helpers for the session-manager tests (pi:
// test/utilities.ts userMsg/assistantMsg subset).
package core

import (
	"os"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
)

// userMsg creates a minimal user message for testing (pi: userMsg).
func userMsg(text string) types.UserMessage {
	return types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{String: text},
		Timestamp: time.Now().UnixMilli(),
	}
}

// assistantMsg creates a minimal assistant message for testing (pi:
// assistantMsg).
func assistantMsg(text string) types.AssistantMessage {
	return types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent(text)},
		API:        "anthropic-messages",
		Provider:   "anthropic",
		Model:      "test",
		Usage:      testUsage(),
		StopReason: types.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}
}

// assistantMsgFull is the assistant fixture used by build-context tests (pi:
// the inline assistant literal with model claude-test).
func assistantMsgFull(text string) types.AssistantMessage {
	msg := assistantMsg(text)
	msg.Model = "claude-test"
	return msg
}

func testUsage() types.Usage {
	return types.Usage{
		Input:       1,
		Output:      1,
		CacheRead:   0,
		CacheWrite:  0,
		TotalTokens: 2,
		Cost:        types.Cost{},
	}
}

func timeNowMillis() int64 {
	return time.Now().UnixMilli()
}

func newTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "session-test-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
