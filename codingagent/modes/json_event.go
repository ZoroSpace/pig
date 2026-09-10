// pi: modes/json-event.ts — session event shape emitted by the JSON and RPC
// stdout protocols: cumulative assistant snapshots are stripped from
// streaming wire events (message_start provides the initial message, deltas
// build it, and message_end provides the final authoritative message).
// Cumulative usage, tool-call ids, and tool names remain available because
// their size is constant. pig's AssistantMessageEvent already carries
// toolCall as a typed field, so pi's content-index extraction collapses to
// reading event.ToolCall.
package modes

import (
	"encoding/json"
	"fmt"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
)

// JsonAgentSessionEvent is the wire event (pi: JsonAgentSessionEvent).
type JsonAgentSessionEvent = core.AgentSessionEvent

// JsonMessageUpdateEvent is the message_update event with the assistant
// snapshot removed (pi: JsonMessageUpdateEvent; pig's AssistantMessageEvent
// clears Partial instead of omitting the key — the JSON shape keeps
// "partial" absent via omitempty).
type JsonMessageUpdateEvent struct {
	Type                  string                    `json:"type"`
	Usage                 *types.Usage              `json:"usage"`
	AssistantMessageEvent JsonAssistantMessageEvent `json:"assistantMessageEvent"`
}

// JsonAssistantMessageEvent is AssistantMessageEvent plus the toolcall_start
// id/toolName fields (pi's { ...deltaEvent, id, toolName } spread). The
// embedded struct's fields marshal inline.
type JsonAssistantMessageEvent struct {
	types.AssistantMessageEvent
	ID       string `json:"id,omitempty"`
	ToolName string `json:"toolName,omitempty"`
}

// toJsonAssistantMessageEvent strips the partial snapshot from one streaming
// assistant event (pi: toJsonAssistantMessageEvent). toolcall_start gains
// the id/toolName top-level fields so consumers can route tool calls without
// the snapshot.
func toJsonAssistantMessageEvent(event *types.AssistantMessageEvent) (*JsonAssistantMessageEvent, error) {
	deltaEvent := JsonAssistantMessageEvent{AssistantMessageEvent: *event}
	deltaEvent.Partial = nil

	if event.Type == types.EventToolCallStart {
		if event.ToolCall == nil {
			return nil, fmt.Errorf("toolcall_start content at index %d is not a tool call", event.ContentIndex)
		}
		deltaEvent.ID = event.ToolCall.ID
		deltaEvent.ToolName = event.ToolCall.Name
	}
	return &deltaEvent, nil
}

// ToJsonEvent removes cumulative assistant snapshots from streaming wire
// events (pi: toJsonEvent). Returns the event unchanged for non
// message_update types.
func ToJsonEvent(event core.AgentSessionEvent) (any, error) {
	if event.Type != agent.AgentEventMessageUpdate {
		return event, nil
	}

	message, ok := event.Message.(*types.AssistantMessage)
	if !ok {
		if _, ok := event.Message.(types.AssistantMessage); !ok {
			return nil, fmt.Errorf("message_update message is not an assistant message")
		}
		value := event.Message.(types.AssistantMessage)
		message = &value
	}
	if event.AssistantMessageEvent == nil {
		return nil, fmt.Errorf("message_update message is not an assistant message")
	}

	assistantEvent, err := toJsonAssistantMessageEvent(event.AssistantMessageEvent)
	if err != nil {
		return nil, err
	}

	return &JsonMessageUpdateEvent{
		Type:                  string(event.Type),
		Usage:                 &message.Usage,
		AssistantMessageEvent: *assistantEvent,
	}, nil
}

// MarshalJsonEvent renders one wire event as JSON (pi's JSON.stringify call
// site in print-mode).
func MarshalJsonEvent(event any) ([]byte, error) {
	return json.Marshal(event)
}
