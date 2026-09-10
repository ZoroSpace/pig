// pi: test/agent-session-retry.test.ts — AgentSession auto-retry behavior.
package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
)

// createRetrySession mirrors pi's local createSession helper.
func createRetrySession(t *testing.T, failCount int, maxRetries int64, delayAssistantMessageEndMs int) (*AgentSession, *int) {
	t.Helper()
	callCount := 0

	session, _, settingsManager := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.Context, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			callCount++
			stream := newMockAssistantStream()
			if callCount <= failCount {
				msg := testAssistantMessage("", withStopReason(types.StopReasonError), withErrorMessage("overloaded_error"))
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
				stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonError, Message: &msg})
			} else {
				msg := testAssistantMessage("Success")
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
				stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &msg})
			}
			stream.End()
			return stream.AssistantMessageEventStream
		},
	})
	if delayAssistantMessageEndMs > 0 {
		// pi overrides session._emitExtensionEvent to delay assistant
		// message_end handling; pig overrides the same seam.
		original := session.emitExtensionEventFn
		session.emitExtensionEventFn = func(ctx context.Context, event agent.AgentEvent) {
			if event.Type == agent.AgentEventMessageEnd {
				if _, ok := event.Message.(types.AssistantMessage); ok {
					time.Sleep(time.Duration(delayAssistantMessageEndMs) * time.Millisecond)
				}
			}
			original(ctx, event)
		}
	}
	settingsManager.ApplyOverrides(Settings{"retry": map[string]any{"enabled": true, "maxRetries": float64(maxRetries), "baseDelayMs": float64(1)}})
	return session, &callCount
}

func TestAgentSessionRetryRetriesAfterTransientErrorAndSucceeds(t *testing.T) {
	session, callCount := createRetrySession(t, 1, 3, 0)
	var events []string
	session.Subscribe(func(event *AgentSessionEvent) {
		switch event.Type {
		case AgentSessionEventAutoRetryStart:
			events = append(events, "start")
		case AgentSessionEventAutoRetryEnd:
			if event.Success {
				events = append(events, "end:success=true")
			} else {
				events = append(events, "end:success=false")
			}
		}
	})

	if err := session.Prompt(context.Background(), "Test", nil); err != nil {
		t.Fatal(err)
	}

	if *callCount != 2 {
		t.Fatalf("callCount = %d, want 2", *callCount)
	}
	if strings.Join(events, ",") != "start,end:success=true" {
		t.Fatalf("events = %v", events)
	}
	if session.IsRetrying() {
		t.Fatal("session still retrying")
	}
}

func TestAgentSessionRetryExhaustsMaxRetriesAndEmitsFailure(t *testing.T) {
	session, callCount := createRetrySession(t, 99, 2, 0)
	var events []string
	session.Subscribe(func(event *AgentSessionEvent) {
		switch event.Type {
		case AgentSessionEventAutoRetryStart:
			events = append(events, "start")
		case AgentSessionEventAutoRetryEnd:
			if event.Success {
				events = append(events, "end:success=true")
			} else {
				events = append(events, "end:success=false")
			}
		}
	})

	if err := session.Prompt(context.Background(), "Test", nil); err != nil {
		t.Fatal(err)
	}

	if *callCount != 3 {
		t.Fatalf("callCount = %d, want 3", *callCount)
	}
	starts := 0
	sawFailure := false
	for _, event := range events {
		if event == "start" {
			starts++
		}
		if event == "end:success=false" {
			sawFailure = true
		}
	}
	if starts != 2 {
		t.Fatalf("starts = %d, want 2", starts)
	}
	if !sawFailure {
		t.Fatal("missing end:success=false event")
	}
	if session.IsRetrying() {
		t.Fatal("session still retrying")
	}
}

func TestAgentSessionRetryPromptWaitsForRetryCompletionWhenAssistantMessageEndHandlingIsDelayed(t *testing.T) {
	session, callCount := createRetrySession(t, 1, 3, 40)

	if err := session.Prompt(context.Background(), "Test", nil); err != nil {
		t.Fatal(err)
	}

	if *callCount != 2 {
		t.Fatalf("callCount = %d, want 2", *callCount)
	}
	if session.IsRetrying() {
		t.Fatal("session still retrying")
	}
}

func TestAgentSessionRetryRetriesProviderNetworkErrorFailures(t *testing.T) {
	callCount := 0
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.Context, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			callCount++
			stream := newMockAssistantStream()
			if callCount == 1 {
				msg := testAssistantMessage("", withStopReason(types.StopReasonError), withErrorMessage("Provider finish_reason: network_error"))
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
				stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonError, Message: &msg})
			} else {
				msg := testAssistantMessage("Recovered after retry")
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
				stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &msg})
			}
			stream.End()
			return stream.AssistantMessageEventStream
		},
	})
	session.SettingsManager.ApplyOverrides(Settings{"retry": map[string]any{"enabled": true, "maxRetries": float64(3), "baseDelayMs": float64(1)}})

	var events []string
	session.Subscribe(func(event *AgentSessionEvent) {
		switch event.Type {
		case AgentSessionEventAutoRetryStart:
			events = append(events, "start")
		case AgentSessionEventAutoRetryEnd:
			if event.Success {
				events = append(events, "end:success=true")
			} else {
				events = append(events, "end:success=false")
			}
		}
	})

	if err := session.Prompt(context.Background(), "Test", nil); err != nil {
		t.Fatal(err)
	}

	if callCount != 2 {
		t.Fatalf("callCount = %d, want 2", callCount)
	}
	if strings.Join(events, ",") != "start,end:success=true" {
		t.Fatalf("events = %v", events)
	}
}

func TestAgentSessionRetryPromptWaitsForFullAgentLoopWhenRetryProducesToolCalls(t *testing.T) {
	// Regression: when auto-retry fires and the retry response includes tool_use,
	// session.prompt() must wait for the entire tool loop to finish before returning.
	callCount := 0
	toolExecuted := false

	echoTool := agent.AgentTool{
		Tool: types.Tool{
			Name:        "echo",
			Description: "Echo text back",
			Parameters:  rawJSON(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		},
		Label: "Echo",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			toolExecuted = true
			return agent.AgentToolResult{Content: []types.Content{types.NewTextContent("echoed")}}, nil
		},
	}

	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:         true,
		BaseToolsOverride: map[string]agent.AgentTool{"echo": echoTool},
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.Context, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			callCount++
			stream := newMockAssistantStream()
			switch callCount {
			case 1:
				// First call: overloaded error
				msg := testAssistantMessage("", withStopReason(types.StopReasonError), withErrorMessage("overloaded_error"))
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
				stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonError, Message: &msg})
			case 2:
				// Second call (retry): text + tool_use
				msg := testAssistantMessage("Looking that up now.", func(m *types.AssistantMessage) {
					m.StopReason = types.StopReasonToolUse
					m.Content = []types.Content{
						types.NewTextContent("Looking that up now."),
						types.ToolCall{Kind: types.KindToolCall, ID: "call_1", Name: "echo", Arguments: map[string]any{"text": "hello"}},
					}
				})
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
				stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonToolUse, Message: &msg})
			default:
				// Third call (after tool result): final response
				msg := testAssistantMessage("Final answer.")
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
				stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &msg})
			}
			stream.End()
			return stream.AssistantMessageEventStream
		},
	})
	session.SettingsManager.ApplyOverrides(Settings{"retry": map[string]any{"enabled": true, "maxRetries": float64(3), "baseDelayMs": float64(1)}})
	if err := session.Prompt(context.Background(), "Test", nil); err != nil {
		t.Fatal(err)
	}

	// All three LLM calls must have completed
	if callCount != 3 {
		t.Fatalf("callCount = %d, want 3", callCount)
	}
	// Tool must have been executed
	if !toolExecuted {
		t.Fatal("tool was not executed")
	}
	// Agent must not be streaming after prompt returns
	if session.IsStreaming() {
		t.Fatal("session still streaming")
	}
	// A follow-up prompt must work (no "Agent is already processing" error)
	if err := session.Prompt(context.Background(), "Follow-up", nil); err != nil {
		if strings.Contains(err.Error(), "Agent is already processing") {
			t.Fatalf("follow-up prompt hit the streaming guard: %v", err)
		}
		t.Fatal(err)
	}
	if callCount != 4 {
		t.Fatalf("callCount after follow-up = %d, want 4", callCount)
	}
}
