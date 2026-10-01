// pi: test/agent-session-concurrent.test.ts — AgentSession concurrent prompt
// guard, queueing, and event ordering.
//
// pi's tests replace _extensionRunner with mock objects; pig builds a real
// ExtensionRunner with injected Extension handlers (the runner's dispatch is
// implemented, so handlers exercise the real path).
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

// waitForStreaming polls until the session is streaming (pi: the 10ms tick).
func waitForStreaming(t *testing.T, session *AgentSession) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if session.IsStreaming() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("session did not start streaming")
}

// createConcurrentSession mirrors pi's local createSession: a stream function
// that responds to abort.
func createConcurrentSession(t *testing.T) *AgentSession {
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := newMockAssistantStream()
			go func() {
				partial := testAssistantMessage("")
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &partial})
				for {
					select {
					case <-ctx.Done():
						// pi's mock keeps the message's default stopReason
						// "stop" (createAssistantMessage), so the loop treats
						// the aborted push as a completed turn and continues
						// with queued steering in the same run.
						aborted := testAssistantMessage("Aborted")
						stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonAborted, Message: &aborted})
						return
					case <-time.After(5 * time.Millisecond):
					}
				}
			}()
			return stream.AssistantMessageEventStream
		},
	})
	return session
}

func TestAgentSessionConcurrentPromptThrowsWhileStreaming(t *testing.T) {
	session := createConcurrentSession(t)

	firstPromptDone := make(chan error, 1)
	go func() { firstPromptDone <- session.Prompt(context.Background(), "First message", nil) }()

	waitForStreaming(t, session)

	err := session.Prompt(context.Background(), "Second message", nil)
	if err == nil || !strings.Contains(err.Error(), "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message.") {
		t.Fatalf("second prompt error = %v", err)
	}

	// Cleanup
	_ = session.Abort(context.Background())
	if err := <-firstPromptDone; err != nil && !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("first prompt = %v", err)
	}
}

func TestAgentSessionConcurrentSteerWhileStreaming(t *testing.T) {
	session := createConcurrentSession(t)

	firstPromptDone := make(chan error, 1)
	go func() { firstPromptDone <- session.Prompt(context.Background(), "First message", nil) }()

	waitForStreaming(t, session)

	// steer should work while streaming
	if _, err := session.Steer(context.Background(), "Steering message", nil, nil); err != nil {
		t.Fatal(err)
	}
	if session.PendingMessageCount() != 1 {
		t.Fatalf("pendingMessageCount = %d", session.PendingMessageCount())
	}

	// Cleanup
	_ = session.Abort(context.Background())
	<-firstPromptDone
}

func TestAgentSessionConcurrentFollowUpWhileStreaming(t *testing.T) {
	session := createConcurrentSession(t)

	firstPromptDone := make(chan error, 1)
	go func() { firstPromptDone <- session.Prompt(context.Background(), "First message", nil) }()

	waitForStreaming(t, session)

	// followUp should work while streaming
	if _, err := session.FollowUp(context.Background(), "Follow-up message", nil, nil); err != nil {
		t.Fatal(err)
	}
	if session.PendingMessageCount() != 1 {
		t.Fatalf("pendingMessageCount = %d", session.PendingMessageCount())
	}

	// Cleanup
	_ = session.Abort(context.Background())
	<-firstPromptDone
}

func TestAgentSessionConcurrentQueuesExtensionOriginSteeringWhileStreaming(t *testing.T) {
	var mu sync.Mutex
	sawSteeringMessage := false
	var lastInputSource InputSource
	var queueEvents [][]string

	model := testModel()
	extensionsResult := &LoadExtensionsResult{
		Extensions: []*Extension{
			{Path: "test-extension"},
			{
				Path:     "input-extension",
				Handlers: map[string][]ExtensionHandler{},
			},
		},
		Runtime: CreateExtensionRuntime(),
	}

	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:        true,
		ExtensionsResult: []*LoadExtensionsResult{extensionsResult},
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := newMockAssistantStream()
			go func() {
				for _, message := range llmContext.Messages {
					if userMessage, ok := message.(types.UserMessage); ok {
						if agentMessageText(userMessage) == "Steer from extension" {
							mu.Lock()
							sawSteeringMessage = true
							mu.Unlock()
							steered := testAssistantMessage("Steered", withModelRef(model))
							stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &steered})
							stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &steered})
							return
						}
					}
				}
				partial := testAssistantMessage("")
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &partial})
				for {
					select {
					case <-ctx.Done():
						// pi's mock keeps the message's default stopReason "stop".
						aborted := testAssistantMessage("Aborted", withModelRef(model))
						stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonAborted, Message: &aborted})
						return
					case <-time.After(5 * time.Millisecond):
					}
				}
			}()
			return stream.AssistantMessageEventStream
		},
	})
	_ = model

	// Register the input handler extension (pi: pi.on("input", ...) captures
	// event.source).
	extensionsResult.Extensions[1].Handlers["input"] = []ExtensionHandler{
		func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			if inputEvent, ok := event.(*InputEvent); ok {
				mu.Lock()
				lastInputSource = inputEvent.Source
				mu.Unlock()
			}
			return nil, nil
		},
	}
	// Rebind the runner so the handler set is visible (the loader would have
	// registered before the runner was built; the test injects after).
	session.extensionRunner.extensions = extensionsResult.Extensions

	session.Subscribe(func(event *AgentSessionEvent) {
		if event.Type == AgentSessionEventQueueUpdate {
			mu.Lock()
			queueEvents = append(queueEvents, append([]string{}, event.Steering...))
			mu.Unlock()
		}
	})

	firstPromptDone := make(chan error, 1)
	go func() { firstPromptDone <- session.Prompt(context.Background(), "First message", &PromptOptions{}) }()
	waitForStreaming(t, session)

	// pi: pi.sendUserMessage("Steer from extension", { deliverAs: "steer" })
	// via the extension runtime API.
	runner := session.GetExtensionRunner()
	runner.runtime.SendUserMessage(context.Background(), "Steer from extension", &SendUserMessageOptions{DeliverAs: "steer"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if session.PendingMessageCount() == 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if session.PendingMessageCount() != 1 {
		t.Fatalf("pendingMessageCount = %d", session.PendingMessageCount())
	}
	steering := session.GetSteeringMessages()
	found := false
	for _, message := range steering {
		if message == "Steer from extension" {
			found = true
		}
	}
	if !found {
		t.Fatalf("steering = %v", steering)
	}
	mu.Lock()
	source := lastInputSource
	queues := queueEvents
	mu.Unlock()
	if source != InputSourceExtension {
		t.Fatalf("input source = %q", source)
	}
	sawInQueueEvent := false
	for _, steeringSnapshot := range queues {
		for _, message := range steeringSnapshot {
			if message == "Steer from extension" {
				sawInQueueEvent = true
			}
		}
	}
	if !sawInQueueEvent {
		t.Fatalf("queue events = %v", queueEvents)
	}

	_ = session.Abort(context.Background())
	<-firstPromptDone

	mu.Lock()
	delivered := sawSteeringMessage
	mu.Unlock()
	if !delivered {
		t.Fatal("steering message was never delivered to the model")
	}
}

func TestAgentSessionConcurrentPromptAfterPreviousCompletes(t *testing.T) {
	// Create session with a stream that completes immediately
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := newMockAssistantStream()
			done := testAssistantMessage("Done", withModelRef(model))
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &done})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &done})
			return stream.AssistantMessageEventStream
		},
	})

	// First prompt completes
	if err := session.Prompt(context.Background(), "First message", nil); err != nil {
		t.Fatal(err)
	}

	// Should not be streaming anymore
	if session.IsStreaming() {
		t.Fatal("session still streaming")
	}

	// Second prompt should work
	if err := session.Prompt(context.Background(), "Second message", nil); err != nil {
		t.Fatal(err)
	}
}

// dummyTool is the shared dummy tool of the ordering tests (pi: the local
// `tool` object).
func dummyTool() agent.AgentTool {
	return agent.AgentTool{
		Tool: types.Tool{
			Name:        "dummy",
			Description: "Dummy tool",
			Parameters:  rawJSON(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`),
		},
		Label: "dummy",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			q := ""
			if args, ok := params.(map[string]any); ok {
				if value, ok := args["q"].(string); ok {
					q = value
				}
			}
			return agent.AgentToolResult{
				Content: []types.Content{types.NewTextContent("result:" + q)},
				Details: map[string]any{},
			}, nil
		},
	}
}

func TestAgentSessionConcurrentWaitsForQueuedAgentEventsBeforeEmittingToolCall(t *testing.T) {
	var mu sync.Mutex
	var snapshots [][]string

	session, sessionManager, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:         true,
		BaseToolsOverride: map[string]agent.AgentTool{"dummy": dummyTool()},
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := newMockAssistantStream()
			go func() {
				toolResultCount := 0
				for _, message := range llmContext.Messages {
					if message.MessageRole() == types.RoleToolResult {
						toolResultCount++
					}
				}
				if toolResultCount > 0 {
					done := testAssistantMessage("done", withModelRef(model), withUsage(types.Usage{Input: 1, Output: 1, TotalTokens: 2}))
					partial := done
					partial.Content = nil
					stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &partial})
					stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &done})
					return
				}
				message := testAssistantMessage("", withModelRef(model), withUsage(types.Usage{Input: 1, Output: 1, TotalTokens: 2}), withStopReason(types.StopReasonToolUse))
				message.Content = []types.Content{
					types.ToolCall{Kind: types.KindToolCall, ID: "toolu_1", Name: "dummy", Arguments: map[string]any{"q": "x"}},
					types.ToolCall{Kind: types.KindToolCall, ID: "toolu_2", Name: "dummy", Arguments: map[string]any{"q": "y"}},
				}
				partial := message
				partial.Content = nil
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &partial})
				stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonToolUse, Message: &message})
			}()
			return stream.AssistantMessageEventStream
		},
		ExtensionsResult: []*LoadExtensionsResult{{
			Extensions: []*Extension{
				{
					Path:     "tool-call-snapshot",
					Handlers: map[string][]ExtensionHandler{},
				},
			},
			Runtime: CreateExtensionRuntime(),
		}},
	})
	session.extensionRunner.extensions[0].Handlers["tool_call"] = []ExtensionHandler{
		func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			mu.Lock()
			var roles []string
			for _, entry := range sessionManager.GetEntries() {
				if messageEntry, ok := entry.(*SessionMessageEntry); ok {
					roles = append(roles, string(messageEntry.Message.MessageRole()))
				}
			}
			snapshots = append(snapshots, roles)
			mu.Unlock()
			return nil, nil
		},
	}

	if err := session.Prompt(context.Background(), "hi", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	mu.Lock()
	defer mu.Unlock()
	if len(snapshots) != 2 {
		t.Fatalf("snapshots = %v, want 2", snapshots)
	}
	for i, snapshot := range snapshots {
		if strings.Join(snapshot, ",") != "system,user,assistant" {
			t.Fatalf("snapshot %d = %v, want [system user assistant]", i, snapshot)
		}
	}
}

func TestAgentSessionConcurrentPersistsMessageEndEventsInOrderWithSlowExtensionHandlers(t *testing.T) {
	session, sessionManager, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:         true,
		BaseToolsOverride: map[string]agent.AgentTool{"dummy": dummyTool()},
		// pi constructs the Agent directly with initialState.tools=[tool];
		// the leading system message then declares the tool, and after the
		// v0.87.0 projection replaces the loop context the declaration is
		// re-synthesized as a second system entry at the next turn.
		InitialTools: []agent.AgentTool{dummyTool()},
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := newMockAssistantStream()
			go func() {
				hasToolResult := false
				for _, message := range llmContext.Messages {
					if message.MessageRole() == types.RoleToolResult {
						hasToolResult = true
					}
				}
				if hasToolResult {
					done := testAssistantMessage("done", withModelRef(model), withUsage(types.Usage{Input: 1, Output: 1, TotalTokens: 2}))
					partial := done
					partial.Content = nil
					stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &partial})
					stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &done})
					return
				}
				message := testAssistantMessage("", withModelRef(model), withUsage(types.Usage{Input: 1, Output: 1, TotalTokens: 2}), withStopReason(types.StopReasonToolUse))
				message.Content = []types.Content{
					types.NewTextContent("calling tool"),
					types.ToolCall{Kind: types.KindToolCall, ID: "toolu_1", Name: "dummy", Arguments: map[string]any{"q": "x"}},
				}
				partial := message
				partial.Content = nil
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &partial})
				stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonToolUse, Message: &message})
			}()
			return stream.AssistantMessageEventStream
		},
		ExtensionsResult: []*LoadExtensionsResult{{
			Extensions: []*Extension{
				{
					Path:     "slow-message-end",
					Handlers: map[string][]ExtensionHandler{},
				},
			},
			Runtime: CreateExtensionRuntime(),
		}},
	})
	session.extensionRunner.extensions[0].Handlers["message_end"] = []ExtensionHandler{
		func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			if _, ok := event.(*MessageEndEvent); ok {
				time.Sleep(40 * time.Millisecond)
			}
			return nil, nil
		},
	}

	if err := session.Prompt(context.Background(), "hi", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	var roles []string
	for _, entry := range sessionManager.GetEntries() {
		if messageEntry, ok := entry.(*SessionMessageEntry); ok {
			roles = append(roles, string(messageEntry.Message.MessageRole()))
		}
	}
	if strings.Join(roles, ",") != "system,user,assistant,toolResult,system,assistant" {
		t.Fatalf("roles = %v", roles)
	}
}
