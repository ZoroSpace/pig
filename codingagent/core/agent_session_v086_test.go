// agent_session_v086_test.go — v0.86.0 behavior locks ported from pi suite
// tests that have no pig counterpart yet (test/suite/agent-session-queue.test.ts,
// agent-session-prompt.test.ts, agent-session-bash-persistence.test.ts,
// regressions/6162-extension-active-tools-next-turn.test.ts):
//   - #8718: steer/followUp run through input handlers with source +
//     streamingBehavior (requires the v0.86.0 steer/followUp source option);
//   - the leading system message in transcripts and persisted entries
//     (v0.86.0 mid-conversation system messages, S6-1);
//   - #6162: setActiveTools and the refreshed system prompt reach the next
//     provider request in the same run.
package core

import (
	"context"
	"sort"
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

// v086InputRecordingExtension builds an extension whose input handler records
// every event and transforms non-"handle" texts (pi: the #8718 extension).
func v086InputRecordingExtension(mu *sync.Mutex, events *[]map[string]any) *Extension {
	return &Extension{
		Path:     "v086-input-extension",
		Handlers: map[string][]ExtensionHandler{},
	}
}

// v086BlockingTool builds a tool whose execution blocks until released (pi:
// the waiting harness's tool).
func v086BlockingTool(name string, started chan struct{}, release chan struct{}) agent.AgentTool {
	return agent.AgentTool{
		Tool: types.Tool{
			Name:        name,
			Description: "Blocks until released",
			Parameters:  rawJSON(`{"type":"object","properties":{}}`),
		},
		Label: name,
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			close(started)
			<-release
			return agent.AgentToolResult{Content: []types.Content{types.NewTextContent("released")}}, nil
		},
	}
}

// v086CountedResponses builds a stream fn serving a fixed script by call
// number (pi: harness.setResponses).
func v086CountedResponses(steps []func(call int) (func(*types.AssistantMessage), types.StopReason)) agent.StreamFn {
	call := 0
	return func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		call++
		stream := newMockAssistantStream()
		var msg types.AssistantMessage
		if call <= len(steps) {
			override, reason := steps[call-1](call)
			msg = testAssistantMessage("ok", withModelRef(model), func(m *types.AssistantMessage) {
				m.StopReason = reason
				override(m)
			})
		} else {
			msg = testAssistantMessage("done", withModelRef(model))
		}
		stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
		reason := msg.StopReason
		if reason == types.StopReasonToolUse {
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: reason, Message: &msg})
		} else {
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: reason, Message: &msg})
		}
		stream.End()
		return stream.AssistantMessageEventStream
	}
}

// TestAgentSessionSteerFollowUpRunThroughInputHandlers ports "runs direct
// steering and follow-up messages through input handlers" (regression
// #8718).
func TestAgentSessionSteerFollowUpRunThroughInputHandlers(t *testing.T) {
	var mu sync.Mutex
	var inputEvents []map[string]any

	extensionsResult := &LoadExtensionsResult{
		Extensions: []*Extension{
			{
				Path:     "v086-input-extension",
				Handlers: map[string][]ExtensionHandler{},
			},
		},
		Runtime: CreateExtensionRuntime(),
	}

	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})
	call := 0
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		BaseToolsOverride:    map[string]agent.AgentTool{"wait": v086BlockingTool("wait", toolStarted, releaseTool)},
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			call++
			stream := newMockAssistantStream()
			var msg types.AssistantMessage
			switch call {
			case 1:
				msg = testAssistantMessage("", withModelRef(model), func(m *types.AssistantMessage) {
					m.StopReason = types.StopReasonToolUse
					m.Content = []types.Content{
						types.NewTextContent(""),
						types.ToolCall{Kind: types.KindToolCall, ID: "call_1", Name: "wait", Arguments: map[string]any{}},
					}
				})
			case 2:
				msg = testAssistantMessage("steered", withModelRef(model))
			default:
				msg = testAssistantMessage("followed up", withModelRef(model))
			}
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: msg.StopReason, Message: &msg})
			stream.End()
			return stream.AssistantMessageEventStream
		},
		ExtensionsResult: []*LoadExtensionsResult{extensionsResult},
	})
	defer session.Dispose()

	// pi: pi.on("input", ...) — record and transform.
	extensionsResult.Extensions[0].Handlers["input"] = []ExtensionHandler{
		func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			inputEvent, ok := event.(*InputEvent)
			if !ok {
				return nil, nil
			}
			mu.Lock()
			inputEvents = append(inputEvents, map[string]any{
				"text":              inputEvent.Text,
				"source":            string(inputEvent.Source),
				"streamingBehavior": inputEvent.StreamingBehavior,
			})
			mu.Unlock()
			if strings.HasPrefix(inputEvent.Text, "handle") {
				return &InputEventResult{Action: "handled"}, nil
			}
			return &InputEventResult{Action: "transform", Text: "transformed: " + inputEvent.Text}, nil
		},
	}
	session.extensionRunner.extensions = extensionsResult.Extensions

	promptDone := make(chan error, 1)
	go func() { promptDone <- session.Prompt(context.Background(), "start", nil) }()
	select {
	case <-toolStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("tool never started")
	}

	mu.Lock()
	inputEvents = nil // drop the initial prompt's input event
	mu.Unlock()

	rpcSource := QueueUserInputOptions{Source: InputSourceRPC}
	if _, err := session.Steer(context.Background(), "steer me", nil, &rpcSource); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Steer(context.Background(), "handle steer", nil, &rpcSource); err != nil {
		t.Fatal(err)
	}
	if _, err := session.FollowUp(context.Background(), "follow me", nil, &rpcSource); err != nil {
		t.Fatal(err)
	}
	if _, err := session.FollowUp(context.Background(), "handle follow", nil, &rpcSource); err != nil {
		t.Fatal(err)
	}

	want := []map[string]any{
		{"text": "steer me", "source": "rpc", "streamingBehavior": "steer"},
		{"text": "handle steer", "source": "rpc", "streamingBehavior": "steer"},
		{"text": "follow me", "source": "rpc", "streamingBehavior": "followUp"},
		{"text": "handle follow", "source": "rpc", "streamingBehavior": "followUp"},
	}
	mu.Lock()
	got := inputEvents
	mu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("input events = %+v, want %+v", got, want)
	}
	for i, event := range got {
		if event["text"] != want[i]["text"] || event["source"] != want[i]["source"] || event["streamingBehavior"] != want[i]["streamingBehavior"] {
			t.Fatalf("input event %d = %+v, want %+v", i, event, want[i])
		}
	}
	if steering := session.GetSteeringMessages(); len(steering) != 1 || steering[0] != "transformed: steer me" {
		t.Fatalf("steering = %v", steering)
	}
	if followUps := session.GetFollowUpMessages(); len(followUps) != 1 || followUps[0] != "transformed: follow me" {
		t.Fatalf("followUps = %v", followUps)
	}

	close(releaseTool)
	select {
	case err := <-promptDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("prompt never completed")
	}
}

// TestAgentSessionNextTurnCustomMessageArrivesWithLeadingSystem ports
// "injects nextTurn custom messages into the next prompt" (pi
// agent-session-queue.test.ts, v0.86.0 system-message role expectations).
func TestAgentSessionNextTurnCustomMessageArrivesWithLeadingSystem(t *testing.T) {
	var mu sync.Mutex
	sawCustomMessage := false

	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			for _, message := range llmContext.Messages {
				userMessage, ok := message.(types.UserMessage)
				if !ok || userMessage.Content.String != "" {
					continue
				}
				for _, part := range userMessage.Content.Parts {
					if text, isText := part.(types.TextContent); isText && text.Text == "carry this" {
						mu.Lock()
						sawCustomMessage = true
						mu.Unlock()
					}
				}
			}
			stream := newMockAssistantStream()
			msg := testAssistantMessage("done", withModelRef(model))
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &msg})
			stream.End()
			return stream.AssistantMessageEventStream
		},
	})
	defer session.Dispose()

	if err := session.SendCustomMessage(context.Background(), &CustomMessagePayload{
		CustomType: "next-turn",
		Content:    "carry this",
		Display:    true,
		Details:    map[string]any{},
	}, &SendMessageOptions{DeliverAs: "nextTurn"}); err != nil {
		t.Fatal(err)
	}

	if err := session.Prompt(context.Background(), "normal prompt", nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	injected := sawCustomMessage
	mu.Unlock()
	if !injected {
		t.Fatal("nextTurn custom message never reached the provider context")
	}
	// v0.86.0: the transcript leads with the system message, and the custom
	// message rides after the user prompt.
	var roles []string
	for _, message := range session.Messages() {
		roles = append(roles, string(message.MessageRole()))
	}
	if strings.Join(roles, ",") != "system,user,custom,assistant" {
		t.Fatalf("roles = %v, want [system user custom assistant]", roles)
	}
}

// TestAgentSessionPromptPersistsLeadingSystemEntry ports the v0.86.0
// system-message expectations from agent-session-prompt.test.ts and
// agent-session-bash-persistence.test.ts: the declared prompt is preceded by
// a persisted system entry, and role listings lead with "system".
func TestAgentSessionPromptPersistsLeadingSystemEntry(t *testing.T) {
	session, sessionManager, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := newMockAssistantStream()
			msg := testAssistantMessage("reply", withModelRef(model))
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &msg})
			stream.End()
			return stream.AssistantMessageEventStream
		},
	})
	defer session.Dispose()

	if err := session.Prompt(context.Background(), "hi", nil); err != nil {
		t.Fatal(err)
	}

	// Transcript roles: [system, user, assistant]; the user text sits at
	// index 1 (pi: harness.session.messages[1]).
	var roles []string
	messages := session.Messages()
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3 (%v)", len(messages), roles)
	}
	for _, message := range messages {
		roles = append(roles, string(message.MessageRole()))
	}
	if strings.Join(roles, ",") != "system,user,assistant" {
		t.Fatalf("roles = %v, want [system user assistant]", roles)
	}
	user, ok := messages[1].(types.UserMessage)
	if !ok {
		t.Fatalf("messages[1] = %#v, want user message", messages[1])
	}
	if user.Content.String != "hi" && !strings.Contains(user.Content.Parts[0].(types.TextContent).Text, "hi") {
		t.Fatalf("messages[1] = %#v, want user %q", messages[1], "hi")
	}

	// The system message is persisted (v0.86.0 handleAgentEvent system role).
	var entryRoles []string
	for _, entry := range sessionManager.GetEntries() {
		if messageEntry, ok := entry.(*SessionMessageEntry); ok && messageEntry.Message != nil {
			entryRoles = append(entryRoles, string(messageEntry.Message.MessageRole()))
		}
	}
	if strings.Join(entryRoles, ",") != "system,user,assistant" {
		t.Fatalf("entry roles = %v, want [system user assistant]", entryRoles)
	}
}

// --- #6162: extension active tools next-turn refresh ---

// v086SwitchToolsExtension registers switch_tools/after_switch whose execute
// swaps the active set (pi: registerSwitchTools).
func v086SwitchToolsExtension(sessionRef **AgentSession) *Extension {
	switchTool := &tools.ToolDefinition{
		Name:          "switch_tools",
		Label:         "Switch Tools",
		Description:   "Switch the active extension tool set",
		PromptSnippet: "Switch to the next extension tool",
		Parameters:    rawJSON(`{"type":"object","properties":{}}`),
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx tools.ToolExecuteContext) (agent.AgentToolResult, error) {
			(*sessionRef).SetActiveToolsByName([]string{"after_switch"})
			return agent.AgentToolResult{Content: []types.Content{types.NewTextContent("switched")}, Details: map[string]any{}}, nil
		},
	}
	afterSwitch := &tools.ToolDefinition{
		Name:          "after_switch",
		Label:         "After Switch",
		Description:   "Tool that should be available after switching",
		PromptSnippet: "Run after the active tool set changes",
		Parameters:    rawJSON(`{"type":"object","properties":{}}`),
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx tools.ToolExecuteContext) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{Content: []types.Content{types.NewTextContent("after")}, Details: map[string]any{}}, nil
		},
	}
	return &Extension{
		Path: "v086-switch-tools",
		Tools: map[string]RegisteredTool{
			"switch_tools": {Definition: switchTool},
			"after_switch": {Definition: afterSwitch},
		},
		Handlers: map[string][]ExtensionHandler{},
	}
}

// v086SortedProviderToolNames resolves the tool names visible to the next
// provider request (pi: getProviderToolNames via getCurrentTools).
func v086SortedProviderToolNames(llmContext *types.TranscriptContext) []string {
	var names []string
	for _, t := range aiutils.GetCurrentTools(llmContext.Messages) {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names
}

func TestExtensionActiveToolsRefreshBeforeNextProviderRequest(t *testing.T) {
	var sessionRef *AgentSession
	extensionsResult := &LoadExtensionsResult{
		Extensions: []*Extension{v086SwitchToolsExtension(&sessionRef)},
		Runtime:    CreateExtensionRuntime(),
	}

	var providerToolNames [][]string
	call := 0
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			call++
			providerToolNames = append(providerToolNames, v086SortedProviderToolNames(llmContext))
			stream := newMockAssistantStream()
			var msg types.AssistantMessage
			if call == 1 {
				msg = testAssistantMessage("", withModelRef(model), func(m *types.AssistantMessage) {
					m.StopReason = types.StopReasonToolUse
					m.Content = []types.Content{
						types.NewTextContent(""),
						types.ToolCall{Kind: types.KindToolCall, ID: "call_1", Name: "switch_tools", Arguments: map[string]any{}},
					}
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
	sessionRef = session
	session.extensionRunner.extensions = extensionsResult.Extensions

	session.SetActiveToolsByName([]string{"switch_tools"})
	if err := session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	if len(providerToolNames) != 2 {
		t.Fatalf("provider requests = %d", len(providerToolNames))
	}
	if strings.Join(providerToolNames[0], ",") != "switch_tools" {
		t.Fatalf("first request tools = %v, want [switch_tools]", providerToolNames[0])
	}
	// pi.setActiveTools must apply before the next provider request in the
	// same run: after_switch is visible, switch_tools is gone.
	if strings.Join(providerToolNames[1], ",") != "after_switch" {
		t.Fatalf("second request tools = %v, want [after_switch]", providerToolNames[1])
	}
}

// TestExtensionActiveToolsReportRefreshedSystemPrompt ports "reports the
// refreshed system prompt during the run" (#6162): the provider context's
// replayed system message matches session.SystemPrompt() at request time.
func TestExtensionActiveToolsReportRefreshedSystemPrompt(t *testing.T) {
	var sessionRef *AgentSession
	extensionsResult := &LoadExtensionsResult{
		Extensions: []*Extension{v086SwitchToolsExtension(&sessionRef)},
		Runtime:    CreateExtensionRuntime(),
	}

	mu := &sync.Mutex{}
	var providerPrompts []string
	var sessionPrompts []string
	call := 0
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			call++
			mu.Lock()
			providerPrompts = append(providerPrompts, aiutils.GetCurrentSystemPrompt(llmContext.Messages))
			sessionPrompts = append(sessionPrompts, sessionRef.SystemPrompt())
			mu.Unlock()
			stream := newMockAssistantStream()
			var msg types.AssistantMessage
			if call == 1 {
				msg = testAssistantMessage("", withModelRef(model), func(m *types.AssistantMessage) {
					m.StopReason = types.StopReasonToolUse
					m.Content = []types.Content{
						types.NewTextContent(""),
						types.ToolCall{Kind: types.KindToolCall, ID: "call_1", Name: "switch_tools", Arguments: map[string]any{}},
					}
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
	sessionRef = session
	session.extensionRunner.extensions = extensionsResult.Extensions

	session.SetActiveToolsByName([]string{"switch_tools"})
	if err := session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(providerPrompts) != 2 {
		t.Fatalf("provider prompts = %d", len(providerPrompts))
	}
	if providerPrompts[0] == providerPrompts[1] {
		t.Fatal("system prompt did not refresh after setActiveTools")
	}
	for i, prompt := range providerPrompts {
		if prompt != sessionPrompts[i] {
			t.Fatalf("provider prompt %d %q != session prompt %q", i, prompt, sessionPrompts[i])
		}
	}
	t.Logf("prompt0 len=%d prompt1 len=%d", len(providerPrompts[0]), len(providerPrompts[1]))
}

// TestBeforeAgentStartPromptOverridePreservedMidRun ports "preserves
// before_agent_start system prompt overrides when tools change mid-run"
// (#6162).
func TestBeforeAgentStartPromptOverridePreservedMidRun(t *testing.T) {
	var sessionRef *AgentSession
	extensionsResult := &LoadExtensionsResult{
		Extensions: []*Extension{v086SwitchToolsExtension(&sessionRef)},
		Runtime:    CreateExtensionRuntime(),
	}

	mu := &sync.Mutex{}
	var providerSystemPrompts []string
	var providerToolNames [][]string
	call := 0
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			call++
			mu.Lock()
			providerSystemPrompts = append(providerSystemPrompts, aiutils.GetCurrentSystemPrompt(llmContext.Messages))
			providerToolNames = append(providerToolNames, v086SortedProviderToolNames(llmContext))
			mu.Unlock()
			stream := newMockAssistantStream()
			var msg types.AssistantMessage
			if call == 1 {
				msg = testAssistantMessage("", withModelRef(model), func(m *types.AssistantMessage) {
					m.StopReason = types.StopReasonToolUse
					m.Content = []types.Content{
						types.NewTextContent(""),
						types.ToolCall{Kind: types.KindToolCall, ID: "call_1", Name: "switch_tools", Arguments: map[string]any{}},
					}
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
	sessionRef = session
	session.extensionRunner.extensions = extensionsResult.Extensions
	extensionsResult.Extensions[0].Handlers["before_agent_start"] = []ExtensionHandler{
		func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			beforeAgentStart, ok := event.(*BeforeAgentStartEvent)
			if !ok {
				return nil, nil
			}
			return &BeforeAgentStartEventResult{
				SystemPrompt: lo.ToPtr(beforeAgentStart.SystemPrompt + "\n\nkeep this run override"),
			}, nil
		},
	}

	session.SetActiveToolsByName([]string{"switch_tools"})
	if err := session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if strings.Join(providerToolNames[0], ",") != "switch_tools" || strings.Join(providerToolNames[1], ",") != "after_switch" {
		t.Fatalf("provider tool names = %v", providerToolNames)
	}
	if len(providerSystemPrompts) != 2 {
		t.Fatalf("provider prompts = %d", len(providerSystemPrompts))
	}
	for i, prompt := range providerSystemPrompts {
		if !strings.Contains(prompt, "keep this run override") {
			t.Fatalf("provider prompt %d lost the before_agent_start override: %q", i, prompt)
		}
	}
}
