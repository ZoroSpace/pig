// Pig-side coverage for pi's modes/json-event.ts (pi locks the shape via the
// RPC suite; the conversion helpers are asserted directly here).
package modes

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
)

func jsonEventSession(t *testing.T, event *types.AssistantMessageEvent, message *types.AssistantMessage) core.AgentSessionEvent {
	t.Helper()
	return core.AgentSessionEvent{
		AgentEvent: agent.AgentEvent{
			Type:                  agent.AgentEventMessageUpdate,
			Message:               message,
			AssistantMessageEvent: event,
		},
	}
}

func TestToJsonEvent(t *testing.T) {
	message := &types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{},
		API:        ai.ApiOpenAICompletions,
		Provider:   "openai",
		Model:      "gpt-4o-mini",
		Usage:      types.Usage{Input: 3, Output: 5},
		StopReason: types.StopReasonStop,
	}

	t.Run("passes through non message_update events", func(t *testing.T) {
		event := core.AgentSessionEvent{AgentEvent: agent.AgentEvent{Type: agent.AgentEventAgentEnd}}
		out, err := ToJsonEvent(event)
		if err != nil {
			t.Fatal(err)
		}
		passed, ok := out.(core.AgentSessionEvent)
		if !ok || passed.Type != event.Type {
			t.Fatalf("expected the event to pass through unchanged, got %T", out)
		}
	})

	t.Run("strips the partial snapshot from toolcall_start", func(t *testing.T) {
		partial := &types.AssistantMessage{
			Role: types.RoleAssistant, Content: []types.Content{},
			API: ai.ApiOpenAICompletions, Provider: "openai", Model: "gpt-4o-mini",
		}
		toolCall := &types.ToolCall{Kind: types.KindToolCall, ID: "call-1", Name: "bash"}
		event := &types.AssistantMessageEvent{
			Type:         types.EventToolCallStart,
			ContentIndex: 0,
			ToolCall:     toolCall,
			Partial:      partial,
		}
		out, err := ToJsonEvent(jsonEventSession(t, event, message))
		if err != nil {
			t.Fatal(err)
		}
		update, ok := out.(*JsonMessageUpdateEvent)
		if !ok {
			t.Fatalf("unexpected event type %T", out)
		}
		if update.AssistantMessageEvent.ID != "call-1" || update.AssistantMessageEvent.ToolName != "bash" {
			t.Fatalf("id/toolName not promoted: %+v", update.AssistantMessageEvent)
		}
		if update.AssistantMessageEvent.Partial != nil {
			t.Fatal("partial snapshot should be stripped")
		}
		if update.Usage.Input != 3 || update.Usage.Output != 5 {
			t.Fatalf("usage not carried: %+v", update.Usage)
		}
	})

	t.Run("strips the partial from delta events", func(t *testing.T) {
		partial := &types.AssistantMessage{
			Role: types.RoleAssistant, Content: []types.Content{},
			API: ai.ApiOpenAICompletions, Provider: "openai", Model: "gpt-4o-mini",
		}
		event := &types.AssistantMessageEvent{Type: types.EventTextDelta, Delta: "he", Partial: partial}
		out, err := ToJsonEvent(jsonEventSession(t, event, message))
		if err != nil {
			t.Fatal(err)
		}
		update := out.(*JsonMessageUpdateEvent)
		if update.AssistantMessageEvent.Partial != nil {
			t.Fatal("partial snapshot should be stripped")
		}
		data, err := json.Marshal(update)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "partial") {
			t.Fatalf("wire format should not contain partial: %s", data)
		}
	})

	t.Run("rejects non assistant message_update events", func(t *testing.T) {
		userMessage := &types.UserMessage{Role: types.RoleUser}
		event := core.AgentSessionEvent{
			AgentEvent: agent.AgentEvent{
				Type:                  agent.AgentEventMessageUpdate,
				Message:               userMessage,
				AssistantMessageEvent: &types.AssistantMessageEvent{Type: types.EventStart},
			},
		}
		if _, err := ToJsonEvent(event); err == nil {
			t.Fatal("expected an error for a non assistant message")
		}
	})
}
