// pi: types.ts — the agent package's type surface. AgentMessage is the union
// of LLM messages plus app-custom messages; AgentLoopConfig and the hook
// contexts are consumed by agent-loop.ts.
//
// Type mapping deviations, all declared here:
//
//   - AgentMessage (pi: Message | CustomAgentMessages[keyof CustomAgentMessages])
//     is a bare alias of types.Message. Go interfaces are already open: a
//     custom app message is any type implementing MessageRole(), so TS's
//     declaration-merging extension point (CustomAgentMessages) needs no
//     counterpart. pi's custom messages ride the same AgentMessage channel
//     and are filtered/converted by convertToLlm; Go consumers do the same.
//   - AgentTool<TParameters, TDetails> loses its generics: parameters arrive
//     as JSON-Schema-validated map[string]any (pi: Static<TParameters>) and
//     details as any (pi: TDetails). Per the TS conditional-type rule, per-
//     tool parameter typing would need per-tool option types with no
//     consumer benefit today.
//   - TS accessor properties on AgentState (get/set tools/messages) become
//     plain struct fields; the copy-on-assign behavior belongs to the Agent
//     implementation (pi: agent.ts, future agent.go), not the data shape.
//   - pi's ThinkingLevel here re-declares the pi-ai union with "off" added;
//     pig already models exactly that union as types.ModelThinkingLevel, so
//     ThinkingLevel aliases it.

package agent

import (
	"context"

	"github.com/ZoroSpace/pig/ai/types"
)

// StreamFn is the stream function used by the agent loop. `Models.StreamSimple`
// satisfies this shape.
//
// Contract:
//   - Must not throw or return a rejected promise for request/model/runtime
//     failures; Go encoding: must not panic on those either.
//   - Must return an AssistantMessageEventStream.
//   - Failures must be encoded in the returned stream via protocol events and
//     a final AssistantMessage with stopReason "error" or "aborted" and
//     errorMessage.
//
// ctx carries the caller's cancellation (pi: signal travels through the
// options object); per the AbortSignal → context rule it is a parameter here.
type StreamFn func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream

// ToolExecutionMode configures how tool calls from a single assistant message
// are executed.
//
//   - "sequential": each tool call is prepared, executed, and finalized before
//     the next one starts.
//   - "parallel": tool calls are prepared sequentially, then allowed tools
//     execute concurrently. `tool_execution_end` is emitted in tool completion
//     order after each tool is finalized, while tool-result message artifacts
//     are emitted later in assistant source order.
type ToolExecutionMode string

const (
	ToolExecutionSequential ToolExecutionMode = "sequential"
	ToolExecutionParallel   ToolExecutionMode = "parallel"
)

// QueueMode controls how many queued user messages are injected when the
// agent loop reaches a queue drain point.
//
//   - "all": drain and inject every queued message at that point.
//   - "one-at-a-time": drain and inject only the oldest queued message,
//     leaving the rest queued for later drain points.
type QueueMode string

const (
	QueueModeAll        QueueMode = "all"
	QueueModeOneAtATime QueueMode = "one-at-a-time"
)

// AgentToolCall is a single tool call content block emitted by an assistant
// message (pi: Extract<AssistantMessage["content"][number], { type:
// "toolCall" }>); pig's content model already names this type.
type AgentToolCall = types.ToolCall

// BeforeToolCallResult is returned from `beforeToolCall`.
//
// Returning Block=true prevents the tool from executing. The loop emits an
// error tool result instead. Reason becomes the text shown in that error
// result. If empty, a default blocked message is used.
type BeforeToolCallResult struct {
	Block  bool
	Reason string
	// Terminate hints that the agent should stop after the current tool batch
	// when this call is blocked. Early termination only happens when every
	// finalized tool result in the batch sets this to true.
	Terminate bool
}

// AfterToolCallResult is the partial override returned from `afterToolCall`.
//
// Merge semantics are field-by-field:
//   - Content: if non-nil, replaces the tool result content array in full
//   - Details: if non-nil, replaces the tool result details value in full
//   - IsError: if non-nil, replaces the tool result error flag
//   - Usage: if non-nil, replaces the tool result usage
//   - Terminate: if non-nil, replaces the early-termination hint
//
// Nil fields keep the original executed tool result values. There is no deep
// merge for Content, Details, or Usage. (pi distinguishes undefined from an
// explicit null override; Go folds both into nil — a hook cannot null out
// Details, matching the practical override use case.)
type AfterToolCallResult struct {
	Content []types.Content
	Details any
	IsError *bool
	// Usage from the final tool execution itself, if available. Not used for
	// main LLM context accounting.
	Usage *types.Usage
	// Terminate hints that the agent should stop after the current tool batch.
	// Early termination only happens when every finalized tool result in the
	// batch sets this to true.
	Terminate *bool
}

// BeforeToolCallContext is passed to `beforeToolCall`.
type BeforeToolCallContext struct {
	// AssistantMessage is the assistant message that requested the tool call.
	AssistantMessage types.AssistantMessage
	// ToolCall is the raw tool call block from AssistantMessage.Content.
	ToolCall AgentToolCall
	// Args are the validated tool arguments for the target tool schema.
	Args any
	// Context is the current agent context at the time the tool call is
	// prepared.
	Context *AgentContext
}

// AfterToolCallContext is passed to `afterToolCall`.
type AfterToolCallContext struct {
	// AssistantMessage is the assistant message that requested the tool call.
	AssistantMessage types.AssistantMessage
	// ToolCall is the raw tool call block from AssistantMessage.Content.
	ToolCall AgentToolCall
	// Args are the validated tool arguments for the target tool schema.
	Args any
	// Result is the executed tool result before any `afterToolCall` overrides
	// are applied.
	Result AgentToolResult
	// IsError reports whether the executed tool result is currently treated
	// as an error.
	IsError bool
	// Context is the current agent context at the time the tool call is
	// finalized.
	Context *AgentContext
}

// ShouldStopAfterTurnContext is passed to `shouldStopAfterTurn`.
type ShouldStopAfterTurnContext struct {
	// Message is the assistant message that completed the turn.
	Message types.AssistantMessage
	// ToolResults are the tool result messages passed to the preceding
	// `turn_end` event.
	ToolResults []types.ToolResultMessage
	// Context is the current agent context after the turn's assistant message
	// and tool results have been appended.
	Context *AgentContext
	// NewMessages are the messages this loop invocation will return if it
	// exits at this point. Prompt runs include the initial prompt messages;
	// continuation runs do not include pre-existing context messages.
	NewMessages []AgentMessage
}

// AgentLoopTurnUpdate is replacement runtime state used by the agent loop
// before starting another provider request.
type AgentLoopTurnUpdate struct {
	// Context is the context for the next provider request.
	Context *AgentContext
	// Messages are appended before the next provider request, with normal
	// lifecycle events (pi v0.86.0: messages).
	Messages []AgentMessage
	// Model is the model for the next provider request.
	Model *types.Model
	// ThinkingLevel is the thinking level for the next provider request.
	ThinkingLevel ThinkingLevel
}

// PrepareNextTurnContext is the context passed to `prepareNextTurn` (pi
// re-declares ShouldStopAfterTurnContext unchanged).
type PrepareNextTurnContext = ShouldStopAfterTurnContext

// AgentLoopConfig extends SimpleStreamOptions with the agent loop's hooks.
// Field comments carry pi's behavioral contracts verbatim; all callbacks are
// synchronous in pig (pi's Promise-returning hooks resolve inside the loop's
// own await points), and ctx carries the abort signal (pi: signal parameter).
type AgentLoopConfig struct {
	types.SimpleStreamOptions

	Model *types.Model

	// ConvertToLlm converts AgentMessage[] to LLM-compatible Message[] before
	// each LLM call.
	//
	// Each AgentMessage must be converted to a SystemMessage, UserMessage,
	// AssistantMessage, or ToolResultMessage that the LLM can understand.
	// AgentMessages that cannot be converted (e.g., UI-only notifications,
	// status messages) should be filtered out.
	//
	// Contract: must not panic. Return a safe fallback value instead.
	// Panicking interrupts the low-level agent loop without producing a
	// normal event sequence.
	ConvertToLlm func(messages []AgentMessage) []types.Message

	// TransformContext is an optional transform applied to the context before
	// `convertToLlm`.
	//
	// Use this for operations that work at the AgentMessage level:
	//   - Context window management (pruning old messages)
	//   - Injecting context from external sources
	//
	// Contract: must not panic. Return the original messages or another safe
	// fallback value instead.
	TransformContext func(ctx context.Context, messages []AgentMessage) []AgentMessage

	// GetApiKey resolves an API key dynamically for each LLM call.
	//
	// Useful for short-lived OAuth tokens (e.g., GitHub Copilot) that may
	// expire during long-running tool execution phases.
	//
	// Contract: must not panic. Return "" when no key is available.
	GetApiKey func(provider types.ProviderID) string

	// ShouldStopAfterTurn is called after each turn fully completes and
	// `turn_end` has been emitted.
	//
	// If it returns true, the loop emits `agent_end` and exits before polling
	// steering or follow-up queues, without starting another LLM call. The
	// current assistant response and any tool executions finish normally.
	// This callback sees the completed-turn context and runs before
	// `PrepareNextTurn`.
	//
	// Use this to request a graceful stop after the current turn, e.g. before
	// context gets too full.
	//
	// Contract: must not panic. Panicking interrupts the low-level agent loop
	// without producing a normal event sequence.
	ShouldStopAfterTurn func(context ShouldStopAfterTurnContext) bool

	// PrepareNextTurn is called after `turn_end` when the loop will continue,
	// immediately before the next turn starts. Return replacement
	// context/model/thinking state to affect that turn.
	// Return nil to keep using the current context/config.
	PrepareNextTurn func(context PrepareNextTurnContext) *AgentLoopTurnUpdate

	// GetSteeringMessages returns steering messages to inject into the
	// conversation mid-run.
	//
	// Called after the current assistant turn finishes executing its tool
	// calls, unless `shouldStopAfterTurn` exits first. If messages are
	// returned, they are added to the context before the next LLM call. Tool
	// calls from the current assistant message are not skipped.
	//
	// Use this for "steering" the agent while it's working.
	//
	// Contract: must not panic. Return nil when no steering messages are
	// available.
	GetSteeringMessages func() []AgentMessage

	// GetFollowUpMessages returns follow-up messages to process after the
	// agent would otherwise stop.
	//
	// Called when the agent has no more tool calls and no steering messages.
	// If messages are returned, they're added to the context and the agent
	// continues with another turn.
	//
	// Use this for follow-up messages that should wait until the agent
	// finishes.
	//
	// Contract: must not panic. Return nil when no follow-up messages are
	// available.
	GetFollowUpMessages func() []AgentMessage

	// ToolExecution selects the tool execution mode.
	//   - "sequential": execute tool calls one by one
	//   - "parallel": preflight tool calls sequentially, then execute allowed
	//     tools concurrently; emit `tool_execution_end` in tool completion
	//     order after each tool is finalized, then emit tool-result message
	//     artifacts later in assistant source order
	//
	// Default: "parallel" (empty value).
	ToolExecution ToolExecutionMode

	// BeforeToolCall is called before a tool is executed, after arguments
	// have been validated.
	//
	// Return a BeforeToolCallResult with Block=true to prevent execution. The
	// loop emits an error tool result instead. A blocked result can also set
	// Terminate to participate in the batch early-termination rule. The hook
	// receives the agent context and is responsible for honoring
	// cancellation.
	//
	// Returning a non-nil error models pi's hook throwing: the loop converts
	// it into an error tool result.
	BeforeToolCall func(ctx context.Context, context BeforeToolCallContext) (*BeforeToolCallResult, error)

	// AfterToolCall is called after a tool finishes executing, before
	// `tool_execution_end` and tool-result message events are emitted.
	//
	// Return an AfterToolCallResult to override parts of the executed tool
	// result:
	//   - Content replaces the full content array
	//   - Details replaces the full details payload
	//   - IsError replaces the error flag
	//   - Usage replaces the tool result usage
	//   - Terminate replaces the early-termination hint
	//
	// Any nil fields keep their original values. No deep merge is performed.
	// The hook receives the agent context and is responsible for honoring
	// cancellation.
	//
	// Returning a non-nil error models pi's hook throwing: the loop converts
	// it into an error tool result.
	AfterToolCall func(ctx context.Context, context AfterToolCallContext) (*AfterToolCallResult, error)
}

// ThinkingLevel is the thinking/reasoning level for models that support it.
// pi's agent re-declares the pi-ai union with "off" added; that exact union
// is types.ModelThinkingLevel in pig. Note: "xhigh" and "max" are only
// supported by selected model families. Use model thinking-level metadata
// from the ai package to detect support for a concrete model.
type ThinkingLevel = types.ModelThinkingLevel

// CustomAgentMessages is missing here by design (pi: interface
// CustomAgentMessages, empty by default, extended via declaration merging).
// Go has no declaration merging: custom message types are declared in the
// app and satisfy AgentMessage directly, since the Go AgentMessage union is
// the open Message interface (one method) rather than a closed union.

// AgentMessage is the union of LLM messages plus custom messages. This
// abstraction allows apps to add custom message types while maintaining type
// safety and compatibility with the base LLM messages (pi: Message |
// CustomAgentMessages[keyof CustomAgentMessages]; in Go the union is simply
// the open types.Message interface — any type implementing MessageRole(),
// including app-defined ones — so a bare alias suffices).
type AgentMessage = types.Message

// AgentState is the public agent state.
//
// Tools and Messages are plain fields in pig; the copy-on-assign accessor
// behavior pi gets from setter properties is enforced by the Agent
// implementation (pi: agent.ts, future agent.go).
type AgentState struct {
	// SystemPrompt is the current system prompt, replayed from the
	// transcript's system messages. Read-only: to change the prompt, append
	// a system message with Content or Sections. In the initial state this
	// seeds the leading system message (pi v0.86.0: readonly getter).
	SystemPrompt string
	// Model is the active model used for future turns.
	Model *types.Model
	// ThinkingLevel is the requested reasoning level for future turns.
	ThinkingLevel ThinkingLevel
	// Tools are the executable tools. Assigning a new slice copies the
	// top-level slice. Differences from the tools declared in the transcript
	// are announced to the model with a system message before the next
	// request.
	Tools []AgentTool
	// Messages is the conversation transcript. Assigning a new slice copies
	// the top-level slice. System messages in the transcript carry the
	// prompt and tool declarations.
	Messages []AgentMessage
	// IsStreaming is true while the agent is processing a prompt or
	// continuation.
	//
	// This remains true until awaited `agent_end` listeners settle.
	IsStreaming bool
	// StreamingMessage is the partial assistant message for the current
	// streamed response, if any.
	StreamingMessage AgentMessage
	// PendingToolCalls are the tool call ids currently executing.
	PendingToolCalls map[string]bool
	// ErrorMessage is from the most recent failed or aborted assistant turn,
	// if any.
	ErrorMessage string
}

// AgentToolResult is the final or partial result produced by a tool.
type AgentToolResult struct {
	// Content is the text or image content returned to the model.
	Content []types.Content
	// Details is arbitrary structured details for logs or UI rendering.
	Details any
	// Usage, from the final tool execution itself, if available. Not used for
	// main LLM context accounting.
	Usage *types.Usage
	// pi v0.86.0 removed AgentToolResult.addedToolNames: tool introductions
	// moved to transcript system messages.
	StopAfterTool *bool
	// Terminate hints that the agent should stop after the current tool
	// batch. Early termination only happens when every finalized tool result
	// in the batch sets this to true.
	Terminate bool
}

// AgentToolUpdateCallback is the callback used by tools to stream partial
// execution updates.
//
// The callback is scoped to the current Execute() invocation. Calls made
// after the tool returns are ignored.
type AgentToolUpdateCallback func(partialResult AgentToolResult)

// AgentTool is a tool definition used by the agent runtime (pi:
// AgentTool<TParameters extends TSchema, TDetails> extends Tool<TParameters>;
// generics are dropped — see the package comment). Execute returns an error
// on failure instead of encoding errors in Content.
type AgentTool struct {
	types.Tool

	// Label is a human-readable label for UI display.
	Label string
	// PrepareArguments is an optional compatibility shim for raw tool-call
	// arguments before schema validation. Must return a value that matches
	// the tool's parameter schema. Returning the input map unchanged (the
	// same reference) means "nothing to prepare"; the loop detects a new map
	// and swaps it into the tool call.
	PrepareArguments func(args any) any
	// Execute runs the tool call. ctx carries the agent's cancellation (pi:
	// signal). Return an error on failure; the loop converts it into an error
	// tool result.
	Execute func(ctx context.Context, toolCallID string, params any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error)
	// ExecutionMode is the per-tool execution mode override.
	//   - "sequential": this tool must execute one at a time with other tool
	//     calls.
	//   - "parallel": this tool can execute concurrently with other tool
	//     calls.
	//
	// If empty, the default execution mode applies.
	ExecutionMode ToolExecutionMode
	// Replay is the recovery policy for an effect whose durable intent
	// exists but whose outcome is unknown (pi: AgentTool.replay).
	Replay AgentToolReplay
}

// AgentToolReplay is the recovery policy carried by AgentTool.Replay (pi:
// `replay?: "never" | "safe"`; closed string union → named type per the
// ToolExecutionMode precedent).
type AgentToolReplay string

const (
	// AgentToolReplayNever means the effect must never be re-issued; its
	// durable intent may exist but the outcome is unknown.
	AgentToolReplayNever AgentToolReplay = "never"
	// AgentToolReplaySafe means re-issuing the effect is safe when its
	// outcome is unknown.
	AgentToolReplaySafe AgentToolReplay = "safe"
)

// AgentContext is the context snapshot passed into the low-level agent loop.
type AgentContext struct {
	// Messages is the transcript visible to the model.
	Messages []AgentMessage
	// Tools are available for execution in this run.
	Tools []*AgentTool
}

// AgentEventType discriminates the events emitted by the Agent for UI
// updates (pi: AgentEvent discriminated union; pig uses a single struct where
// exactly the fields named by Type are meaningful).
type AgentEventType string

const (
	AgentEventAgentStart          AgentEventType = "agent_start"
	AgentEventAgentEnd            AgentEventType = "agent_end"
	AgentEventTurnStart           AgentEventType = "turn_start"
	AgentEventTurnEnd             AgentEventType = "turn_end"
	AgentEventMessageStart        AgentEventType = "message_start"
	AgentEventMessageUpdate       AgentEventType = "message_update"
	AgentEventMessageEnd          AgentEventType = "message_end"
	AgentEventToolExecutionStart  AgentEventType = "tool_execution_start"
	AgentEventToolExecutionUpdate AgentEventType = "tool_execution_update"
	AgentEventToolExecutionEnd    AgentEventType = "tool_execution_end"
)

// AgentEvent is one event emitted by the agent loop. pi models it as a
// discriminated union; pig uses a single struct where exactly the fields
// named by Type are meaningful:
//
//	agent_start            (no payload)
//	agent_end              Messages
//	turn_start             (no payload)
//	turn_end               Message, ToolResults
//	message_start          Message
//	message_update         Message, AssistantMessageEvent — only emitted for
//	                       assistant messages during streaming
//	message_end            Message
//	tool_execution_start   ToolCallID, ToolName, Args
//	tool_execution_update  ToolCallID, ToolName, Args, PartialResult
//	tool_execution_end     ToolCallID, ToolName, Result, IsError
type AgentEvent struct {
	Type AgentEventType

	// Messages (agent_end) are the messages produced by the run. agent_end is
	// the last event emitted for a run, but awaited Agent.subscribe()
	// listeners for that event are still part of run settlement. The agent
	// becomes idle only after those listeners finish.
	Messages []AgentMessage
	// Message (turn_end, message_start, message_update, message_end) is the
	// message the event refers to.
	Message AgentMessage
	// ToolResults (turn_end) are the tool result messages of the turn.
	ToolResults []types.ToolResultMessage
	// AssistantMessageEvent (message_update) is the raw streaming event.
	AssistantMessageEvent *types.AssistantMessageEvent

	// ToolCallID (tool_execution_*) identifies the tool call.
	ToolCallID string
	// ToolName (tool_execution_*) names the tool.
	ToolName string
	// Args (tool_execution_*) are the raw tool call arguments.
	Args any
	// PartialResult (tool_execution_update) is the streamed partial result.
	PartialResult any
	// Result (tool_execution_end) is the finalized AgentToolResult.
	Result any
	// IsError (tool_execution_end) reports tool failure.
	IsError bool
}
