// agent_session_custom_message_run_test.go — pi: a447ba11f (v1.2.0),
// test/suite/agent-session-prompt.test.ts "runs started by a custom message".
// A run started by sendMessage(..., { triggerTurn: true }) now goes through
// the same setup as prompt(): compaction check, before_agent_start, tool
// loadout, nextTurn messages, handler messages, and the system patch.
package core

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/samber/lo"
)

// customMessageRunNoopTool is pi's noop tool: a tool call forces a second
// request inside the same run, which previously patched away the sections the
// before_agent_start handler had added.
func customMessageRunNoopTool() *tools.ToolDefinition {
	return &tools.ToolDefinition{
		Name:        "noop",
		Label:       "Noop",
		Description: "Does nothing",
		Parameters:  rawJSON(`{"type":"object","properties":{}}`),
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx tools.ToolExecuteContext) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{Content: []types.Content{types.NewTextContent("ok")}, Details: map[string]any{}}, nil
		},
	}
}

func TestCustomMessageRunKeepsSameSystemPromptForEveryRequest(t *testing.T) {
	mu := &sync.Mutex{}
	var events []*BeforeAgentStartEvent
	var prompts []string
	call := 0

	extensionsResult := &LoadExtensionsResult{
		Extensions: []*Extension{{
			Path:     "custom-message-run",
			Tools:    map[string]RegisteredTool{"noop": {Definition: customMessageRunNoopTool()}},
			Handlers: map[string][]ExtensionHandler{},
		}},
		Runtime: CreateExtensionRuntime(),
	}

	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			mu.Lock()
			call++
			current := call
			prompts = append(prompts, aiutils.GetCurrentSystemPrompt(llmContext.Messages))
			mu.Unlock()
			stream := newMockAssistantStream()
			var msg types.AssistantMessage
			switch current {
			case 2:
				msg = testAssistantMessage("hi", withModelRef(model), func(m *types.AssistantMessage) {
					m.StopReason = types.StopReasonToolUse
					m.Content = []types.Content{types.NewTextContent("hi"), fauxToolCall("noop", map[string]any{})}
				})
			case 3:
				msg = testAssistantMessage("done", withModelRef(model))
			default:
				msg = testAssistantMessage("hi", withModelRef(model))
			}
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: msg.StopReason, Message: &msg})
			stream.End()
			return stream.AssistantMessageEventStream
		},
		ExtensionsResult: []*LoadExtensionsResult{extensionsResult},
	})
	defer session.Dispose()
	session.extensionRunner.extensions = extensionsResult.Extensions
	extensionsResult.Extensions[0].Handlers["before_agent_start"] = []ExtensionHandler{
		func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			before, ok := event.(*BeforeAgentStartEvent)
			if !ok {
				return nil, nil
			}
			mu.Lock()
			events = append(events, before)
			mu.Unlock()
			before.SystemPromptOptions.Sections.Keys = append(before.SystemPromptOptions.Sections.Keys, "extension_rules")
			before.SystemPromptOptions.Sections.Values["extension_rules"] = lo.ToPtr("Answer in one word.")
			return nil, nil
		},
	}
	session.SetActiveToolsByName([]string{"noop"})

	if err := session.Prompt(context.Background(), "hello", nil); err != nil {
		t.Fatal(err)
	}
	triggerTurn := true
	if err := session.SendCustomMessage(context.Background(), &CustomMessagePayload{
		CustomType: "task-done",
		Content:    "background task finished",
		Display:    true,
	}, &SendMessageOptions{TriggerTurn: &triggerTurn}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 3 {
		t.Fatalf("requests = %d, want 3", len(prompts))
	}
	if !strings.Contains(prompts[0], "Answer in one word.") {
		t.Fatalf("prompt 0 missing handler section: %q", prompts[0])
	}
	unique := map[string]bool{}
	for _, prompt := range prompts {
		unique[prompt] = true
	}
	if len(unique) != 1 {
		t.Fatalf("system prompt changed mid-run: %#v", prompts)
	}
	if len(events) != 2 {
		t.Fatalf("before_agent_start events = %d, want 2", len(events))
	}
	if events[0].MessageType != "user" || events[1].MessageType != "custom" {
		t.Fatalf("message types = %q,%q", events[0].MessageType, events[1].MessageType)
	}
	if events[1].Message == nil || events[1].Message.CustomType != "task-done" {
		t.Fatalf("custom event message = %+v", events[1].Message)
	}
}

// TestUserPromptRunKeepsHandlerSectionsAcrossToolCall is the user-prompt
// variant of the same guarantee: the next-turn refresh must carry the
// handler-mutated Sections from the run's prompt options (pi spreads
// _runSystemPromptOptions; pig's explicit rebuild must include Sections).
func TestUserPromptRunKeepsHandlerSectionsAcrossToolCall(t *testing.T) {
	mu := &sync.Mutex{}
	var prompts []string
	call := 0

	extensionsResult := &LoadExtensionsResult{
		Extensions: []*Extension{{
			Path:     "user-prompt-sections",
			Tools:    map[string]RegisteredTool{"noop": {Definition: customMessageRunNoopTool()}},
			Handlers: map[string][]ExtensionHandler{},
		}},
		Runtime: CreateExtensionRuntime(),
	}

	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			mu.Lock()
			call++
			current := call
			prompts = append(prompts, aiutils.GetCurrentSystemPrompt(llmContext.Messages))
			mu.Unlock()
			stream := newMockAssistantStream()
			var msg types.AssistantMessage
			if current == 1 {
				msg = testAssistantMessage("hi", withModelRef(model), func(m *types.AssistantMessage) {
					m.StopReason = types.StopReasonToolUse
					m.Content = []types.Content{types.NewTextContent("hi"), fauxToolCall("noop", map[string]any{})}
				})
			} else {
				msg = testAssistantMessage("done", withModelRef(model))
			}
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: msg.StopReason, Message: &msg})
			stream.End()
			return stream.AssistantMessageEventStream
		},
		ExtensionsResult: []*LoadExtensionsResult{extensionsResult},
	})
	defer session.Dispose()
	session.extensionRunner.extensions = extensionsResult.Extensions
	extensionsResult.Extensions[0].Handlers["before_agent_start"] = []ExtensionHandler{
		func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			before, ok := event.(*BeforeAgentStartEvent)
			if !ok {
				return nil, nil
			}
			before.SystemPromptOptions.Sections.Keys = append(before.SystemPromptOptions.Sections.Keys, "extension_rules")
			before.SystemPromptOptions.Sections.Values["extension_rules"] = lo.ToPtr("Answer in one word.")
			return nil, nil
		},
	}
	session.SetActiveToolsByName([]string{"noop"})

	if err := session.Prompt(context.Background(), "hello", nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 2 {
		t.Fatalf("requests = %d, want 2", len(prompts))
	}
	for i, prompt := range prompts {
		if !strings.Contains(prompt, "Answer in one word.") {
			t.Fatalf("request %d lost the handler section: %q", i, prompt)
		}
	}
}

// TestCustomMessageSentWhileStartingJoinsTheStartingRun ports "queues a
// custom message sent while before_agent_start runs into the starting run":
// the session is busy from the start of setup, so the second message is
// steered into the first run instead of starting a second one. pig's
// extension handlers are synchronous, so the second message is dispatched
// from the handler itself.
func TestCustomMessageSentWhileStartingJoinsTheStartingRun(t *testing.T) {
	var sessionRef *AgentSession
	mu := &sync.Mutex{}
	var requests [][]agent.AgentMessage

	extensionsResult := &LoadExtensionsResult{
		Extensions: []*Extension{{
			Path:     "custom-message-while-starting",
			Handlers: map[string][]ExtensionHandler{},
		}},
		Runtime: CreateExtensionRuntime(),
	}

	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			mu.Lock()
			requests = append(requests, append([]agent.AgentMessage{}, llmContext.Messages...))
			mu.Unlock()
			stream := newMockAssistantStream()
			msg := testAssistantMessage("handled both", withModelRef(model))
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: msg.StopReason, Message: &msg})
			stream.End()
			return stream.AssistantMessageEventStream
		},
		ExtensionsResult: []*LoadExtensionsResult{extensionsResult},
	})
	defer session.Dispose()
	session.extensionRunner.extensions = extensionsResult.Extensions
	extensionsResult.Extensions[0].Handlers["before_agent_start"] = []ExtensionHandler{
		func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			before, ok := event.(*BeforeAgentStartEvent)
			if !ok || before.MessageType != "custom" || before.Message == nil || before.Message.CustomType != "first" {
				return nil, nil
			}
			trigger := true
			if err := sessionRef.SendCustomMessage(ctx, &CustomMessagePayload{
				CustomType: "second",
				Content:    "task two finished",
				Display:    true,
			}, &SendMessageOptions{TriggerTurn: &trigger}); err != nil {
				t.Errorf("second send failed: %v", err)
			}
			return nil, nil
		},
	}
	sessionRef = session

	trigger := true
	if err := session.SendCustomMessage(context.Background(), &CustomMessagePayload{
		CustomType: "first",
		Content:    "task one finished",
		Display:    true,
	}, &SendMessageOptions{TriggerTurn: &trigger}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	var texts []string
	for _, message := range requests[0] {
		user, ok := message.(types.UserMessage)
		if !ok {
			continue
		}
		for _, part := range user.Content.Parts {
			if text, ok := part.(types.TextContent); ok {
				texts = append(texts, text.Text)
			}
		}
	}
	// The first message is the run's starting message; the second was steered
	// in and drained into the same request (order: steered first in pig's
	// queue drain, so only membership is asserted).
	if len(texts) != 2 {
		t.Fatalf("user texts = %#v, want both tasks", texts)
	}
	joined := strings.Join(texts, "|")
	if !strings.Contains(joined, "task one finished") || !strings.Contains(joined, "task two finished") {
		t.Fatalf("user texts = %#v", texts)
	}
}

// TestCustomMessageWithoutTriggerTurnFromHandlerJoinsFirstRequest ports
// "includes a custom message sent without triggerTurn while the run starts in
// its first request".
func TestCustomMessageWithoutTriggerTurnFromHandlerJoinsFirstRequest(t *testing.T) {
	var sessionRef *AgentSession
	mu := &sync.Mutex{}
	var requests [][]agent.AgentMessage

	extensionsResult := &LoadExtensionsResult{
		Extensions: []*Extension{{
			Path:     "custom-message-without-trigger",
			Handlers: map[string][]ExtensionHandler{},
		}},
		Runtime: CreateExtensionRuntime(),
	}

	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			mu.Lock()
			requests = append(requests, append([]agent.AgentMessage{}, llmContext.Messages...))
			mu.Unlock()
			stream := newMockAssistantStream()
			msg := testAssistantMessage("done", withModelRef(model))
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: msg.StopReason, Message: &msg})
			stream.End()
			return stream.AssistantMessageEventStream
		},
		ExtensionsResult: []*LoadExtensionsResult{extensionsResult},
	})
	defer session.Dispose()
	session.extensionRunner.extensions = extensionsResult.Extensions
	extensionsResult.Extensions[0].Handlers["before_agent_start"] = []ExtensionHandler{
		func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			if _, ok := event.(*BeforeAgentStartEvent); !ok {
				return nil, nil
			}
			noTrigger := false
			if err := sessionRef.SendCustomMessage(ctx, &CustomMessagePayload{
				CustomType: "note",
				Content:    "context from handler",
				Display:    true,
			}, &SendMessageOptions{TriggerTurn: &noTrigger}); err != nil {
				t.Errorf("handler send failed: %v", err)
			}
			return nil, nil
		},
	}
	sessionRef = session

	if err := session.Prompt(context.Background(), "hello", nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	if !strings.Contains(mustJSON(t, requests[0]), "context from handler") {
		t.Fatalf("handler message missing from first request: %s", mustJSON(t, requests[0]))
	}
}

// TestCustomMessageRunAbortsWhenAbortRequestedDuringSetup ports "aborts a run
// when abort is requested while before_agent_start runs".
func TestCustomMessageRunAbortsWhenAbortRequestedDuringSetup(t *testing.T) {
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	var startOnce sync.Once

	extensionsResult := &LoadExtensionsResult{
		Extensions: []*Extension{{
			Path: "abort-during-setup",
			Handlers: map[string][]ExtensionHandler{
				"before_agent_start": {func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
					startOnce.Do(func() { close(handlerStarted) })
					<-releaseHandler
					return nil, nil
				}},
			},
		}},
		Runtime: CreateExtensionRuntime(),
	}

	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := newMockAssistantStream()
			go func() {
				partial := testAssistantMessage("")
				stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &partial})
				for {
					select {
					case <-ctx.Done():
						aborted := testAssistantMessage("")
						aborted.StopReason = types.StopReasonAborted
						stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonAborted, Message: &aborted})
						return
					case <-time.After(5 * time.Millisecond):
					}
				}
			}()
			return stream.AssistantMessageEventStream
		},
		ExtensionsResult: []*LoadExtensionsResult{extensionsResult},
	})
	defer session.Dispose()
	session.extensionRunner.extensions = extensionsResult.Extensions

	runDone := make(chan error, 1)
	go func() {
		trigger := true
		runDone <- session.SendCustomMessage(context.Background(), &CustomMessagePayload{
			CustomType: "task-done",
			Content:    "background task finished",
			Display:    true,
		}, &SendMessageOptions{TriggerTurn: &trigger})
	}()

	<-handlerStarted
	abortDone := make(chan error, 1)
	go func() { abortDone <- session.Abort(context.Background()) }()
	// Let abort() set the abort flag and cancel the (not-yet-started) run.
	time.Sleep(20 * time.Millisecond)
	close(releaseHandler)

	if err := <-runDone; err != nil {
		t.Fatalf("run error = %v", err)
	}
	if err := <-abortDone; err != nil {
		t.Fatalf("abort error = %v", err)
	}

	messages := session.Messages()
	last := messages[len(messages)-1]
	assistant, ok := last.(types.AssistantMessage)
	if !ok || assistant.StopReason != types.StopReasonAborted {
		t.Fatalf("last message = %+v", last)
	}
	if !session.IsIdle() {
		t.Fatal("session should be idle after abort")
	}
}

// mustJSON marshals a message list for substring assertions (test-only).
func mustJSON(t *testing.T, messages []agent.AgentMessage) string {
	t.Helper()
	encoded, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	return string(encoded)
}
