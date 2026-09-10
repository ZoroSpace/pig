// pi: agent-loop.ts — agent loop that works with AgentMessage throughout.
// Transforms to Message[] only at the LLM call boundary.
//
// Mapping notes:
//   - pi's `signal: AbortSignal | undefined` parameters become the leading
//     ctx context.Context (AbortSignal → context rule); `signal?.aborted`
//     checks become signalAborted(ctx).
//   - AgentEventSink returns Promise<void> | void in pi; pig callbacks are
//     synchronous (callback rule).
//   - When streamFn is nil and no default is configured, pi throws inside the
//     run promise after agent_start/turn_start/message events were emitted,
//     leaving the EventStream unresolved (unhandled rejection). pig cannot
//     leave a goroutine hanging: it returns the messages collected so far and
//     the caller ends the stream with them (same resolution rule as the
//     EventStream zero-result deviation).
package agent

import (
	"context"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

// AgentEventSink consumes agent events. pi's Promise<void> | void collapses
// to a synchronous callback; emit points are all sequential in the loop.
type AgentEventSink func(event AgentEvent)

// AgentLoop starts an agent loop with new prompt messages. The prompts are
// added to the context and events are emitted for them. ctx carries the
// caller's cancellation (pi: signal).
func AgentLoop(ctx context.Context, prompts []AgentMessage, agentContext AgentContext, config AgentLoopConfig, streamFn StreamFn) *types.EventStream[AgentEvent, []AgentMessage] {
	stream := createAgentStream()

	go func() {
		messages := RunAgentLoop(ctx, prompts, agentContext, config, func(event AgentEvent) {
			stream.Push(event)
		}, streamFn)
		stream.EndWith(messages)
	}()

	return stream
}

// AgentLoopContinue continues an agent loop from the current context without
// adding a new message. Used for retries - context already has user message
// or tool results.
//
// **Important:** The last message in context must convert to a `user` or
// `toolResult` message via `convertToLlm`. If it doesn't, the LLM provider
// will reject the request. This cannot be validated here since
// `convertToLlm` is only called once per turn.
//
// pi throws synchronously on the two invalid-context cases; pig returns an
// error before the stream is created.
func AgentLoopContinue(ctx context.Context, agentContext AgentContext, config AgentLoopConfig, streamFn StreamFn) (*types.EventStream[AgentEvent, []AgentMessage], error) {
	if len(agentContext.Messages) == 0 {
		return nil, &CannotContinueError{Reason: "Cannot continue: no messages in context"}
	}

	if agentContext.Messages[len(agentContext.Messages)-1].MessageRole() == types.RoleAssistant {
		return nil, &CannotContinueError{Reason: "Cannot continue from message role: assistant"}
	}

	stream := createAgentStream()

	go func() {
		messages, _ := RunAgentLoopContinue(ctx, agentContext, config, func(event AgentEvent) {
			stream.Push(event)
		}, streamFn)
		stream.EndWith(messages)
	}()

	return stream, nil
}

// CannotContinueError is returned by AgentLoopContinue for the contexts pi
// rejects synchronously (empty context, last message role assistant).
type CannotContinueError struct {
	Reason string
}

func (e *CannotContinueError) Error() string { return e.Reason }

// RunAgentLoop is the prompt-entry variant of the loop without the EventStream
// wrapper; events go to emit (pi: runAgentLoop).
func RunAgentLoop(ctx context.Context, prompts []AgentMessage, agentContext AgentContext, config AgentLoopConfig, emit AgentEventSink, streamFn StreamFn) []AgentMessage {
	newMessages := append([]AgentMessage(nil), prompts...)
	currentContext := agentContext
	currentContext.Messages = append(append([]AgentMessage(nil), agentContext.Messages...), prompts...)

	emit(AgentEvent{Type: AgentEventAgentStart})
	emit(AgentEvent{Type: AgentEventTurnStart})
	for _, prompt := range prompts {
		emit(AgentEvent{Type: AgentEventMessageStart, Message: prompt})
		emit(AgentEvent{Type: AgentEventMessageEnd, Message: prompt})
	}

	newMessages = runLoop(ctx, &currentContext, newMessages, config, emit, streamFn)
	return newMessages
}

// RunAgentLoopContinue is the continue-entry variant of the loop without the
// EventStream wrapper; events go to emit (pi: runAgentLoopContinue).
func RunAgentLoopContinue(ctx context.Context, agentContext AgentContext, config AgentLoopConfig, emit AgentEventSink, streamFn StreamFn) ([]AgentMessage, error) {
	if len(agentContext.Messages) == 0 {
		return nil, &CannotContinueError{Reason: "Cannot continue: no messages in context"}
	}

	if agentContext.Messages[len(agentContext.Messages)-1].MessageRole() == types.RoleAssistant {
		return nil, &CannotContinueError{Reason: "Cannot continue from message role: assistant"}
	}

	newMessages := []AgentMessage{}
	currentContext := agentContext

	emit(AgentEvent{Type: AgentEventAgentStart})
	emit(AgentEvent{Type: AgentEventTurnStart})

	newMessages = runLoop(ctx, &currentContext, newMessages, config, emit, streamFn)
	return newMessages, nil
}

func createAgentStream() *types.EventStream[AgentEvent, []AgentMessage] {
	return types.NewEventStream(
		func(event AgentEvent) bool { return event.Type == AgentEventAgentEnd },
		func(event AgentEvent) []AgentMessage {
			if event.Type == AgentEventAgentEnd {
				return event.Messages
			}
			return []AgentMessage{}
		},
	)
}

// signalAborted reports cancellation (pi: signal?.aborted; nil ctx never
// aborts, matching an undefined signal).
func signalAborted(ctx context.Context) bool {
	return ctx != nil && ctx.Err() != nil
}

// runLoop is the main loop logic shared by AgentLoop and AgentLoopContinue.
// It returns the final newMessages slice (appends inside may reallocate the
// caller's backing array).
func runLoop(ctx context.Context, initialContext *AgentContext, newMessages []AgentMessage, initialConfig AgentLoopConfig, emit AgentEventSink, streamFunction StreamFn) []AgentMessage {
	currentContext := initialContext
	config := initialConfig
	// pi: lastCompletedTurn — the completed-turn snapshot handed to
	// prepareNextTurn at the start of the next turn (moved from the turn_end
	// site in v0.85.1: preparation now runs immediately before the next turn
	// starts, not before the loop decides whether to continue).
	var lastCompletedTurn *ShouldStopAfterTurnContext
	// Check for steering messages at start (user may have typed while waiting)
	var pendingMessages []AgentMessage
	if config.GetSteeringMessages != nil {
		pendingMessages = config.GetSteeringMessages()
	}
	if streamFunction == nil {
		resolved, err := GetDefaultStreamFn()
		if err != nil {
			// pi throws here after the start events were emitted, leaving the
			// stream unresolved; pig ends with what was collected (see the
			// package comment).
			emit(AgentEvent{Type: AgentEventAgentEnd, Messages: newMessages})
			return newMessages
		}
		streamFunction = resolved
	}

	// Outer loop: continues when queued follow-up messages arrive after agent would stop
	for {
		hasMoreToolCalls := true

		// Inner loop: process tool calls and steering messages
		for hasMoreToolCalls || len(pendingMessages) > 0 {
			if lastCompletedTurn != nil {
				if config.PrepareNextTurn != nil {
					nextTurnSnapshot := config.PrepareNextTurn(*lastCompletedTurn)
					if nextTurnSnapshot != nil {
						if nextTurnSnapshot.Context != nil {
							currentContext = nextTurnSnapshot.Context
						}
						if nextTurnSnapshot.Model != nil {
							config.Model = nextTurnSnapshot.Model
						}
						if nextTurnSnapshot.ThinkingLevel == "" {
							// keep config.Reasoning (pi: undefined ?? config.reasoning)
						} else if nextTurnSnapshot.ThinkingLevel == types.ModelThinkingOff {
							config.Reasoning = ""
						} else {
							config.Reasoning = types.ThinkingLevel(nextTurnSnapshot.ThinkingLevel)
						}
					}
				}
				// Preparation can be long-running (for example, compaction). Pick up
				// steering queued while it ran. Only poll again if the earlier poll
				// returned nothing; otherwise one-at-a-time mode would deliver two
				// messages in this turn.
				if len(pendingMessages) == 0 && config.GetSteeringMessages != nil {
					pendingMessages = config.GetSteeringMessages()
				}
				emit(AgentEvent{Type: AgentEventTurnStart})
			}

			// Process pending messages (inject before next assistant response)
			if len(pendingMessages) > 0 {
				for _, message := range pendingMessages {
					emit(AgentEvent{Type: AgentEventMessageStart, Message: message})
					emit(AgentEvent{Type: AgentEventMessageEnd, Message: message})
					currentContext.Messages = append(currentContext.Messages, message)
					newMessages = append(newMessages, message)
				}
				pendingMessages = nil
			}

			// Stream assistant response
			message := streamAssistantResponse(ctx, currentContext, config, emit, streamFunction)
			newMessages = append(newMessages, message)

			if message.StopReason == types.StopReasonError || message.StopReason == types.StopReasonAborted {
				emit(AgentEvent{Type: AgentEventTurnEnd, Message: message})
				emit(AgentEvent{Type: AgentEventAgentEnd, Messages: newMessages})
				return newMessages
			}

			// Check for tool calls
			var toolCalls []AgentToolCall
			for _, content := range message.Content {
				if content.ContentKind() == types.KindToolCall {
					toolCalls = append(toolCalls, content.(types.ToolCall))
				}
			}

			var toolResults []types.ToolResultMessage
			hasMoreToolCalls = false
			if len(toolCalls) > 0 {
				// A "length" stop means the output was cut off by the token limit, so
				// every tool call in the message may carry truncated arguments. Fail
				// them all instead of executing potentially borked calls.
				var executedToolBatch executedToolCallBatch
				if message.StopReason == types.StopReasonLength {
					executedToolBatch = failToolCallsFromTruncatedMessage(toolCalls, emit)
				} else {
					executedToolBatch = executeToolCalls(ctx, currentContext, &message, config, emit)
				}
				toolResults = append(toolResults, executedToolBatch.messages...)
				hasMoreToolCalls = !executedToolBatch.terminate

				for _, result := range toolResults {
					currentContext.Messages = append(currentContext.Messages, result)
					newMessages = append(newMessages, result)
				}
			}

			emit(AgentEvent{Type: AgentEventTurnEnd, Message: message, ToolResults: toolResults})

			lastCompletedTurn = &ShouldStopAfterTurnContext{
				Message:     message,
				ToolResults: toolResults,
				Context:     currentContext,
				NewMessages: newMessages,
			}

			if config.ShouldStopAfterTurn != nil &&
				config.ShouldStopAfterTurn(*lastCompletedTurn) {
				emit(AgentEvent{Type: AgentEventAgentEnd, Messages: newMessages})
				return newMessages
			}

			if config.GetSteeringMessages != nil {
				pendingMessages = config.GetSteeringMessages()
			} else {
				pendingMessages = nil
			}
		}

		// Agent would stop here. Check for follow-up messages.
		var followUpMessages []AgentMessage
		if config.GetFollowUpMessages != nil {
			followUpMessages = config.GetFollowUpMessages()
		}
		if len(followUpMessages) > 0 {
			// Set as pending so inner loop processes them
			pendingMessages = followUpMessages
			continue
		}

		// No more messages, exit
		break
	}

	emit(AgentEvent{Type: AgentEventAgentEnd, Messages: newMessages})
	return newMessages
}

// streamAssistantResponse streams an assistant response from the LLM. This is
// where AgentMessage[] gets transformed to Message[] for the LLM.
func streamAssistantResponse(ctx context.Context, agentContext *AgentContext, config AgentLoopConfig, emit AgentEventSink, streamFunction StreamFn) types.AssistantMessage {
	// Apply context transform if configured (AgentMessage[] → AgentMessage[])
	messages := agentContext.Messages
	if config.TransformContext != nil {
		messages = config.TransformContext(ctx, messages)
	}

	// Convert to LLM-compatible messages (AgentMessage[] → Message[])
	llmMessages := config.ConvertToLlm(messages)

	// Build LLM context. AgentTool embeds types.Tool; pi passes the
	// AgentTool[] array where Tool[] is expected via structural typing, so pig
	// flattens the wrapper (no logic change).
	var llmTools []types.Tool
	if agentContext.Tools != nil {
		llmTools = make([]types.Tool, len(agentContext.Tools))
		for index, tool := range agentContext.Tools {
			llmTools[index] = tool.Tool
		}
	}
	llmContext := &types.Context{
		SystemPrompt: agentContext.SystemPrompt,
		Messages:     llmMessages,
		Tools:        llmTools,
	}

	// Resolve API key (important for expiring tokens)
	resolvedApiKey := config.APIKey
	if config.GetApiKey != nil {
		if key := config.GetApiKey(config.Model.Provider); key != "" {
			resolvedApiKey = key
		}
	}

	// pi spreads the config into SimpleStreamOptions with the resolved apiKey
	// and signal; pig copies the embedded options struct and passes ctx.
	options := config.SimpleStreamOptions
	options.APIKey = resolvedApiKey

	response := streamFunction(ctx, config.Model, llmContext, &options)

	var partialMessage *types.AssistantMessage
	addedPartial := false

	for event := range response.Events() {
		switch event.Type {
		case types.EventStart:
			partialMessage = event.Partial
			agentContext.Messages = append(agentContext.Messages, *partialMessage)
			addedPartial = true
			emit(AgentEvent{Type: AgentEventMessageStart, Message: *partialMessage})

		case types.EventTextStart,
			types.EventTextDelta,
			types.EventTextEnd,
			types.EventThinkingStart,
			types.EventThinkingDelta,
			types.EventThinkingEnd,
			types.EventToolCallStart,
			types.EventToolCallDelta,
			types.EventToolCallEnd:
			if partialMessage != nil {
				partialMessage = event.Partial
				agentContext.Messages[len(agentContext.Messages)-1] = *partialMessage
				updatedEvent := event
				emit(AgentEvent{
					Type:                  AgentEventMessageUpdate,
					AssistantMessageEvent: &updatedEvent,
					Message:               *partialMessage,
				})
			}

		case types.EventDone, types.EventError:
			finalMessage := response.Result()
			if addedPartial {
				agentContext.Messages[len(agentContext.Messages)-1] = finalMessage
			} else {
				agentContext.Messages = append(agentContext.Messages, finalMessage)
			}
			if !addedPartial {
				emit(AgentEvent{Type: AgentEventMessageStart, Message: finalMessage})
			}
			emit(AgentEvent{Type: AgentEventMessageEnd, Message: finalMessage})
			return finalMessage
		}
	}

	// Stream ended without a terminal event (pi: the for-await exits and
	// result() is still awaited; pig's Result resolves the zero value per the
	// EventStream deviation note).
	finalMessage := response.Result()
	if addedPartial {
		agentContext.Messages[len(agentContext.Messages)-1] = finalMessage
	} else {
		agentContext.Messages = append(agentContext.Messages, finalMessage)
		emit(AgentEvent{Type: AgentEventMessageStart, Message: finalMessage})
	}
	emit(AgentEvent{Type: AgentEventMessageEnd, Message: finalMessage})
	return finalMessage
}

// failToolCallsFromTruncatedMessage fails all tool calls from an assistant
// message that was truncated by the output token limit. Streamed tool-call
// arguments are finalized with a best-effort JSON salvage parser, so a
// truncated message can yield tool calls whose arguments parse and validate
// but are silently incomplete. None of them are safe to execute; report each
// as an error so the model can re-issue them.
func failToolCallsFromTruncatedMessage(toolCalls []AgentToolCall, emit AgentEventSink) executedToolCallBatch {
	var messages []types.ToolResultMessage
	for _, toolCall := range toolCalls {
		emit(AgentEvent{
			Type:       AgentEventToolExecutionStart,
			ToolCallID: toolCall.ID,
			ToolName:   toolCall.Name,
			Args:       toolCall.Arguments,
		})
		finalized := finalizedToolCallOutcome{
			toolCall: toolCall,
			result: createErrorToolResult(
				`Tool call "` + toolCall.Name + `" was not executed: the response hit the output token limit, so its arguments may be truncated. Re-issue the tool call with complete arguments.`,
			),
			isError: true,
		}
		emitToolExecutionEnd(&finalized, emit)
		toolResultMessage := createToolResultMessage(&finalized)
		emitToolResultMessage(&toolResultMessage, emit)
		messages = append(messages, toolResultMessage)
	}
	return executedToolCallBatch{messages: messages, terminate: false}
}

// executeToolCalls executes tool calls from an assistant message.
func executeToolCalls(ctx context.Context, currentContext *AgentContext, assistantMessage *types.AssistantMessage, config AgentLoopConfig, emit AgentEventSink) executedToolCallBatch {
	var toolCalls []AgentToolCall
	for _, content := range assistantMessage.Content {
		if content.ContentKind() == types.KindToolCall {
			toolCalls = append(toolCalls, content.(types.ToolCall))
		}
	}
	hasSequentialToolCall := false
	for _, toolCall := range toolCalls {
		for _, tool := range currentContext.Tools {
			if tool.Name == toolCall.Name && tool.ExecutionMode == ToolExecutionSequential {
				hasSequentialToolCall = true
				break
			}
		}
	}
	if config.ToolExecution == ToolExecutionSequential || hasSequentialToolCall {
		return executeToolCallsSequential(ctx, currentContext, assistantMessage, toolCalls, config, emit)
	}
	return executeToolCallsParallel(ctx, currentContext, assistantMessage, toolCalls, config, emit)
}

// executedToolCallBatch is one batch of executed tool calls (pi:
// ExecutedToolCallBatch).
type executedToolCallBatch struct {
	messages  []types.ToolResultMessage
	terminate bool
}

func executeToolCallsSequential(ctx context.Context, currentContext *AgentContext, assistantMessage *types.AssistantMessage, toolCalls []AgentToolCall, config AgentLoopConfig, emit AgentEventSink) executedToolCallBatch {
	var finalizedCalls []finalizedToolCallOutcome
	var messages []types.ToolResultMessage

	for _, toolCall := range toolCalls {
		emit(AgentEvent{
			Type:       AgentEventToolExecutionStart,
			ToolCallID: toolCall.ID,
			ToolName:   toolCall.Name,
			Args:       toolCall.Arguments,
		})

		preparation := prepareToolCall(ctx, currentContext, assistantMessage, toolCall, config)
		var finalized finalizedToolCallOutcome
		if preparation.immediate {
			finalized = finalizedToolCallOutcome{
				toolCall: toolCall,
				result:   preparation.result,
				isError:  preparation.isError,
			}
		} else {
			executed := executePreparedToolCall(ctx, &preparation, emit)
			finalized = finalizeExecutedToolCall(ctx, currentContext, assistantMessage, &preparation, &executed, config)
		}

		emitToolExecutionEnd(&finalized, emit)
		toolResultMessage := createToolResultMessage(&finalized)
		emitToolResultMessage(&toolResultMessage, emit)
		finalizedCalls = append(finalizedCalls, finalized)
		messages = append(messages, toolResultMessage)

		if signalAborted(ctx) {
			break
		}
	}

	return executedToolCallBatch{
		messages:  messages,
		terminate: shouldTerminateToolBatch(finalizedCalls),
	}
}

func executeToolCallsParallel(ctx context.Context, currentContext *AgentContext, assistantMessage *types.AssistantMessage, toolCalls []AgentToolCall, config AgentLoopConfig, emit AgentEventSink) executedToolCallBatch {
	var finalizedCalls []finalizedToolCallEntry

	for _, toolCall := range toolCalls {
		emit(AgentEvent{
			Type:       AgentEventToolExecutionStart,
			ToolCallID: toolCall.ID,
			ToolName:   toolCall.Name,
			Args:       toolCall.Arguments,
		})

		preparation := prepareToolCall(ctx, currentContext, assistantMessage, toolCall, config)
		if preparation.immediate {
			finalized := finalizedToolCallOutcome{
				toolCall: toolCall,
				result:   preparation.result,
				isError:  preparation.isError,
			}
			emitToolExecutionEnd(&finalized, emit)
			finalizedCalls = append(finalizedCalls, finalizedToolCallEntry{outcome: &finalized})
			if signalAborted(ctx) {
				break
			}
			continue
		}

		prepared := preparation
		finalizedCalls = append(finalizedCalls, finalizedToolCallEntry{run: func() finalizedToolCallOutcome {
			// pi: signal.aborted check before dispatch — cancellation landing
			// between preparation and execution yields an error tool result
			// instead of running the tool.
			if signalAborted(ctx) {
				finalized := finalizedToolCallOutcome{
					toolCall: prepared.toolCall,
					result:   createErrorToolResult("Operation aborted"),
					isError:  true,
				}
				emitToolExecutionEnd(&finalized, emit)
				return finalized
			}
			executed := executePreparedToolCall(ctx, &prepared, emit)
			finalized := finalizeExecutedToolCall(ctx, currentContext, assistantMessage, &prepared, &executed, config)
			emitToolExecutionEnd(&finalized, emit)
			return finalized
		}})
		if signalAborted(ctx) {
			break
		}
	}

	// pi resolves the entries with Promise.all: deferred entries run
	// concurrently, results collect in source order. pig mirrors that by
	// launching every run entry and receiving in source order.
	orderedFinalizedCalls := make([]finalizedToolCallOutcome, len(finalizedCalls))
	doneChans := make([]chan finalizedToolCallOutcome, len(finalizedCalls))
	for index, entry := range finalizedCalls {
		if entry.run != nil {
			doneChans[index] = make(chan finalizedToolCallOutcome, 1)
			run := entry.run
			done := doneChans[index]
			go func() { done <- run() }()
		}
	}
	for index, entry := range finalizedCalls {
		if entry.outcome != nil {
			orderedFinalizedCalls[index] = *entry.outcome
		} else {
			orderedFinalizedCalls[index] = <-doneChans[index]
		}
	}

	var messages []types.ToolResultMessage
	for index := range orderedFinalizedCalls {
		toolResultMessage := createToolResultMessage(&orderedFinalizedCalls[index])
		emitToolResultMessage(&toolResultMessage, emit)
		messages = append(messages, toolResultMessage)
	}

	return executedToolCallBatch{
		messages:  messages,
		terminate: shouldTerminateToolBatch(orderedFinalizedCalls),
	}
}

// toolCallPreparation folds pi's PreparedToolCall | ImmediateToolCallOutcome
// union into one struct discriminated by immediate.
type toolCallPreparation struct {
	immediate bool
	// immediate payload (pi: ImmediateToolCallOutcome)
	result  AgentToolResult
	isError bool
	// prepared payload (pi: PreparedToolCall)
	toolCall AgentToolCall
	tool     *AgentTool
	args     any
}

// executedToolCallOutcome is the raw outcome of running a prepared tool call
// (pi: ExecutedToolCallOutcome).
type executedToolCallOutcome struct {
	result  AgentToolResult
	isError bool
}

// finalizedToolCallOutcome is a tool call with its final result (pi:
// FinalizedToolCallOutcome).
type finalizedToolCallOutcome struct {
	toolCall AgentToolCall
	result   AgentToolResult
	isError  bool
}

// finalizedToolCallEntry is pi's FinalizedToolCallEntry union (immediate
// outcome, or a deferred run resolving to one).
type finalizedToolCallEntry struct {
	outcome *finalizedToolCallOutcome
	run     func() finalizedToolCallOutcome
}

func shouldTerminateToolBatch(finalizedCalls []finalizedToolCallOutcome) bool {
	if len(finalizedCalls) == 0 {
		return false
	}
	for _, finalized := range finalizedCalls {
		if !finalized.result.Terminate {
			return false
		}
	}
	return true
}

// sameMapReference reports whether two maps are the same object (pi compares
// `preparedArguments === toolCall.arguments`; Go maps are not comparable).
func sameMapReference(a, b any) bool {
	aMap, aOk := a.(map[string]any)
	bMap, bOk := b.(map[string]any)
	if !aOk || !bOk {
		return false
	}
	return reflect.ValueOf(aMap).Pointer() == reflect.ValueOf(bMap).Pointer()
}

func prepareToolCallArguments(tool *AgentTool, toolCall AgentToolCall) AgentToolCall {
	if tool.PrepareArguments == nil {
		return toolCall
	}
	preparedArguments := tool.PrepareArguments(toolCall.Arguments)
	if sameMapReference(preparedArguments, toolCall.Arguments) {
		return toolCall
	}
	prepared := toolCall
	prepared.Arguments = preparedArguments.(map[string]any)
	return prepared
}

func prepareToolCall(ctx context.Context, currentContext *AgentContext, assistantMessage *types.AssistantMessage, toolCall AgentToolCall, config AgentLoopConfig) toolCallPreparation {
	var tool *AgentTool
	for _, candidate := range currentContext.Tools {
		if candidate.Name == toolCall.Name {
			tool = candidate
			break
		}
	}
	if tool == nil {
		return toolCallPreparation{
			immediate: true,
			result:    createErrorToolResult("Tool " + toolCall.Name + " not found"),
			isError:   true,
		}
	}

	preparedToolCall := prepareToolCallArguments(tool, toolCall)
	validatedArgs, err := ai.ValidateToolArguments(&tool.Tool, &preparedToolCall)
	if err != nil {
		return toolCallPreparation{
			immediate: true,
			result:    createErrorToolResult(err.Error()),
			isError:   true,
		}
	}
	if config.BeforeToolCall != nil {
		beforeResult, err := config.BeforeToolCall(ctx, BeforeToolCallContext{
			AssistantMessage: *assistantMessage,
			ToolCall:         toolCall,
			Args:             validatedArgs,
			Context:          currentContext,
		})
		if err != nil {
			return toolCallPreparation{
				immediate: true,
				result:    createErrorToolResult(err.Error()),
				isError:   true,
			}
		}
		if signalAborted(ctx) {
			return toolCallPreparation{
				immediate: true,
				result:    createErrorToolResult("Operation aborted"),
				isError:   true,
			}
		}
		if beforeResult != nil && beforeResult.Block {
			reason := beforeResult.Reason
			if reason == "" {
				reason = "Tool execution was blocked"
			}
			result := createErrorToolResult(reason)
			if beforeResult.Terminate {
				result.Terminate = true
			}
			return toolCallPreparation{
				immediate: true,
				result:    result,
				isError:   true,
			}
		}
	}
	if signalAborted(ctx) {
		return toolCallPreparation{
			immediate: true,
			result:    createErrorToolResult("Operation aborted"),
			isError:   true,
		}
	}
	return toolCallPreparation{
		toolCall: toolCall,
		tool:     tool,
		args:     validatedArgs,
	}
}

func executePreparedToolCall(ctx context.Context, prepared *toolCallPreparation, emit AgentEventSink) executedToolCallOutcome {
	// pi queues update emissions as promises and awaits them after execute
	// returns; emissions themselves are synchronous in the onUpdate callback,
	// so pig emits inline behind the same acceptingUpdates gate. The gate is
	// atomic: settled tools' onUpdate may be invoked from another goroutine
	// (pi relies on the single-threaded event loop here).
	var acceptingUpdates atomic.Bool
	acceptingUpdates.Store(true)
	onUpdate := func(partialResult AgentToolResult) {
		if !acceptingUpdates.Load() {
			return
		}
		emit(AgentEvent{
			Type:          AgentEventToolExecutionUpdate,
			ToolCallID:    prepared.toolCall.ID,
			ToolName:      prepared.toolCall.Name,
			Args:          prepared.toolCall.Arguments,
			PartialResult: partialResult,
		})
	}

	result, err := prepared.tool.Execute(ctx, prepared.toolCall.ID, prepared.args, onUpdate)
	acceptingUpdates.Store(false)
	if err != nil {
		return executedToolCallOutcome{
			result:  createErrorToolResult(err.Error()),
			isError: true,
		}
	}
	return executedToolCallOutcome{result: result, isError: false}
}

func finalizeExecutedToolCall(ctx context.Context, currentContext *AgentContext, assistantMessage *types.AssistantMessage, prepared *toolCallPreparation, executed *executedToolCallOutcome, config AgentLoopConfig) finalizedToolCallOutcome {
	result := executed.result
	isError := executed.isError

	if config.AfterToolCall != nil {
		afterResult, err := config.AfterToolCall(ctx, AfterToolCallContext{
			AssistantMessage: *assistantMessage,
			ToolCall:         prepared.toolCall,
			Args:             prepared.args,
			Result:           result,
			IsError:          isError,
			Context:          currentContext,
		})
		if err != nil {
			result = createErrorToolResult(err.Error())
			isError = true
		} else if afterResult != nil {
			if afterResult.Content != nil {
				result.Content = afterResult.Content
			}
			if afterResult.Details != nil {
				result.Details = afterResult.Details
			}
			if afterResult.Usage != nil {
				result.Usage = afterResult.Usage
			}
			if afterResult.Terminate != nil {
				result.Terminate = *afterResult.Terminate
			}
			if afterResult.IsError != nil {
				isError = *afterResult.IsError
			}
		}
	}

	return finalizedToolCallOutcome{
		toolCall: prepared.toolCall,
		result:   result,
		isError:  isError,
	}
}

func createErrorToolResult(message string) AgentToolResult {
	return AgentToolResult{
		Content: []types.Content{types.NewTextContent(message)},
		Details: map[string]any{},
	}
}

func emitToolExecutionEnd(finalized *finalizedToolCallOutcome, emit AgentEventSink) {
	emit(AgentEvent{
		Type:       AgentEventToolExecutionEnd,
		ToolCallID: finalized.toolCall.ID,
		ToolName:   finalized.toolCall.Name,
		Result:     finalized.result,
		IsError:    finalized.isError,
	})
}

func createToolResultMessage(finalized *finalizedToolCallOutcome) types.ToolResultMessage {
	content := finalized.result.Content
	// Untyped tools (JS extensions) can return results without content; normalize
	// so the nil never enters session history or provider payloads.
	if content == nil {
		content = []types.Content{}
	}
	toolResultMessage := types.ToolResultMessage{
		Role:       types.RoleToolResult,
		ToolCallID: finalized.toolCall.ID,
		ToolName:   finalized.toolCall.Name,
		Content:    content,
		Details:    finalized.result.Details,
		Usage:      finalized.result.Usage,
		IsError:    finalized.isError,
		Timestamp:  time.Now().UnixMilli(),
	}
	if len(finalized.result.AddedToolNames) > 0 {
		toolResultMessage.AddedToolNames = finalized.result.AddedToolNames
	}
	return toolResultMessage
}

func emitToolResultMessage(toolResultMessage *types.ToolResultMessage, emit AgentEventSink) {
	emit(AgentEvent{Type: AgentEventMessageStart, Message: *toolResultMessage})
	emit(AgentEvent{Type: AgentEventMessageEnd, Message: *toolResultMessage})
}
