// pi: test/agent-session-retry.test.ts — AgentSession auto-retry behavior.
package core

import (
	"context"
	"strings"
	"sync"
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
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
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
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
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
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
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

// --- v0.86.0 additions (pi: test/agent-session-retry.test.ts + retry-events) ---

// retryErrorStream builds a stream fn that fails the first `failCount` calls
// with overloaded_error and succeeds afterwards (the common retry fixture).
func retryErrorStream(failCount *int, successText string) agent.StreamFn {
	return func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		stream := newMockAssistantStream()
		var msg types.AssistantMessage
		if *failCount > 0 {
			*failCount--
			msg = testAssistantMessage("", withStopReason(types.StopReasonError), withErrorMessage("overloaded_error"))
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonError, Message: &msg})
		} else {
			msg = testAssistantMessage(successText)
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &msg})
		}
		stream.End()
		return stream.AssistantMessageEventStream
	}
}

// TestAgentSessionRetryCapsAgentDelay ports "caps agent retry delay"
// (regression #8826): baseDelayMs doubling is clamped to maxAgentDelayMs.
func TestAgentSessionRetryCapsAgentDelay(t *testing.T) {
	failCount := 4
	session, _, settingsManager := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey: true,
		StreamFn:  retryErrorStream(&failCount, "Success"),
	})
	defer session.Dispose()
	settingsManager.ApplyOverrides(Settings{"retry": map[string]any{
		"enabled": true, "maxRetries": float64(5), "baseDelayMs": float64(1), "maxAgentDelayMs": float64(5),
	}})

	var delays []int64
	session.Subscribe(func(event *AgentSessionEvent) {
		if event.Type == AgentSessionEventAutoRetryStart {
			delays = append(delays, event.DelayMs)
		}
	})

	if err := session.Prompt(context.Background(), "Test", nil); err != nil {
		t.Fatal(err)
	}
	// 1, 2, 4 double normally; the next 8 clamps to the 5ms cap.
	if len(delays) != 4 || delays[0] != 1 || delays[1] != 2 || delays[2] != 4 || delays[3] != 5 {
		t.Fatalf("delays = %v, want [1 2 4 5]", delays)
	}
}

// TestAgentSessionRetryFinalizesStateWhenAbortRequestedAfterFailedAttempt
// ports "finalizes retry state when abort is requested after a retry attempt
// fails" (regression #9340).
func TestAgentSessionRetryFinalizesStateWhenAbortRequestedAfterFailedAttempt(t *testing.T) {
	failCount := 99
	session, _, settingsManager := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey: true,
		StreamFn:  retryErrorStream(&failCount, "Success"),
	})
	defer session.Dispose()
	settingsManager.ApplyOverrides(Settings{"retry": map[string]any{
		"enabled": true, "maxRetries": float64(3), "baseDelayMs": float64(0),
	}})

	var mu sync.Mutex
	errorCount := 0
	var agentEnds []*AgentSessionEvent
	var retryEnds []*AgentSessionEvent
	session.Subscribe(func(event *AgentSessionEvent) {
		if event.Type == AgentSessionEventAutoRetryEnd {
			mu.Lock()
			retryEnds = append(retryEnds, event)
			mu.Unlock()
		}
		if event.Type != agent.AgentEventMessageEnd {
			return
		}
		assistant, ok := event.Message.(types.AssistantMessage)
		if !ok || assistant.StopReason != types.StopReasonError {
			return
		}
		mu.Lock()
		errorCount++
		shouldAbort := errorCount == 2
		mu.Unlock()
		if shouldAbort {
			// pi: void harness.session.abort() — the abort request is
			// recorded synchronously during message_end handling.
			_ = session.Abort(context.Background())
		}
	})
	session.Subscribe(func(event *AgentSessionEvent) {
		if event.Type == agent.AgentEventAgentEnd {
			mu.Lock()
			agentEnds = append(agentEnds, event)
			mu.Unlock()
		}
	})

	if err := session.Prompt(context.Background(), "test", nil); err != nil {
		t.Fatal(err)
	}

	if session.RetryAttempt() != 0 {
		t.Fatalf("retryAttempt = %d, want 0", session.RetryAttempt())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(agentEnds) == 0 {
		t.Fatal("missing agent_end event")
	}
	last := agentEnds[len(agentEnds)-1]
	if last.WillRetry {
		t.Fatal("agent_end willRetry = true, want false")
	}
	if len(retryEnds) == 0 {
		t.Fatal("missing auto_retry_end event")
	}
	retryEnd := retryEnds[len(retryEnds)-1]
	if retryEnd.Success {
		t.Fatal("auto_retry_end success = true, want false")
	}
	if retryEnd.Attempt != 1 {
		t.Fatalf("auto_retry_end attempt = %d, want 1", retryEnd.Attempt)
	}
	if retryEnd.FinalError != "Retry cancelled" {
		t.Fatalf("finalError = %q, want %q", retryEnd.FinalError, "Retry cancelled")
	}
}
