// pi: test/agent.test.ts. Mock assistant streams push their events
// synchronously instead of on a microtask; async-listener interleavings use
// channels and Prompt runs in a goroutine where pi left the promise
// un-awaited.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// ---- shared fixtures ----

func agentToolSchema(t *testing.T) json.RawMessage {
	return rawSchema(t, map[string]any{"type": "object", "properties": map[string]any{}})
}

func agentTestAssistantMessage(text string) types.AssistantMessage {
	return types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent(text)},
		API:        "openai-responses",
		Provider:   "openai",
		Model:      "mock",
		Usage:      types.Usage{Cost: types.Cost{}},
		StopReason: types.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}
}

func agentTestToolUseMessage(t *testing.T, content []types.Content) types.AssistantMessage {
	return types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    content,
		API:        "openai-responses",
		Provider:   "openai",
		Model:      "mock",
		Usage:      types.Usage{Cost: types.Cost{}},
		StopReason: types.StopReasonToolUse,
		Timestamp:  time.Now().UnixMilli(),
	}
}

func unusedStreamFunction(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
	panic("Unexpected stream call")
}

func textToolCall(id, name string, arguments map[string]any) types.ToolCall {
	return types.NewToolCall(id, name, arguments)
}

func waitFor[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel")
		var zero T
		return zero
	}
}

// ---- tests ----

func TestAgentUsesConfiguredDefaultStreamFn(t *testing.T) {
	calls := 0
	SetDefaultStreamFn(func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		calls++
		return mockStreamFn(doneEvent(agentTestAssistantMessage("fallback")))(ctx, model, llmContext, options)
	})
	defer SetDefaultStreamFn(nil)

	agent, err := NewAgent(AgentOptions{})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if err := agent.PromptText(context.Background(), "Hello"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestAgentCreatesWithDefaultState(t *testing.T) {
	agent, err := NewAgent(AgentOptions{StreamFn: unusedStreamFunction})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	state := agent.State()
	if state.SystemPrompt != "" {
		t.Errorf("systemPrompt = %q", state.SystemPrompt)
	}
	if state.Model == nil {
		t.Error("model is nil")
	}
	if state.ThinkingLevel != types.ModelThinkingOff {
		t.Errorf("thinkingLevel = %v, want off", state.ThinkingLevel)
	}
	if len(state.Tools) != 0 {
		t.Errorf("tools = %v, want empty", state.Tools)
	}
	if len(state.Messages) != 0 {
		t.Errorf("messages = %v, want empty", state.Messages)
	}
	if state.IsStreaming {
		t.Error("isStreaming = true")
	}
	if state.StreamingMessage != nil {
		t.Error("streamingMessage is set")
	}
	if len(state.PendingToolCalls) != 0 {
		t.Errorf("pendingToolCalls = %v, want empty", state.PendingToolCalls)
	}
	if state.ErrorMessage != "" {
		t.Errorf("errorMessage = %q", state.ErrorMessage)
	}
}

func TestAgentCreatesWithCustomInitialState(t *testing.T) {
	customModel := &types.Model{ID: "gpt-4o-mini", Name: "gpt-4o-mini", Provider: "openai"}
	agent, err := NewAgent(AgentOptions{
		StreamFn: unusedStreamFunction,
		InitialState: &AgentState{
			SystemPrompt:  "You are a helpful assistant.",
			Model:         customModel,
			ThinkingLevel: types.ModelThinkingLow,
		},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	state := agent.State()
	if state.SystemPrompt != "You are a helpful assistant." {
		t.Errorf("systemPrompt = %q", state.SystemPrompt)
	}
	if state.Model != customModel {
		t.Error("model identity mismatch")
	}
	if state.ThinkingLevel != types.ModelThinkingLow {
		t.Errorf("thinkingLevel = %v, want low", state.ThinkingLevel)
	}
}

func TestAgentSubscribeAndUnsubscribe(t *testing.T) {
	agent, err := NewAgent(AgentOptions{StreamFn: unusedStreamFunction})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	eventCount := 0
	unsubscribe := agent.Subscribe(func(ctx context.Context, event AgentEvent) {
		eventCount++
	})

	// No initial event on subscribe
	if eventCount != 0 {
		t.Fatalf("eventCount = %d, want 0", eventCount)
	}

	// State mutators don't emit events
	agent.State().SystemPrompt = "Test prompt"
	if eventCount != 0 {
		t.Fatalf("eventCount = %d, want 0", eventCount)
	}
	if agent.State().SystemPrompt != "Test prompt" {
		t.Errorf("systemPrompt = %q", agent.State().SystemPrompt)
	}

	// Unsubscribe should work
	unsubscribe()
	agent.State().SystemPrompt = "Another prompt"
	if eventCount != 0 {
		t.Errorf("eventCount = %d, want 0", eventCount)
	}
}

func TestAgentEmitsFullLifecycleForThrownRunFailures(t *testing.T) {
	agent, err := NewAgent(AgentOptions{
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			panic(errors.New("provider exploded"))
		},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	var events []AgentEventType
	agent.Subscribe(func(ctx context.Context, event AgentEvent) {
		events = append(events, event.Type)
	})

	if err := agent.PromptText(context.Background(), "hello"); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	want := []AgentEventType{
		AgentEventAgentStart,
		AgentEventTurnStart,
		AgentEventMessageStart,
		AgentEventMessageEnd,
		AgentEventMessageStart,
		AgentEventMessageEnd,
		AgentEventTurnEnd,
		AgentEventAgentEnd,
	}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for index, eventType := range want {
		if events[index] != eventType {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}

	state := agent.Snapshot()
	lastMessage := state.Messages[len(state.Messages)-1].(types.AssistantMessage)
	if lastMessage.StopReason != types.StopReasonError {
		t.Errorf("stopReason = %v, want error", lastMessage.StopReason)
	}
	if lastMessage.ErrorMessage != "provider exploded" {
		t.Errorf("errorMessage = %q", lastMessage.ErrorMessage)
	}
	if state.ErrorMessage != "provider exploded" {
		t.Errorf("state.errorMessage = %q", state.ErrorMessage)
	}
}

func TestAgentAwaitsSubscribersBeforePromptResolves(t *testing.T) {
	barrier := make(chan struct{})
	release := make(chan struct{})
	agent, err := NewAgent(AgentOptions{
		StreamFn: mockStreamFn(doneEvent(agentTestAssistantMessage("ok"))),
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	var listenerFinished atomic.Bool
	agent.Subscribe(func(ctx context.Context, event AgentEvent) {
		if event.Type == AgentEventAgentEnd {
			<-barrier
			listenerFinished.Store(true)
		}
	})

	promptDone := make(chan error, 1)
	go func() { promptDone <- agent.PromptText(context.Background(), "hello") }()

	time.Sleep(20 * time.Millisecond)
	if listenerFinished.Load() {
		t.Error("listener finished before barrier released")
	}
	if !agent.Snapshot().IsStreaming {
		t.Error("isStreaming = false during blocked listener")
	}

	close(release)
	close(barrier)
	if err := waitFor(t, promptDone); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	_ = release

	if !listenerFinished.Load() {
		t.Error("listener did not finish")
	}
	if agent.Snapshot().IsStreaming {
		t.Error("isStreaming = true after prompt")
	}
}

func TestAgentWaitForIdleWaitsForSubscribers(t *testing.T) {
	barrier := make(chan struct{})
	release := make(chan struct{})
	agent, err := NewAgent(AgentOptions{
		StreamFn: mockStreamFn(doneEvent(agentTestAssistantMessage("ok"))),
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	agent.Subscribe(func(ctx context.Context, event AgentEvent) {
		if event.Type == AgentEventMessageEnd {
			if _, ok := event.Message.(types.AssistantMessage); ok {
				<-barrier
			}
		}
	})

	promptDone := make(chan error, 1)
	go func() { promptDone <- agent.PromptText(context.Background(), "hello") }()

	// pi relies on microtask ordering (the run has started by the time
	// waitForIdle is called); pig polls for the run to register.
	for !agent.Snapshot().IsStreaming {
		time.Sleep(2 * time.Millisecond)
	}
	idleDone := make(chan struct{})
	go func() { agent.WaitForIdle(); close(idleDone) }()

	time.Sleep(20 * time.Millisecond)
	select {
	case <-idleDone:
		t.Error("waitForIdle resolved while listener blocked")
	default:
	}
	if !agent.Snapshot().IsStreaming {
		t.Error("isStreaming = false during blocked listener")
	}

	close(release)
	close(barrier)
	if err := waitFor(t, promptDone); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	select {
	case <-idleDone:
	case <-time.After(2 * time.Second):
		t.Fatal("waitForIdle did not resolve")
	}
	_ = release
}

// Regression (docs/fix/2026-09-14-tui-turn-memory-leak.md): finishRun must
// cancel the run context. pi's AbortController is garbage-collected when the
// run ends, but a Go cancelCtx stays registered in its parent's children map
// until canceled — one leaked registration per turn, pinning the run's whole
// context subtree. Once finishRun clears activeRun nobody observes the
// context anymore (Signal() returns nil), so Done must be closed.
func TestAgentCancelsRunContextAfterRunFinishes(t *testing.T) {
	runContexts := make(chan context.Context, 1)
	agent, err := NewAgent(AgentOptions{
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			runContexts <- ctx
			return mockStreamFn(doneEvent(agentTestAssistantMessage("ok")))(ctx, model, llmContext, options)
		},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if err := agent.PromptText(context.Background(), "hello"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	agent.WaitForIdle()
	var runCtx context.Context
	select {
	case runCtx = <-runContexts:
	case <-time.After(2 * time.Second):
		t.Fatal("streamFn never invoked")
	}
	select {
	case <-runCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("run context not canceled after the run finished (finishRun leaks the ctx registration)")
	}
}

func TestAgentPassesActiveRunContextToSubscribers(t *testing.T) {
	receivedSignal := make(chan context.Context, 1)
	agent, err := NewAgent(AgentOptions{
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := types.NewAssistantMessageEventStream()
			go func() {
				message := agentTestAssistantMessage("")
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &message})
				for {
					if ctx.Err() != nil {
						aborted := agentTestAssistantMessage("Aborted")
						stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonAborted, Message: &aborted})
						return
					}
					time.Sleep(5 * time.Millisecond)
				}
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	agent.Subscribe(func(ctx context.Context, event AgentEvent) {
		if event.Type == AgentEventAgentStart {
			receivedSignal <- ctx
		}
	})

	promptDone := make(chan error, 1)
	go func() { promptDone <- agent.PromptText(context.Background(), "hello") }()

	var signal context.Context
	select {
	case signal = <-receivedSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("listener did not receive the run context")
	}
	if signal.Err() != nil {
		t.Error("run context canceled before abort")
	}

	agent.Abort()
	if err := waitFor(t, promptDone); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	if signal.Err() == nil {
		t.Error("run context not canceled after abort")
	}
}

func TestAgentIgnoresToolUpdatesAfterToolSettles(t *testing.T) {
	var delayedUpdate atomic.Pointer[AgentToolUpdateCallback]
	var events atomic.Int64
	var updateEvents atomic.Int64

	tool := &AgentTool{
		Tool:  types.Tool{Name: "delayed_tool", Description: "Captures progress callbacks", Parameters: agentToolSchema(t)},
		Label: "Delayed Tool",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			delayedUpdate.Store(&onUpdate)
			onUpdate(AgentToolResult{
				Content: []types.Content{types.NewTextContent("running")},
				Details: map[string]any{"status": "running"},
			})
			return AgentToolResult{
				Content:   []types.Content{types.NewTextContent("ok")},
				Details:   map[string]any{"status": "done"},
				Terminate: true,
			}, nil
		},
	}
	agent, err := NewAgent(AgentOptions{
		InitialState: &AgentState{Tools: []AgentTool{*tool}},
		StreamFn: mockStreamFn(doneEvent(agentTestToolUseMessage(t, []types.Content{
			textToolCall("call-1", "delayed_tool", map[string]any{}),
		}))),
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	agent.Subscribe(func(ctx context.Context, event AgentEvent) {
		events.Add(1)
		if event.Type == AgentEventToolExecutionUpdate {
			updateEvents.Add(1)
		}
	})

	if err := agent.PromptText(context.Background(), "run tool"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	eventCountAfterPrompt := events.Load()

	if captured := delayedUpdate.Load(); captured != nil {
		(*captured)(AgentToolResult{
			Content: []types.Content{types.NewTextContent("late")},
			Details: map[string]any{"status": "late"},
		})
	}
	time.Sleep(10 * time.Millisecond)

	if updateEvents.Load() != 1 {
		t.Errorf("tool_execution_update count = %d, want 1", updateEvents.Load())
	}
	if events.Load() != eventCountAfterPrompt {
		t.Errorf("events = %d, want %d (late update ignored)", events.Load(), eventCountAfterPrompt)
	}
}

func TestAgentIgnoresSettledParallelToolUpdateWhileAnotherRuns(t *testing.T) {
	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	var settledToolUpdate atomic.Pointer[AgentToolUpdateCallback]
	var updateEvents atomic.Int64

	settledTool := &AgentTool{
		Tool:  types.Tool{Name: "settled_tool", Description: "Captures progress callbacks", Parameters: agentToolSchema(t)},
		Label: "Settled Tool",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			settledToolUpdate.Store(&onUpdate)
			return AgentToolResult{
				Content:   []types.Content{types.NewTextContent("done")},
				Details:   map[string]any{"status": "done"},
				Terminate: true,
			}, nil
		},
	}
	slowTool := &AgentTool{
		Tool:  types.Tool{Name: "slow_tool", Description: "Keeps the agent run active", Parameters: agentToolSchema(t)},
		Label: "Slow Tool",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			close(slowStarted)
			<-releaseSlow
			return AgentToolResult{
				Content:   []types.Content{types.NewTextContent("done")},
				Details:   map[string]any{"status": "done"},
				Terminate: true,
			}, nil
		},
	}
	agent, err := NewAgent(AgentOptions{
		InitialState: &AgentState{Tools: []AgentTool{*settledTool, *slowTool}},
		StreamFn: mockStreamFn(doneEvent(agentTestToolUseMessage(t, []types.Content{
			textToolCall("call-1", "settled_tool", map[string]any{}),
			textToolCall("call-2", "slow_tool", map[string]any{}),
		}))),
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	agent.Subscribe(func(ctx context.Context, event AgentEvent) {
		if event.Type == AgentEventToolExecutionUpdate {
			updateEvents.Add(1)
		}
	})

	promptDone := make(chan error, 1)
	go func() { promptDone <- agent.PromptText(context.Background(), "run tools") }()

	waitFor(t, slowStarted)
	time.Sleep(20 * time.Millisecond)

	if captured := settledToolUpdate.Load(); captured != nil {
		(*captured)(AgentToolResult{
			Content: []types.Content{types.NewTextContent("late")},
			Details: map[string]any{"status": "late"},
		})
	}
	time.Sleep(10 * time.Millisecond)
	if updateEvents.Load() != 0 {
		t.Errorf("tool_execution_update count = %d, want 0", updateEvents.Load())
	}

	close(releaseSlow)
	if err := waitFor(t, promptDone); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if updateEvents.Load() != 0 {
		t.Errorf("tool_execution_update count = %d, want 0", updateEvents.Load())
	}
}

func TestAgentStateMutators(t *testing.T) {
	agent, err := NewAgent(AgentOptions{StreamFn: unusedStreamFunction})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	// setSystemPrompt
	agent.State().SystemPrompt = "Custom prompt"
	if agent.State().SystemPrompt != "Custom prompt" {
		t.Errorf("systemPrompt = %q", agent.State().SystemPrompt)
	}

	// setModel
	newModel := &types.Model{ID: "gemini-2.5-flash", Provider: "google"}
	agent.State().Model = newModel
	if agent.State().Model != newModel {
		t.Error("model identity mismatch")
	}

	// setThinkingLevel
	agent.State().ThinkingLevel = types.ModelThinkingHigh
	if agent.State().ThinkingLevel != types.ModelThinkingHigh {
		t.Errorf("thinkingLevel = %v", agent.State().ThinkingLevel)
	}

	// setTools copies the top-level slice
	tools := []AgentTool{{Tool: types.Tool{Name: "test", Description: "test tool"}}}
	agent.SetTools(tools)
	if len(agent.State().Tools) != 1 || agent.State().Tools[0].Name != "test" {
		t.Fatalf("tools = %v", agent.State().Tools)
	}
	tools[0].Name = "mutated"
	if agent.State().Tools[0].Name != "test" {
		t.Error("SetTools did not copy the top-level slice")
	}

	// replaceMessages copies the top-level slice
	messages := []AgentMessage{createUserMessage("Hello")}
	agent.SetMessages(messages)
	if len(agent.State().Messages) != 1 {
		t.Fatalf("messages = %d", len(agent.State().Messages))
	}
	messages[0] = createUserMessage("mutated")
	if agent.State().Messages[0].(types.UserMessage).Content.String != "Hello" {
		t.Error("SetMessages did not copy the top-level slice")
	}

	// appendMessage
	agent.State().Messages = append(agent.State().Messages,
		agentTestAssistantMessage("Hi"))
	if len(agent.State().Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(agent.State().Messages))
	}

	// clearMessages
	agent.SetMessages(nil)
	if len(agent.State().Messages) != 0 {
		t.Fatalf("messages = %d, want 0", len(agent.State().Messages))
	}
}

func TestAgentSteeringQueue(t *testing.T) {
	agent, err := NewAgent(AgentOptions{StreamFn: unusedStreamFunction})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	message := createUserMessage("Steering message")
	agent.Steer(message)

	// The message is queued but not yet in state.messages
	for _, existing := range agent.State().Messages {
		if existing == AgentMessage(message) {
			t.Error("steering message leaked into transcript")
		}
	}
	if !agent.HasQueuedMessages() {
		t.Error("HasQueuedMessages = false")
	}
}

func TestAgentFollowUpQueue(t *testing.T) {
	agent, err := NewAgent(AgentOptions{StreamFn: unusedStreamFunction})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	message := createUserMessage("Follow-up message")
	agent.FollowUp(message)

	// The message is queued but not yet in state.messages
	for _, existing := range agent.State().Messages {
		if existing == AgentMessage(message) {
			t.Error("follow-up message leaked into transcript")
		}
	}
	if !agent.HasQueuedMessages() {
		t.Error("HasQueuedMessages = false")
	}
}

func TestAgentAbortWhenIdle(t *testing.T) {
	agent, err := NewAgent(AgentOptions{StreamFn: unusedStreamFunction})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	// Should not panic even if nothing is running
	agent.Abort()
	if agent.Signal() != nil {
		t.Error("signal set while idle")
	}
}

func TestAgentRejectsResetWhileProcessing(t *testing.T) {
	streamStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	var releaseOnce sync.Once
	agent, err := NewAgent(AgentOptions{
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := types.NewAssistantMessageEventStream()
			go func() {
				started := agentTestAssistantMessage("")
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &started})
				close(streamStarted)
				<-releaseResponse
				stream.Push(doneEvent(agentTestAssistantMessage("Done")))
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	promptDone := make(chan error, 1)
	go func() { promptDone <- agent.PromptText(context.Background(), "Hello") }()
	waitFor(t, streamStarted)

	state := agent.Snapshot()
	if !state.IsStreaming {
		t.Error("isStreaming = false during run")
	}
	if len(state.Messages) != 1 || state.Messages[0].MessageRole() != types.RoleUser {
		t.Errorf("messages = %v, want [user]", state.Messages)
	}
	if resetErr := agent.Reset(); resetErr == nil || resetErr.Error() != "Agent is already processing. Wait for completion before resetting." {
		t.Fatalf("reset error = %v", resetErr)
	}
	state = agent.Snapshot()
	if !state.IsStreaming {
		t.Error("isStreaming = false after rejected reset")
	}
	if len(state.Messages) != 1 {
		t.Errorf("messages = %d, want 1 (transcript intact)", len(state.Messages))
	}

	releaseOnce.Do(func() { close(releaseResponse) })
	if err := waitFor(t, promptDone); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	state = agent.Snapshot()
	if state.IsStreaming {
		t.Error("isStreaming = true after run")
	}
	if len(state.Messages) != 2 {
		t.Fatalf("messages = %d, want [user assistant]", len(state.Messages))
	}
}

func TestAgentRejectsPromptWhileStreaming(t *testing.T) {
	agent, err := NewAgent(AgentOptions{
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := types.NewAssistantMessageEventStream()
			go func() {
				started := agentTestAssistantMessage("")
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &started})
				for {
					if ctx.Err() != nil {
						aborted := agentTestAssistantMessage("Aborted")
						stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonAborted, Message: &aborted})
						return
					}
					time.Sleep(5 * time.Millisecond)
				}
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	promptDone := make(chan error, 1)
	go func() { promptDone <- agent.PromptText(context.Background(), "First message") }()
	time.Sleep(20 * time.Millisecond)
	if !agent.Snapshot().IsStreaming {
		t.Fatal("isStreaming = false during run")
	}

	if promptErr := agent.PromptText(context.Background(), "Second message"); promptErr == nil ||
		promptErr.Error() != "Agent is already processing a prompt. Use steer() or followUp() to queue messages, or wait for completion." {
		t.Fatalf("second prompt error = %v", promptErr)
	}

	agent.Abort()
	if err := waitFor(t, promptDone); err != nil {
		t.Fatalf("first prompt: %v", err)
	}
}

func TestAgentRejectsContinueWhileStreaming(t *testing.T) {
	agent, err := NewAgent(AgentOptions{
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := types.NewAssistantMessageEventStream()
			go func() {
				started := agentTestAssistantMessage("")
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &started})
				for {
					if ctx.Err() != nil {
						aborted := agentTestAssistantMessage("Aborted")
						stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonAborted, Message: &aborted})
						return
					}
					time.Sleep(5 * time.Millisecond)
				}
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	promptDone := make(chan error, 1)
	go func() { promptDone <- agent.PromptText(context.Background(), "First message") }()
	time.Sleep(20 * time.Millisecond)
	if !agent.Snapshot().IsStreaming {
		t.Fatal("isStreaming = false during run")
	}

	if continueErr := agent.Continue(context.Background()); continueErr == nil ||
		continueErr.Error() != "Agent is already processing. Wait for completion before continuing." {
		t.Fatalf("continue error = %v", continueErr)
	}

	agent.Abort()
	if err := waitFor(t, promptDone); err != nil {
		t.Fatalf("prompt: %v", err)
	}
}

func TestAgentContinueProcessesQueuedFollowUpsAfterAssistantTurn(t *testing.T) {
	agent, err := NewAgent(AgentOptions{
		StreamFn: mockStreamFn(doneEvent(agentTestAssistantMessage("Processed"))),
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	agent.SetMessages([]AgentMessage{
		types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{Parts: []types.Content{types.NewTextContent("Initial")}},
			Timestamp: time.Now().UnixMilli() - 10,
		},
		agentTestAssistantMessage("Initial response"),
	})

	agent.FollowUp(types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.NewTextContent("Queued follow-up")}},
		Timestamp: time.Now().UnixMilli(),
	})

	if err := agent.Continue(context.Background()); err != nil {
		t.Fatalf("continue: %v", err)
	}

	hasQueuedFollowUp := false
	messages := agent.State().Messages
	for _, message := range messages {
		if user, ok := message.(types.UserMessage); ok {
			for _, part := range user.Content.Parts {
				if text, ok := part.(types.TextContent); ok && text.Text == "Queued follow-up" {
					hasQueuedFollowUp = true
				}
			}
		}
	}
	if !hasQueuedFollowUp {
		t.Error("queued follow-up not found in transcript")
	}
	if last := messages[len(messages)-1]; last.MessageRole() != types.RoleAssistant {
		t.Errorf("last message role = %v, want assistant", last.MessageRole())
	}
}

func TestAgentContinueKeepsOneAtATimeSteeringFromAssistantTail(t *testing.T) {
	var responseCount atomic.Int64
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		count := responseCount.Add(1)
		return mockStreamFn(doneEvent(agentTestAssistantMessage("Processed "+strconv.FormatInt(count, 10))))(ctx, model, llmContext, options)
	}
	agent, err := NewAgent(AgentOptions{StreamFn: streamFn})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	agent.SetMessages([]AgentMessage{
		types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{Parts: []types.Content{types.NewTextContent("Initial")}},
			Timestamp: time.Now().UnixMilli() - 10,
		},
		agentTestAssistantMessage("Initial response"),
	})

	agent.Steer(types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.NewTextContent("Steering 1")}},
		Timestamp: time.Now().UnixMilli(),
	})
	agent.Steer(types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.NewTextContent("Steering 2")}},
		Timestamp: time.Now().UnixMilli() + 1,
	})

	if err := agent.Continue(context.Background()); err != nil {
		t.Fatalf("continue: %v", err)
	}

	messages := agent.State().Messages
	recent := messages[len(messages)-4:]
	wantRoles := []types.Role{types.RoleUser, types.RoleAssistant, types.RoleUser, types.RoleAssistant}
	if len(recent) != len(wantRoles) {
		t.Fatalf("recent messages = %d, want 4", len(recent))
	}
	for index, role := range wantRoles {
		if recent[index].MessageRole() != role {
			t.Fatalf("recent roles = %v, want %v", recent, wantRoles)
		}
	}
	if responseCount.Load() != 2 {
		t.Errorf("responseCount = %d, want 2", responseCount.Load())
	}
}

func TestAgentLegacyPrepareNextTurnReceivesRunContext(t *testing.T) {
	tool := &AgentTool{
		Tool:  types.Tool{Name: "noop", Description: "Noop tool", Parameters: agentToolSchema(t)},
		Label: "Noop",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("ok")},
				Details: map[string]any{},
			}, nil
		},
	}
	var requestCount atomic.Int64
	var sawRunContext atomic.Bool
	agent, err := NewAgent(AgentOptions{
		InitialState: &AgentState{Tools: []AgentTool{*tool}},
		PrepareNextTurn: func(ctx context.Context) *AgentLoopTurnUpdate {
			sawRunContext.Store(ctx != nil && ctx.Err() == nil)
			return nil
		},
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			count := requestCount.Add(1)
			var message types.AssistantMessage
			if count == 1 {
				message = agentTestToolUseMessage(t, []types.Content{
					textToolCall("tool-1", "noop", map[string]any{}),
				})
			} else {
				message = agentTestAssistantMessage("done")
			}
			return mockStreamFn(doneEvent(message))(ctx, model, llmContext, options)
		},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	if err := agent.PromptText(context.Background(), "start"); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	if requestCount.Load() != 2 {
		t.Errorf("requestCount = %d, want 2", requestCount.Load())
	}
	if !sawRunContext.Load() {
		t.Error("prepareNextTurn did not receive a live run context")
	}
}

func TestAgentForwardsShouldStopAfterTurn(t *testing.T) {
	tool := &AgentTool{
		Tool:  types.Tool{Name: "noop", Description: "Noop tool", Parameters: agentToolSchema(t)},
		Label: "Noop",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("tool complete")},
				Details: map[string]any{},
			}, nil
		},
	}
	var requestCount atomic.Int64
	var sawRunContext atomic.Bool
	var callbackContextRoles []types.Role
	agent, err := NewAgent(AgentOptions{
		InitialState: &AgentState{Tools: []AgentTool{*tool}},
		ShouldStopAfterTurn: func(ctx context.Context, context ShouldStopAfterTurnContext) bool {
			sawRunContext.Store(ctx != nil && ctx.Err() == nil)
			callbackContextRoles = nil
			for _, message := range context.Context.Messages {
				callbackContextRoles = append(callbackContextRoles, message.MessageRole())
			}
			return true
		},
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			count := requestCount.Add(1)
			var message types.AssistantMessage
			if count == 1 {
				message = agentTestToolUseMessage(t, []types.Content{
					textToolCall("tool-1", "noop", map[string]any{}),
				})
			} else {
				message = agentTestAssistantMessage("should not run")
			}
			return mockStreamFn(doneEvent(message))(ctx, model, llmContext, options)
		},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	if err := agent.PromptText(context.Background(), "start"); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	if requestCount.Load() != 1 {
		t.Errorf("requestCount = %d, want 1", requestCount.Load())
	}
	if !sawRunContext.Load() {
		t.Error("shouldStopAfterTurn did not receive a live run context")
	}
	// v0.86.0: the loop announces the tool loadout with a leading system
	// message (pi: expect(callbackContextRoles).toEqual(["system", ...])).
	wantRoles := []types.Role{types.RoleSystem, types.RoleUser, types.RoleAssistant, types.RoleToolResult}
	if len(callbackContextRoles) != len(wantRoles) {
		t.Fatalf("context roles = %v, want %v", callbackContextRoles, wantRoles)
	}
	for index, role := range wantRoles {
		if callbackContextRoles[index] != role {
			t.Fatalf("context roles = %v, want %v", callbackContextRoles, wantRoles)
		}
	}
}

func TestAgentForwardsSessionID(t *testing.T) {
	var receivedSessionID atomic.Pointer[string]
	agent, err := NewAgent(AgentOptions{
		SessionID: "session-abc",
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			sessionID := options.SessionID
			receivedSessionID.Store(&sessionID)
			return mockStreamFn(doneEvent(agentTestAssistantMessage("ok")))(ctx, model, llmContext, options)
		},
	})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	if err := agent.PromptText(context.Background(), "hello"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if receivedSessionID.Load() == nil || *receivedSessionID.Load() != "session-abc" {
		t.Errorf("sessionId = %v, want session-abc", receivedSessionID.Load())
	}

	// Test setter
	agent.SessionID = "session-def"
	if agent.SessionID != "session-def" {
		t.Errorf("sessionId = %q", agent.SessionID)
	}

	if err := agent.PromptText(context.Background(), "hello again"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if receivedSessionID.Load() == nil || *receivedSessionID.Load() != "session-def" {
		t.Errorf("sessionId = %v, want session-def", receivedSessionID.Load())
	}
}

// --- pi v0.86.0 agent.test.ts: transcript state / tool-change declaration.

// agentTestTool builds a minimal executable tool (pi: createTool).
func agentTestTool(name string) AgentTool {
	return AgentTool{
		Tool: types.Tool{Name: name, Description: name + " tool", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			return AgentToolResult{Content: []types.Content{types.NewTextContent("ok")}}, nil
		},
	}
}

// agentTestDoneStreamFn returns a stream fn that records the system-message
// tool deltas of every request and answers "done" (pi: the request recorder
// in the tool-change tests).
func agentTestDoneStreamFn(requests *[][]string) StreamFn {
	return func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		var deltas []string
		for _, message := range llmContext.Messages {
			if sys, ok := message.(types.SystemMessage); ok {
				added := "+"
				for i, tool := range sys.ToolsAdded {
					if i > 0 {
						added += ","
					}
					added += tool.Name
				}
				deltas = append(deltas, added)
				removed := "-"
				for i, ref := range sys.ToolsRemoved {
					if i > 0 {
						removed += ","
					}
					removed += ref.Name
				}
				deltas = append(deltas, removed)
			}
		}
		*requests = append(*requests, deltas)
		return mockStreamFn(doneEvent(agentTestAssistantMessage("done")))(ctx, model, llmContext, options)
	}
}

func agentSystemMessageAt(messages []AgentMessage, index int) types.SystemMessage {
	message := messages[index]
	if sys, ok := message.(types.SystemMessage); ok {
		return sys
	}
	if sys, ok := message.(*types.SystemMessage); ok {
		return *sys
	}
	panic(fmt.Sprintf("messages[%d] = %T, want SystemMessage", index, message))
}

func TestAgentConvertsInitialPromptAndToolsIntoTranscriptState(t *testing.T) {
	tool := agentTestTool("echo")
	agent, err := NewAgent(AgentOptions{
		InitialState: &AgentState{SystemPrompt: "You are helpful.", Tools: []AgentTool{tool}},
		StreamFn:     unusedStreamFunction,
	})
	if err != nil {
		t.Fatal(err)
	}
	initial := agentSystemMessageAt(agent.State().Messages, 0)
	if initial.Role != types.RoleSystem || initial.Content.String != "You are helpful." {
		t.Fatalf("initial = %+v", initial)
	}
	if len(initial.ToolsAdded) != 1 || initial.ToolsAdded[0].Name != "echo" {
		t.Fatalf("toolsAdded = %+v", initial.ToolsAdded)
	}
}

func TestAgentDeclaresToolLoadoutChangesBeforeNextRequest(t *testing.T) {
	first := agentTestTool("first")
	second := agentTestTool("second")
	var requests [][]string
	agent, err := NewAgent(AgentOptions{
		InitialState: &AgentState{SystemPrompt: "You are helpful.", Tools: []AgentTool{first}},
		StreamFn:     agentTestDoneStreamFn(&requests),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := agent.PromptText(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	agent.State().Tools = []AgentTool{second}
	if err := agent.PromptText(context.Background(), "two"); err != nil {
		t.Fatal(err)
	}
	if err := agent.PromptText(context.Background(), "three"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"+first", "-"},
		{"+first", "-", "+second", "-first"},
		{"+first", "-", "+second", "-first"},
	}
	if len(requests) != len(want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
	for i := range want {
		if !reflect.DeepEqual(requests[i], want[i]) {
			t.Fatalf("requests[%d] = %v, want %v", i, requests[i], want[i])
		}
	}

	var update *types.SystemMessage
	for _, message := range agent.State().Messages {
		if sys, ok := message.(types.SystemMessage); ok && len(sys.ToolsRemoved) > 0 {
			sysCopy := sys
			update = &sysCopy
		}
	}
	if update == nil || len(update.ToolsAdded) != 1 || update.ToolsAdded[0].Name != "second" ||
		len(update.ToolsRemoved) != 1 || update.ToolsRemoved[0].Name != "first" {
		t.Fatalf("update = %+v", update)
	}
}

func TestAgentMergesToolChangesIntoPendingSystemMessage(t *testing.T) {
	tool := agentTestTool("echo")
	agent, err := NewAgent(AgentOptions{
		InitialState: &AgentState{SystemPrompt: "You are helpful."},
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			count := 0
			for _, message := range llmContext.Messages {
				if message.MessageRole() == types.RoleSystem {
					count++
				}
			}
			if count != 2 {
				t.Errorf("system messages in transcript = %d, want 2", count)
			}
			return mockStreamFn(doneEvent(agentTestAssistantMessage("done")))(ctx, model, llmContext, options)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	agent.State().Tools = []AgentTool{tool}
	err = agent.Prompt(context.Background(),
		types.SystemMessage{
			Role:      types.RoleSystem,
			Content:   types.SystemContent{},
			Sections:  &types.SystemSections{Keys: []string{"skills"}, Values: map[string]*string{"skills": lo.ToPtr("<skills>x</skills>")}},
			Timestamp: 1,
		},
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	merged := agentSystemMessageAt(agent.State().Messages, 1)
	if merged.Role != types.RoleSystem || merged.Content.String != "" || merged.Timestamp != 1 {
		t.Fatalf("merged = %+v", merged)
	}
	if merged.Sections == nil || len(merged.Sections.Keys) != 1 || merged.Sections.Keys[0] != "skills" {
		t.Fatalf("sections = %+v", merged.Sections)
	}
	if len(merged.ToolsAdded) != 1 || merged.ToolsAdded[0].Name != "echo" || merged.ToolsAdded[0].Description != "echo tool" {
		t.Fatalf("toolsAdded = %+v", merged.ToolsAdded)
	}
}

func TestAgentRewritesPendingToolDeclarationsToMatchExecutableSet(t *testing.T) {
	agent, err := NewAgent(AgentOptions{
		InitialState: &AgentState{SystemPrompt: "You are helpful.", Tools: []AgentTool{agentTestTool("first")}},
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			return mockStreamFn(doneEvent(agentTestAssistantMessage("done")))(ctx, model, llmContext, options)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The pending message claims to add `second` and remove `first`, but the
	// executable set still has `first` and lacks `second`: the executable set
	// wins.
	err = agent.Prompt(context.Background(),
		types.SystemMessage{
			Role:         types.RoleSystem,
			Content:      types.SystemContent{},
			Sections:     &types.SystemSections{Keys: []string{"note"}, Values: map[string]*string{"note": lo.ToPtr("<note>x</note>")}},
			ToolsAdded:   []types.Tool{utils.ToToolDeclaration(agentTestTool("second").Tool)},
			ToolsRemoved: []types.ToolReference{{Name: "first"}},
			Timestamp:    1,
		},
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := agentSystemMessageAt(agent.State().Messages, 1)
	if rewritten.Role != types.RoleSystem || rewritten.Content.String != "" || rewritten.Timestamp != 1 {
		t.Fatalf("rewritten = %+v", rewritten)
	}
	if rewritten.Sections == nil || len(rewritten.Sections.Keys) != 1 || rewritten.Sections.Keys[0] != "note" {
		t.Fatalf("sections = %+v", rewritten.Sections)
	}
	if len(rewritten.ToolsAdded) != 0 || len(rewritten.ToolsRemoved) != 0 {
		t.Fatalf("tool fields = %+v / %+v", rewritten.ToolsAdded, rewritten.ToolsRemoved)
	}
	current := utils.GetCurrentSystemMessage(agent.State().Messages)
	if current == nil || len(current.ToolsAdded) != 1 || current.ToolsAdded[0].Name != "first" {
		t.Fatalf("current = %+v", current)
	}
}

func TestAgentRestoresTranscriptBaselineOnReset(t *testing.T) {
	tool := agentTestTool("echo")
	agent, err := NewAgent(AgentOptions{
		InitialState: &AgentState{
			SystemPrompt: "You are helpful.",
			Tools:        []AgentTool{tool},
			Messages:     []AgentMessage{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "old"}, Timestamp: 1}},
		},
		StreamFn: unusedStreamFunction,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := agent.Reset(); err != nil {
		t.Fatal(err)
	}

	if len(agent.State().Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(agent.State().Messages))
	}
	initial := agentSystemMessageAt(agent.State().Messages, 0)
	if initial.Role != types.RoleSystem || initial.Content.String != "You are helpful." {
		t.Fatalf("initial = %+v", initial)
	}
	if len(initial.ToolsAdded) != 1 || initial.ToolsAdded[0].Name != "echo" {
		t.Fatalf("toolsAdded = %+v", initial.ToolsAdded)
	}
}
