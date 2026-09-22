// pi: test/agent-loop.test.ts. Mock assistant streams push their events
// synchronously instead of on a microtask — EventStream queues either way,
// and the loop consumes identically.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
)

// ---- shared test fixtures ----

// customMessage models pi's app-declared custom AgentMessage (declaration
// merging); any type implementing MessageRole() rides the same interface.
type customMessage struct {
	Role      types.Role
	Text      string
	Timestamp int64
}

func (m customMessage) MessageRole() types.Role { return m.Role }

func rawSchema(t *testing.T, schema any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	return encoded
}

func echoToolSchema(t *testing.T) json.RawMessage {
	return rawSchema(t, map[string]any{
		"type":       "object",
		"properties": map[string]any{"value": map[string]any{"type": "string"}},
		"required":   []string{"value"},
	})
}

func createUsage() types.Usage {
	return types.Usage{Cost: types.Cost{}}
}

func createModel() *types.Model {
	return &types.Model{
		ID:            "mock",
		Name:          "mock",
		API:           "openai-responses",
		Provider:      "openai",
		BaseURL:       "https://example.invalid",
		Reasoning:     false,
		Input:         []types.ModelInput{types.ModelInputText},
		Cost:          types.ModelCost{},
		ContextWindow: 8192,
		MaxTokens:     2048,
	}
}

func createAssistantMessage(content []types.Content, stopReason types.StopReason) types.AssistantMessage {
	if stopReason == "" {
		stopReason = types.StopReasonStop
	}
	return types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    content,
		API:        "openai-responses",
		Provider:   "openai",
		Model:      "mock",
		Usage:      createUsage(),
		StopReason: stopReason,
		Timestamp:  time.Now().UnixMilli(),
	}
}

func createUserMessage(text string) types.UserMessage {
	return types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{String: text},
		Timestamp: time.Now().UnixMilli(),
	}
}

// identityConverter passes through standard messages (test helper).
// v0.86.0: system messages ride the transcript.
func identityConverter(messages []AgentMessage) []types.Message {
	var out []types.Message
	for _, message := range messages {
		switch message.MessageRole() {
		case types.RoleSystem, types.RoleUser, types.RoleAssistant, types.RoleToolResult:
			out = append(out, message)
		}
	}
	return out
}

func doneEvent(message types.AssistantMessage) types.AssistantMessageEvent {
	return types.AssistantMessageEvent{Type: types.EventDone, Reason: message.StopReason, Message: &message}
}

func mockStreamFn(events ...types.AssistantMessageEvent) StreamFn {
	return func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		stream := types.NewAssistantMessageEventStream()
		for _, event := range events {
			stream.Push(event)
		}
		return stream
	}
}

func collectEvents(t *testing.T, stream *types.EventStream[AgentEvent, []AgentMessage]) ([]AgentEvent, []AgentMessage) {
	t.Helper()
	var events []AgentEvent
	for event := range stream.Events() {
		events = append(events, event)
	}
	messages := stream.Result()
	return events, messages
}

func eventTypes(events []AgentEvent) []AgentEventType {
	out := make([]AgentEventType, len(events))
	for index, event := range events {
		out[index] = event.Type
	}
	return out
}

// ---- default stream function compatibility ----

func TestDefaultStreamFnCompatibility(t *testing.T) {
	calls := 0
	SetDefaultStreamFn(func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		calls++
		message := createAssistantMessage([]types.Content{types.NewTextContent("fallback")}, "")
		return mockStreamFn(doneEvent(message))(ctx, model, llmContext, options)
	})
	defer SetDefaultStreamFn(nil)

	agentContext := AgentContext{Tools: []*AgentTool{}}
	config := AgentLoopConfig{Model: createModel(), ConvertToLlm: identityConverter}
	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("Hello")}, agentContext, config, nil)
	_, _ = collectEvents(t, stream)
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

// ---- agentLoop with AgentMessage ----

func TestAgentLoopEmitsEventsWithAgentMessageTypes(t *testing.T) {
	agentContext := AgentContext{Messages: []AgentMessage{types.SystemMessage{
		Role: types.RoleSystem, Content: types.SystemContent{String: "You are helpful."}, Timestamp: 1,
	}}, Tools: []*AgentTool{}}
	config := AgentLoopConfig{Model: createModel(), ConvertToLlm: identityConverter}

	message := createAssistantMessage([]types.Content{types.NewTextContent("Hi there!")}, "")
	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("Hello")}, agentContext, config,
		mockStreamFn(doneEvent(message)))

	events, messages := collectEvents(t, stream)

	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(messages))
	}
	if messages[0].MessageRole() != types.RoleUser || messages[1].MessageRole() != types.RoleAssistant {
		t.Fatalf("roles = %v, %v", messages[0].MessageRole(), messages[1].MessageRole())
	}

	found := map[AgentEventType]bool{}
	for _, event := range events {
		found[event.Type] = true
	}
	for _, want := range []AgentEventType{AgentEventAgentStart, AgentEventTurnStart, AgentEventMessageStart, AgentEventMessageEnd, AgentEventTurnEnd, AgentEventAgentEnd} {
		if !found[want] {
			t.Errorf("missing event %v", want)
		}
	}
}

func TestAgentLoopHandlesCustomMessageTypesViaConvertToLlm(t *testing.T) {
	notification := customMessage{Role: "notification", Text: "This is a notification", Timestamp: time.Now().UnixMilli()}

	agentContext := AgentContext{
		Messages: []AgentMessage{notification},
		Tools:    []*AgentTool{},
	}

	var convertedMessages []types.Message
	config := AgentLoopConfig{
		Model: createModel(),
		ConvertToLlm: func(messages []AgentMessage) []types.Message {
			convertedMessages = nil
			for _, message := range messages {
				if message.MessageRole() == "notification" {
					continue
				}
				switch message.MessageRole() {
				case types.RoleUser, types.RoleAssistant, types.RoleToolResult:
					convertedMessages = append(convertedMessages, message)
				}
			}
			return convertedMessages
		},
	}

	message := createAssistantMessage([]types.Content{types.NewTextContent("Response")}, "")
	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("Hello")}, agentContext, config,
		mockStreamFn(doneEvent(message)))
	_, _ = collectEvents(t, stream)

	if len(convertedMessages) != 1 {
		t.Fatalf("converted = %d, want 1", len(convertedMessages))
	}
	if convertedMessages[0].MessageRole() != types.RoleUser {
		t.Fatalf("role = %v, want user", convertedMessages[0].MessageRole())
	}
}

func TestAgentLoopAppliesTransformContextBeforeConvertToLlm(t *testing.T) {
	agentContext := AgentContext{
		Messages: []AgentMessage{
			createUserMessage("old message 1"),
			createAssistantMessage([]types.Content{types.NewTextContent("old response 1")}, ""),
			createUserMessage("old message 2"),
			createAssistantMessage([]types.Content{types.NewTextContent("old response 2")}, ""),
		},
		Tools: []*AgentTool{},
	}

	var transformedMessages, convertedMessages []AgentMessage

	config := AgentLoopConfig{
		Model: createModel(),
		TransformContext: func(ctx context.Context, messages []AgentMessage) []AgentMessage {
			// Keep only last 2 messages (prune old ones)
			transformedMessages = messages[len(messages)-2:]
			return transformedMessages
		},
		ConvertToLlm: func(messages []AgentMessage) []types.Message {
			convertedMessages = messages
			return identityConverter(messages)
		},
	}

	message := createAssistantMessage([]types.Content{types.NewTextContent("Response")}, "")
	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("new message")}, agentContext, config,
		mockStreamFn(doneEvent(message)))
	_, _ = collectEvents(t, stream)

	if len(transformedMessages) != 2 {
		t.Fatalf("transformed = %d, want 2", len(transformedMessages))
	}
	if len(convertedMessages) != 2 {
		t.Fatalf("converted = %d, want 2", len(convertedMessages))
	}
}

func TestAgentLoopHandlesToolCallsAndResults(t *testing.T) {
	executed := []string{}
	toolUsage := &types.Usage{
		Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, TotalTokens: 10,
		Cost: types.Cost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1},
	}
	patchedToolUsage := &types.Usage{
		Input: 5, Output: 6, CacheRead: 7, CacheWrite: 8, TotalTokens: 26,
		Cost: types.Cost{Input: 0.5, Output: 0.6, CacheRead: 0.7, CacheWrite: 0.8, Total: 2.6},
	}
	var observedToolUsage *types.Usage

	tool := &AgentTool{
		Tool:  types.Tool{Name: "echo", Description: "Echo tool", Parameters: echoToolSchema(t)},
		Label: "Echo",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			executed = append(executed, params.(map[string]any)["value"].(string))
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("echoed: " + params.(map[string]any)["value"].(string))},
				Details: map[string]any{"value": params.(map[string]any)["value"]},
				Usage:   toolUsage,
			}, nil
		},
	}

	agentContext := AgentContext{Tools: []*AgentTool{tool}}
	config := AgentLoopConfig{
		Model:        createModel(),
		ConvertToLlm: identityConverter,
		AfterToolCall: func(ctx context.Context, context AfterToolCallContext) (*AfterToolCallResult, error) {
			observedToolUsage = context.Result.Usage
			return &AfterToolCallResult{Usage: patchedToolUsage}, nil
		},
	}

	callIndex := 0
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		var message types.AssistantMessage
		if callIndex == 0 {
			message = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "echo", map[string]any{"value": "hello"}),
			}, types.StopReasonToolUse)
		} else {
			message = createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
		}
		callIndex++
		return mockStreamFn(doneEvent(message))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("echo something")}, agentContext, config, streamFn)
	events, messages := collectEvents(t, stream)

	if len(executed) != 1 || executed[0] != "hello" {
		t.Fatalf("executed = %v, want [hello]", executed)
	}

	var toolEnd *AgentEvent
	for index := range events {
		if events[index].Type == AgentEventToolExecutionEnd {
			toolEnd = &events[index]
		}
	}
	if toolEnd == nil {
		t.Fatal("missing tool_execution_end")
	}
	if toolEnd.IsError {
		t.Error("tool_execution_end.IsError = true, want false")
	}
	if observedToolUsage != toolUsage {
		t.Errorf("observedToolUsage = %v, want the executed usage", observedToolUsage)
	}

	toolResultFound := false
	for _, message := range messages {
		if result, ok := message.(types.ToolResultMessage); ok {
			toolResultFound = true
			if result.Usage != patchedToolUsage {
				t.Errorf("toolResult.Usage = %v, want patched usage", result.Usage)
			}
		}
	}
	if !toolResultFound {
		t.Error("missing toolResult message")
	}
}

func TestAgentLoopDoesNotExecuteToolCallsFromLengthTruncatedMessage(t *testing.T) {
	executed := []string{}
	tool := &AgentTool{
		Tool:  types.Tool{Name: "echo", Description: "Echo tool", Parameters: echoToolSchema(t)},
		Label: "Echo",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			executed = append(executed, params.(map[string]any)["value"].(string))
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("echoed: " + params.(map[string]any)["value"].(string))},
				Details: map[string]any{"value": params.(map[string]any)["value"]},
			}, nil
		},
	}

	agentContext := AgentContext{Tools: []*AgentTool{tool}}
	config := AgentLoopConfig{Model: createModel(), ConvertToLlm: identityConverter}

	callIndex := 0
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		var message types.AssistantMessage
		if callIndex == 0 {
			// Output hit the token limit mid tool call. The salvage parser can
			// produce arguments that validate but are silently truncated, so
			// nothing in this message may execute.
			message = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "echo", map[string]any{"value": "hel"}),
			}, types.StopReasonLength)
		} else {
			message = createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
		}
		callIndex++
		return mockStreamFn(doneEvent(message))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("echo something")}, agentContext, config, streamFn)
	events, messages := collectEvents(t, stream)

	if len(executed) != 0 {
		t.Fatalf("executed = %v, want none", executed)
	}

	var toolEnd *AgentEvent
	for index := range events {
		if events[index].Type == AgentEventToolExecutionEnd {
			toolEnd = &events[index]
		}
	}
	if toolEnd == nil {
		t.Fatal("missing tool_execution_end")
	}
	if !toolEnd.IsError {
		t.Error("tool_execution_end.IsError = false, want true")
	}
	result := toolEnd.Result.(AgentToolResult)
	text := result.Content[0].(types.TextContent).Text
	if !contains(text, "output token limit") {
		t.Errorf("error text %q should mention output token limit", text)
	}

	if callIndex != 2 {
		t.Errorf("callIndex = %d, want 2 (the loop continues so the model can re-issue)", callIndex)
	}
	if messages[len(messages)-1].MessageRole() != types.RoleAssistant {
		t.Errorf("last message role = %v, want assistant", messages[len(messages)-1].MessageRole())
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestAgentLoopExecutesMutatedBeforeToolCallArgsWithoutRevalidation(t *testing.T) {
	executed := []any{}
	tool := &AgentTool{
		Tool:  types.Tool{Name: "echo", Description: "Echo tool", Parameters: echoToolSchema(t)},
		Label: "Echo",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			executed = append(executed, params.(map[string]any)["value"])
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("echoed: value")},
				Details: map[string]any{"value": params.(map[string]any)["value"]},
			}, nil
		},
	}

	agentContext := AgentContext{Tools: []*AgentTool{tool}}
	config := AgentLoopConfig{
		Model:        createModel(),
		ConvertToLlm: identityConverter,
		BeforeToolCall: func(ctx context.Context, context BeforeToolCallContext) (*BeforeToolCallResult, error) {
			context.Args.(map[string]any)["value"] = float64(123)
			return nil, nil
		},
	}

	callIndex := 0
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		var message types.AssistantMessage
		if callIndex == 0 {
			message = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "echo", map[string]any{"value": "hello"}),
			}, types.StopReasonToolUse)
		} else {
			message = createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
		}
		callIndex++
		return mockStreamFn(doneEvent(message))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("echo something")}, agentContext, config, streamFn)
	_, _ = collectEvents(t, stream)

	if len(executed) != 1 || executed[0] != float64(123) {
		t.Fatalf("executed = %v, want [123]", executed)
	}
}

func TestAgentLoopPreparesToolArgumentsForValidation(t *testing.T) {
	type edit struct {
		OldText string
		NewText string
	}
	var executed [][]edit

	tool := &AgentTool{
		Tool: types.Tool{
			Name:        "edit",
			Description: "Edit tool",
			Parameters: rawSchema(t, map[string]any{
				"type": "object",
				"properties": map[string]any{
					"edits": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"oldText": map[string]any{"type": "string"},
								"newText": map[string]any{"type": "string"},
							},
							"required": []string{"oldText", "newText"},
						},
					},
				},
				"required": []string{"edits"},
			}),
		},
		Label: "Edit",
		PrepareArguments: func(args any) any {
			input, ok := args.(map[string]any)
			if !ok {
				return args
			}
			oldText, oldOK := input["oldText"].(string)
			newText, newOK := input["newText"].(string)
			if !oldOK || !newOK {
				return args
			}
			edits, _ := input["edits"].([]any)
			return map[string]any{
				"edits": append(append([]any{}, edits...), map[string]any{"oldText": oldText, "newText": newText}),
			}
		},
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			var batch []edit
			for _, raw := range params.(map[string]any)["edits"].([]any) {
				item := raw.(map[string]any)
				batch = append(batch, edit{OldText: item["oldText"].(string), NewText: item["newText"].(string)})
			}
			executed = append(executed, batch)
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("edited")},
				Details: map[string]any{"count": len(batch)},
			}, nil
		},
	}

	agentContext := AgentContext{Tools: []*AgentTool{tool}}
	config := AgentLoopConfig{Model: createModel(), ConvertToLlm: identityConverter}

	callIndex := 0
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		var message types.AssistantMessage
		if callIndex == 0 {
			message = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "edit", map[string]any{"oldText": "before", "newText": "after"}),
			}, types.StopReasonToolUse)
		} else {
			message = createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
		}
		callIndex++
		return mockStreamFn(doneEvent(message))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("edit something")}, agentContext, config, streamFn)
	_, _ = collectEvents(t, stream)

	if len(executed) != 1 || len(executed[0]) != 1 || executed[0][0] != (edit{OldText: "before", NewText: "after"}) {
		t.Fatalf("executed = %v, want [[{before after}]]", executed)
	}
}

func TestAgentLoopParallelEmitsToolExecutionEndInCompletionOrder(t *testing.T) {
	var firstResolved atomic.Bool
	parallelObserved := false
	releaseFirst := make(chan struct{})

	tool := &AgentTool{
		Tool:  types.Tool{Name: "echo", Description: "Echo tool", Parameters: echoToolSchema(t)},
		Label: "Echo",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			value := params.(map[string]any)["value"].(string)
			if value == "first" {
				<-releaseFirst
				firstResolved.Store(true)
			}
			if value == "second" && !firstResolved.Load() {
				parallelObserved = true
			}
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("echoed: " + value)},
				Details: map[string]any{"value": value},
			}, nil
		},
	}

	agentContext := AgentContext{Tools: []*AgentTool{tool}}
	config := AgentLoopConfig{
		Model:         createModel(),
		ConvertToLlm:  identityConverter,
		ToolExecution: ToolExecutionParallel,
	}

	callIndex := 0
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		var message types.AssistantMessage
		if callIndex == 0 {
			message = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "echo", map[string]any{"value": "first"}),
				types.NewToolCall("tool-2", "echo", map[string]any{"value": "second"}),
			}, types.StopReasonToolUse)
			time.AfterFunc(20*time.Millisecond, func() { close(releaseFirst) })
		} else {
			message = createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
		}
		callIndex++
		return mockStreamFn(doneEvent(message))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("echo both")}, agentContext, config, streamFn)
	events, _ := collectEvents(t, stream)

	if !parallelObserved {
		t.Error("second tool did not run in parallel with the first")
	}

	var toolExecutionEndIDs []string
	var toolResultIDs []string
	var turnToolResultIDs []string
	for _, event := range events {
		switch event.Type {
		case AgentEventToolExecutionEnd:
			toolExecutionEndIDs = append(toolExecutionEndIDs, event.ToolCallID)
		case AgentEventMessageEnd:
			if result, ok := event.Message.(types.ToolResultMessage); ok {
				toolResultIDs = append(toolResultIDs, result.ToolCallID)
			}
		case AgentEventTurnEnd:
			for _, toolResult := range event.ToolResults {
				turnToolResultIDs = append(turnToolResultIDs, toolResult.ToolCallID)
			}
		}
	}

	if len(toolExecutionEndIDs) != 2 || toolExecutionEndIDs[0] != "tool-2" || toolExecutionEndIDs[1] != "tool-1" {
		t.Errorf("tool_execution_end order = %v, want [tool-2 tool-1]", toolExecutionEndIDs)
	}
	if len(toolResultIDs) != 2 || toolResultIDs[0] != "tool-1" || toolResultIDs[1] != "tool-2" {
		t.Errorf("tool result order = %v, want [tool-1 tool-2]", toolResultIDs)
	}
	if len(turnToolResultIDs) != 2 || turnToolResultIDs[0] != "tool-1" || turnToolResultIDs[1] != "tool-2" {
		t.Errorf("turn_end tool result order = %v, want [tool-1 tool-2]", turnToolResultIDs)
	}
}

func TestAgentLoopInjectsQueuedMessagesAfterAllToolCallsComplete(t *testing.T) {
	executed := []string{}
	tool := &AgentTool{
		Tool:  types.Tool{Name: "echo", Description: "Echo tool", Parameters: echoToolSchema(t)},
		Label: "Echo",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			value := params.(map[string]any)["value"].(string)
			executed = append(executed, value)
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("ok:" + value)},
				Details: map[string]any{"value": value},
			}, nil
		},
	}

	agentContext := AgentContext{Tools: []*AgentTool{tool}}

	queuedDelivered := false
	queuedUserMessage := createUserMessage("interrupt")

	config := AgentLoopConfig{
		Model:         createModel(),
		ConvertToLlm:  identityConverter,
		ToolExecution: ToolExecutionSequential,
		GetSteeringMessages: func() []AgentMessage {
			// Return steering message after tool execution has started.
			if len(executed) >= 1 && !queuedDelivered {
				queuedDelivered = true
				return []AgentMessage{queuedUserMessage}
			}
			return nil
		},
	}

	sawInterruptInContext := false
	callIndex := 0
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		// Check if interrupt message is in context on second call
		if callIndex == 1 {
			for _, message := range llmContext.Messages {
				if user, ok := message.(types.UserMessage); ok && user.Content.String == "interrupt" {
					sawInterruptInContext = true
				}
			}
		}
		var assistant types.AssistantMessage
		if callIndex == 0 {
			assistant = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "echo", map[string]any{"value": "first"}),
				types.NewToolCall("tool-2", "echo", map[string]any{"value": "second"}),
			}, types.StopReasonToolUse)
		} else {
			assistant = createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
		}
		callIndex++
		return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("start")}, agentContext, config, streamFn)
	events, _ := collectEvents(t, stream)

	if len(executed) != 2 || executed[0] != "first" || executed[1] != "second" {
		t.Fatalf("executed = %v, want [first second] (both tools before steering is injected)", executed)
	}

	toolEnds := 0
	for _, event := range events {
		if event.Type == AgentEventToolExecutionEnd {
			toolEnds++
			if event.IsError {
				t.Error("tool_execution_end.IsError = true, want false")
			}
		}
	}
	if toolEnds != 2 {
		t.Fatalf("tool_execution_end count = %d, want 2", toolEnds)
	}

	var eventSequence []string
	for _, event := range events {
		if event.Type != AgentEventMessageStart {
			continue
		}
		switch message := event.Message.(type) {
		case types.ToolResultMessage:
			eventSequence = append(eventSequence, "tool:"+message.ToolCallID)
		case types.UserMessage:
			eventSequence = append(eventSequence, message.Content.String)
		}
	}

	interruptIndex := indexOfSlice(eventSequence, "interrupt")
	tool1Index := indexOfSlice(eventSequence, "tool:tool-1")
	tool2Index := indexOfSlice(eventSequence, "tool:tool-2")
	if interruptIndex < 0 {
		t.Fatal("interrupt not delivered")
	}
	if !(tool1Index < interruptIndex && tool2Index < interruptIndex) {
		t.Errorf("event sequence = %v, interrupt must come after both tool results", eventSequence)
	}

	if !sawInterruptInContext {
		t.Error("interrupt message missing from context on second LLM call")
	}
}

func indexOfSlice(list []string, want string) int {
	for index, item := range list {
		if item == want {
			return index
		}
	}
	return -1
}

func TestAgentLoopForcesSequentialExecutionWithSequentialToolMode(t *testing.T) {
	var firstResolved atomic.Bool
	parallelObserved := false
	releaseFirst := make(chan struct{})

	slowTool := &AgentTool{
		Tool:          types.Tool{Name: "slow", Description: "Slow tool", Parameters: echoToolSchema(t)},
		Label:         "Slow",
		ExecutionMode: ToolExecutionSequential,
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			value := params.(map[string]any)["value"].(string)
			if value == "first" {
				<-releaseFirst
				firstResolved.Store(true)
			}
			if value == "second" && !firstResolved.Load() {
				parallelObserved = true
			}
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("slow: " + value)},
				Details: map[string]any{"value": value},
			}, nil
		},
	}

	agentContext := AgentContext{Tools: []*AgentTool{slowTool}}
	// config is parallel (default), but tool forces sequential
	config := AgentLoopConfig{Model: createModel(), ConvertToLlm: identityConverter}

	callIndex := 0
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		var message types.AssistantMessage
		if callIndex == 0 {
			message = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "slow", map[string]any{"value": "first"}),
				types.NewToolCall("tool-2", "slow", map[string]any{"value": "second"}),
			}, types.StopReasonToolUse)
			time.AfterFunc(20*time.Millisecond, func() { close(releaseFirst) })
		} else {
			message = createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
		}
		callIndex++
		return mockStreamFn(doneEvent(message))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("run both")}, agentContext, config, streamFn)
	events, _ := collectEvents(t, stream)

	if parallelObserved {
		t.Error("second tool started before first finished despite executionMode=sequential")
	}

	var toolResultIDs []string
	for _, event := range events {
		if event.Type == AgentEventMessageEnd {
			if result, ok := event.Message.(types.ToolResultMessage); ok {
				toolResultIDs = append(toolResultIDs, result.ToolCallID)
			}
		}
	}
	if len(toolResultIDs) != 2 || toolResultIDs[0] != "tool-1" || toolResultIDs[1] != "tool-2" {
		t.Errorf("tool result order = %v, want [tool-1 tool-2]", toolResultIDs)
	}
}

func TestAgentLoopForcesSequentialWhenOneOfMultipleToolsIsSequential(t *testing.T) {
	var executionOrder []string
	releaseSlow := make(chan struct{})

	slowTool := &AgentTool{
		Tool:          types.Tool{Name: "slow", Description: "Slow tool", Parameters: echoToolSchema(t)},
		Label:         "Slow",
		ExecutionMode: ToolExecutionSequential,
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			value := params.(map[string]any)["value"].(string)
			executionOrder = append(executionOrder, "slow:"+value)
			if value == "a" {
				<-releaseSlow
			}
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("slow: " + value)},
				Details: map[string]any{"value": value},
			}, nil
		},
	}
	fastTool := &AgentTool{
		Tool:  types.Tool{Name: "fast", Description: "Fast tool", Parameters: echoToolSchema(t)},
		Label: "Fast",
		// no executionMode = defaults to parallel
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			value := params.(map[string]any)["value"].(string)
			executionOrder = append(executionOrder, "fast:"+value)
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("fast: " + value)},
				Details: map[string]any{"value": value},
			}, nil
		},
	}

	agentContext := AgentContext{Tools: []*AgentTool{slowTool, fastTool}}
	config := AgentLoopConfig{Model: createModel(), ConvertToLlm: identityConverter}

	callIndex := 0
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		var message types.AssistantMessage
		if callIndex == 0 {
			message = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "slow", map[string]any{"value": "a"}),
				types.NewToolCall("tool-2", "fast", map[string]any{"value": "b"}),
			}, types.StopReasonToolUse)
			time.AfterFunc(20*time.Millisecond, func() { close(releaseSlow) })
		} else {
			message = createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
		}
		callIndex++
		return mockStreamFn(doneEvent(message))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("run both")}, agentContext, config, streamFn)
	_, _ = collectEvents(t, stream)

	if len(executionOrder) == 0 || executionOrder[0] != "slow:a" {
		t.Fatalf("execution order = %v, must start with slow:a", executionOrder)
	}
	if indexOfSlice(executionOrder, "fast:b") < 0 {
		t.Errorf("execution order = %v, missing fast:b", executionOrder)
	}
}

func TestAgentLoopAllowsParallelWhenAllToolsAreParallel(t *testing.T) {
	var firstResolved atomic.Bool
	parallelObserved := false
	releaseFirst := make(chan struct{})

	tool := &AgentTool{
		Tool:          types.Tool{Name: "echo", Description: "Echo tool", Parameters: echoToolSchema(t)},
		Label:         "Echo",
		ExecutionMode: ToolExecutionParallel,
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			value := params.(map[string]any)["value"].(string)
			if value == "first" {
				<-releaseFirst
				firstResolved.Store(true)
			}
			if value == "second" && !firstResolved.Load() {
				parallelObserved = true
			}
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("echoed: " + value)},
				Details: map[string]any{"value": value},
			}, nil
		},
	}

	agentContext := AgentContext{Tools: []*AgentTool{tool}}
	config := AgentLoopConfig{Model: createModel(), ConvertToLlm: identityConverter}

	callIndex := 0
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		var message types.AssistantMessage
		if callIndex == 0 {
			message = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "echo", map[string]any{"value": "first"}),
				types.NewToolCall("tool-2", "echo", map[string]any{"value": "second"}),
			}, types.StopReasonToolUse)
			time.AfterFunc(20*time.Millisecond, func() { close(releaseFirst) })
		} else {
			message = createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
		}
		callIndex++
		return mockStreamFn(doneEvent(message))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("echo both")}, agentContext, config, streamFn)
	_, _ = collectEvents(t, stream)

	if !parallelObserved {
		t.Error("second tool should start before first finishes with executionMode=parallel")
	}
}

func TestAgentLoopUsesPrepareNextTurnSnapshotBeforeContinuing(t *testing.T) {
	tool := &AgentTool{
		Tool:  types.Tool{Name: "echo", Description: "Echo tool", Parameters: echoToolSchema(t)},
		Label: "Echo",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			value := params.(map[string]any)["value"].(string)
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("echoed: " + value)},
				Details: map[string]any{"value": value},
			}, nil
		},
	}
	agentContext := AgentContext{Tools: []*AgentTool{tool}}

	prepared := false
	prepareCalls := 0
	config := AgentLoopConfig{
		Model:        createModel(),
		ConvertToLlm: identityConverter,
		PrepareNextTurn: func(context PrepareNextTurnContext) *AgentLoopTurnUpdate {
			prepareCalls++
			if prepared {
				return nil
			}
			prepared = true
			return &AgentLoopTurnUpdate{
				Context: &AgentContext{
					Messages: append([]AgentMessage{}, context.Context.Messages...),
					Tools:    context.Context.Tools,
				},
				// v0.86.0: prompt changes flow as appended system messages.
				Messages: []AgentMessage{types.SystemMessage{
					Role: types.RoleSystem, Content: types.SystemContent{String: "updated guidance"}, Timestamp: 1,
				}},
			}
		},
	}

	llmCalls := 0
	convertedSecondTurnHasUpdate := false
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		llmCalls++
		var assistant types.AssistantMessage
		if llmCalls == 2 {
			for _, m := range llmContext.Messages {
				if sys, ok := m.(types.SystemMessage); ok && sys.Content.String == "updated guidance" {
					convertedSecondTurnHasUpdate = true
				}
			}
			assistant = createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
		} else {
			assistant = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "echo", map[string]any{"value": "hello"}),
			}, types.StopReasonToolUse)
		}
		return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("echo something")}, agentContext, config, streamFn)
	_, _ = collectEvents(t, stream)

	if llmCalls != 2 {
		t.Fatalf("llmCalls = %d, want 2", llmCalls)
	}
	if prepareCalls != 1 {
		t.Fatalf("prepareCalls = %d, want 1", prepareCalls)
	}
	if !convertedSecondTurnHasUpdate {
		t.Fatalf("second turn transcript missing the appended system message")
	}
}

// pi v0.87.0: "action:end receives finalized turn context and stops before
// queue polling" (formerly shouldStopAfterTurn returns true).
func TestAgentLoopActionEndReceivesFinalizedTurnContext(t *testing.T) {
	executed := []string{}
	tool := &AgentTool{
		Tool:  types.Tool{Name: "echo", Description: "Echo tool", Parameters: echoToolSchema(t)},
		Label: "Echo",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			value := params.(map[string]any)["value"].(string)
			executed = append(executed, value)
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("echoed: " + value)},
				Details: map[string]any{"value": value},
			}, nil
		},
	}

	agentContext := AgentContext{Tools: []*AgentTool{tool}}

	steeringPolls := 0
	followUpPolls := 0
	var callbackToolResultIDs []string
	var callbackContextRoles []types.Role
	config := AgentLoopConfig{
		Model:        createModel(),
		ConvertToLlm: identityConverter,
		GetSteeringMessages: func() []AgentMessage {
			steeringPolls++
			return nil
		},
		GetFollowUpMessages: func() []AgentMessage {
			followUpPolls++
			return []AgentMessage{createUserMessage("follow up should stay queued")}
		},
		FinishTurn: func(ctx context.Context, turn AgentTurnContext) *AgentTurnDecision {
			if turn.Message.MessageRole() != types.RoleAssistant {
				t.Errorf("message role = %v, want assistant", turn.Message.MessageRole())
			}
			callbackToolResultIDs = nil
			for _, toolResult := range turn.ToolResults {
				callbackToolResultIDs = append(callbackToolResultIDs, toolResult.ToolCallID)
			}
			callbackContextRoles = nil
			for _, message := range turn.Context.Messages {
				callbackContextRoles = append(callbackContextRoles, message.MessageRole())
			}
			return &AgentTurnDecision{Action: AgentTurnActionEnd}
		},
	}

	llmCalls := 0
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		llmCalls++
		var assistant types.AssistantMessage
		if llmCalls == 1 {
			assistant = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "echo", map[string]any{"value": "hello"}),
			}, types.StopReasonToolUse)
		} else {
			assistant = createAssistantMessage([]types.Content{types.NewTextContent("should not run")}, "")
		}
		return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("echo something")}, agentContext, config, streamFn)
	events, messages := collectEvents(t, stream)

	if llmCalls != 1 {
		t.Errorf("llmCalls = %d, want 1", llmCalls)
	}
	if len(executed) != 1 || executed[0] != "hello" {
		t.Errorf("executed = %v, want [hello]", executed)
	}
	if steeringPolls != 1 {
		t.Errorf("steeringPolls = %d, want 1", steeringPolls)
	}
	if followUpPolls != 0 {
		t.Errorf("followUpPolls = %d, want 0", followUpPolls)
	}
	if len(callbackToolResultIDs) != 1 || callbackToolResultIDs[0] != "tool-1" {
		t.Errorf("callbackToolResultIDs = %v, want [tool-1]", callbackToolResultIDs)
	}
	// v0.86.0: the transcript declares no tools, so the loop announces the
	// loadout with a leading system message.
	wantRoles := []types.Role{types.RoleSystem, types.RoleUser, types.RoleAssistant, types.RoleToolResult}
	if len(callbackContextRoles) != len(wantRoles) {
		t.Fatalf("callbackContextRoles = %v, want %v", callbackContextRoles, wantRoles)
	}
	for index, role := range wantRoles {
		if callbackContextRoles[index] != role {
			t.Fatalf("callbackContextRoles = %v, want %v", callbackContextRoles, wantRoles)
		}
	}
	if len(messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(messages))
	}

	wantEvents := []AgentEventType{
		AgentEventAgentStart,
		AgentEventTurnStart,
		AgentEventMessageStart,
		AgentEventMessageEnd,
		AgentEventMessageStart,
		AgentEventMessageEnd,
		AgentEventMessageStart,
		AgentEventMessageEnd,
		AgentEventToolExecutionStart,
		AgentEventToolExecutionEnd,
		AgentEventMessageStart,
		AgentEventMessageEnd,
		AgentEventTurnEnd,
		AgentEventAgentEnd,
	}
	gotEvents := eventTypes(events)
	if len(gotEvents) != len(wantEvents) {
		t.Fatalf("events = %v, want %v", gotEvents, wantEvents)
	}
	for index, want := range wantEvents {
		if gotEvents[index] != want {
			t.Fatalf("events = %v, want %v", gotEvents, wantEvents)
		}
	}
}

func TestAgentLoopStopsAfterToolBatchWhenEveryResultTerminates(t *testing.T) {
	tool := &AgentTool{
		Tool:  types.Tool{Name: "echo", Description: "Echo tool", Parameters: echoToolSchema(t)},
		Label: "Echo",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			value := params.(map[string]any)["value"].(string)
			return AgentToolResult{
				Content:   []types.Content{types.NewTextContent("echoed: " + value)},
				Details:   map[string]any{"value": value},
				Terminate: true,
			}, nil
		},
	}

	agentContext := AgentContext{Tools: []*AgentTool{tool}}
	config := AgentLoopConfig{Model: createModel(), ConvertToLlm: identityConverter}

	llmCalls := 0
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		llmCalls++
		assistant := createAssistantMessage([]types.Content{
			types.NewToolCall("tool-1", "echo", map[string]any{"value": "hello"}),
		}, types.StopReasonToolUse)
		return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("echo something")}, agentContext, config, streamFn)
	events, messages := collectEvents(t, stream)

	if llmCalls != 1 {
		t.Errorf("llmCalls = %d, want 1", llmCalls)
	}
	// v0.86.0: the loop announces the tool loadout with a leading system
	// message.
	if len(messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(messages))
	}
	if messages[0].MessageRole() != types.RoleSystem {
		t.Fatalf("messages[0] = %v, want system", messages[0].MessageRole())
	}
	turnEnds := 0
	for _, event := range events {
		if event.Type == AgentEventTurnEnd {
			turnEnds++
		}
	}
	if turnEnds != 1 {
		t.Errorf("turn_end count = %d, want 1", turnEnds)
	}
}

func TestAgentLoopStopsAfterBlockedToolCallWhenBeforeToolCallTerminates(t *testing.T) {
	executed := false
	tool := &AgentTool{
		Tool:  types.Tool{Name: "echo", Description: "Echo tool", Parameters: echoToolSchema(t)},
		Label: "Echo",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			executed = true
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("should not execute")},
				Details: map[string]any{"value": "unexpected"},
			}, nil
		},
	}
	agentContext := AgentContext{Tools: []*AgentTool{tool}}
	config := AgentLoopConfig{
		Model:        createModel(),
		ConvertToLlm: identityConverter,
		BeforeToolCall: func(ctx context.Context, context BeforeToolCallContext) (*BeforeToolCallResult, error) {
			return &BeforeToolCallResult{Block: true, Reason: "Blocked by policy", Terminate: true}, nil
		},
	}

	llmCalls := 0
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		llmCalls++
		var assistant types.AssistantMessage
		if llmCalls == 1 {
			assistant = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "echo", map[string]any{"value": "hello"}),
			}, types.StopReasonToolUse)
		} else {
			assistant = createAssistantMessage([]types.Content{types.NewTextContent("should not run")}, "")
		}
		return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("echo something")}, agentContext, config, streamFn)
	_, messages := collectEvents(t, stream)

	if executed {
		t.Error("tool executed despite block")
	}
	if llmCalls != 1 {
		t.Errorf("llmCalls = %d, want 1", llmCalls)
	}
	for _, message := range messages {
		if result, ok := message.(types.ToolResultMessage); ok {
			if !result.IsError {
				t.Error("toolResult.IsError = false, want true")
			}
			found := false
			for _, content := range result.Content {
				if text, ok := content.(types.TextContent); ok && text.Text == "Blocked by policy" {
					found = true
				}
			}
			if !found {
				t.Errorf("toolResult content missing blocked reason, got %v", result.Content)
			}
			return
		}
	}
	t.Fatal("missing toolResult message")
}

func TestAgentLoopContinuesAfterMixedBatchWithOneTerminatingBlockedCall(t *testing.T) {
	executed := []string{}
	tool := &AgentTool{
		Tool:  types.Tool{Name: "echo", Description: "Echo tool", Parameters: echoToolSchema(t)},
		Label: "Echo",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			value := params.(map[string]any)["value"].(string)
			executed = append(executed, value)
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("echoed: " + value)},
				Details: map[string]any{"value": value},
			}, nil
		},
	}
	agentContext := AgentContext{Tools: []*AgentTool{tool}}
	config := AgentLoopConfig{
		Model:         createModel(),
		ConvertToLlm:  identityConverter,
		ToolExecution: ToolExecutionParallel,
		BeforeToolCall: func(ctx context.Context, context BeforeToolCallContext) (*BeforeToolCallResult, error) {
			value := context.Args.(map[string]any)["value"].(string)
			if value == "first" {
				return &BeforeToolCallResult{Block: true, Reason: "Blocked first", Terminate: true}, nil
			}
			return nil, nil
		},
	}

	llmCalls := 0
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		llmCalls++
		var assistant types.AssistantMessage
		if llmCalls == 1 {
			assistant = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "echo", map[string]any{"value": "first"}),
				types.NewToolCall("tool-2", "echo", map[string]any{"value": "second"}),
			}, types.StopReasonToolUse)
		} else {
			assistant = createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
		}
		return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("echo both")}, agentContext, config, streamFn)
	_, _ = collectEvents(t, stream)

	if len(executed) != 1 || executed[0] != "second" {
		t.Errorf("executed = %v, want [second]", executed)
	}
	if llmCalls != 2 {
		t.Errorf("llmCalls = %d, want 2 (mixed batch does not terminate)", llmCalls)
	}
}

func TestAgentLoopContinuesAfterParallelToolCallsWhenNotAllTerminate(t *testing.T) {
	tool := &AgentTool{
		Tool:  types.Tool{Name: "echo", Description: "Echo tool", Parameters: echoToolSchema(t)},
		Label: "Echo",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			value := params.(map[string]any)["value"].(string)
			return AgentToolResult{
				Content:   []types.Content{types.NewTextContent("echoed: " + value)},
				Details:   map[string]any{"value": value},
				Terminate: value == "first",
			}, nil
		},
	}

	agentContext := AgentContext{Tools: []*AgentTool{tool}}
	config := AgentLoopConfig{
		Model:         createModel(),
		ConvertToLlm:  identityConverter,
		ToolExecution: ToolExecutionParallel,
	}

	callIndex := 0
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		var assistant types.AssistantMessage
		if callIndex == 0 {
			assistant = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "echo", map[string]any{"value": "first"}),
				types.NewToolCall("tool-2", "echo", map[string]any{"value": "second"}),
			}, types.StopReasonToolUse)
		} else {
			assistant = createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
		}
		callIndex++
		return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("echo both")}, agentContext, config, streamFn)
	_, messages := collectEvents(t, stream)

	if callIndex != 2 {
		t.Errorf("callIndex = %d, want 2", callIndex)
	}
	// v0.86.0: leading tool-loadout system message.
	wantRoles := []types.Role{types.RoleSystem, types.RoleUser, types.RoleAssistant, types.RoleToolResult, types.RoleToolResult, types.RoleAssistant}
	if len(messages) != len(wantRoles) {
		t.Fatalf("messages = %d, want %d", len(messages), len(wantRoles))
	}
	for index, role := range wantRoles {
		if messages[index].MessageRole() != role {
			t.Fatalf("message roles = %v, want %v", messages, wantRoles)
		}
	}
}

func TestAgentLoopAllowsAfterToolCallToMarkBatchAsTerminating(t *testing.T) {
	tool := &AgentTool{
		Tool:  types.Tool{Name: "echo", Description: "Echo tool", Parameters: echoToolSchema(t)},
		Label: "Echo",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			value := params.(map[string]any)["value"].(string)
			return AgentToolResult{
				Content: []types.Content{types.NewTextContent("echoed: " + value)},
				Details: map[string]any{"value": value},
			}, nil
		},
	}

	agentContext := AgentContext{Tools: []*AgentTool{tool}}
	terminate := true
	config := AgentLoopConfig{
		Model:        createModel(),
		ConvertToLlm: identityConverter,
		AfterToolCall: func(ctx context.Context, context AfterToolCallContext) (*AfterToolCallResult, error) {
			return &AfterToolCallResult{Terminate: &terminate}, nil
		},
	}

	llmCalls := 0
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		llmCalls++
		assistant := createAssistantMessage([]types.Content{
			types.NewToolCall("tool-1", "echo", map[string]any{"value": "hello"}),
		}, types.StopReasonToolUse)
		return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("echo something")}, agentContext, config, streamFn)
	_, _ = collectEvents(t, stream)

	if llmCalls != 1 {
		t.Errorf("llmCalls = %d, want 1", llmCalls)
	}
}

// ---- agentLoopContinue with AgentMessage ----

func TestAgentLoopContinueThrowsWhenContextHasNoMessages(t *testing.T) {
	agentContext := AgentContext{Messages: []AgentMessage{}, Tools: []*AgentTool{}}
	config := AgentLoopConfig{Model: createModel(), ConvertToLlm: identityConverter}

	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		t.Error("stream called unexpectedly")
		return types.NewAssistantMessageEventStream()
	}

	_, err := AgentLoopContinue(context.Background(), agentContext, config, streamFn)
	if err == nil || err.Error() != "Cannot continue: no messages in context" {
		t.Fatalf("err = %v, want Cannot continue: no messages in context", err)
	}
}

func TestAgentLoopContinueFromExistingContext(t *testing.T) {
	agentContext := AgentContext{
		Messages: []AgentMessage{createUserMessage("Hello")},
		Tools:    []*AgentTool{},
	}
	config := AgentLoopConfig{Model: createModel(), ConvertToLlm: identityConverter}

	message := createAssistantMessage([]types.Content{types.NewTextContent("Response")}, "")
	stream, err := AgentLoopContinue(context.Background(), agentContext, config, mockStreamFn(doneEvent(message)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events, messages := collectEvents(t, stream)

	// Should only return the new assistant message (not the existing user message)
	if len(messages) != 1 || messages[0].MessageRole() != types.RoleAssistant {
		t.Fatalf("messages = %v, want one assistant message", messages)
	}

	// Should NOT have user message events (that's the key difference from AgentLoop)
	messageEndEvents := 0
	for _, event := range events {
		if event.Type == AgentEventMessageEnd {
			messageEndEvents++
			if event.Message.MessageRole() != types.RoleAssistant {
				t.Errorf("message_end role = %v, want assistant", event.Message.MessageRole())
			}
		}
	}
	if messageEndEvents != 1 {
		t.Fatalf("message_end count = %d, want 1", messageEndEvents)
	}
}

func TestAgentLoopContinueAllowsCustomMessageTypesAsLastMessage(t *testing.T) {
	// Custom message that will be converted to user message by convertToLlm
	custom := customMessage{Role: "custom", Text: "Hook content", Timestamp: time.Now().UnixMilli()}

	agentContext := AgentContext{
		Messages: []AgentMessage{custom},
		Tools:    []*AgentTool{},
	}

	config := AgentLoopConfig{
		Model: createModel(),
		ConvertToLlm: func(messages []AgentMessage) []types.Message {
			var out []types.Message
			for _, message := range messages {
				if custom, ok := message.(customMessage); ok && custom.Role == "custom" {
					out = append(out, createUserMessage(custom.Text))
					continue
				}
				switch message.MessageRole() {
				case types.RoleUser, types.RoleAssistant, types.RoleToolResult:
					out = append(out, message)
				}
			}
			return out
		},
	}

	message := createAssistantMessage([]types.Content{types.NewTextContent("Response to custom message")}, "")
	stream, err := AgentLoopContinue(context.Background(), agentContext, config, mockStreamFn(doneEvent(message)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, messages := collectEvents(t, stream)
	if len(messages) != 1 || messages[0].MessageRole() != types.RoleAssistant {
		t.Fatalf("messages = %v, want one assistant message", messages)
	}
}

func TestAgentLoopContinueRejectsAssistantLastMessage(t *testing.T) {
	agentContext := AgentContext{
		Messages: []AgentMessage{createAssistantMessage([]types.Content{types.NewTextContent("hi")}, "")},
		Tools:    []*AgentTool{},
	}
	config := AgentLoopConfig{Model: createModel(), ConvertToLlm: identityConverter}

	_, err := AgentLoopContinue(context.Background(), agentContext, config, nil)
	if err == nil || err.Error() != "Cannot continue from message role: assistant" {
		t.Fatalf("err = %v, want Cannot continue from message role: assistant", err)
	}
}

// ---- pi v0.87.0: finishTurn / prepareRequest (agent-loop.test.ts) ----

// pi: "runs finishTurn after tool-result messages and before turn_end".
func TestAgentLoopRunsFinishTurnAfterToolResultsBeforeTurnEnd(t *testing.T) {
	tool := &AgentTool{
		Tool:  types.Tool{Name: "echo", Description: "Echo tool", Parameters: echoToolSchema(t)},
		Label: "Echo",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			value := params.(map[string]any)["value"].(string)
			return AgentToolResult{
				Content:   []types.Content{types.NewTextContent(value)},
				Details:   map[string]any{"value": value},
				Terminate: true,
			}, nil
		},
	}
	var ordering []string
	var mu sync.Mutex
	record := func(entry string) {
		mu.Lock()
		defer mu.Unlock()
		ordering = append(ordering, entry)
	}
	config := AgentLoopConfig{
		Model:        createModel(),
		ConvertToLlm: identityConverter,
		FinishTurn: func(ctx context.Context, turn AgentTurnContext) *AgentTurnDecision {
			record("finishTurn")
			if len(turn.ToolResults) != 1 {
				t.Errorf("toolResults = %d, want 1", len(turn.ToolResults))
			}
			messages := turn.Context.Messages
			if len(messages) == 0 || messages[len(messages)-1].MessageRole() != types.RoleToolResult {
				t.Errorf("last context message role = %v, want toolResult", messages[len(messages)-1].MessageRole())
			}
			return nil
		},
	}

	RunAgentLoop(
		context.Background(),
		[]AgentMessage{createUserMessage("echo")},
		AgentContext{Tools: []*AgentTool{tool}},
		config,
		func(event AgentEvent) {
			if event.Type == AgentEventMessageEnd {
				record("message_end:" + string(event.Message.MessageRole()))
			}
			if event.Type == AgentEventTurnEnd {
				record("turn_end")
			}
		},
		func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			assistant := createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "echo", map[string]any{"value": "hello"}),
			}, types.StopReasonToolUse)
			return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
		},
	)

	if len(ordering) < 3 {
		t.Fatalf("ordering = %v, want at least 3 entries", ordering)
	}
	tail := ordering[len(ordering)-3:]
	wantTail := []string{"message_end:toolResult", "finishTurn", "turn_end"}
	for index, want := range wantTail {
		if tail[index] != want {
			t.Fatalf("ordering tail = %v, want %v", tail, wantTail)
		}
	}
}

// pi: "runs finishTurn for a %s assistant before turn_end without changing
// the hard exit" (parameterized over error and aborted).
func TestAgentLoopRunsFinishTurnForErrorAbortedBeforeTurnEnd(t *testing.T) {
	for _, reason := range []types.StopReason{types.StopReasonError, types.StopReasonAborted} {
		t.Run(string(reason), func(t *testing.T) {
			var ordering []string
			var providerCalls, steeringPolls, followUpPolls atomic.Int64
			RunAgentLoop(
				context.Background(),
				[]AgentMessage{createUserMessage("run")},
				AgentContext{},
				AgentLoopConfig{
					Model:        createModel(),
					ConvertToLlm: identityConverter,
					FinishTurn: func(ctx context.Context, turn AgentTurnContext) *AgentTurnDecision {
						if turn.Message.StopReason != reason {
							t.Errorf("stopReason = %v, want %v", turn.Message.StopReason, reason)
						}
						ordering = append(ordering, "finishTurn")
						return &AgentTurnDecision{Action: AgentTurnActionContinue}
					},
					GetSteeringMessages: func() []AgentMessage {
						steeringPolls.Add(1)
						return nil
					},
					GetFollowUpMessages: func() []AgentMessage {
						followUpPolls.Add(1)
						return []AgentMessage{createUserMessage("queued")}
					},
				},
				func(event AgentEvent) {
					if event.Type == AgentEventTurnEnd {
						ordering = append(ordering, "turn_end")
					}
				},
				func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
					providerCalls.Add(1)
					failure := createAssistantMessage(nil, reason)
					failure.ErrorMessage = string(reason)
					return mockStreamFn(types.AssistantMessageEvent{Type: types.EventError, Reason: reason, Message: &failure})(ctx, model, llmContext, options)
				},
			)

			if len(ordering) != 2 || ordering[0] != "finishTurn" || ordering[1] != "turn_end" {
				t.Fatalf("ordering = %v, want [finishTurn turn_end]", ordering)
			}
			if providerCalls.Load() != 1 {
				t.Errorf("providerCalls = %d, want 1", providerCalls.Load())
			}
			if steeringPolls.Load() != 1 {
				t.Errorf("steeringPolls = %d, want 1", steeringPolls.Load())
			}
			if followUpPolls.Load() != 0 {
				t.Errorf("followUpPolls = %d, want 0", followUpPolls.Load())
			}
		})
	}
}

// pi: "action:end skips queue polling and next-turn preparation".
func TestAgentLoopActionEndSkipsQueuePollingAndNextTurnPreparation(t *testing.T) {
	tool := &AgentTool{
		Tool:  types.Tool{Name: "noop", Description: "Noop tool", Parameters: rawSchema(t, map[string]any{"type": "object", "properties": map[string]any{}})},
		Label: "Noop",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			return AgentToolResult{Content: []types.Content{types.NewTextContent("done")}}, nil
		},
	}
	var providerCalls, steeringPolls, followUpPolls, prepareNextTurnCalls atomic.Int64
	config := AgentLoopConfig{
		Model:        createModel(),
		ConvertToLlm: identityConverter,
		FinishTurn: func(ctx context.Context, turn AgentTurnContext) *AgentTurnDecision {
			return &AgentTurnDecision{Action: AgentTurnActionEnd}
		},
		PrepareNextTurn: func(turn PrepareNextTurnContext) *AgentLoopTurnUpdate {
			prepareNextTurnCalls.Add(1)
			return nil
		},
		GetSteeringMessages: func() []AgentMessage {
			steeringPolls.Add(1)
			return nil
		},
		GetFollowUpMessages: func() []AgentMessage {
			followUpPolls.Add(1)
			return []AgentMessage{createUserMessage("queued")}
		},
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("run")}, AgentContext{Tools: []*AgentTool{tool}}, config, func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		providerCalls.Add(1)
		assistant := createAssistantMessage([]types.Content{
			types.NewToolCall("tool-1", "noop", map[string]any{}),
		}, types.StopReasonToolUse)
		return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
	})
	collectEvents(t, stream)

	if providerCalls.Load() != 1 {
		t.Errorf("providerCalls = %d, want 1", providerCalls.Load())
	}
	if steeringPolls.Load() != 1 {
		t.Errorf("steeringPolls = %d, want 1", steeringPolls.Load())
	}
	if followUpPolls.Load() != 0 {
		t.Errorf("followUpPolls = %d, want 0", followUpPolls.Load())
	}
	if prepareNextTurnCalls.Load() != 0 {
		t.Errorf("prepareNextTurnCalls = %d, want 0", prepareNextTurnCalls.Load())
	}
}

// pi: "makes exactly one context-only request when no natural request
// satisfies continuation".
func TestAgentLoopExplicitContinuationMakesOneContextOnlyRequest(t *testing.T) {
	var providerCalls, finishCalls atomic.Int64
	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("run")}, AgentContext{}, AgentLoopConfig{
		Model:        createModel(),
		ConvertToLlm: identityConverter,
		FinishTurn: func(ctx context.Context, turn AgentTurnContext) *AgentTurnDecision {
			count := finishCalls.Add(1)
			if count == 1 {
				return &AgentTurnDecision{Action: AgentTurnActionContinue}
			}
			return nil
		},
	}, func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		count := providerCalls.Add(1)
		assistant := createAssistantMessage([]types.Content{types.NewTextContent(fmt.Sprintf("response %d", count))}, "")
		return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
	})
	collectEvents(t, stream)

	if providerCalls.Load() != 2 {
		t.Errorf("providerCalls = %d, want 2", providerCalls.Load())
	}
	if finishCalls.Load() != 2 {
		t.Errorf("finishCalls = %d, want 2", finishCalls.Load())
	}
}

// pi: "lets a natural tool-result request satisfy continuation".
func TestAgentLoopNaturalToolResultRequestSatisfiesContinuation(t *testing.T) {
	tool := &AgentTool{
		Tool:  types.Tool{Name: "noop", Description: "Noop tool", Parameters: agentToolSchema(t)},
		Label: "Noop",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			return AgentToolResult{Content: []types.Content{types.NewTextContent("done")}}, nil
		},
	}
	var providerCalls, finishCalls atomic.Int64
	config := AgentLoopConfig{
		Model:        createModel(),
		ConvertToLlm: identityConverter,
		FinishTurn: func(ctx context.Context, turn AgentTurnContext) *AgentTurnDecision {
			count := finishCalls.Add(1)
			if count == 1 {
				return &AgentTurnDecision{Action: AgentTurnActionContinue}
			}
			return nil
		},
	}
	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("run")}, AgentContext{Tools: []*AgentTool{tool}}, config, func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		count := providerCalls.Add(1)
		var assistant types.AssistantMessage
		if count == 1 {
			assistant = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "noop", map[string]any{}),
			}, types.StopReasonToolUse)
		} else {
			assistant = createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
		}
		return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
	})
	collectEvents(t, stream)

	if providerCalls.Load() != 2 {
		t.Errorf("providerCalls = %d, want 2", providerCalls.Load())
	}
	if finishCalls.Load() != 2 {
		t.Errorf("finishCalls = %d, want 2", finishCalls.Load())
	}
}

// pi: "lets a natural %s request satisfy continuation" (parameterized over
// steering and follow-up).
func TestAgentLoopNaturalQueuedRequestSatisfiesContinuation(t *testing.T) {
	for _, queueKind := range []string{"steering", "follow-up"} {
		t.Run(queueKind, func(t *testing.T) {
			queuedMessage := createUserMessage(queueKind)
			var providerCalls, finishCalls, steeringPolls atomic.Int64
			var followUpDelivered atomic.Bool
			var mu sync.Mutex
			var secondRequestUsers []string
			config := AgentLoopConfig{
				Model:        createModel(),
				ConvertToLlm: identityConverter,
				FinishTurn: func(ctx context.Context, turn AgentTurnContext) *AgentTurnDecision {
					count := finishCalls.Add(1)
					if count == 1 {
						return &AgentTurnDecision{Action: AgentTurnActionContinue}
					}
					return nil
				},
				GetSteeringMessages: func() []AgentMessage {
					poll := steeringPolls.Add(1)
					if queueKind == "steering" && poll == 2 {
						return []AgentMessage{queuedMessage}
					}
					return nil
				},
				GetFollowUpMessages: func() []AgentMessage {
					if queueKind != "follow-up" {
						return nil
					}
					if followUpDelivered.CompareAndSwap(false, true) {
						return []AgentMessage{queuedMessage}
					}
					return nil
				},
			}

			stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("run")}, AgentContext{}, config, func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
				count := providerCalls.Add(1)
				if count == 2 {
					mu.Lock()
					for _, message := range llmContext.Messages {
						if user, ok := message.(types.UserMessage); ok && user.Content.String != "" {
							secondRequestUsers = append(secondRequestUsers, user.Content.String)
						}
					}
					mu.Unlock()
				}
				assistant := createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
				return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
			})
			collectEvents(t, stream)

			if providerCalls.Load() != 2 {
				t.Errorf("providerCalls = %d, want 2", providerCalls.Load())
			}
			if finishCalls.Load() != 2 {
				t.Errorf("finishCalls = %d, want 2", finishCalls.Load())
			}
			mu.Lock()
			defer mu.Unlock()
			found := false
			for _, user := range secondRequestUsers {
				if user == queueKind {
					found = true
				}
			}
			if !found {
				t.Errorf("secondRequestUsers = %v, want to contain %q", secondRequestUsers, queueKind)
			}
		})
	}
}

// pi: "prepares the initial request after pending messages and can replace
// request state".
func TestAgentLoopPreparesInitialRequestAfterPendingMessages(t *testing.T) {
	replacementModel := createModel()
	replacementModel.ID = "replacement"
	replacementModel.Name = "replacement"
	canonicalMessage := createUserMessage("canonical projection")
	steeringMessage := createUserMessage("steering")
	var mu sync.Mutex
	var completedMessages []AgentMessage
	var steeringDelivered atomic.Bool
	var prepareCalls atomic.Int64
	config := AgentLoopConfig{
		Model:        createModel(),
		ConvertToLlm: identityConverter,
		GetSteeringMessages: func() []AgentMessage {
			if steeringDelivered.CompareAndSwap(false, true) {
				return []AgentMessage{steeringMessage}
			}
			return nil
		},
		PrepareRequest: func(ctx context.Context, request PrepareRequestContext) *AgentRequestUpdate {
			prepareCalls.Add(1)
			mu.Lock()
			containsSteeringCompleted := false
			for _, message := range completedMessages {
				if message.MessageRole() == types.RoleUser {
					if user, ok := message.(types.UserMessage); ok && user.Content.String == "steering" {
						containsSteeringCompleted = true
					}
				}
			}
			mu.Unlock()
			if !containsSteeringCompleted {
				t.Error("prepareRequest ran before pending messages were emitted")
			}
			containsSteeringContext := false
			for _, message := range request.Context.Messages {
				if user, ok := message.(types.UserMessage); ok && user.Content.String == "steering" {
					containsSteeringContext = true
				}
			}
			if !containsSteeringContext {
				t.Error("prepareRequest context does not contain the steering message")
			}
			return &AgentRequestUpdate{
				Context:       &AgentContext{Messages: []AgentMessage{canonicalMessage}},
				Model:         replacementModel,
				ThinkingLevel: types.ModelThinkingHigh,
			}
		},
	}

	RunAgentLoop(
		context.Background(),
		[]AgentMessage{createUserMessage("prompt")},
		AgentContext{},
		config,
		func(event AgentEvent) {
			if event.Type == AgentEventMessageEnd {
				mu.Lock()
				completedMessages = append(completedMessages, event.Message)
				mu.Unlock()
			}
		},
		func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			if model != replacementModel {
				t.Errorf("streamFn model = %v, want replacement", model.ID)
			}
			if len(llmContext.Messages) != 1 {
				t.Fatalf("llmContext.Messages = %d, want 1", len(llmContext.Messages))
			}
			if user, ok := llmContext.Messages[0].(types.UserMessage); !ok || user.Content.String != "canonical projection" {
				t.Errorf("llmContext.Messages[0] = %v, want canonical projection", llmContext.Messages[0])
			}
			if options == nil || options.Reasoning != types.ThinkingLevelHigh {
				t.Errorf("options.Reasoning = %v, want high", options)
			}
			assistant := createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
			return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
		},
	)

	if prepareCalls.Load() != 1 {
		t.Errorf("prepareCalls = %d, want 1", prepareCalls.Load())
	}
}

// pi: "does not poll steering after prepareRequest".
func TestAgentLoopDoesNotPollSteeringAfterPrepareRequest(t *testing.T) {
	var mu sync.Mutex
	var queued []AgentMessage
	lateSteering := createUserMessage("late steering")
	var requestIncludedSteering []bool
	var requestPreparations, steeringPolls atomic.Int64
	config := AgentLoopConfig{
		Model:        createModel(),
		ConvertToLlm: identityConverter,
		GetSteeringMessages: func() []AgentMessage {
			steeringPolls.Add(1)
			mu.Lock()
			drained := queued
			queued = nil
			mu.Unlock()
			return drained
		},
		PrepareRequest: func(ctx context.Context, request PrepareRequestContext) *AgentRequestUpdate {
			count := requestPreparations.Add(1)
			if count == 1 {
				mu.Lock()
				queued = append(queued, lateSteering)
				mu.Unlock()
			}
			return nil
		},
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("run")}, AgentContext{}, config, func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		includes := false
		for _, message := range llmContext.Messages {
			if user, ok := message.(types.UserMessage); ok && user.Content.String == "late steering" {
				includes = true
			}
		}
		mu.Lock()
		requestIncludedSteering = append(requestIncludedSteering, includes)
		mu.Unlock()
		assistant := createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
		return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
	})
	collectEvents(t, stream)

	if len(requestIncludedSteering) != 2 || requestIncludedSteering[0] || !requestIncludedSteering[1] {
		t.Errorf("requestIncludedSteering = %v, want [false true]", requestIncludedSteering)
	}
	if requestPreparations.Load() != 2 {
		t.Errorf("requestPreparations = %d, want 2", requestPreparations.Load())
	}
	// Startup, post-turn delivery, then the final natural-stop check.
	if steeringPolls.Load() != 3 {
		t.Errorf("steeringPolls = %d, want 3", steeringPolls.Load())
	}
}

// pi: "picks up steering queued during prepareNextTurn before the next
// request".
func TestAgentLoopPicksUpSteeringQueuedDuringPrepareNextTurn(t *testing.T) {
	tool := &AgentTool{
		Tool:  types.Tool{Name: "noop", Description: "Noop tool", Parameters: agentToolSchema(t)},
		Label: "Noop",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
			return AgentToolResult{Content: []types.Content{types.NewTextContent("done")}}, nil
		},
	}
	var mu sync.Mutex
	var queued []AgentMessage
	lateSteering := createUserMessage("late steering")
	var providerCalls atomic.Int64
	var secondRequestIncludedSteering atomic.Bool
	config := AgentLoopConfig{
		Model:        createModel(),
		ConvertToLlm: identityConverter,
		PrepareNextTurn: func(turn PrepareNextTurnContext) *AgentLoopTurnUpdate {
			mu.Lock()
			queued = append(queued, lateSteering)
			mu.Unlock()
			return nil
		},
		GetSteeringMessages: func() []AgentMessage {
			mu.Lock()
			drained := queued
			queued = nil
			mu.Unlock()
			return drained
		},
	}

	stream := AgentLoop(context.Background(), []AgentMessage{createUserMessage("run")}, AgentContext{Tools: []*AgentTool{tool}}, config, func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		count := providerCalls.Add(1)
		if count == 2 {
			for _, message := range llmContext.Messages {
				if user, ok := message.(types.UserMessage); ok && user.Content.String == "late steering" {
					secondRequestIncludedSteering.Store(true)
				}
			}
		}
		var assistant types.AssistantMessage
		if count == 1 {
			assistant = createAssistantMessage([]types.Content{
				types.NewToolCall("tool-1", "noop", map[string]any{}),
			}, types.StopReasonToolUse)
		} else {
			assistant = createAssistantMessage([]types.Content{types.NewTextContent("done")}, "")
		}
		return mockStreamFn(doneEvent(assistant))(ctx, model, llmContext, options)
	})
	collectEvents(t, stream)

	if providerCalls.Load() != 2 {
		t.Errorf("providerCalls = %d, want 2", providerCalls.Load())
	}
	if !secondRequestIncludedSteering.Load() {
		t.Error("second request did not include steering queued during prepareNextTurn")
	}
}
