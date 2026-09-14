// pi: agent.ts — stateful wrapper around the low-level agent loop.
//
// `Agent` owns the current transcript, emits lifecycle events, executes
// tools, and exposes queueing APIs for steering and follow-up messages.
//
// Mapping deviations, all declared here:
//
//   - TS's single-threaded event loop guaranteed unsynchronized reads of
//     agent state from other "tasks". pig guards state, queues, listeners,
//     and the active run with one mutex, and adds Snapshot() for race-safe
//     reads from goroutines that are not the run goroutine. Direct reads
//     through State() are safe from listener callbacks (same goroutine as
//     the state write) or when idle.
//   - pi's state.tools/state.messages accessor properties copy the assigned
//     top-level array. Go cannot intercept field assignment, so the copy
//     semantics live on SetTools/SetMessages; plain field writes through
//     State() do not copy.
//   - pi's runWithLifecycle catches ANY throw from the executor (a throwing
//     streamFn, a throwing hook, a rejected listener) and converts it into
//     the failure event sequence. pig models that catch-all with recover()
//     around the executor; a panic in a parallel tool goroutine is NOT
//     covered (different goroutine) — tools report failures via error
//     returns, matching the loop's contract.
//   - processEvents' "Agent listener invoked outside active run" throw is
//     unreachable in pig (the event sink is only installed inside
//     runWithLifecycle); it panics to keep the invariant loud, and the
//     panic lands in the same recover → handleRunFailure path as TS's
//     throw.
//   - prompt() overloads: Go splits into Prompt(ctx, messages...) (the
//     AgentMessage | AgentMessage[] overload) and PromptText(ctx, input,
//     images...) (the string + images overload).
//   - AbortController → ctx+cancel; the run context derives from the ctx
//     passed to Prompt/Continue (pig addition: caller ctx cancellation also
//     aborts the run; pi only aborts via agent.abort()).
package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/utils"
)

// defaultConvertToLlm is the default convertToLlm: pass standard messages
// through, drop everything else.
func defaultConvertToLlm(messages []AgentMessage) []types.Message {
	var out []types.Message
	for _, message := range messages {
		switch message.MessageRole() {
		case types.RoleUser, types.RoleAssistant, types.RoleToolResult:
			out = append(out, message)
		}
	}
	return out
}

var emptyUsage = types.Usage{
	Cost: types.Cost{},
}

var defaultModel = &types.Model{
	ID:            "unknown",
	Name:          "unknown",
	API:           "unknown",
	Provider:      "unknown",
	BaseURL:       "",
	Reasoning:     false,
	Input:         []types.ModelInput{},
	Cost:          types.ModelCost{},
	ContextWindow: 0,
	MaxTokens:     0,
}

// AgentOptions are the options for constructing an Agent (pi: AgentOptions).
// Hook ctx parameters carry the active run's cancellation (pi: the signal
// argument); unset optional fields use their zero value.
type AgentOptions struct {
	// InitialState seeds the agent state; nil/zero fields use defaults
	// (pi: Partial<Omit<AgentState, ...>>).
	InitialState *AgentState
	ConvertToLlm func(messages []AgentMessage) []types.Message
	// TransformContext is applied to the context before convertToLlm (pi:
	// (messages, signal?); ctx carries the run cancellation).
	TransformContext func(ctx context.Context, messages []AgentMessage) []AgentMessage
	// StreamFn is required; when nil, the default stream function must be
	// configured (SetDefaultStreamFn) or NewAgent fails (pi: throws).
	StreamFn StreamFn
	// GetApiKey resolves an API key per LLM call (pi: (provider) =>
	// string | undefined | Promise<...>; sync in pig, "" = no key).
	GetApiKey func(provider types.ProviderID) string
	// OnPayload and OnResponse forward to the stream options.
	OnPayload  func(payload any, model *types.Model) any
	OnResponse func(response types.ProviderResponse, model *types.Model)
	// BeforeToolCall and AfterToolCall forward to the loop config; their
	// error returns model pi's hook throws (caught by the loop).
	BeforeToolCall func(ctx context.Context, context BeforeToolCallContext) (*BeforeToolCallResult, error)
	AfterToolCall  func(ctx context.Context, context AfterToolCallContext) (*AfterToolCallResult, error)
	// ShouldStopAfterTurn forwards to the loop config with the run context
	// injected (pi: (context, signal)).
	ShouldStopAfterTurn func(ctx context.Context, context ShouldStopAfterTurnContext) bool
	// PrepareNextTurn receives only the run context (pi: (signal)).
	PrepareNextTurn func(ctx context.Context) *AgentLoopTurnUpdate
	// PrepareNextTurnWithContext receives the turn context and the run
	// context (pi: (context, signal)); it wins over PrepareNextTurn.
	PrepareNextTurnWithContext func(ctx context.Context, context PrepareNextTurnContext) *AgentLoopTurnUpdate
	// SteeringMode and FollowUpMode control queue draining; empty defaults
	// to QueueModeOneAtATime.
	SteeringMode QueueMode
	FollowUpMode QueueMode
	// SessionID forwards to providers for cache-aware backends.
	SessionID string
	// ThinkingBudgets forwards per-level thinking token budgets to the stream
	// function.
	ThinkingBudgets *types.ThinkingBudgets
	// Transport is the preferred transport; empty defaults to
	// types.TransportAuto.
	Transport types.Transport
	// MaxRetryDelayMs caps provider-requested retry delays.
	MaxRetryDelayMs *int64
	// ToolExecution is the tool execution strategy for assistant messages
	// that contain multiple tool calls; empty defaults to
	// ToolExecutionParallel.
	ToolExecution ToolExecutionMode
}

// pendingMessageQueue is one steering/follow-up queue (pi:
// PendingMessageQueue). pig adds a mutex: enqueue runs on the caller's
// goroutine while the run goroutine drains.
type pendingMessageQueue struct {
	mu       sync.Mutex
	messages []AgentMessage
	mode     QueueMode
}

func newPendingMessageQueue(mode QueueMode) *pendingMessageQueue {
	return &pendingMessageQueue{mode: mode}
}

func (q *pendingMessageQueue) enqueue(message AgentMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = append(q.messages, message)
}

func (q *pendingMessageQueue) hasItems() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages) > 0
}

func (q *pendingMessageQueue) drain() []AgentMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.mode == QueueModeAll {
		drained := q.messages
		q.messages = nil
		return drained
	}
	if len(q.messages) == 0 {
		return nil
	}
	first := q.messages[0]
	q.messages = q.messages[1:]
	return []AgentMessage{first}
}

func (q *pendingMessageQueue) clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = nil
}

func (q *pendingMessageQueue) setMode(mode QueueMode) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.mode = mode
}

func (q *pendingMessageQueue) getMode() QueueMode {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.mode
}

// activeRun is the runtime record of one in-flight prompt/continuation (pi:
// ActiveRun). pi's promise+resolve pair becomes a closed channel; the
// AbortController becomes ctx+cancel.
type activeRun struct {
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
}

// Agent is the stateful wrapper around the low-level agent loop.
type Agent struct {
	mu            sync.Mutex
	state         AgentState
	listeners     []func(ctx context.Context, event AgentEvent)
	steeringQueue *pendingMessageQueue
	followUpQueue *pendingMessageQueue

	// Public, user-settable fields (pi: public fields on Agent).
	ConvertToLlm               func(messages []AgentMessage) []types.Message
	TransformContext           func(ctx context.Context, messages []AgentMessage) []AgentMessage
	StreamFunction             StreamFn
	GetApiKey                  func(provider types.ProviderID) string
	OnPayload                  func(payload any, model *types.Model) any
	OnResponse                 func(response types.ProviderResponse, model *types.Model)
	BeforeToolCall             func(ctx context.Context, context BeforeToolCallContext) (*BeforeToolCallResult, error)
	AfterToolCall              func(ctx context.Context, context AfterToolCallContext) (*AfterToolCallResult, error)
	ShouldStopAfterTurn        func(ctx context.Context, context ShouldStopAfterTurnContext) bool
	PrepareNextTurn            func(ctx context.Context) *AgentLoopTurnUpdate
	PrepareNextTurnWithContext func(ctx context.Context, context PrepareNextTurnContext) *AgentLoopTurnUpdate
	// SessionID is forwarded to providers for cache-aware backends.
	SessionID string
	// ThinkingBudgets is forwarded to the stream function.
	ThinkingBudgets *types.ThinkingBudgets
	// Transport is forwarded to the stream function.
	Transport types.Transport
	// MaxRetryDelayMs caps provider-requested retry delays.
	MaxRetryDelayMs *int64
	// ToolExecution is the tool execution strategy for assistant messages
	// that contain multiple tool calls.
	ToolExecution ToolExecutionMode

	activeRun *activeRun
}

// NewAgent constructs an Agent. pi throws for a missing stream function with
// no default configured; NewAgent returns the error instead.
func NewAgent(options AgentOptions) (*Agent, error) {
	agent := &Agent{}

	agent.state = AgentState{
		SystemPrompt:     "",
		Model:            defaultModel,
		ThinkingLevel:    types.ModelThinkingOff,
		Tools:            []AgentTool{},
		Messages:         []AgentMessage{},
		PendingToolCalls: map[string]bool{},
	}
	if options.InitialState != nil {
		initial := options.InitialState
		agent.state.SystemPrompt = initial.SystemPrompt
		if initial.Model != nil {
			agent.state.Model = initial.Model
		}
		if initial.ThinkingLevel != "" {
			agent.state.ThinkingLevel = initial.ThinkingLevel
		}
		agent.state.Tools = append([]AgentTool(nil), initial.Tools...)
		agent.state.Messages = append([]AgentMessage(nil), initial.Messages...)
	}

	agent.ConvertToLlm = options.ConvertToLlm
	if agent.ConvertToLlm == nil {
		agent.ConvertToLlm = defaultConvertToLlm
	}
	agent.TransformContext = options.TransformContext
	agent.StreamFunction = options.StreamFn
	if agent.StreamFunction == nil {
		resolved, err := GetDefaultStreamFn()
		if err != nil {
			return nil, err
		}
		agent.StreamFunction = resolved
	}
	agent.GetApiKey = options.GetApiKey
	agent.OnPayload = options.OnPayload
	agent.OnResponse = options.OnResponse
	agent.BeforeToolCall = options.BeforeToolCall
	agent.AfterToolCall = options.AfterToolCall
	agent.ShouldStopAfterTurn = options.ShouldStopAfterTurn
	agent.PrepareNextTurn = options.PrepareNextTurn
	agent.PrepareNextTurnWithContext = options.PrepareNextTurnWithContext
	agent.steeringQueue = newPendingMessageQueue(options.SteeringMode)
	if agent.steeringQueue.mode == "" {
		agent.steeringQueue.mode = QueueModeOneAtATime
	}
	agent.followUpQueue = newPendingMessageQueue(options.FollowUpMode)
	if agent.followUpQueue.mode == "" {
		agent.followUpQueue.mode = QueueModeOneAtATime
	}
	agent.SessionID = options.SessionID
	agent.ThinkingBudgets = options.ThinkingBudgets
	agent.Transport = options.Transport
	if agent.Transport == "" {
		agent.Transport = types.TransportAuto
	}
	agent.MaxRetryDelayMs = options.MaxRetryDelayMs
	agent.ToolExecution = options.ToolExecution
	if agent.ToolExecution == "" {
		agent.ToolExecution = ToolExecutionParallel
	}

	return agent, nil
}

// Subscribe registers a listener for agent lifecycle events and returns an
// unsubscribe function.
//
// Listener promises are awaited in subscription order and are included in the
// current run's settlement (in pig: listeners run synchronously in the emit
// goroutine, so Prompt does not return until they finish). Listeners also
// receive the active run context for the current run.
//
// `agent_end` is the final emitted event for a run, but the agent does not
// become idle until all awaited listeners for that event have settled.
func (a *Agent) Subscribe(listener func(ctx context.Context, event AgentEvent)) (unsubscribe func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.listeners = append(a.listeners, listener)
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		for index, registered := range a.listeners {
			// pi: Set.delete on the listener function; Go funcs aren't
			// comparable, so identity goes through utils.SameFuncValue.
			if utils.SameFuncValue(registered, listener) {
				a.listeners = append(a.listeners[:index], a.listeners[index+1:]...)
				break
			}
		}
	}
}

// State returns the agent state for direct access.
//
// Assigning SystemPrompt/Model/ThinkingLevel works as plain fields. For
// Tools/Messages use SetTools/SetMessages to get pi's copy-on-assign
// behavior. Reads from goroutines outside the run while a run is active
// should use Snapshot instead.
func (a *Agent) State() *AgentState {
	return &a.state
}

// Snapshot returns a race-safe copy of the agent state (pig addition: pi
// relies on the single-threaded event loop; pig callers outside the run
// goroutine need synchronization).
func (a *Agent) Snapshot() AgentState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshotLocked()
}

func (a *Agent) snapshotLocked() AgentState {
	snapshot := a.state
	snapshot.Tools = append([]AgentTool(nil), a.state.Tools...)
	snapshot.Messages = append([]AgentMessage(nil), a.state.Messages...)
	pending := make(map[string]bool, len(a.state.PendingToolCalls))
	for id := range a.state.PendingToolCalls {
		pending[id] = true
	}
	snapshot.PendingToolCalls = pending
	return snapshot
}

// SetTools replaces the tools, copying the top-level slice (pi: state.tools
// setter).
func (a *Agent) SetTools(tools []AgentTool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Tools = append([]AgentTool(nil), tools...)
}

// SetMessages replaces the transcript, copying the top-level slice (pi:
// state.messages setter).
func (a *Agent) SetMessages(messages []AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = append([]AgentMessage(nil), messages...)
}

// SetSteeringMode controls how queued steering messages are drained (pi:
// steeringMode accessor).
func (a *Agent) SetSteeringMode(mode QueueMode) { a.steeringQueue.setMode(mode) }

// SteeringMode reports how queued steering messages are drained.
func (a *Agent) SteeringMode() QueueMode { return a.steeringQueue.getMode() }

// SetFollowUpMode controls how queued follow-up messages are drained (pi:
// followUpMode accessor).
func (a *Agent) SetFollowUpMode(mode QueueMode) { a.followUpQueue.setMode(mode) }

// FollowUpMode reports how queued follow-up messages are drained.
func (a *Agent) FollowUpMode() QueueMode { return a.followUpQueue.getMode() }

// Steer queues a message to be injected after the current assistant turn
// finishes.
func (a *Agent) Steer(message AgentMessage) { a.steeringQueue.enqueue(message) }

// FollowUp queues a message to run only after the agent would otherwise
// stop.
func (a *Agent) FollowUp(message AgentMessage) { a.followUpQueue.enqueue(message) }

// ClearSteeringQueue removes all queued steering messages.
func (a *Agent) ClearSteeringQueue() { a.steeringQueue.clear() }

// ClearFollowUpQueue removes all queued follow-up messages.
func (a *Agent) ClearFollowUpQueue() { a.followUpQueue.clear() }

// ClearAllQueues removes all queued steering and follow-up messages.
func (a *Agent) ClearAllQueues() {
	a.ClearSteeringQueue()
	a.ClearFollowUpQueue()
}

// HasQueuedMessages returns true when either queue still contains pending
// messages.
func (a *Agent) HasQueuedMessages() bool {
	return a.steeringQueue.hasItems() || a.followUpQueue.hasItems()
}

// Signal returns the active run context, or nil when idle (pi: get signal).
func (a *Agent) Signal() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeRun == nil {
		return nil
	}
	return a.activeRun.ctx
}

// Abort aborts the current run, if one is active. Safe to call when idle.
func (a *Agent) Abort() {
	a.mu.Lock()
	run := a.activeRun
	a.mu.Unlock()
	if run != nil {
		run.cancel()
	}
}

// WaitForIdle resolves when the current run and all awaited event listeners
// have finished. This returns after `agent_end` listeners settle.
func (a *Agent) WaitForIdle() {
	a.mu.Lock()
	run := a.activeRun
	a.mu.Unlock()
	if run != nil {
		<-run.done
	}
}

// Reset clears transcript state, runtime state, and queued messages. pi
// throws when a run is active; pig returns an error.
func (a *Agent) Reset() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeRun != nil {
		return errors.New("Agent is already processing. Wait for completion before resetting.")
	}

	a.state.Messages = []AgentMessage{}
	a.state.IsStreaming = false
	a.state.StreamingMessage = nil
	a.state.PendingToolCalls = map[string]bool{}
	a.state.ErrorMessage = ""
	a.followUpQueue.clear()
	a.steeringQueue.clear()
	return nil
}

// Prompt starts a new prompt from one message or a batch of messages (pi:
// prompt(message: AgentMessage | AgentMessage[])). pi throws while a run is
// active; pig returns an error.
func (a *Agent) Prompt(ctx context.Context, messages ...AgentMessage) error {
	a.mu.Lock()
	if a.activeRun != nil {
		a.mu.Unlock()
		return errors.New("Agent is already processing a prompt. Use steer() or followUp() to queue messages, or wait for completion.")
	}
	a.mu.Unlock()
	return a.runPromptMessages(ctx, messages, false)
}

// PromptText starts a new prompt from text and optional images (pi:
// prompt(input: string, images?: ImageContent[])).
func (a *Agent) PromptText(ctx context.Context, input string, images ...types.ImageContent) error {
	a.mu.Lock()
	if a.activeRun != nil {
		a.mu.Unlock()
		return errors.New("Agent is already processing a prompt. Use steer() or followUp() to queue messages, or wait for completion.")
	}
	a.mu.Unlock()
	messages, err := a.normalizePromptInput(input, images)
	if err != nil {
		return err
	}
	return a.runPromptMessages(ctx, messages, false)
}

// Continue continues from the current transcript. The last message must be a
// user or tool-result message. pi throws on the invalid cases; pig returns
// errors.
func (a *Agent) Continue(ctx context.Context) error {
	a.mu.Lock()
	if a.activeRun != nil {
		a.mu.Unlock()
		return errors.New("Agent is already processing. Wait for completion before continuing.")
	}
	a.mu.Unlock()
	lastMessage := a.lastMessage()

	if lastMessage == nil {
		return errors.New("No messages to continue from")
	}

	if lastMessage.MessageRole() == types.RoleAssistant {
		queuedSteering := a.steeringQueue.drain()
		if len(queuedSteering) > 0 {
			return a.runPromptMessages(ctx, queuedSteering, true)
		}

		queuedFollowUps := a.followUpQueue.drain()
		if len(queuedFollowUps) > 0 {
			return a.runPromptMessages(ctx, queuedFollowUps, false)
		}

		return errors.New("Cannot continue from message role: assistant")
	}

	return a.runContinuation(ctx)
}

// lastMessage returns the newest transcript message, or nil when empty (pi:
// this._state.messages[this._state.messages.length - 1]).
func (a *Agent) lastMessage() AgentMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.state.Messages) == 0 {
		return nil
	}
	return a.state.Messages[len(a.state.Messages)-1]
}

func (a *Agent) normalizePromptInput(input string, images []types.ImageContent) ([]AgentMessage, error) {
	content := []types.Content{types.NewTextContent(input)}
	if len(images) > 0 {
		for _, image := range images {
			content = append(content, image)
		}
	}
	return []AgentMessage{types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: content},
		Timestamp: time.Now().UnixMilli(),
	}}, nil
}

func (a *Agent) runPromptMessages(ctx context.Context, messages []AgentMessage, skipInitialSteeringPoll bool) error {
	return a.runWithLifecycle(ctx, func(runCtx context.Context) error {
		RunAgentLoop(
			runCtx,
			messages,
			a.createContextSnapshot(),
			a.createLoopConfig(skipInitialSteeringPoll),
			func(event AgentEvent) { a.processEvents(event) },
			a.StreamFunction,
		)
		return nil
	})
}

func (a *Agent) runContinuation(ctx context.Context) error {
	return a.runWithLifecycle(ctx, func(runCtx context.Context) error {
		_, err := RunAgentLoopContinue(
			runCtx,
			a.createContextSnapshot(),
			a.createLoopConfig(false),
			func(event AgentEvent) { a.processEvents(event) },
			a.StreamFunction,
		)
		return err
	})
}

func (a *Agent) createContextSnapshot() AgentContext {
	a.mu.Lock()
	defer a.mu.Unlock()
	tools := make([]*AgentTool, len(a.state.Tools))
	for index := range a.state.Tools {
		tools[index] = &a.state.Tools[index]
	}
	return AgentContext{
		SystemPrompt: a.state.SystemPrompt,
		Messages:     append([]AgentMessage(nil), a.state.Messages...),
		Tools:        tools,
	}
}

func (a *Agent) createLoopConfig(skipInitialSteeringPoll bool) AgentLoopConfig {
	a.mu.Lock()
	model := a.state.Model
	thinkingLevel := a.state.ThinkingLevel
	a.mu.Unlock()
	return AgentLoopConfig{
		SimpleStreamOptions: types.SimpleStreamOptions{
			StreamOptions: types.StreamOptions{
				ProviderRequestOptions: types.ProviderRequestOptions{
					OnPayload:       a.OnPayload,
					OnResponse:      a.OnResponse,
					MaxRetryDelayMs: a.MaxRetryDelayMs,
				},
				Transport: a.Transport,
				SessionID: a.SessionID,
			},
			// pi: thinkingLevel "off" → undefined reasoning; pig uses the
			// empty ThinkingLevel.
			Reasoning:       reasoningFromThinkingLevel(thinkingLevel),
			ThinkingBudgets: a.ThinkingBudgets,
		},
		Model:          model,
		ToolExecution:  a.ToolExecution,
		BeforeToolCall: a.BeforeToolCall,
		AfterToolCall:  a.AfterToolCall,
		// pi wraps the hooks to inject this.signal; in pig the loop ctx IS
		// the run ctx, but the AgentOptions signatures carry ctx explicitly,
		// so close over the run context for them.
		ShouldStopAfterTurn: a.wrapShouldStopAfterTurn(),
		PrepareNextTurn:     a.wrapPrepareNextTurn(),
		ConvertToLlm:        a.ConvertToLlm,
		TransformContext:    a.TransformContext,
		GetApiKey:           a.GetApiKey,
		GetSteeringMessages: func() []AgentMessage {
			if skipInitialSteeringPoll {
				skipInitialSteeringPoll = false
				return nil
			}
			return a.steeringQueue.drain()
		},
		GetFollowUpMessages: func() []AgentMessage {
			return a.followUpQueue.drain()
		},
	}
}

func reasoningFromThinkingLevel(thinkingLevel ThinkingLevel) types.ThinkingLevel {
	if thinkingLevel == types.ModelThinkingOff || thinkingLevel == "" {
		return ""
	}
	return types.ThinkingLevel(thinkingLevel)
}

func (a *Agent) wrapShouldStopAfterTurn() func(context ShouldStopAfterTurnContext) bool {
	if a.ShouldStopAfterTurn == nil {
		return nil
	}
	return func(context ShouldStopAfterTurnContext) bool {
		return a.ShouldStopAfterTurn(a.runContextOrPanic(), context)
	}
}

func (a *Agent) wrapPrepareNextTurn() func(context PrepareNextTurnContext) *AgentLoopTurnUpdate {
	if a.PrepareNextTurnWithContext == nil && a.PrepareNextTurn == nil {
		return nil
	}
	return func(context PrepareNextTurnContext) *AgentLoopTurnUpdate {
		runCtx := a.runContextOrPanic()
		if a.PrepareNextTurnWithContext != nil {
			return a.PrepareNextTurnWithContext(runCtx, context)
		}
		return a.PrepareNextTurn(runCtx)
	}
}

// runContextOrPanic returns the active run context (pi: this.signal inside
// the wrapped hooks). The wraps are only installed while a run is active, so
// a nil here is a programming error; pi's equivalent read yields undefined
// signal, pig panics to keep the invariant loud.
func (a *Agent) runContextOrPanic() context.Context {
	ctx := a.Signal()
	if ctx == nil {
		panic("Agent hook invoked outside active run")
	}
	return ctx
}

// runWithLifecycle owns the active-run bookkeeping around one executor call.
// pi's catch-all try/catch becomes recover(): ANY failure — executor error
// or panic, e.g. a throwing streamFn — is converted into the failure event
// sequence by handleRunFailure.
func (a *Agent) runWithLifecycle(ctx context.Context, executor func(runCtx context.Context) error) error {
	a.mu.Lock()
	if a.activeRun != nil {
		a.mu.Unlock()
		return errors.New("Agent is already processing.")
	}
	runCtx, cancel := context.WithCancel(ctx)
	run := &activeRun{done: make(chan struct{}), ctx: runCtx, cancel: cancel}
	a.activeRun = run

	a.state.IsStreaming = true
	a.state.StreamingMessage = nil
	a.state.ErrorMessage = ""
	a.mu.Unlock()

	var runErr error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				if err, ok := recovered.(error); ok {
					runErr = err
				} else {
					runErr = fmt.Errorf("%v", recovered)
				}
			}
		}()
		runErr = executor(runCtx)
	}()

	if runErr != nil {
		a.handleRunFailure(runErr, runCtx.Err() != nil)
	}
	a.finishRun()
	return nil
}

func (a *Agent) handleRunFailure(err error, aborted bool) {
	a.mu.Lock()
	model := a.state.Model
	a.mu.Unlock()
	failureMessage := types.AssistantMessage{
		Role:     types.RoleAssistant,
		Content:  []types.Content{types.NewTextContent("")},
		API:      model.API,
		Provider: model.Provider,
		Model:    model.ID,
		Usage:    emptyUsage,
		StopReason: func() types.StopReason {
			if aborted {
				return types.StopReasonAborted
			}
			return types.StopReasonError
		}(),
		ErrorMessage: err.Error(),
		Timestamp:    time.Now().UnixMilli(),
	}
	a.processEvents(AgentEvent{Type: AgentEventMessageStart, Message: failureMessage})
	a.processEvents(AgentEvent{Type: AgentEventMessageEnd, Message: failureMessage})
	a.processEvents(AgentEvent{Type: AgentEventTurnEnd, Message: failureMessage, ToolResults: []types.ToolResultMessage{}})
	a.processEvents(AgentEvent{Type: AgentEventAgentEnd, Messages: []AgentMessage{failureMessage}})
}

func (a *Agent) finishRun() {
	a.mu.Lock()
	run := a.activeRun
	a.state.IsStreaming = false
	a.state.StreamingMessage = nil
	a.state.PendingToolCalls = map[string]bool{}
	a.activeRun = nil
	a.mu.Unlock()
	if run != nil {
		close(run.done)
		// pi has no counterpart: the run's AbortController goes out of scope
		// and is garbage-collected after the run, while a Go cancelCtx stays
		// registered in its parent's children map until canceled — one leaked
		// registration per turn, pinning the whole run-context subtree. The
		// run is over and nobody observes this context anymore (Signal()
		// returns nil once activeRun is cleared), so canceling is safe and
		// releases the subtree. Canceling after close(done): a waiter woken
		// by done can only start a NEW run, whose context is a sibling.
		run.cancel()
	}
}

// processEvents reduces internal state for a loop event, then invokes
// listeners in subscription order.
//
// `agent_end` only means no further loop events will be emitted. The run is
// considered idle later, after all awaited listeners for `agent_end` finish
// and finishRun() clears runtime-owned state.
func (a *Agent) processEvents(event AgentEvent) {
	a.mu.Lock()
	switch event.Type {
	case AgentEventMessageStart:
		a.state.StreamingMessage = event.Message

	case AgentEventMessageUpdate:
		a.state.StreamingMessage = event.Message

	case AgentEventMessageEnd:
		a.state.StreamingMessage = nil
		a.state.Messages = append(a.state.Messages, event.Message)

	case AgentEventToolExecutionStart:
		pendingToolCalls := make(map[string]bool, len(a.state.PendingToolCalls)+1)
		for id := range a.state.PendingToolCalls {
			pendingToolCalls[id] = true
		}
		pendingToolCalls[event.ToolCallID] = true
		a.state.PendingToolCalls = pendingToolCalls

	case AgentEventToolExecutionEnd:
		pendingToolCalls := make(map[string]bool, len(a.state.PendingToolCalls))
		for id := range a.state.PendingToolCalls {
			pendingToolCalls[id] = true
		}
		delete(pendingToolCalls, event.ToolCallID)
		a.state.PendingToolCalls = pendingToolCalls

	case AgentEventTurnEnd:
		if message, ok := event.Message.(types.AssistantMessage); ok && message.ErrorMessage != "" {
			a.state.ErrorMessage = message.ErrorMessage
		}

	case AgentEventAgentEnd:
		a.state.StreamingMessage = nil
	}
	run := a.activeRun
	a.mu.Unlock()

	if run == nil {
		// pi throws "Agent listener invoked outside active run"; see the
		// package comment for the panic mapping.
		panic("Agent listener invoked outside active run")
	}

	a.mu.Lock()
	listeners := make([]func(ctx context.Context, event AgentEvent), len(a.listeners))
	copy(listeners, a.listeners)
	a.mu.Unlock()
	for _, listener := range listeners {
		listener(run.ctx, event)
	}
}
