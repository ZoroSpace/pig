// agent_session_tool_result_events_test.go — full-stack lock for the live
// tool result seam (docs/fix/2026-09-11-tool-execution-live-output-empty.md).
//
// interactive-mode's handleEvent consumes AgentSessionEvents straight from
// session.Subscribe; whatever Result/PartialResult carry here is exactly
// what resultToComponentResult must convert. The unit tests in
// codingagent/modes/interactive pin the conversion; this test pins the
// payload the live event pump delivers (agent loop → AgentSession →
// listeners) for a tool that streams a partial and returns final content.
package core

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
)

func toolResultText(t *testing.T, payload any) (string, bool) {
	t.Helper()
	switch r := payload.(type) {
	case agent.AgentToolResult:
		var texts []string
		for _, block := range r.Content {
			if text, ok := block.(types.TextContent); ok {
				texts = append(texts, text.Text)
			}
		}
		return strings.Join(texts, "\n"), true
	case *agent.AgentToolResult:
		if r == nil {
			return "", false
		}
		return toolResultText(t, *r)
	case types.ToolResultMessage:
		var texts []string
		for _, block := range r.Content {
			if text, ok := block.(types.TextContent); ok {
				texts = append(texts, text.Text)
			}
		}
		return strings.Join(texts, "\n"), true
	case *types.ToolResultMessage:
		if r == nil {
			return "", false
		}
		return toolResultText(t, *r)
	default:
		return "", false
	}
}

func TestAgentSessionToolExecutionEventsCarryContent(t *testing.T) {
	const partialProbe = "PARTIAL OUTPUT PROBE"
	const finalProbe = "FINAL OUTPUT PROBE"

	echoTool := agent.AgentTool{
		Tool: types.Tool{
			Name:        "echo",
			Description: "Echo text back",
			Parameters:  rawJSON(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		},
		Label: "Echo",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			if onUpdate != nil {
				onUpdate(agent.AgentToolResult{
					Content: []types.Content{types.NewTextContent(partialProbe)},
					Details: map[string]any{"phase": "partial"},
				})
			}
			return agent.AgentToolResult{
				Content: []types.Content{types.NewTextContent(finalProbe)},
				Details: map[string]any{"phase": "final"},
			}, nil
		},
	}

	callCount := 0
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:         true,
		BaseToolsOverride: map[string]agent.AgentTool{"echo": echoTool},
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			callCount++
			stream := newMockAssistantStream()
			if callCount == 1 {
				msg := testAssistantMessage("Running the tool.", func(m *types.AssistantMessage) {
					m.StopReason = types.StopReasonToolUse
					m.Content = []types.Content{
						types.NewTextContent("Running the tool."),
						types.ToolCall{Kind: types.KindToolCall, ID: "call_1", Name: "echo", Arguments: map[string]any{"text": "hello"}},
					}
				})
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
				stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonToolUse, Message: &msg})
			} else {
				msg := testAssistantMessage("Final answer.")
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
				stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &msg})
			}
			stream.End()
			return stream.AssistantMessageEventStream
		},
	})

	var mu sync.Mutex
	var updates []any
	var ends []any
	session.Subscribe(func(event *AgentSessionEvent) {
		switch event.Type {
		case agent.AgentEventToolExecutionUpdate:
			mu.Lock()
			updates = append(updates, event.PartialResult)
			mu.Unlock()
		case agent.AgentEventToolExecutionEnd:
			mu.Lock()
			ends = append(ends, event.Result)
			mu.Unlock()
		}
	})

	if err := session.Prompt(context.Background(), "Run echo", nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(ends) != 1 {
		t.Fatalf("tool_execution_end events = %d, want 1", len(ends))
	}
	if text, ok := toolResultText(t, ends[0]); !ok || !strings.Contains(text, finalProbe) {
		t.Fatalf("tool_execution_end Result payload lost content: type=%T text=%q ok=%v", ends[0], text, ok)
	}
	if len(updates) < 1 {
		t.Fatalf("tool_execution_update events = %d, want >= 1", len(updates))
	}
	foundPartial := false
	for _, update := range updates {
		if text, ok := toolResultText(t, update); ok && strings.Contains(text, partialProbe) {
			foundPartial = true
		}
	}
	if !foundPartial {
		t.Fatalf("tool_execution_update PartialResult payloads lost content: %+v", updates)
	}
}
