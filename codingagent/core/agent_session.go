// pi: src/core/agent-session.ts — AgentSession, the core abstraction for
// agent lifecycle and session management shared between all run modes
// (interactive, print, rpc). It encapsulates agent state access, event
// subscription with automatic session persistence, model and thinking level
// management, compaction (manual and auto), bash execution, and session
// switching/branching.
//
// Segment deviations (declared):
//   - AgentSessionEvent is a discriminated union in pi; pig follows the
//     agent.AgentEvent precedent (single struct, fields named by Type are
//     meaningful). The compaction-end result field is named CompactionResult
//     to avoid colliding with the embedded AgentEvent.Result (pi reuses
//     `result` across union members).
//   - pi mutates the message object in place on extension message_end
//     replacement (object identity shared with agent state). pig's agent
//     messages are values, so the replacement is located in agent state by
//     role+timestamp and rewritten there; the local event copy is swapped
//     too. Only reachable with extension handlers (Phase 7).
//   - Private-method spies used by pi tests (_runAutoCompaction) map to the
//     runAutoCompactionFn field (same-package tests overwrite it); see the
//     test files.
//   - exportToHtml (export-html + theme, Phase 8) is deferred.
//   - pi's fire-and-forget `void runner.emit(...)` calls run synchronously on
//     a background context (no goroutine: pi's promise is unobserved).
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/utils"
	pigutils "github.com/ZoroSpace/pig/utils"
)

// ============================================================================
// Skill Block Parsing
// ============================================================================

// ParsedSkillBlock is a parsed skill block from a user message (pi:
// ParsedSkillBlock).
type ParsedSkillBlock struct {
	Name     string
	Location string
	Content  string
	// UserMessage is the optional trailing user text; nil = absent (pi:
	// userMessage: string | undefined).
	UserMessage *string
}

// skillBlockRe parses a skill block from message text (pi: the parseSkillBlock
// regex; [\s\S] → (?s) dotall).
var skillBlockRe = regexp.MustCompile(`(?s)^<skill name="([^"]+)" location="([^"]+)">\n(.*?)\n</skill>(?:\n\n(.+))?$`)

// ParseSkillBlock parses a skill block from message text. Returns nil if the
// text doesn't contain a skill block.
func ParseSkillBlock(text string) *ParsedSkillBlock {
	match := skillBlockRe.FindStringSubmatch(text)
	if match == nil {
		return nil
	}
	result := &ParsedSkillBlock{
		Name:     match[1],
		Location: match[2],
		Content:  match[3],
	}
	if trimmed := strings.TrimSpace(match[4]); trimmed != "" {
		result.UserMessage = &trimmed
	}
	return result
}

// ============================================================================
// Events
// ============================================================================

// AgentSessionEvent is a session-specific event that extends the core
// AgentEvent (pi: AgentSessionEvent discriminated union; pig follows the
// agent.AgentEvent single-struct precedent — exactly the fields named by Type
// are meaningful). Agent event payloads (message_start/message_end/turn_*/
// tool_execution_*/agent_start/agent_end) ride the embedded AgentEvent.
type AgentSessionEvent struct {
	agent.AgentEvent

	// WillRetry (agent_end) reports whether auto-retry will continue the run.
	WillRetry bool

	// Steering/FollowUp (queue_update) are the pending queue snapshots.
	Steering []string
	FollowUp []string

	// Reason (compaction_start/compaction_end): "manual" | "threshold" |
	// "overflow".
	Reason string
	// CompactionResult (compaction_end) is the compaction result; nil =
	// undefined (pi: result; renamed from Result to avoid the embedded
	// AgentEvent.Result collision, see header).
	CompactionResult *CompactionResult
	// Aborted (compaction_end) reports cancellation.
	Aborted bool
	// ErrorMessage (compaction_end) is the failure message; "" = undefined.
	ErrorMessage string

	// Entry (entry_appended) is the appended custom entry.
	Entry SessionEntry

	// Name (session_info_changed) is the session display name; nil =
	// cleared (pi: name: string | undefined).
	Name *string

	// ThinkingLevel (thinking_level_changed) is the new level.
	ThinkingLevel agent.ThinkingLevel

	// Attempt/MaxAttempts/DelayMs (auto_retry_start,
	// summarization_retry_scheduled) describe the retry schedule.
	Attempt     int
	MaxAttempts int
	DelayMs     int64
	// FinalError (auto_retry_end) is the terminal error; "" = undefined.
	FinalError string
	// Success (auto_retry_end) reports retry success.
	Success bool

	// SummaryRetrySource (summarization_retry_attempt_start):
	// "branchSummary" | "compaction".
	SummaryRetrySource string

	// ID (bash_execution_update) is the optional execution id; nil =
	// absent (pi: id?).
	ID *string
	// Delta (bash_execution_update) is the streamed output chunk.
	Delta string
}

// MarshalJSON renders the event as pi's AgentSessionEvent union wire shape
// (pi serializes the union object with JSON.stringify: only the members of
// the active variant, camelCase keys). The Go struct carries every variant's
// members; this encoder picks the ones the event type owns, keeping --mode
// json / RPC output wire-compatible with pi.
func (e AgentSessionEvent) MarshalJSON() ([]byte, error) {
	obj := map[string]any{"type": string(e.Type)}
	switch e.Type {
	case agent.AgentEventAgentEnd:
		obj["messages"] = e.Messages
		obj["willRetry"] = e.WillRetry
	case agent.AgentEventTurnEnd:
		obj["message"] = e.Message
		// pi's turn_end always carries toolResults (empty array when there
		// are no tool calls); nil slice renders as [] not null.
		if e.ToolResults == nil {
			obj["toolResults"] = []types.ToolResultMessage{}
		} else {
			obj["toolResults"] = e.ToolResults
		}
	case agent.AgentEventMessageStart, agent.AgentEventMessageEnd:
		obj["message"] = e.Message
	case agent.AgentEventMessageUpdate:
		obj["message"] = e.Message
		if e.AssistantMessageEvent != nil {
			obj["assistantMessageEvent"] = e.AssistantMessageEvent
		}
	case agent.AgentEventToolExecutionStart:
		obj["toolCallId"] = e.ToolCallID
		obj["toolName"] = e.ToolName
		obj["args"] = e.Args
	case agent.AgentEventToolExecutionUpdate:
		obj["toolCallId"] = e.ToolCallID
		obj["toolName"] = e.ToolName
		obj["args"] = e.Args
		obj["partialResult"] = e.PartialResult
	case agent.AgentEventToolExecutionEnd:
		obj["toolCallId"] = e.ToolCallID
		obj["toolName"] = e.ToolName
		obj["result"] = e.Result
		obj["isError"] = e.IsError
	case AgentSessionEventQueueUpdate:
		// pi always carries both arrays; nil slice renders as [] not null.
		if e.Steering == nil {
			obj["steering"] = []string{}
		} else {
			obj["steering"] = e.Steering
		}
		if e.FollowUp == nil {
			obj["followUp"] = []string{}
		} else {
			obj["followUp"] = e.FollowUp
		}
	case AgentSessionEventCompactionStart:
		obj["reason"] = e.Reason
	case AgentSessionEventEntryAppended:
		obj["entry"] = e.Entry
	case AgentSessionEventSessionInfoChanged:
		// pi: name: string | undefined — undefined is omitted by
		// JSON.stringify, so nil Name drops the key.
		if e.Name != nil {
			obj["name"] = *e.Name
		}
	case AgentSessionEventThinkingLevelChanged:
		obj["level"] = string(e.ThinkingLevel)
	case AgentSessionEventCompactionEnd:
		obj["reason"] = e.Reason
		if e.CompactionResult != nil {
			obj["result"] = e.CompactionResult
		}
		obj["aborted"] = e.Aborted
		obj["willRetry"] = e.WillRetry
		if e.ErrorMessage != "" {
			obj["errorMessage"] = e.ErrorMessage
		}
	case AgentSessionEventAutoRetryStart, AgentSessionEventSummarizationRetryScheduled:
		obj["attempt"] = e.Attempt
		obj["maxAttempts"] = e.MaxAttempts
		obj["delayMs"] = e.DelayMs
		obj["errorMessage"] = e.ErrorMessage
	case AgentSessionEventAutoRetryEnd:
		obj["success"] = e.Success
		obj["attempt"] = e.Attempt
		if e.FinalError != "" {
			obj["finalError"] = e.FinalError
		}
	case AgentSessionEventSummarizationRetryAttemptStart:
		obj["source"] = e.SummaryRetrySource
		if e.SummaryRetrySource == "compaction" {
			obj["reason"] = e.Reason
		}
	case AgentSessionEventBashExecutionUpdate:
		if e.ID != nil {
			obj["id"] = *e.ID
		}
		obj["delta"] = e.Delta
	case AgentSessionEventAgentSettled, AgentSessionEventSummarizationRetryFinished:
		// type-only members.
	}
	return json.Marshal(obj)
}

// AgentSessionEventType extends the agent event vocabulary with the
// session-specific event types (pi: the AgentSessionEvent union members).
const (
	AgentSessionEventAgentSettled                   agent.AgentEventType = "agent_settled"
	AgentSessionEventQueueUpdate                    agent.AgentEventType = "queue_update"
	AgentSessionEventCompactionStart                agent.AgentEventType = "compaction_start"
	AgentSessionEventEntryAppended                  agent.AgentEventType = "entry_appended"
	AgentSessionEventSessionInfoChanged             agent.AgentEventType = "session_info_changed"
	AgentSessionEventThinkingLevelChanged           agent.AgentEventType = "thinking_level_changed"
	AgentSessionEventCompactionEnd                  agent.AgentEventType = "compaction_end"
	AgentSessionEventAutoRetryStart                 agent.AgentEventType = "auto_retry_start"
	AgentSessionEventAutoRetryEnd                   agent.AgentEventType = "auto_retry_end"
	AgentSessionEventSummarizationRetryScheduled    agent.AgentEventType = "summarization_retry_scheduled"
	AgentSessionEventSummarizationRetryAttemptStart agent.AgentEventType = "summarization_retry_attempt_start"
	AgentSessionEventSummarizationRetryFinished     agent.AgentEventType = "summarization_retry_finished"
	AgentSessionEventBashExecutionUpdate            agent.AgentEventType = "bash_execution_update"
)

// AgentSessionEventListener listens to agent session events (pi:
// AgentSessionEventListener). pig wraps listeners in listenerEntry so
// unsubscribe can identify the exact registration (Go funcs are not
// comparable; pi uses Set identity).
type AgentSessionEventListener func(event *AgentSessionEvent)

type listenerEntry struct {
	fn AgentSessionEventListener
}

// ============================================================================
// Types
// ============================================================================

// withoutDeletedHeaders drops the null-valued entries (pi:
// withoutDeletedHeaders).
func withoutDeletedHeaders(headers types.ProviderHeaders) map[string]string {
	if headers == nil {
		return nil
	}
	result := map[string]string{}
	for key, value := range headers {
		if value != nil {
			result[key] = *value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// AgentSessionConfig configures a new AgentSession (pi: AgentSessionConfig).
type AgentSessionConfig struct {
	Agent           *agent.Agent
	SessionManager  *SessionManager
	SettingsManager *SettingsManager
	Cwd             string
	// ScopedModels are the models to cycle through with Ctrl+P (from
	// --models flag).
	ScopedModels []ScopedModel
	// ResourceLoader supplies extensions, skills, prompts, themes, context
	// files, and the system prompt.
	ResourceLoader ResourceLoader
	// CustomTools are SDK custom tools registered outside extensions.
	CustomTools []*tools.ToolDefinition
	// ModelRuntime is the canonical model/auth runtime used by
	// coding-agent internals.
	ModelRuntime *ModelRuntime
	// InitialActiveToolNames are the initial active built-in tool names.
	// Default: [read, bash, edit, write].
	InitialActiveToolNames []string
	// AllowedToolNames is an optional allowlist of tool names. When
	// provided, only these tool names are exposed.
	AllowedToolNames []string
	// ExcludedToolNames is an optional denylist of tool names. When
	// provided, these tool names are not exposed.
	ExcludedToolNames []string
	// BaseToolsOverride overrides base tools (useful for custom runtimes).
	// These are synthesized into minimal ToolDefinitions internally so
	// AgentSession can keep a definition-first registry even when callers
	// provide plain AgentTool instances.
	BaseToolsOverride map[string]agent.AgentTool
	// ExtensionRunnerRef is the mutable ref used by Agent to access the
	// current ExtensionRunner.
	ExtensionRunnerRef *ExtensionRunnerRef
	// SessionStartEvent is the session start event metadata emitted when
	// extensions bind to this runtime.
	SessionStartEvent *SessionStartEvent
}

// ExtensionRunnerRef is pi's `{ current?: ExtensionRunner }` mutable ref.
type ExtensionRunnerRef struct {
	// Current is the active runner; nil = none (pi: current?).
	Current *ExtensionRunner
}

// ExtensionBindings are the extension host bindings (pi: ExtensionBindings).
type ExtensionBindings struct {
	UIContext             ExtensionUIContext
	Mode                  ExtensionMode
	CommandContextActions *ExtensionCommandContextActions
	AbortHandler          func()
	ShutdownHandler       ShutdownHandler
	OnError               ExtensionErrorListener
}

// PromptOptions are the options for AgentSession.Prompt (pi: PromptOptions).
type PromptOptions struct {
	// ExpandPromptTemplates dispatches extension commands and expands skill
	// commands and prompt templates (default: true).
	ExpandPromptTemplates *bool
	// Images are the image attachments.
	Images []types.ImageContent
	// StreamingBehavior queues the message while streaming: "steer"
	// (interrupt) or "followUp" (wait). Required if streaming.
	StreamingBehavior string
	// Source is the source of input for extension input event handlers.
	// Defaults to "interactive".
	Source InputSource
	// PreflightResult is an internal hook used by RPC mode to observe
	// prompt preflight acceptance or rejection.
	PreflightResult func(success bool)
}

// ModelMutationOptions are the options for model/thinking mutations (pi:
// ModelMutationOptions).
type ModelMutationOptions struct {
	// Persist writes the new value to global defaults. Defaults to
	// session-only.
	Persist bool
}

// ModelCycleResult is the result from CycleModel (pi: ModelCycleResult).
type ModelCycleResult struct {
	Model         *types.Model
	ThinkingLevel agent.ThinkingLevel
	// IsScoped reports cycling through scoped models (--models flag) vs
	// all available.
	IsScoped bool
}

// SessionStats are the session statistics for /session (pi: SessionStats).
type SessionStats struct {
	SessionFile       string // "" = sessions disabled (pi: undefined)
	SessionID         string
	UserMessages      int
	AssistantMessages int
	ToolCalls         int
	ToolResults       int
	TotalMessages     int
	Tokens            struct {
		Input      int64
		Output     int64
		CacheRead  int64
		CacheWrite int64
		Total      int64
	}
	Cost         float64
	ContextUsage *ContextUsage
}

// toolDefinitionEntry is one registry entry (pi: ToolDefinitionEntry).
type toolDefinitionEntry struct {
	definition *tools.ToolDefinition
	sourceInfo SourceInfo
}

func estimateMessagesTokens(messages []agent.AgentMessage) int64 {
	var tokens int64
	for i := range messages {
		tokens += EstimateTokens(messages[i])
	}
	return tokens
}

// ============================================================================
// AgentSession Class
// ============================================================================

// AgentSession is the core abstraction for agent lifecycle and session
// management (pi: AgentSession).
type AgentSession struct {
	Agent           *agent.Agent
	SessionManager  *SessionManager
	SettingsManager *SettingsManager

	scopedModels []ScopedModel

	// mu serializes the session-owned state that pi leaves to the event
	// loop (steering/follow-up queues, listener list, idle tracking,
	// pending bash/next-turn messages). Run-flow state mutations happen on
	// the prompt caller's goroutine (pig's agent runs synchronously).
	mu sync.Mutex

	unsubscribeAgent func()
	eventListeners   []*listenerEntry
	// listenerPanicObserver is a pig panic-containment addition (pi: a
	// listener throw crashes the process): invoked — itself guarded — when
	// a listener panics during emit(), after the panic has been logged, so
	// the host UI can surface it. See docs/feat/PANIC-RESILIENCE.md.
	listenerPanicObserver func(event *AgentSessionEvent, logPath string)
	isAgentRunActive      bool
	// idleWait is pi's _idleWaitPromise/_resolveIdleWait pair: closed when
	// the session settles idle.
	idleWait chan struct{}

	// steeringMessages tracks pending steering messages for UI display.
	// Removed when delivered.
	steeringMessages []string
	// followUpMessages tracks pending follow-up messages for UI display.
	// Removed when delivered.
	followUpMessages []string
	// pendingNextTurnMessages are queued to be included with the next user
	// prompt as context ("asides").
	pendingNextTurnMessages []agent.AgentMessage
	// pendingCustomMessages are context-only custom messages queued during a
	// run, flushed once the current turn's tool results are in (pi:
	// _pendingCustomMessages).
	pendingCustomMessages []agent.AgentMessage

	// compactionAbort context/cancel pairs (pi: AbortController fields).
	compactionCancel          context.CancelFunc
	autoCompactionCancel      context.CancelFunc
	overflowRecoveryAttempted bool

	// branchSummaryCancel cancels branch summarization.
	branchSummaryCancel context.CancelFunc

	// retryCancel cancels the retry backoff sleep.
	retryCancel  context.CancelFunc
	retryAttempt int

	// bashCancels tracks running bash executions (pi: _bashAbortControllers
	// Set<AbortController>). CancelFunc is not comparable, so the set is
	// keyed by monotonically increasing tokens mapped to the cancel funcs.
	bashCancels         map[int64]context.CancelFunc
	bashCancelToken     int64
	pendingBashMessages []*harness.BashExecutionMessage

	// Extension system
	extensionRunner *ExtensionRunner
	turnIndex       int

	resourceLoader                 ResourceLoader
	customTools                    []*tools.ToolDefinition
	baseToolDefinitions            map[string]*tools.ToolDefinition
	cwd                            string
	extensionRunnerRef             *ExtensionRunnerRef
	initialActiveToolNames         []string
	allowedToolNames               map[string]struct{}
	excludedToolNames              map[string]struct{}
	baseToolsOverride              map[string]agent.AgentTool
	sessionStartEvent              *SessionStartEvent
	extensionUIContext             ExtensionUIContext
	extensionMode                  ExtensionMode
	extensionCommandContextActions *ExtensionCommandContextActions
	extensionAbortHandler          func()
	extensionShutdownHandler       ShutdownHandler
	extensionErrorListener         ExtensionErrorListener
	extensionErrorUnsubscriber     func()

	modelRuntime *ModelRuntime

	// Tool registry for extension getTools/setTools
	toolRegistry    map[string]agent.AgentTool
	toolDefinitions map[string]toolDefinitionEntry
	// toolDefinitionOrder keeps pi's Map insertion order for getAllTools.
	toolDefinitionOrder     []string
	baseToolDefinitionOrder []string
	toolPromptSnippets      map[string]string
	toolPromptGuidelines    map[string][]string

	// baseSystemPrompt (without extension appends) is used to apply fresh
	// appends each turn
	baseSystemPrompt        string
	baseSystemPromptOptions BuildSystemPromptOptions
	systemPromptOverride    string

	// lastAssistantMessage tracks the last assistant message for
	// auto-compaction check.
	lastAssistantMessage *types.AssistantMessage

	// runAutoCompactionFn is the seam for pi tests' _runAutoCompaction spies
	// (see header); it always points at (*AgentSession).runAutoCompaction in
	// production.
	runAutoCompactionFn func(ctx context.Context, reason string, willRetry bool) (bool, error)

	// emitExtensionEventFn is the seam for pi tests that override
	// _emitExtensionEvent (a property assignment on the session); it always
	// points at (*AgentSession).emitExtensionEvent in production.
	emitExtensionEventFn func(ctx context.Context, event agent.AgentEvent)
}

// NewAgentSession creates the session (pi: constructor).
func NewAgentSession(config AgentSessionConfig) *AgentSession {
	s := &AgentSession{
		Agent:           config.Agent,
		SessionManager:  config.SessionManager,
		SettingsManager: config.SettingsManager,
		scopedModels:    config.ScopedModels,
		resourceLoader:  config.ResourceLoader,
		customTools:     config.CustomTools,
		cwd:             config.Cwd,
		modelRuntime:    config.ModelRuntime,
		bashCancels:     map[int64]context.CancelFunc{},
	}
	s.extensionRunnerRef = config.ExtensionRunnerRef
	s.initialActiveToolNames = config.InitialActiveToolNames
	if config.AllowedToolNames != nil {
		s.allowedToolNames = map[string]struct{}{}
		for _, name := range config.AllowedToolNames {
			s.allowedToolNames[name] = struct{}{}
		}
	}
	if config.ExcludedToolNames != nil {
		s.excludedToolNames = map[string]struct{}{}
		for _, name := range config.ExcludedToolNames {
			s.excludedToolNames[name] = struct{}{}
		}
	}
	s.baseToolsOverride = config.BaseToolsOverride
	if config.SessionStartEvent != nil {
		s.sessionStartEvent = config.SessionStartEvent
	} else {
		s.sessionStartEvent = &SessionStartEvent{Type: "session_start", Reason: "startup"}
	}
	s.extensionMode = ExtensionModePrint
	s.toolRegistry = map[string]agent.AgentTool{}
	s.toolDefinitions = map[string]toolDefinitionEntry{}
	s.toolPromptSnippets = map[string]string{}
	s.toolPromptGuidelines = map[string][]string{}

	// Always subscribe to agent events for internal handling
	// (session persistence, extensions, auto-compaction, retry logic)
	s.unsubscribeAgent = s.Agent.Subscribe(s.handleAgentEvent)

	s.installAgentToolHooks()
	s.installAgentNextTurnRefresh()

	s.buildRuntime(buildRuntimeOptions{
		activeToolNames:          s.initialActiveToolNames,
		includeAllExtensionTools: true,
	})

	s.runAutoCompactionFn = s.runAutoCompaction
	s.emitExtensionEventFn = s.emitExtensionEvent
	return s
}

// GetModelRuntime returns the canonical model/auth runtime (pi:
// get modelRuntime).
func (s *AgentSession) GetModelRuntime() *ModelRuntime {
	return s.modelRuntime
}

// requestAuth is the resolved request credential bundle (pi: the
// { model, apiKey?, headers?, env? } return shape of
// _getRequiredRequestAuth/_getSummarizationRequestAuth).
type requestAuth struct {
	model    *types.Model
	apiKey   string
	headers  map[string]string
	env      types.ProviderEnv
	fallback bool // _getSummarizationRequestAuth's `{ model }` shape
}

func (s *AgentSession) getRequiredRequestAuth(ctx context.Context, model *types.Model) (*requestAuth, error) {
	result, err := s.modelRuntime.GetAuthForModel(ctx, model, nil)
	if err != nil {
		// pi checks error.cause.message; pig walks the wrapped chain.
		if errorChainContains(err, "authHeader requires a resolved API key") {
			return nil, fmt.Errorf("%s", FormatNoApiKeyFoundMessage(string(model.Provider)))
		}
		return nil, err
	}
	if result != nil && (result.Auth.APIKey != "" || result.Auth.Headers != nil) {
		requestModel := model
		if result.Auth.BaseURL != "" {
			copied := *model
			copied.BaseURL = result.Auth.BaseURL
			requestModel = &copied
		}
		return &requestAuth{
			model:   requestModel,
			apiKey:  result.Auth.APIKey,
			headers: withoutDeletedHeaders(result.Auth.Headers),
			env:     result.Env,
		}, nil
	}

	isOAuth := s.modelRuntime.IsUsingOAuth(string(model.Provider))
	if isOAuth {
		return nil, fmt.Errorf(
			"Authentication failed for %q. Credentials may have expired or network is unavailable. Run '/login %s' to re-authenticate.",
			model.Provider, model.Provider,
		)
	}
	return nil, fmt.Errorf("%s", FormatNoApiKeyFoundMessage(string(model.Provider)))
}

func (s *AgentSession) getSummarizationRequestAuth(ctx context.Context, model *types.Model) (*requestAuth, error) {
	// pi: `this.agent.streamFunction === streamSimple`; Go funcs aren't
	// comparable, so identity goes through utils.SameFuncValue. The SDK path
	// always wraps streamSimple, so this is false there (matches pi).
	if pigutils.SameFuncValue(s.Agent.StreamFunction, defaultStreamFn) {
		return s.getRequiredRequestAuth(ctx, model)
	}

	result, err := s.modelRuntime.GetAuthForModel(ctx, model, nil)
	if err != nil || result == nil {
		return &requestAuth{model: model, fallback: true}, nil
	}
	requestModel := model
	if result.Auth.BaseURL != "" {
		copied := *model
		copied.BaseURL = result.Auth.BaseURL
		requestModel = &copied
	}
	return &requestAuth{
		model:   requestModel,
		apiKey:  result.Auth.APIKey,
		headers: withoutDeletedHeaders(result.Auth.Headers),
		env:     result.Env,
	}, nil
}

// errorChainContains reports whether err or any wrapped error has exactly the
// given message (pi: error.cause message check).
func errorChainContains(err error, message string) bool {
	for err != nil {
		if err.Error() == message {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}

// installAgentToolHooks installs the tool hooks once on the Agent instance
// (pi: _installAgentToolHooks). The callbacks read s.extensionRunner at
// execution time, so extension reload swaps in the new runner without
// reinstalling hooks.
func (s *AgentSession) installAgentToolHooks() {
	s.Agent.BeforeToolCall = func(ctx context.Context, callContext agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
		runner := s.extensionRunner
		if !runner.HasHandlers("tool_call") {
			return nil, nil
		}

		result, err := runner.EmitToolCall(ctx, &ToolCallEvent{
			Type:       "tool_call",
			ToolName:   callContext.ToolCall.Name,
			ToolCallID: callContext.ToolCall.ID,
			Input:      callContext.Args,
		})
		if err != nil {
			// pi rethrows Errors verbatim and wraps non-Error throws; pig
			// errors are the only failure channel, propagate as-is.
			return nil, err
		}
		if result != nil && result.Block {
			return &agent.BeforeToolCallResult{Block: true, Reason: result.Reason, Terminate: result.Terminate}, nil
		}
		return nil, nil
	}

	s.Agent.AfterToolCall = func(ctx context.Context, callContext agent.AfterToolCallContext) (*agent.AfterToolCallResult, error) {
		runner := s.extensionRunner
		var hookResult *ToolResultEventResult
		if runner.HasHandlers("tool_result") {
			result, err := runner.EmitToolResult(ctx, &ToolResultEvent{
				Type:       "tool_result",
				ToolName:   callContext.ToolCall.Name,
				ToolCallID: callContext.ToolCall.ID,
				Input:      callContext.Args,
				Content:    callContext.Result.Content,
				Details:    callContext.Result.Details,
				IsError:    callContext.IsError,
				Usage:      callContext.Result.Usage,
			})
			if err != nil {
				return nil, err
			}
			hookResult = result
		}

		content := callContext.Result.Content
		if hookResult != nil && hookResult.Content != nil {
			content = hookResult.Content
		} else if content == nil {
			content = []types.Content{}
		}
		// Runs after the extension hook so images injected or replaced by
		// extensions are normalized too.
		normalizedContent, err := utils.NormalizeToolResultImages(content, &utils.NormalizeToolResultImagesOptions{
			AutoResizeImages: lo.ToPtr(s.SettingsManager.GetImageAutoResize()),
		})
		if err != nil {
			return nil, err
		}

		if hookResult == nil && slices.EqualFunc(normalizedContent, callContext.Result.Content, contentEqual) {
			return nil, nil
		}

		result := &agent.AfterToolCallResult{
			Content: normalizedContent,
		}
		if hookResult != nil {
			result.Details = hookResult.Details
			if hookResult.IsError != nil {
				result.IsError = hookResult.IsError
			} else {
				result.IsError = lo.ToPtr(callContext.IsError)
			}
			result.Usage = hookResult.Usage
		} else {
			result.IsError = lo.ToPtr(callContext.IsError)
		}
		return result, nil
	}
}

// contentEqual compares two content values structurally (pi: `===` on the
// content array reference to detect "nothing changed"; pig compares values
// since content is a slice and NormalizeToolResultImages may rebuild it).
func contentEqual(a, b types.Content) bool {
	return reflect.DeepEqual(a, b)
}

// compactBeforeNextAssistantResponse runs threshold compaction before the
// next assistant response when the estimated context exceeds the limit (pi:
// _compactBeforeNextAssistantResponse, v0.85.1).
func (s *AgentSession) compactBeforeNextAssistantResponse(ctx context.Context, llmContext *agent.AgentContext) *agent.AgentContext {
	if llmContext == nil {
		return llmContext
	}
	model := s.Model()
	settings := s.SettingsManager.GetCompactionSettings()
	if model == nil || model.ContextWindow <= 0 {
		return llmContext
	}
	if !ShouldCompact(EstimateContextTokens(llmContext.Messages).Tokens, model.ContextWindow, settings) {
		return llmContext
	}
	_, _ = s.runAutoCompactionChecked(ctx, "threshold", false)
	nextContext := &agent.AgentContext{}
	*nextContext = *llmContext
	nextContext.Messages = slices.Clone(s.Agent.State().Messages)
	return nextContext
}

// installAgentNextTurnRefresh installs the prepare-next-turn refresh (pi:
// _installAgentNextTurnRefresh). Overrides the agent's prepareNextTurn with
// one that refreshes systemPrompt/tools/model/thinkingLevel from agent state.
func (s *AgentSession) installAgentNextTurnRefresh() {
	previous := s.Agent.PrepareNextTurnWithContext
	if previous == nil && s.Agent.PrepareNextTurn != nil {
		prepareNextTurn := s.Agent.PrepareNextTurn
		previous = func(ctx context.Context, _ agent.PrepareNextTurnContext) *agent.AgentLoopTurnUpdate {
			return prepareNextTurn(ctx)
		}
	}
	s.Agent.PrepareNextTurnWithContext = func(ctx context.Context, turn agent.PrepareNextTurnContext) *agent.AgentLoopTurnUpdate {
		// pi (v0.85.1): compact before the next assistant response, before the
		// previous prepareNextTurnWithContext runs.
		turnContext := s.compactBeforeNextAssistantResponse(ctx, turn.Context)
		if turn.Context != nil {
			turn.Context = turnContext
		}
		var previousSnapshot *agent.AgentLoopTurnUpdate
		if previous != nil {
			previousSnapshot = previous(ctx, turn)
		}
		previousContext := &agent.AgentContext{}
		if turn.Context != nil {
			copied := *turn.Context
			previousContext = &copied
		}
		if previousSnapshot != nil && previousSnapshot.Context != nil {
			copied := *previousSnapshot.Context
			previousContext = &copied
		}

		// pi v0.86.0: the loop context no longer carries the system prompt;
		// prompt changes flow through transcript system messages (declareTool
		// Changes inserts them). systemPromptOverride is wired to that path in
		// the coding-agent sync batch.
		stateTools := s.Agent.State().Tools
		contextTools := make([]*agent.AgentTool, len(stateTools))
		for i := range stateTools {
			toolCopy := stateTools[i]
			contextTools[i] = &toolCopy
		}
		previousContext.Tools = contextTools

		update := &agent.AgentLoopTurnUpdate{Context: previousContext}
		if previousSnapshot != nil {
			*update = *previousSnapshot
			update.Context = previousContext
		}
		state := s.Agent.State()
		update.Model = state.Model
		update.ThinkingLevel = state.ThinkingLevel
		return update
	}
}

// ============================================================================
// Event Subscription
// ============================================================================

// emit sends an event to all listeners (pi: _emit).
// pig panic containment: a panic in one listener is logged and contained so
// the event pump and the remaining listeners survive (pi's single event loop
// would crash the whole process — the observed latex-render panic class).
// See docs/feat/PANIC-RESILIENCE.md.
func (s *AgentSession) emit(event *AgentSessionEvent) {
	s.mu.Lock()
	listeners := slices.Clone(s.eventListeners)
	observer := s.listenerPanicObserver
	s.mu.Unlock()
	for _, listener := range listeners {
		logPath, panicked := pigutils.Guard("core.agentSession.listener", func() { listener.fn(event) })
		if panicked && observer != nil {
			pigutils.Guard("core.agentSession.listenerPanicObserver", func() { observer(event, logPath) })
		}
	}
}

func (s *AgentSession) emitQueueUpdate() {
	s.mu.Lock()
	steering := slices.Clone(s.steeringMessages)
	followUp := slices.Clone(s.followUpMessages)
	s.mu.Unlock()
	if steering == nil {
		steering = []string{}
	}
	if followUp == nil {
		followUp = []string{}
	}
	s.emit(&AgentSessionEvent{AgentEvent: agent.AgentEvent{Type: AgentSessionEventQueueUpdate}, Steering: steering, FollowUp: followUp})
}

func (s *AgentSession) emitSessionCompactFailed(ctx context.Context, event *SessionCompactFailedEvent) error {
	if !s.extensionRunner.HasHandlers("session_compact_failed") {
		return nil
	}
	_, err := s.extensionRunner.Emit(ctx, event)
	return err
}

// getIdleWait returns the channel closed when the session settles idle (pi:
// _getIdleWaitPromise).
func (s *AgentSession) getIdleWait() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idleWait == nil {
		s.idleWait = make(chan struct{})
	}
	return s.idleWait
}

func (s *AgentSession) resolveIdleWaitIfIdle() {
	s.mu.Lock()
	// pi (v0.85.1): gate on the full isIdle (run active + compaction), not
	// just the agent run flag.
	if s.isAgentRunActive || (s.autoCompactionCancel != nil || s.compactionCancel != nil || s.branchSummaryCancel != nil) || s.idleWait == nil {
		s.mu.Unlock()
		return
	}
	ch := s.idleWait
	s.idleWait = nil
	s.mu.Unlock()
	close(ch)
}

func (s *AgentSession) emitAgentSettled(ctx context.Context) {
	s.mu.Lock()
	s.isAgentRunActive = false
	s.mu.Unlock()
	defer s.resolveIdleWaitIfIdle()
	_, _ = s.extensionRunner.Emit(ctx, &AgentSettledEvent{Type: "agent_settled"})
	s.emit(&AgentSessionEvent{AgentEvent: agent.AgentEvent{Type: AgentSessionEventAgentSettled}})
}

// handleAgentEvent is the internal handler for agent events, shared by
// subscribe and reconnect (pi: _handleAgentEvent).
func (s *AgentSession) handleAgentEvent(ctx context.Context, event agent.AgentEvent) {
	// When a user message starts, check if it's from either queue and remove
	// it BEFORE emitting. This ensures the UI sees the updated queue state.
	if event.Type == agent.AgentEventMessageStart && event.Message.MessageRole() == types.RoleUser {
		s.mu.Lock()
		s.overflowRecoveryAttempted = false
		s.mu.Unlock()
		messageText := agentMessageText(event.Message)
		if messageText != "" {
			// Check steering queue first, then the follow-up queue.
			s.mu.Lock()
			if index := slices.Index(s.steeringMessages, messageText); index != -1 {
				s.steeringMessages = slices.Delete(s.steeringMessages, index, index+1)
				s.mu.Unlock()
				s.emitQueueUpdate()
			} else if index := slices.Index(s.followUpMessages, messageText); index != -1 {
				s.followUpMessages = slices.Delete(s.followUpMessages, index, index+1)
				s.mu.Unlock()
				s.emitQueueUpdate()
			} else {
				s.mu.Unlock()
			}
		}
	}

	// Emit to extensions first
	s.emitExtensionEventFn(ctx, event)

	// Notify all listeners
	willRetry := false
	if event.Type == agent.AgentEventAgentEnd {
		willRetry = s.willRetryAfterAgentEnd(event)
	}
	s.emit(&AgentSessionEvent{AgentEvent: event, WillRetry: willRetry})

	// Handle session persistence
	if event.Type == agent.AgentEventMessageEnd {
		// Check if this is a custom message from extensions
		if custom, ok := event.Message.(harness.CustomMessage); ok {
			// Persist as CustomMessageEntry
			_, _ = s.SessionManager.AppendCustomMessageEntry(custom.CustomType, custom.Content, custom.Display, custom.Details)
		} else if role := event.Message.MessageRole(); role == types.RoleUser || role == types.RoleAssistant || role == types.RoleToolResult {
			// Regular LLM message - persist as SessionMessageEntry
			_, _ = s.SessionManager.AppendMessage(event.Message)
		}
		// Other message types (bashExecution, compactionSummary, branchSummary) are persisted elsewhere

		// Track assistant message for auto-compaction (checked on agent_end)
		if assistantMsg, ok := event.Message.(types.AssistantMessage); ok {
			s.mu.Lock()
			s.lastAssistantMessage = &assistantMsg
			resetOverflow := assistantMsg.StopReason != types.StopReasonError && assistantMsg.StopReason != types.StopReasonLength
			resetRetry := assistantMsg.StopReason != types.StopReasonError && s.retryAttempt > 0
			completedAttempt := s.retryAttempt
			if resetRetry {
				s.retryAttempt = 0
			}
			s.mu.Unlock()
			if resetOverflow {
				// nothing to do beyond the flag above; kept for shape parity
			}
			// Reset retry counter immediately on successful assistant response
			// This prevents accumulation across multiple LLM calls within a turn
			if resetRetry {
				s.emit(&AgentSessionEvent{
					AgentEvent: agent.AgentEvent{Type: AgentSessionEventAutoRetryEnd},
					Success:    true,
					Attempt:    completedAttempt,
				})
			}
		}
	}
}

func (s *AgentSession) willRetryAfterAgentEnd(event agent.AgentEvent) bool {
	settings := s.SettingsManager.GetRetrySettings()
	if !settings.Enabled {
		return false
	}
	s.mu.Lock()
	attempt := s.retryAttempt
	s.mu.Unlock()
	if int64(attempt) >= settings.MaxRetries {
		return false
	}

	for i := len(event.Messages) - 1; i >= 0; i-- {
		if message, ok := event.Messages[i].(types.AssistantMessage); ok {
			return s.isRetryableError(&message)
		}
	}
	return false
}

// findLastAssistantMessage finds the last assistant message in agent state
// (including aborted ones) (pi: _findLastAssistantMessage).
func (s *AgentSession) findLastAssistantMessage() *types.AssistantMessage {
	messages := s.Agent.State().Messages
	for i := len(messages) - 1; i >= 0; i-- {
		if msg, ok := messages[i].(types.AssistantMessage); ok {
			return &msg
		}
	}
	return nil
}

// replaceMessageInPlace swaps a message_end event's message for an extension
// replacement (pi: _replaceMessageInPlace; see header for the pig value-type
// adaptation: agent state is rewritten by role+timestamp match).
func (s *AgentSession) replaceMessageInPlace(event *agent.AgentEvent, replacement agent.AgentMessage) {
	// Swap the local event copy so later listeners and session persistence
	// see the normalized message.
	event.Message = replacement

	// Rewrite the copy stored in agent state (pi: in-place object mutation).
	state := s.Agent.State()
	for i := len(state.Messages) - 1; i >= 0; i-- {
		existing := state.Messages[i]
		if existing.MessageRole() == replacement.MessageRole() &&
			messageTimestamp(existing) == messageTimestamp(replacement) {
			state.Messages[i] = replacement
			return
		}
	}
}

// agentMessageText extracts the text content of a message (pi:
// contentText(event.message.content, "") over the message union).
func agentMessageText(message agent.AgentMessage) string {
	switch m := message.(type) {
	case types.UserMessage:
		if m.Content.Parts == nil {
			return m.Content.String
		}
		return ai.ContentText(m.Content.Parts, "")
	case types.AssistantMessage:
		return ai.ContentText(m.Content, "")
	case types.ToolResultMessage:
		return ai.ContentText(m.Content, "")
	case harness.CustomMessage:
		return customContentText(m.Content)
	}
	return ""
}

// messageContentNil reports whether the message content is missing/null (pi:
// `replacement.content == null`).
func messageContentNil(message agent.AgentMessage) bool {
	switch m := message.(type) {
	case types.UserMessage:
		return m.Content.Parts == nil && m.Content.String == ""
	case types.AssistantMessage:
		return m.Content == nil
	case types.ToolResultMessage:
		return m.Content == nil
	case harness.CustomMessage:
		return m.Content == nil
	}
	return false
}

// withEmptyContent replaces a nil/missing message content with an empty list
// (pi: `{ ...replacement, content: [] }`).
func withEmptyContent(message agent.AgentMessage) agent.AgentMessage {
	switch m := message.(type) {
	case types.UserMessage:
		m.Content = types.UserContent{Parts: []types.Content{}}
		return m
	case types.AssistantMessage:
		m.Content = []types.Content{}
		return m
	case types.ToolResultMessage:
		m.Content = []types.Content{}
		return m
	case harness.CustomMessage:
		m.Content = []types.Content{}
		return m
	}
	return message
}

func messageTimestamp(message agent.AgentMessage) int64 {
	switch m := message.(type) {
	case types.UserMessage:
		return m.Timestamp
	case types.AssistantMessage:
		return m.Timestamp
	case types.ToolResultMessage:
		return m.Timestamp
	case harness.CustomMessage:
		return m.Timestamp
	}
	return 0
}

// emitExtensionEvent emits extension events based on agent events (pi:
// _emitExtensionEvent).
func (s *AgentSession) emitExtensionEvent(ctx context.Context, event agent.AgentEvent) {
	switch event.Type {
	case agent.AgentEventAgentStart:
		s.mu.Lock()
		s.turnIndex = 0
		s.mu.Unlock()
		_, _ = s.extensionRunner.Emit(ctx, &AgentStartEvent{Type: "agent_start"})
	case agent.AgentEventAgentEnd:
		_, _ = s.extensionRunner.Emit(ctx, &AgentEndEvent{Type: "agent_end", Messages: event.Messages})
	case agent.AgentEventTurnStart:
		s.mu.Lock()
		turnIndex := s.turnIndex
		s.mu.Unlock()
		_, _ = s.extensionRunner.Emit(ctx, &TurnStartEvent{Type: "turn_start", TurnIndex: turnIndex, Timestamp: time.Now().UnixMilli()})
	case agent.AgentEventTurnEnd:
		s.mu.Lock()
		turnIndex := s.turnIndex
		s.turnIndex++
		s.mu.Unlock()
		_, _ = s.extensionRunner.Emit(ctx, &TurnEndEvent{Type: "turn_end", TurnIndex: turnIndex, Message: event.Message, ToolResults: event.ToolResults})
		// A turn ends after its assistant message and every tool result has
		// been appended, so this is the first point in the run where a
		// context-only custom message can be inserted without landing between
		// a tool call and its result. Flushing after the extension and
		// listener dispatch above also picks up messages that turn_end
		// handlers queued (pi, v0.85.1).
		s.flushPendingCustomMessages()
	case agent.AgentEventMessageStart:
		_, _ = s.extensionRunner.Emit(ctx, &MessageStartEvent{Type: "message_start", Message: event.Message})
	case agent.AgentEventMessageUpdate:
		var assistantMessageEvent types.AssistantMessageEvent
		if event.AssistantMessageEvent != nil {
			assistantMessageEvent = *event.AssistantMessageEvent
		}
		_, _ = s.extensionRunner.Emit(ctx, &MessageUpdateEvent{Type: "message_update", Message: event.Message, AssistantMessageEvent: assistantMessageEvent})
	case agent.AgentEventMessageEnd:
		replacement, err := s.extensionRunner.EmitMessageEnd(ctx, &MessageEndEvent{Type: "message_end", Message: event.Message})
		if err != nil {
			// pi lets listener throws bubble through the async chain; the
			// agent loop's emit is fire-and-forget, so errors are reported
			// via the runner (declared adaptation).
			s.extensionRunner.EmitError(ExtensionError{ExtensionPath: "<runtime>", Event: "message_end", Error: err.Error()})
			return
		}
		if replacement != nil {
			// Untyped extension handlers can return messages with null/missing
			// content; normalize so it never enters agent state or session history.
			normalized := replacement
			if role := replacement.MessageRole(); role == types.RoleUser || role == types.RoleAssistant || role == types.RoleToolResult || role == "custom" {
				if messageContentNil(replacement) {
					normalized = withEmptyContent(replacement)
				}
			}
			s.replaceMessageInPlace(&event, normalized)
		}
	case agent.AgentEventToolExecutionStart:
		_, _ = s.extensionRunner.Emit(ctx, &ToolExecutionStartEvent{Type: "tool_execution_start", ToolCallID: event.ToolCallID, ToolName: event.ToolName, Args: event.Args})
	case agent.AgentEventToolExecutionUpdate:
		_, _ = s.extensionRunner.Emit(ctx, &ToolExecutionUpdateEvent{Type: "tool_execution_update", ToolCallID: event.ToolCallID, ToolName: event.ToolName, Args: event.Args, PartialResult: event.PartialResult})
	case agent.AgentEventToolExecutionEnd:
		var toolResult agent.AgentToolResult
		if result, ok := event.Result.(agent.AgentToolResult); ok {
			toolResult = result
		}
		_, _ = s.extensionRunner.Emit(ctx, &ToolExecutionEndEvent{Type: "tool_execution_end", ToolCallID: event.ToolCallID, ToolName: event.ToolName, Result: toolResult, IsError: event.IsError})
	}
}

// Subscribe adds a listener; session persistence is handled internally (saves
// messages on message_end). Returns the unsubscribe function for this
// listener (pi: subscribe).
func (s *AgentSession) Subscribe(listener AgentSessionEventListener) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := &listenerEntry{fn: listener}
	s.eventListeners = append(s.eventListeners, entry)
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for index, registered := range s.eventListeners {
			if registered == entry {
				s.eventListeners = slices.Delete(s.eventListeners, index, index+1)
				break
			}
		}
	}
}

// SetListenerPanicObserver registers a callback invoked (guarded) when a
// session listener panics during emit() (pig addition; pi crashes instead).
// Interactive mode uses it to surface the recovered panic in the chat UI.
func (s *AgentSession) SetListenerPanicObserver(observer func(event *AgentSessionEvent, logPath string)) {
	s.mu.Lock()
	s.listenerPanicObserver = observer
	s.mu.Unlock()
}

// disconnectFromAgent disconnects from agent events during disposal (pi:
// _disconnectFromAgent).
func (s *AgentSession) disconnectFromAgent() {
	if s.unsubscribeAgent != nil {
		s.unsubscribeAgent()
		s.unsubscribeAgent = nil
	}
}

// Dispose removes all listeners and disconnects from the agent. Call this when
// completely done with the session (pi: dispose).
func (s *AgentSession) Dispose() {
	func() {
		defer func() { _ = recover() }() // Dispose must succeed even if an abort hook throws.
		s.AbortRetry()
		s.AbortCompaction()
		s.AbortBranchSummary()
		s.AbortBash()
		s.Agent.Abort()
	}()

	s.extensionRunner.Invalidate("This extension ctx is stale after session replacement or reload. Do not use a captured pig or command ctx after ctx.newSession(), ctx.fork(), ctx.switchSession(), or ctx.reload(). For newSession, fork, and switchSession, move post-replacement work into withSession and use the ctx passed to withSession. For reload, do not use the old ctx after await ctx.reload().")
	s.disconnectFromAgent()
	s.mu.Lock()
	s.eventListeners = nil
	s.mu.Unlock()
	// pi's dispose wraps this in try/catch already; errors are swallowed.
	_ = ai.CleanupSessionResources(s.SessionID())
}

// ============================================================================
// Read-only State Access
// ============================================================================

// State returns the full agent state (pi: get state).
func (s *AgentSession) State() *agent.AgentState {
	return s.Agent.State()
}

// Model returns the current model; nil = not yet selected (pi: get model).
func (s *AgentSession) Model() *types.Model {
	return s.Agent.State().Model
}

// ThinkingLevel returns the current thinking level (pi: get thinkingLevel).
func (s *AgentSession) ThinkingLevel() agent.ThinkingLevel {
	return s.Agent.State().ThinkingLevel
}

// IsStreaming reports whether the session is currently processing an agent run
// or post-run continuation (pi: get isStreaming).
func (s *AgentSession) IsStreaming() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isAgentRunActive
}

// IsIdle reports whether the session has no active agent run, compaction,
// branch summary, retry, or queued continuation (pi: get isIdle; v0.85.1
// adds the compaction gate).
func (s *AgentSession) IsIdle() bool {
	return !s.IsStreaming() && !s.IsCompacting()
}

// SystemPrompt returns the current effective system prompt (includes any
// per-turn extension modifications) (pi: get systemPrompt).
func (s *AgentSession) SystemPrompt() string {
	return s.Agent.State().SystemPrompt
}

// RetryAttempt returns the current retry attempt (0 if not retrying) (pi:
// get retryAttempt).
func (s *AgentSession) RetryAttempt() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retryAttempt
}

// GetActiveToolNames returns the names of currently active tools (pi:
// getActiveToolNames).
func (s *AgentSession) GetActiveToolNames() []string {
	toolsList := s.Agent.State().Tools
	names := make([]string, 0, len(toolsList))
	for _, t := range toolsList {
		names = append(names, t.Name)
	}
	return names
}

// GetAllTools returns all configured tools with name, description, parameter
// schema, prompt guidelines, and source metadata (pi: getAllTools).
func (s *AgentSession) GetAllTools() []ToolInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	infos := make([]ToolInfo, 0, len(s.toolDefinitions))
	// Map iteration order is unspecified; pi's Map preserves insertion order.
	// The registry is rebuilt by refreshToolRegistry in a deterministic order,
	// so collect via the ordered base names + custom tools sequence.
	for _, name := range s.toolDefinitionOrder {
		entry, ok := s.toolDefinitions[name]
		if !ok {
			continue
		}
		infos = append(infos, ToolInfo{
			Name:             entry.definition.Name,
			Description:      entry.definition.Description,
			Parameters:       entry.definition.Parameters,
			PromptGuidelines: entry.definition.PromptGuidelines,
			SourceInfo:       entry.sourceInfo,
		})
	}
	return infos
}

// GetToolDefinition returns the named tool definition, or nil (pi:
// getToolDefinition).
func (s *AgentSession) GetToolDefinition(name string) *tools.ToolDefinition {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.toolDefinitions[name]; ok {
		return entry.definition
	}
	return nil
}

// SetActiveToolsByName sets active tools by name. Only tools in the registry
// can be enabled. Unknown tool names are ignored. Also rebuilds the system
// prompt to reflect the new tool set. Changes take effect on the next agent
// turn (pi: setActiveToolsByName).
func (s *AgentSession) SetActiveToolsByName(toolNames []string) {
	var toolsList []agent.AgentTool
	var validToolNames []string
	s.mu.Lock()
	for _, name := range toolNames {
		if tool, ok := s.toolRegistry[name]; ok {
			toolsList = append(toolsList, tool)
			validToolNames = append(validToolNames, name)
		}
	}
	s.mu.Unlock()
	if toolsList == nil {
		toolsList = []agent.AgentTool{}
	}
	s.Agent.SetTools(toolsList)

	// Rebuild base system prompt with new tool set
	basePrompt := s.rebuildSystemPrompt(validToolNames)
	s.mu.Lock()
	s.baseSystemPrompt = basePrompt
	override := s.systemPromptOverride
	s.mu.Unlock()
	if override != "" {
		s.Agent.State().SystemPrompt = override
	} else {
		s.Agent.State().SystemPrompt = basePrompt
	}
}

// IsCompacting reports whether compaction or branch summarization is currently
// running (pi: get isCompacting).
func (s *AgentSession) IsCompacting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.autoCompactionCancel != nil || s.compactionCancel != nil || s.branchSummaryCancel != nil
}

// Messages returns all messages including custom types like
// BashExecutionMessage (pi: get messages).
func (s *AgentSession) Messages() []agent.AgentMessage {
	return s.Agent.State().Messages
}

// SteeringMode returns the current steering mode (pi: get steeringMode).
func (s *AgentSession) SteeringMode() agent.QueueMode {
	return s.Agent.SteeringMode()
}

// FollowUpMode returns the current follow-up mode (pi: get followUpMode).
func (s *AgentSession) FollowUpMode() agent.QueueMode {
	return s.Agent.FollowUpMode()
}

// SessionFile returns the current session file path, or "" if sessions are
// disabled (pi: get sessionFile).
func (s *AgentSession) SessionFile() string {
	return s.SessionManager.GetSessionFile()
}

// SessionID returns the current session ID (pi: get sessionId).
func (s *AgentSession) SessionID() string {
	return s.SessionManager.GetSessionID()
}

// SessionName returns the current session display name, if set (pi:
// get sessionName). The second return is false when unset.
func (s *AgentSession) SessionName() (string, bool) {
	return s.SessionManager.GetSessionName()
}

// ScopedModels returns the scoped models for cycling (from --models flag) (pi:
// get scopedModels).
func (s *AgentSession) ScopedModels() []ScopedModel {
	return s.scopedModels
}

// SetScopedModels updates the scoped models for cycling (pi:
// setScopedModels).
func (s *AgentSession) SetScopedModels(scopedModels []ScopedModel) {
	s.scopedModels = scopedModels
}

// addPersistedDefaultToNonEmptyScope adds a persisted default model to a
// non-empty scope and keeps the enabled-models patterns in sync (pi:
// _addPersistedDefaultToNonEmptyScope, v0.85.1).
func (s *AgentSession) addPersistedDefaultToNonEmptyScope(model *types.Model) {
	if len(s.scopedModels) == 0 {
		return
	}
	for _, scoped := range s.scopedModels {
		if ai.ModelsAreEqual(&scoped.Model, model) {
			return
		}
	}
	s.scopedModels = append(s.scopedModels, ScopedModel{Model: *model})

	enabledModels, ok := s.SettingsManager.GetEnabledModels()
	if !ok || len(enabledModels) == 0 {
		return
	}
	modelReference := string(model.Provider) + "/" + model.ID
	for _, pattern := range enabledModels {
		if strings.ToLower(pattern) == strings.ToLower(modelReference) {
			return
		}
	}
	s.SettingsManager.SetEnabledModels(append(enabledModels, modelReference))
}

// PromptTemplates returns the file-based prompt templates (pi:
// get promptTemplates).
func (s *AgentSession) PromptTemplates() []PromptTemplate {
	prompts, _ := s.resourceLoader.GetPrompts()
	return prompts
}

func normalizePromptSnippet(text string) string {
	if text == "" {
		return ""
	}
	oneLine := strings.Join(strings.Fields(strings.ReplaceAll(text, "\n", " ")), " ")
	// pi: replace [\r\n]+ with " ", collapse \s+, trim. strings.Fields-based
	// join is equivalent for non-ASCII whitespace except exotic separators
	// (declared, same convention as parseCommandArgs).
	return oneLine
}

func normalizePromptGuidelines(guidelines []string) []string {
	if len(guidelines) == 0 {
		return []string{}
	}
	seen := map[string]struct{}{}
	var unique []string
	for _, guideline := range guidelines {
		normalized := strings.TrimSpace(guideline)
		if normalized == "" {
			continue
		}
		if _, dup := seen[normalized]; dup {
			continue
		}
		seen[normalized] = struct{}{}
		unique = append(unique, normalized)
	}
	return unique
}

func (s *AgentSession) rebuildSystemPrompt(toolNames []string) string {
	validToolNames := make([]string, 0, len(toolNames))
	s.mu.Lock()
	for _, name := range toolNames {
		if _, ok := s.toolRegistry[name]; ok {
			validToolNames = append(validToolNames, name)
		}
	}
	toolSnippets := map[string]string{}
	var promptGuidelines []string
	for _, name := range validToolNames {
		if snippet, ok := s.toolPromptSnippets[name]; ok && snippet != "" {
			toolSnippets[name] = snippet
		}
		if toolGuidelines, ok := s.toolPromptGuidelines[name]; ok {
			promptGuidelines = append(promptGuidelines, toolGuidelines...)
		}
	}
	s.mu.Unlock()

	loaderSystemPrompt, _ := s.resourceLoader.GetSystemPrompt()
	loaderAppendSystemPrompt := s.resourceLoader.GetAppendSystemPrompt()
	appendSystemPrompt := strings.Join(loaderAppendSystemPrompt, "\n\n")
	loadedSkills, _ := s.resourceLoader.GetSkills()
	var loadedContextFiles []ContextFile
	for _, agentsFile := range s.resourceLoader.GetAgentsFiles() {
		loadedContextFiles = append(loadedContextFiles, ContextFile{Path: agentsFile.Path, Content: agentsFile.Content})
	}

	options := BuildSystemPromptOptions{
		Cwd:                s.cwd,
		Skills:             loadedSkills,
		ContextFiles:       loadedContextFiles,
		CustomPrompt:       loaderSystemPrompt,
		AppendSystemPrompt: appendSystemPrompt,
		SelectedTools:      validToolNames,
		ToolSnippets:       toolSnippets,
		PromptGuidelines:   promptGuidelines,
	}
	s.mu.Lock()
	s.baseSystemPromptOptions = options
	s.mu.Unlock()
	return BuildSystemPrompt(options)
}

// ============================================================================
// Prompting
// ============================================================================

// runAgentPrompt runs the prompt and drains post-run continuation (pi:
// _runAgentPrompt).
func (s *AgentSession) runAgentPrompt(ctx context.Context, messages []agent.AgentMessage) error {
	s.mu.Lock()
	s.isAgentRunActive = true
	s.mu.Unlock()
	settled := false
	defer func() {
		s.mu.Lock()
		s.systemPromptOverride = ""
		s.mu.Unlock()
		s.flushPendingBashMessages()
		s.flushPendingCustomMessages()
		if !settled {
			s.emitAgentSettled(context.Background())
		}
	}()

	if err := s.Agent.Prompt(ctx, messages...); err != nil {
		return err
	}
	for {
		shouldContinue, err := s.handlePostAgentRun(ctx)
		if err != nil {
			return err
		}
		if !shouldContinue {
			break
		}
		if err := s.Agent.Continue(ctx); err != nil {
			return err
		}
	}
	settled = true
	s.emitAgentSettled(ctx)
	return nil
}

// handlePostAgentRun decides whether the post-run loop should continue (pi:
// _handlePostAgentRun).
func (s *AgentSession) handlePostAgentRun(ctx context.Context) (bool, error) {
	s.mu.Lock()
	msg := s.lastAssistantMessage
	s.lastAssistantMessage = nil
	s.mu.Unlock()
	if msg == nil {
		return false, nil
	}

	if s.isRetryableError(msg) {
		shouldRetry, err := s.prepareRetry(ctx, msg)
		if err != nil {
			return false, err
		}
		if shouldRetry {
			return true, nil
		}
	}

	s.mu.Lock()
	retryAttempt := s.retryAttempt
	s.mu.Unlock()
	if msg.StopReason == types.StopReasonError && retryAttempt > 0 {
		s.emit(&AgentSessionEvent{
			AgentEvent: agent.AgentEvent{Type: AgentSessionEventAutoRetryEnd},
			Success:    false,
			Attempt:    retryAttempt,
			FinalError: msg.ErrorMessage,
		})
		s.mu.Lock()
		s.retryAttempt = 0
		s.mu.Unlock()
	}

	shouldCompact, err := s.checkCompaction(ctx, msg, true)
	if err != nil {
		return false, err
	}
	if shouldCompact {
		return true, nil
	}

	// The agent loop drains both queues before emitting agent_end. Any messages
	// here were queued by agent_end extension handlers and need a continuation.
	return s.Agent.HasQueuedMessages(), nil
}

// Prompt sends a prompt to the agent (pi: prompt). It handles extension
// commands (registered via pi.registerCommand) immediately, even during
// streaming, expands file-based prompt templates by default, queues via
// Steer/FollowUp during streaming based on the StreamingBehavior option, and
// validates model and API key before sending (when not streaming).
func (s *AgentSession) Prompt(ctx context.Context, text string, options *PromptOptions) error {
	expandPromptTemplates := true
	if options != nil && options.ExpandPromptTemplates != nil {
		expandPromptTemplates = *options.ExpandPromptTemplates
	}
	var preflightResult func(bool)
	if options != nil {
		preflightResult = options.PreflightResult
	}
	var messages []agent.AgentMessage

	accept := func() {
		if preflightResult != nil {
			preflightResult(true)
		}
	}
	reject := func() {
		if preflightResult != nil {
			preflightResult(false)
		}
	}

	runErr := func() error {
		// Handle extension commands first (execute immediately, even during
		// streaming). Extension commands manage their own LLM interaction via
		// pi.sendMessage().
		if expandPromptTemplates && strings.HasPrefix(text, "/") {
			handled := s.tryExecuteExtensionCommand(ctx, text)
			if handled {
				accept()
				return nil
			}
		}

		s.mu.Lock()
		compacting := s.compactionCancel != nil
		s.mu.Unlock()
		if compacting {
			return fmt.Errorf("Cannot submit a prompt while compaction is in progress. Wait for compaction to finish and retry.")
		}

		// Emit input event for extension interception (before skill/template expansion)
		currentText := text
		var currentImages []types.ImageContent
		if options != nil {
			currentImages = options.Images
		}
		if s.extensionRunner.HasHandlers("input") {
			source := InputSourceInteractive
			if options != nil && options.Source != "" {
				source = options.Source
			}
			var streamingBehavior string
			if s.IsStreaming() && options != nil {
				streamingBehavior = options.StreamingBehavior
			}
			inputResult, err := s.extensionRunner.EmitInput(ctx, currentText, currentImages, source, streamingBehavior)
			if err != nil {
				return err
			}
			if inputResult.Action == "handled" {
				accept()
				return nil
			}
			if inputResult.Action == "transform" {
				currentText = inputResult.Text
				if inputResult.Images != nil {
					currentImages = inputResult.Images
				}
			}
		}

		// Expand skill commands (/skill:name args) and prompt templates (/template args)
		expandedText := currentText
		if expandPromptTemplates {
			expandedText = s.expandSkillCommand(ctx, expandedText)
			expandedText = ExpandPromptTemplate(expandedText, s.PromptTemplates())
		}

		// If streaming, queue via steer() or followUp() based on option
		if s.IsStreaming() {
			if options == nil || options.StreamingBehavior == "" {
				return fmt.Errorf("Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message.")
			}
			if options.StreamingBehavior == "followUp" {
				s.queueFollowUp(expandedText, currentImages)
			} else {
				s.queueSteer(expandedText, currentImages)
			}
			accept()
			return nil
		}

		// Flush any pending bash and custom messages before the new prompt
		s.flushPendingBashMessages()
		s.flushPendingCustomMessages()

		// Validate model
		if s.Model() == nil {
			return fmt.Errorf("%s", FormatNoModelSelectedMessage())
		}
		model := s.Model()

		hasConfiguredAuth := s.modelRuntime.HasConfiguredAuth(string(model.Provider))
		if !hasConfiguredAuth {
			check, err := s.modelRuntime.CheckAuth(ctx, string(model.Provider))
			if err != nil {
				return err
			}
			hasConfiguredAuth = check != nil
		}
		if !hasConfiguredAuth {
			isOAuth := s.modelRuntime.IsUsingOAuth(string(model.Provider))
			if isOAuth {
				return fmt.Errorf(
					"Authentication failed for %q. Credentials may have expired or network is unavailable. Run '/login %s' to re-authenticate.",
					model.Provider, model.Provider,
				)
			}
			return fmt.Errorf("%s", FormatNoApiKeyFoundMessage(string(model.Provider)))
		}

		// Check if we need to compact before sending (catches aborted responses).
		// The user's new prompt is sent below, so do not call agent.continue() here.
		if lastAssistant := s.findLastAssistantMessage(); lastAssistant != nil {
			if _, err := s.checkCompaction(ctx, lastAssistant, false); err != nil {
				return err
			}
		}

		// Build messages array (custom message if any, then user message)
		messages = []agent.AgentMessage{}

		// Add user message
		userContent := types.UserContent{Parts: []types.Content{types.NewTextContent(expandedText)}}
		for _, image := range currentImages {
			userContent.Parts = append(userContent.Parts, image)
		}
		messages = append(messages, types.UserMessage{
			Role:      types.RoleUser,
			Content:   userContent,
			Timestamp: time.Now().UnixMilli(),
		})

		// Inject any pending "nextTurn" messages as context alongside the user message
		s.mu.Lock()
		for _, msg := range s.pendingNextTurnMessages {
			messages = append(messages, msg)
		}
		s.pendingNextTurnMessages = nil
		s.mu.Unlock()

		// Emit before_agent_start extension event
		result, err := s.extensionRunner.EmitBeforeAgentStart(ctx, expandedText, currentImages, s.baseSystemPrompt, s.baseSystemPromptOptionsSnapshot())
		if err != nil {
			return err
		}
		// Add all custom messages from extensions
		if result != nil {
			for _, msg := range result.Messages {
				messages = append(messages, harness.CustomMessage{
					Role:       "custom",
					CustomType: msg.CustomType,
					// Untyped extensions can pass null/missing content; normalize at ingestion.
					Content:   normalizePayloadContent(msg.Content),
					Display:   msg.Display,
					Details:   msg.Details,
					Timestamp: time.Now().UnixMilli(),
				})
			}
		}
		// Apply extension-modified system prompt, or reset to base
		if result != nil && result.SystemPrompt != nil {
			s.mu.Lock()
			s.systemPromptOverride = *result.SystemPrompt
			s.mu.Unlock()
			s.Agent.State().SystemPrompt = *result.SystemPrompt
		} else {
			// Ensure we're using the base prompt (in case previous turn had modifications)
			s.mu.Lock()
			s.systemPromptOverride = ""
			basePrompt := s.baseSystemPrompt
			s.mu.Unlock()
			s.Agent.State().SystemPrompt = basePrompt
		}
		return nil
	}()

	if runErr != nil {
		reject()
		return runErr
	}
	if messages == nil {
		// Extension command executed or input handled: no prompt to send.
		return nil
	}

	accept()
	return s.runAgentPrompt(ctx, messages)
}

// baseSystemPromptOptionsSnapshot copies the stored options (pi reads the
// field directly; pig copies to avoid callers mutating session state).
func (s *AgentSession) baseSystemPromptOptionsSnapshot() BuildSystemPromptOptions {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.baseSystemPromptOptions
}

// tryExecuteExtensionCommand tries to execute an extension command. Returns
// true if the command was found and executed (pi: _tryExecuteExtensionCommand).
func (s *AgentSession) tryExecuteExtensionCommand(ctx context.Context, text string) bool {
	// Parse command name and args
	commandName := strings.TrimPrefix(text, "/")
	args := ""
	if spaceIndex := strings.Index(text, " "); spaceIndex != -1 {
		commandName = text[1:spaceIndex]
		args = text[spaceIndex+1:]
	}

	command := s.extensionRunner.GetCommand(commandName)
	if command == nil {
		return false
	}

	// Get command context from extension runner (includes session control methods)
	commandCtx := s.extensionRunner.CreateCommandContext()

	if err := command.Handler(ctx, args, commandCtx); err != nil {
		// Emit error via extension runner
		s.extensionRunner.EmitError(ExtensionError{
			ExtensionPath: fmt.Sprintf("command:%s", commandName),
			Event:         "command",
			Error:         err.Error(),
		})
	}
	return true
}

// expandSkillCommand expands skill commands (/skill:name args) to their full
// content. Returns the expanded text, or the original text if not a skill
// command or skill not found. Emits errors via the extension runner if file
// read fails (pi: _expandSkillCommand).
func (s *AgentSession) expandSkillCommand(ctx context.Context, text string) string {
	if !strings.HasPrefix(text, "/skill:") {
		return text
	}

	skillName := strings.TrimPrefix(text, "/skill:")
	args := ""
	if spaceIndex := strings.Index(text, " "); spaceIndex != -1 {
		skillName = text[len("/skill:"):spaceIndex]
		args = strings.TrimSpace(text[spaceIndex+1:])
	}

	var skill *Skill
	skills, _ := s.resourceLoader.GetSkills()
	for i := range skills {
		if skills[i].Name == skillName {
			skill = &skills[i]
			break
		}
	}
	if skill == nil {
		return text // Unknown skill, pass through
	}

	content, err := os.ReadFile(skill.FilePath)
	if err != nil {
		// Emit error like extension commands do
		s.extensionRunner.EmitError(ExtensionError{
			ExtensionPath: skill.FilePath,
			Event:         "skill_expansion",
			Error:         err.Error(),
		})
		return text // Return original on error
	}
	body := strings.TrimSpace(mustStripFrontmatter(string(content)))
	skillBlock := fmt.Sprintf("<skill name=%q location=%q>\nReferences are relative to %s.\n\n%s\n</skill>", skill.Name, skill.FilePath, skill.BaseDir, body)
	if args != "" {
		return fmt.Sprintf("%s\n\n%s", skillBlock, args)
	}
	return skillBlock
}

// mustStripFrontmatter strips frontmatter, treating read failures as empty
// (pi: stripFrontmatter never throws; pig's parser returns an error only for
// malformed yaml, which pi would also fail on).
func mustStripFrontmatter(content string) string {
	body, err := utils.StripFrontmatter(content)
	if err != nil {
		return content
	}
	return body
}

// Steer queues a steering message while the agent is running. Delivered after
// the current assistant turn finishes executing its tool calls, before the
// next LLM call. Expands skill commands and prompt templates. Errors on
// extension commands (pi: steer).
func (s *AgentSession) Steer(ctx context.Context, text string, images []types.ImageContent) error {
	// Check for extension commands (cannot be queued)
	if strings.HasPrefix(text, "/") {
		if err := s.throwIfExtensionCommand(text); err != nil {
			return err
		}
	}

	// Expand skill commands and prompt templates
	expandedText := s.expandSkillCommand(ctx, text)
	expandedText = ExpandPromptTemplate(expandedText, s.PromptTemplates())

	s.queueSteer(expandedText, images)
	return nil
}

// FollowUp queues a follow-up message to be processed after the agent finishes.
// Delivered only when agent has no more tool calls or steering messages.
// Expands skill commands and prompt templates. Errors on extension commands
// (pi: followUp).
func (s *AgentSession) FollowUp(ctx context.Context, text string, images []types.ImageContent) error {
	// Check for extension commands (cannot be queued)
	if strings.HasPrefix(text, "/") {
		if err := s.throwIfExtensionCommand(text); err != nil {
			return err
		}
	}

	// Expand skill commands and prompt templates
	expandedText := s.expandSkillCommand(ctx, text)
	expandedText = ExpandPromptTemplate(expandedText, s.PromptTemplates())

	s.queueFollowUp(expandedText, images)
	return nil
}

// queueSteer queues a steering message (already expanded, no extension command
// check) (pi: _queueSteer).
func (s *AgentSession) queueSteer(text string, images []types.ImageContent) {
	s.mu.Lock()
	s.steeringMessages = append(s.steeringMessages, text)
	s.mu.Unlock()
	s.emitQueueUpdate()
	content := types.UserContent{Parts: []types.Content{types.NewTextContent(text)}}
	for _, image := range images {
		content.Parts = append(content.Parts, image)
	}
	s.Agent.Steer(types.UserMessage{
		Role:      types.RoleUser,
		Content:   content,
		Timestamp: time.Now().UnixMilli(),
	})
}

// queueFollowUp queues a follow-up message (already expanded, no extension
// command check) (pi: _queueFollowUp).
func (s *AgentSession) queueFollowUp(text string, images []types.ImageContent) {
	s.mu.Lock()
	s.followUpMessages = append(s.followUpMessages, text)
	s.mu.Unlock()
	s.emitQueueUpdate()
	content := types.UserContent{Parts: []types.Content{types.NewTextContent(text)}}
	for _, image := range images {
		content.Parts = append(content.Parts, image)
	}
	s.Agent.FollowUp(types.UserMessage{
		Role:      types.RoleUser,
		Content:   content,
		Timestamp: time.Now().UnixMilli(),
	})
}

// throwIfExtensionCommand errors when the text is an extension command (pi:
// _throwIfExtensionCommand).
func (s *AgentSession) throwIfExtensionCommand(text string) error {
	commandName := strings.TrimPrefix(text, "/")
	if spaceIndex := strings.Index(text, " "); spaceIndex != -1 {
		commandName = text[1:spaceIndex]
	}
	command := s.extensionRunner.GetCommand(commandName)
	if command != nil {
		return fmt.Errorf("Extension command \"/%s\" cannot be queued. Use prompt() or execute the command when not streaming.", commandName)
	}
	return nil
}

// SendCustomMessage sends a custom message to the session and creates a
// CustomMessageEntry (pi: sendCustomMessage).
//
// Handles three cases:
//   - Streaming: queues message, processed when loop pulls from queue
//   - Not streaming + triggerTurn: appends to state/session, starts new turn
//   - Not streaming + no trigger: appends to state/session, no turn
func (s *AgentSession) SendCustomMessage(ctx context.Context, message *CustomMessagePayload, options *SendMessageOptions) error {
	appMessage := harness.CustomMessage{
		Role:       "custom",
		CustomType: message.CustomType,
		// Untyped extensions can pass null/missing content; normalize at ingestion.
		Content:   normalizePayloadContent(message.Content),
		Display:   message.Display,
		Details:   message.Details,
		Timestamp: time.Now().UnixMilli(),
	}
	deliverAs := ""
	triggerTurn := false
	if options != nil {
		deliverAs = options.DeliverAs
		if options.TriggerTurn != nil {
			triggerTurn = *options.TriggerTurn
		}
	}
	if deliverAs == "nextTurn" {
		s.mu.Lock()
		s.pendingNextTurnMessages = append(s.pendingNextTurnMessages, appMessage)
		s.mu.Unlock()
		return nil
	}
	if s.IsStreaming() && (options == nil || options.TriggerTurn == nil || *options.TriggerTurn) {
		if deliverAs == "followUp" {
			s.Agent.FollowUp(appMessage)
		} else {
			s.Agent.Steer(appMessage)
		}
		return nil
	}
	if triggerTurn {
		return s.runAgentPrompt(ctx, []agent.AgentMessage{appMessage})
	}
	if s.IsStreaming() {
		// Appending now would put the message between an assistant tool call
		// and its result, which providers that validate message order reject
		// on replay. Defer to the end of the turn. Nothing is emitted yet:
		// message events must not describe messages the session tree does not
		// contain (pi, v0.85.1).
		s.mu.Lock()
		s.pendingCustomMessages = append(s.pendingCustomMessages, appMessage)
		s.mu.Unlock()
		return nil
	}
	s.appendCustomMessage(appMessage)
	return nil
}

// appendCustomMessage appends a custom message to agent state and the
// session tree, emitting message_start/message_end (pi: _appendCustomMessage).
func (s *AgentSession) appendCustomMessage(appMessage agent.AgentMessage) {
	state := s.Agent.State()
	state.Messages = append(state.Messages, appMessage)
	custom := appMessage.(harness.CustomMessage)
	_, _ = s.SessionManager.AppendCustomMessageEntry(custom.CustomType, custom.Content, custom.Display, custom.Details)
	s.emit(&AgentSessionEvent{AgentEvent: agent.AgentEvent{Type: agent.AgentEventMessageStart, Message: appMessage}})
	s.emit(&AgentSessionEvent{AgentEvent: agent.AgentEvent{Type: agent.AgentEventMessageEnd, Message: appMessage}})
}

// flushPendingCustomMessages appends custom messages queued while the agent
// was running; called once the current turn's tool results are in agent state
// and session history (pi: _flushPendingCustomMessages).
func (s *AgentSession) flushPendingCustomMessages() {
	s.mu.Lock()
	if len(s.pendingCustomMessages) == 0 {
		s.mu.Unlock()
		return
	}
	pending := s.pendingCustomMessages
	s.pendingCustomMessages = nil
	s.mu.Unlock()
	for _, appMessage := range pending {
		s.appendCustomMessage(appMessage)
	}
}

// normalizePayloadContent maps pi's `content ?? []` ingestion normalization
// for extension-provided custom message content.
func normalizePayloadContent(content any) any {
	if content == nil {
		return []types.Content{}
	}
	return content
}

// SendUserMessage sends a user message to the agent. Always triggers a turn.
// When the agent is streaming, use DeliverAs to specify how to queue the
// message (pi: sendUserMessage).
func (s *AgentSession) SendUserMessage(ctx context.Context, content any, options *SendUserMessageOptions) error {
	// Normalize content to text string + optional images
	var text string
	var images []types.ImageContent

	switch value := content.(type) {
	case string:
		text = value
	case []types.Content:
		var textParts []string
		for _, part := range value {
			if textPart, ok := part.(types.TextContent); ok {
				textParts = append(textParts, textPart.Text)
			} else if image, ok := part.(types.ImageContent); ok {
				images = append(images, image)
			}
		}
		text = strings.Join(textParts, "\n")
	default:
		return fmt.Errorf("sendUserMessage: content must be a string or a content array")
	}

	expand := false
	if options != nil && options.DeliverAs != "" {
		// pi: expandPromptTemplates defaults to false; deliverAs rides through.
	}
	_ = expand
	promptOptions := &PromptOptions{
		ExpandPromptTemplates: lo.ToPtr(false),
		StreamingBehavior:     "",
		Images:                images,
		Source:                InputSourceExtension,
	}
	if options != nil {
		promptOptions.StreamingBehavior = options.DeliverAs
	}
	return s.Prompt(ctx, text, promptOptions)
}

// ClearQueue clears all queued messages and returns them. Useful for restoring
// to editor when user aborts (pi: clearQueue).
func (s *AgentSession) ClearQueue() (steering []string, followUp []string) {
	s.mu.Lock()
	steering = slices.Clone(s.steeringMessages)
	followUp = slices.Clone(s.followUpMessages)
	s.steeringMessages = nil
	s.followUpMessages = nil
	s.mu.Unlock()
	s.Agent.ClearAllQueues()
	s.emitQueueUpdate()
	return steering, followUp
}

// PendingMessageCount returns the number of pending messages (includes both
// steering and follow-up) (pi: get pendingMessageCount).
func (s *AgentSession) PendingMessageCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.steeringMessages) + len(s.followUpMessages)
}

// GetSteeringMessages returns the pending steering messages (read-only) (pi:
// getSteeringMessages).
func (s *AgentSession) GetSteeringMessages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.steeringMessages))
	copy(out, s.steeringMessages)
	return out
}

// GetFollowUpMessages returns the pending follow-up messages (read-only) (pi:
// getFollowUpMessages).
func (s *AgentSession) GetFollowUpMessages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.followUpMessages))
	copy(out, s.followUpMessages)
	return out
}

// GetResourceLoader returns the resource loader (pi: get resourceLoader).
func (s *AgentSession) GetResourceLoader() ResourceLoader {
	return s.resourceLoader
}

// Abort aborts the current operation and waits for the agent to become idle
// (pi: abort).
func (s *AgentSession) Abort(ctx context.Context) error {
	s.AbortRetry()
	s.AbortCompaction()
	s.AbortBranchSummary()
	s.Agent.Abort()
	return s.WaitForIdle(ctx)
}

// WaitForIdle blocks until the session is idle (pi: waitForIdle). ctx
// cancellation aborts the wait (pi has no timeout).
func (s *AgentSession) WaitForIdle(ctx context.Context) error {
	s.mu.Lock()
	if !s.isAgentRunActive {
		s.mu.Unlock()
		return nil
	}
	ch := s.idleWait
	s.mu.Unlock()
	if ch == nil {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ============================================================================
// Model Management
// ============================================================================

func (s *AgentSession) emitModelSelect(ctx context.Context, nextModel *types.Model, previousModel *types.Model, source string) error {
	if ai.ModelsAreEqual(previousModel, nextModel) {
		return nil
	}
	_, err := s.extensionRunner.Emit(ctx, &ModelSelectEvent{Type: "model_select", Model: nextModel, PreviousModel: previousModel, Source: ModelSelectSource(source)})
	return err
}

// SetModel sets the model directly. Validates that auth is configured and
// saves to the session transcript. Persists to global defaults only when
// options.Persist is true (pi: setModel).
func (s *AgentSession) SetModel(ctx context.Context, model *types.Model, options *ModelMutationOptions) error {
	check, err := s.modelRuntime.CheckAuth(ctx, string(model.Provider))
	if err != nil {
		return err
	}
	if check == nil {
		return fmt.Errorf("No API key for %s/%s", model.Provider, model.ID)
	}

	previousModel := s.Model()
	thinkingLevel := s.getThinkingLevelForModelSwitch(model, "")
	s.Agent.State().Model = model
	_, _ = s.SessionManager.AppendModelChange(string(model.Provider), model.ID)
	if options != nil && options.Persist {
		s.SettingsManager.SetDefaultModelAndProvider(string(model.Provider), model.ID)
		s.addPersistedDefaultToNonEmptyScope(model)
	}

	// Apply thinking level for the new model.
	// Per-model thinking level overrides take priority over the global default.
	// Model persistence does not implicitly rewrite the global thinking default.
	s.SetThinkingLevel(thinkingLevel, options)

	return s.emitModelSelect(ctx, model, previousModel, "set")
}

// CycleModel cycles to the next/previous model. Uses scoped models (from
// --models flag) if available, otherwise all available models. Returns nil
// when only one model is available (pi: cycleModel).
func (s *AgentSession) CycleModel(ctx context.Context, direction string, options *ModelMutationOptions) (*ModelCycleResult, error) {
	if len(s.scopedModels) > 0 {
		return s.cycleScopedModel(ctx, direction, options)
	}
	return s.cycleAvailableModel(ctx, direction, options)
}

func (s *AgentSession) cycleScopedModel(ctx context.Context, direction string, options *ModelMutationOptions) (*ModelCycleResult, error) {
	available := map[string]struct{}{}
	for _, model := range s.modelRuntime.GetAvailableSnapshot() {
		available[string(model.Provider)+"\x00"+model.ID] = struct{}{}
	}
	scopedModels := make([]ScopedModel, 0, len(s.scopedModels))
	for _, scoped := range s.scopedModels {
		if _, ok := available[string(scoped.Model.Provider)+"\x00"+scoped.Model.ID]; ok {
			scopedModels = append(scopedModels, scoped)
		}
	}
	if len(scopedModels) <= 1 {
		return nil, nil
	}

	currentModel := s.Model()
	currentIndex := -1
	for i, scoped := range scopedModels {
		if ai.ModelsAreEqual(&scoped.Model, currentModel) {
			currentIndex = i
			break
		}
	}
	if currentIndex == -1 {
		currentIndex = 0
	}
	length := len(scopedModels)
	nextIndex := 0
	if direction == "backward" {
		nextIndex = (currentIndex - 1 + length) % length
	} else {
		nextIndex = (currentIndex + 1) % length
	}
	next := scopedModels[nextIndex]
	nextModel := next.Model
	thinkingLevel := s.getThinkingLevelForModelSwitch(&nextModel, next.ThinkingLevel)

	// Apply model
	s.Agent.State().Model = &nextModel
	_, _ = s.SessionManager.AppendModelChange(string(nextModel.Provider), nextModel.ID)
	if options != nil && options.Persist {
		s.SettingsManager.SetDefaultModelAndProvider(string(nextModel.Provider), nextModel.ID)
		s.addPersistedDefaultToNonEmptyScope(&nextModel)
	}

	// Apply thinking level for the new model.
	// - Explicit scoped model thinking level overrides defaults
	// - Per-model thinking level overrides take priority over the global default
	// SetThinkingLevel clamps to model capabilities.
	// Model persistence does not implicitly rewrite the global thinking default.
	s.SetThinkingLevel(thinkingLevel, options)

	if err := s.emitModelSelect(ctx, &nextModel, currentModel, "cycle"); err != nil {
		return nil, err
	}
	return &ModelCycleResult{Model: &nextModel, ThinkingLevel: s.ThinkingLevel(), IsScoped: true}, nil
}

func (s *AgentSession) cycleAvailableModel(ctx context.Context, direction string, options *ModelMutationOptions) (*ModelCycleResult, error) {
	availableModels := s.modelRuntime.GetAvailableSnapshot()
	if len(availableModels) <= 1 {
		return nil, nil
	}

	currentModel := s.Model()
	currentIndex := -1
	for i := range availableModels {
		if ai.ModelsAreEqual(&availableModels[i], currentModel) {
			currentIndex = i
			break
		}
	}
	if currentIndex == -1 {
		currentIndex = 0
	}
	length := len(availableModels)
	nextIndex := 0
	if direction == "backward" {
		nextIndex = (currentIndex - 1 + length) % length
	} else {
		nextIndex = (currentIndex + 1) % length
	}
	nextModel := availableModels[nextIndex]

	thinkingLevel := s.getThinkingLevelForModelSwitch(&nextModel, "")
	s.Agent.State().Model = &nextModel
	_, _ = s.SessionManager.AppendModelChange(string(nextModel.Provider), nextModel.ID)
	if options != nil && options.Persist {
		s.SettingsManager.SetDefaultModelAndProvider(string(nextModel.Provider), nextModel.ID)
		s.addPersistedDefaultToNonEmptyScope(&nextModel)
	}

	// Apply thinking level for the new model.
	// Model persistence does not implicitly rewrite the global thinking default.
	s.SetThinkingLevel(thinkingLevel, options)

	if err := s.emitModelSelect(ctx, &nextModel, currentModel, "cycle"); err != nil {
		return nil, err
	}
	return &ModelCycleResult{Model: &nextModel, ThinkingLevel: s.ThinkingLevel(), IsScoped: false}, nil
}

// ============================================================================
// Thinking Level Management
// ============================================================================

// SetThinkingLevel sets the thinking level. Clamps to model capabilities based
// on available thinking levels. Saves the clamped level to the session
// transcript only if the level actually changes. Persists the requested level
// to global defaults only when options.Persist is true (pi: setThinkingLevel).
func (s *AgentSession) SetThinkingLevel(level agent.ThinkingLevel, options *ModelMutationOptions) {
	availableLevels := s.GetAvailableThinkingLevels()
	effectiveLevel := level
	if !slices.Contains(availableLevels, level) {
		effectiveLevel = s.clampThinkingLevel(level, availableLevels)
	}

	// Only persist if actually changing
	previousLevel := s.Agent.State().ThinkingLevel
	isChanging := effectiveLevel != previousLevel

	s.Agent.State().ThinkingLevel = effectiveLevel

	if options != nil && options.Persist {
		s.SettingsManager.SetDefaultThinkingLevel(level)
	}

	if isChanging {
		_, _ = s.SessionManager.AppendThinkingLevelChange(string(effectiveLevel))
		s.emit(&AgentSessionEvent{AgentEvent: agent.AgentEvent{Type: AgentSessionEventThinkingLevelChanged}, ThinkingLevel: effectiveLevel})
		// pi fire-and-forgets (void); pig emits synchronously on a background
		// context (declared in the file header).
		_, _ = s.extensionRunner.Emit(context.Background(), &ThinkingLevelSelectEvent{Type: "thinking_level_select", Level: effectiveLevel, PreviousLevel: previousLevel})
	}
}

// CycleThinkingLevel cycles to the next thinking level. Returns "" when the
// model doesn't support thinking (pi: cycleThinkingLevel).
func (s *AgentSession) CycleThinkingLevel(options *ModelMutationOptions) agent.ThinkingLevel {
	if !s.SupportsThinking() {
		return ""
	}

	levels := s.GetAvailableThinkingLevels()
	currentIndex := slices.Index(levels, s.ThinkingLevel())
	nextIndex := (currentIndex + 1) % len(levels)
	nextLevel := levels[nextIndex]

	s.SetThinkingLevel(nextLevel, options)
	return nextLevel
}

// GetAvailableThinkingLevels returns the available thinking levels for the
// current model. The provider will clamp to what the specific model supports
// internally (pi: getAvailableThinkingLevels).
func (s *AgentSession) GetAvailableThinkingLevels() []agent.ThinkingLevel {
	model := s.Model()
	if model == nil {
		return slices.Clone(ThinkingLevelOptions)
	}
	return ai.GetSupportedThinkingLevels(model)
}

// SupportsThinking reports whether the current model supports
// thinking/reasoning (pi: supportsThinking).
func (s *AgentSession) SupportsThinking() bool {
	model := s.Model()
	return model != nil && model.Reasoning
}

func (s *AgentSession) getThinkingLevelForModelSwitch(targetModel *types.Model, explicitLevel agent.ThinkingLevel) agent.ThinkingLevel {
	if explicitLevel != "" {
		return explicitLevel
	}
	// Per-model default takes priority when switching to a model that has one
	if targetModel != nil {
		if perModel, ok := s.SettingsManager.GetModelThinkingLevel(string(targetModel.Provider), targetModel.ID); ok {
			return perModel
		}
	}
	if level, ok := s.SettingsManager.GetDefaultThinkingLevel(); ok {
		return level
	}
	if current := s.ThinkingLevel(); current != "" {
		return current
	}
	return DefaultThinkingLevel
}

func (s *AgentSession) clampThinkingLevel(level agent.ThinkingLevel, _ []agent.ThinkingLevel) agent.ThinkingLevel {
	model := s.Model()
	if model == nil {
		return "off"
	}
	return ai.ClampThinkingLevel(model, level)
}

// ============================================================================
// Queue Mode Management
// ============================================================================

func (s *AgentSession) syncQueueModesFromSettings() {
	s.Agent.SetSteeringMode(queueModeFromString(s.SettingsManager.GetSteeringMode()))
	s.Agent.SetFollowUpMode(queueModeFromString(s.SettingsManager.GetFollowUpMode()))
}

// queueModeFromString maps pi's "all" | "one-at-a-time" to the agent queue
// mode; unknown values keep pi's falsy default (one-at-a-time).
func queueModeFromString(mode string) agent.QueueMode {
	if mode == "all" {
		return agent.QueueModeAll
	}
	return agent.QueueModeOneAtATime
}

// SetSteeringMode sets the steering message mode. Saves to settings (pi:
// setSteeringMode).
func (s *AgentSession) SetSteeringMode(mode string) {
	s.Agent.SetSteeringMode(queueModeFromString(mode))
	s.SettingsManager.SetSteeringMode(mode)
}

// SetFollowUpMode sets the follow-up message mode. Saves to settings (pi:
// setFollowUpMode).
func (s *AgentSession) SetFollowUpMode(mode string) {
	s.Agent.SetFollowUpMode(queueModeFromString(mode))
	s.SettingsManager.SetFollowUpMode(mode)
}

// ============================================================================
// Compaction
// ============================================================================

// runDefaultCompaction generates pi's built-in compaction summary for manual
// and automatic compaction (pi: _runDefaultCompaction).
func (s *AgentSession) runDefaultCompaction(
	ctx context.Context,
	preparation *CompactionPreparation,
	requestModel *types.Model,
	auth *requestAuth,
	customInstructions string,
	reason string,
) (*CompactionResult, error) {
	return Compact(
		ctx,
		preparation,
		requestModel,
		auth.apiKey,
		auth.headers,
		customInstructions,
		types.ThinkingLevel(s.ThinkingLevel()),
		s.Agent.StreamFunction,
		auth.env,
		retryPolicyFromSettings(s.SettingsManager.GetRetrySettings()),
		s.summarizationRetryCallbacks(summaryRetrySourceCompaction, reason),
		"", // sessionId
	)
}

// retryPolicyFromSettings converts the settings retry values into the ai
// retry policy (pi: the settings.retry object is passed straight through;
// pig materializes ai.RetryPolicy).
func retryPolicyFromSettings(settings RetrySettingsValues) *ai.RetryPolicy {
	return &ai.RetryPolicy{
		Enabled:     settings.Enabled,
		MaxRetries:  int(settings.MaxRetries),
		BaseDelayMs: settings.BaseDelayMs,
	}
}

// summaryRetrySource discriminates the summarization retry source (pi:
// `source: { source: "branchSummary" } | { source: "compaction", reason }`).
type summaryRetrySource struct {
	source string // "branchSummary" | "compaction"
	reason string // compaction only: "manual" | "threshold" | "overflow"
}

const (
	summaryRetrySourceBranchSummary = "branchSummary"
	summaryRetrySourceCompaction    = "compaction"
)

// summarizationRetryCallbacks builds the retry policy callbacks shared by
// compaction and branch-summary summarization calls (pi:
// _summarizationRetryCallbacks). Uses the same settings.retry budget/backoff
// as agent-turn retries so a single transient stream drop no longer fails the
// whole operation.
func (s *AgentSession) summarizationRetryCallbacks(source string, reason string) *ai.RetryCallbacks {
	return &ai.RetryCallbacks{
		OnRetryScheduled: func(attempt int, maxAttempts int, delayMs int64, errorMessage string) {
			s.emit(&AgentSessionEvent{
				AgentEvent:  agent.AgentEvent{Type: AgentSessionEventSummarizationRetryScheduled},
				Attempt:     attempt,
				MaxAttempts: maxAttempts,
				DelayMs:     delayMs,
				FinalError:  errorMessage,
			})
		},
		OnRetryAttemptStart: func() {
			s.emit(&AgentSessionEvent{
				AgentEvent:         agent.AgentEvent{Type: AgentSessionEventSummarizationRetryAttemptStart},
				SummaryRetrySource: source,
				// pi spreads the compaction reason into the event; the reason
				// rides the session-level Reason field.
				Reason: reason,
			})
		},
		OnRetryFinished: func(success bool, attempt int, finalError string) {
			s.emit(&AgentSessionEvent{AgentEvent: agent.AgentEvent{Type: AgentSessionEventSummarizationRetryFinished}})
		},
	}
}

// Compact manually compacts the session context (pi: compact). This is the
// manual entry point used by /compact, RPC, and extensions. It is separate
// from automatic threshold/overflow compaction, which enters through
// checkCompaction and runAutoCompaction. After preparation and the
// session_before_compact hook, both paths call the lower-level Compact
// function, unless the hook cancels or supplies a custom result.
//
// Aborts the current agent operation first. Manual compaction never retries
// or continues the interrupted agent turn.
func (s *AgentSession) Compact(ctx context.Context, customInstructions string) (*CompactionResult, error) {
	if err := s.Abort(ctx); err != nil {
		return nil, err
	}
	compactionCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.mu.Lock()
	s.compactionCancel = cancel
	s.mu.Unlock()
	s.emit(&AgentSessionEvent{AgentEvent: agent.AgentEvent{Type: AgentSessionEventCompactionStart}, Reason: "manual"})
	fromExtension := false

	defer func() {
		s.mu.Lock()
		s.compactionCancel = nil
		s.mu.Unlock()
		// pi (v0.85.1): _clearManualCompactionState also resolves the idle
		// wait; the sites that cleared before emitting compaction_end are
		// covered by this defer.
		s.resolveIdleWaitIfIdle()
	}()

	fail := func(err error) (*CompactionResult, error) {
		message := err.Error()
		aborted := message == "Compaction cancelled" || errors.Is(err, context.Canceled)
		errorMessage := ""
		if !aborted {
			errorMessage = fmt.Sprintf("Compaction failed: %s", message)
		}
		s.emit(&AgentSessionEvent{
			AgentEvent:   agent.AgentEvent{Type: AgentSessionEventCompactionEnd},
			Reason:       "manual",
			Aborted:      aborted,
			ErrorMessage: errorMessage,
		})
		_ = s.emitSessionCompactFailed(ctx, &SessionCompactFailedEvent{
			Type:          "session_compact_failed",
			Reason:        "manual",
			ErrorMessage:  errorMessage,
			Aborted:       aborted,
			FromExtension: fromExtension,
		})
		return nil, err
	}

	model := s.Model()
	if model == nil {
		return fail(fmt.Errorf("%s", FormatNoModelSelectedMessage()))
	}

	auth, err := s.getSummarizationRequestAuth(compactionCtx, model)
	if err != nil {
		return fail(err)
	}

	pathEntries := s.SessionManager.GetBranch("")
	settings := s.SettingsManager.GetCompactionSettings()

	preparation := PrepareCompaction(pathEntries, settings)
	if preparation == nil {
		// Check why we can't compact
		if len(pathEntries) > 0 {
			if _, ok := pathEntries[len(pathEntries)-1].(*CompactionEntry); ok {
				return fail(fmt.Errorf("Already compacted"))
			}
		}
		return fail(fmt.Errorf("Nothing to compact (session too small)"))
	}

	var extensionCompaction *CompactionResult

	if s.extensionRunner.HasHandlers("session_before_compact") {
		result, err := s.extensionRunner.Emit(compactionCtx, &SessionBeforeCompactEvent{
			Type:               "session_before_compact",
			Preparation:        preparation,
			BranchEntries:      pathEntries,
			CustomInstructions: customInstructions,
			Reason:             "manual",
			WillRetry:          false,
		})
		if err != nil {
			return fail(err)
		}
		if beforeResult, ok := result.(*SessionBeforeCompactResult); ok && beforeResult != nil {
			if beforeResult.Cancel {
				return fail(fmt.Errorf("Compaction cancelled"))
			}
			if beforeResult.Compaction != nil {
				extensionCompaction = beforeResult.Compaction
				fromExtension = true
			}
		}
	}

	var summary string
	var firstKeptEntryID string
	var tokensBefore int64
	var usage *types.Usage
	var details any

	if extensionCompaction != nil {
		// Extension provided compaction content
		summary = extensionCompaction.Summary
		firstKeptEntryID = extensionCompaction.FirstKeptEntryID
		tokensBefore = extensionCompaction.TokensBefore
		usage = extensionCompaction.Usage
		details = extensionCompaction.Details
	} else {
		// Shared default summary generator, also used by automatic compaction.
		result, err := s.runDefaultCompaction(compactionCtx, preparation, auth.model, auth, customInstructions, "manual")
		if err != nil {
			return fail(err)
		}
		summary = result.Summary
		firstKeptEntryID = result.FirstKeptEntryID
		tokensBefore = result.TokensBefore
		usage = result.Usage
		details = result.Details
	}

	if compactionCtx.Err() != nil {
		return fail(fmt.Errorf("Compaction cancelled"))
	}

	if _, err := s.SessionManager.AppendCompaction(summary, firstKeptEntryID, tokensBefore, details, lo.ToPtr(fromExtension), usage); err != nil {
		return fail(err)
	}
	newEntries := s.SessionManager.GetEntries()
	sessionContext := s.SessionManager.BuildSessionContext()
	s.Agent.SetMessages(sessionContext.Messages)
	estimatedTokensAfter := estimateMessagesTokens(sessionContext.Messages)

	// Get the saved compaction entry for the extension event
	var savedCompactionEntry *CompactionEntry
	for _, entry := range newEntries {
		if compactionEntry, ok := entry.(*CompactionEntry); ok && compactionEntry.Summary == summary {
			savedCompactionEntry = compactionEntry
			break
		}
	}

	if s.extensionRunner != nil && savedCompactionEntry != nil {
		_, _ = s.extensionRunner.Emit(ctx, &SessionCompactEvent{
			Type:            "session_compact",
			CompactionEntry: savedCompactionEntry,
			FromExtension:   fromExtension,
			Reason:          "manual",
			WillRetry:       false,
		})
	}

	compactionResult := &CompactionResult{
		Summary:              summary,
		FirstKeptEntryID:     firstKeptEntryID,
		TokensBefore:         tokensBefore,
		EstimatedTokensAfter: &estimatedTokensAfter,
		Usage:                usage,
		Details:              details,
	}
	// compaction_end listeners may submit queued prompts, so expose idle state before notifying them.
	s.mu.Lock()
	s.compactionCancel = nil
	s.mu.Unlock()
	s.emit(&AgentSessionEvent{
		AgentEvent:       agent.AgentEvent{Type: AgentSessionEventCompactionEnd},
		Reason:           "manual",
		CompactionResult: compactionResult,
	})
	return compactionResult, nil
}

// AbortCompaction cancels in-progress compaction (manual or auto) (pi:
// abortCompaction).
func (s *AgentSession) AbortCompaction() {
	s.mu.Lock()
	compactionCancel := s.compactionCancel
	autoCompactionCancel := s.autoCompactionCancel
	s.mu.Unlock()
	if compactionCancel != nil {
		compactionCancel()
	}
	if autoCompactionCancel != nil {
		autoCompactionCancel()
	}
}

// AbortBranchSummary cancels in-progress branch summarization (pi:
// abortBranchSummary).
func (s *AgentSession) AbortBranchSummary() {
	s.mu.Lock()
	branchSummaryCancel := s.branchSummaryCancel
	s.mu.Unlock()
	if branchSummaryCancel != nil {
		branchSummaryCancel()
	}
}

// checkCompaction dispatches automatic compaction after agent_end or before
// prompt submission (pi: _checkCompaction). Manual compaction does not call
// this method; it enters through Compact.
//
// skipAbortedCheck=false includes aborted messages (for the pre-prompt check).
// Returns whether the post-run loop should call agent.Continue for overflow
// recovery or queued messages.
func (s *AgentSession) checkCompaction(ctx context.Context, assistantMessage *types.AssistantMessage, skipAbortedCheck bool) (bool, error) {
	settings := s.SettingsManager.GetCompactionSettings()
	if !settings.Enabled {
		return false, nil
	}

	// Skip if message was aborted (user cancelled) - unless skipAbortedCheck is false
	if skipAbortedCheck && assistantMessage.StopReason == types.StopReasonAborted {
		return false, nil
	}

	model := s.Model()
	contextWindow := int64(0)
	if model != nil {
		contextWindow = model.ContextWindow
	}

	// Skip overflow check if the message came from a different model.
	// This handles the case where user switched from a smaller-context model (e.g. opus)
	// to a larger-context model (e.g. codex) - the overflow error from the old model
	// shouldn't trigger compaction for the new model.
	sameModel := model != nil &&
		string(assistantMessage.Provider) == string(model.Provider) &&
		assistantMessage.Model == model.ID

	// Skip compaction checks if this assistant message is older than the latest
	// compaction boundary. This prevents a stale pre-compaction usage/error
	// from retriggering compaction on the first prompt after compaction.
	compactionEntry := GetLatestCompactionEntry(s.SessionManager.GetBranch(""))
	if compactionEntry != nil && assistantMessage.Timestamp <= entryTimestampMillis(compactionEntry.EntryBase.Timestamp) {
		return false, nil
	}

	// Automatic cases 1 and 2: context overflow.
	// A length stop is recoverable when output ended below the model's original desired limit,
	// independent of the configured context size or any context-clamped provider request limit.
	var maxTokens int64
	if model != nil {
		maxTokens = model.MaxTokens
	}
	contextOverflow := sameModel && aiutils.IsContextOverflow(assistantMessage, contextWindow)
	recoverableLength := sameModel && aiutils.IsRecoverableLength(assistantMessage, maxTokens)
	if contextOverflow || recoverableLength {
		willRetry := assistantMessage.StopReason != types.StopReasonStop

		// Case 2: the response completed successfully. Compact, but do not retry because
		// agent.continue() cannot continue from a completed assistant response.
		if !willRetry {
			return s.runAutoCompactionChecked(ctx, "overflow", false)
		}

		s.mu.Lock()
		alreadyAttempted := s.overflowRecoveryAttempted
		s.mu.Unlock()
		if alreadyAttempted {
			errorMessage := "Context overflow recovery failed after one compact-and-retry attempt. Try reducing context or switching to a larger-context model."
			if !contextOverflow {
				errorMessage = "Truncated response recovery failed after one compact-and-retry attempt."
			}
			s.emit(&AgentSessionEvent{
				AgentEvent:   agent.AgentEvent{Type: AgentSessionEventCompactionEnd},
				Reason:       "overflow",
				ErrorMessage: errorMessage,
			})
			_ = s.emitSessionCompactFailed(ctx, &SessionCompactFailedEvent{
				Type:         "session_compact_failed",
				Reason:       "overflow",
				ErrorMessage: errorMessage,
			})
			return false, nil
		}

		// Case 1: remove the failed or truncated message from agent state, compact, and
		// retry once. The message remains in session history but is excluded from retry context.
		s.mu.Lock()
		s.overflowRecoveryAttempted = true
		s.mu.Unlock()
		state := s.Agent.State()
		if len(state.Messages) > 0 && state.Messages[len(state.Messages)-1].MessageRole() == types.RoleAssistant {
			s.Agent.SetMessages(state.Messages[:len(state.Messages)-1])
		}
		return s.runAutoCompactionChecked(ctx, "overflow", willRetry)
	}

	// Case 3: threshold compaction without retry.
	// For error messages or all-zero usage messages, estimate from the last valid response.
	// This ensures sessions that hit persistent API errors (e.g. 529) or malformed zero-usage
	// responses can still compact and do not reset context accounting.
	var contextTokens int64
	directContextTokens := CalculateContextTokens(&assistantMessage.Usage)
	if assistantMessage.StopReason == types.StopReasonError || directContextTokens == 0 {
		messages := s.Agent.State().Messages
		estimate := EstimateContextTokens(messages)
		// Without provider usage, estimate.tokens is the pure message-size estimate.
		// Only usage-backed estimates need the stale pre-compaction check.
		if estimate.LastUsageIndex != nil {
			// Verify the usage source is post-compaction. Kept pre-compaction messages
			// have stale usage reflecting the old (larger) context and would falsely
			// trigger compaction right after one just finished.
			usageMsg := messages[*estimate.LastUsageIndex]
			if compactionEntry != nil {
				if usageAssistant, ok := usageMsg.(types.AssistantMessage); ok &&
					usageAssistant.Timestamp <= entryTimestampMillis(compactionEntry.EntryBase.Timestamp) {
					return false, nil
				}
			}
		}
		contextTokens = estimate.Tokens
	} else {
		contextTokens = directContextTokens
	}
	if ShouldCompact(contextTokens, contextWindow, settings) {
		return s.runAutoCompactionChecked(ctx, "threshold", false)
	}
	return false, nil
}

// runAutoCompactionChecked guards the nil model check before delegating to the
// runAutoCompaction seam (pi: the model check lives at the top of
// _runAutoCompaction; pig keeps the seam signature identical for tests).
func (s *AgentSession) runAutoCompactionChecked(ctx context.Context, reason string, willRetry bool) (bool, error) {
	if s.Model() == nil {
		return false, nil
	}
	return s.runAutoCompactionFn(ctx, reason, willRetry)
}

// runAutoCompaction executes threshold or overflow compaction. Manual
// compaction uses Compact instead. Both paths call the lower-level Compact
// function after preparation and extension interception (pi:
// _runAutoCompaction).
func (s *AgentSession) runAutoCompaction(ctx context.Context, reason string, willRetry bool) (bool, error) {
	settings := s.SettingsManager.GetCompactionSettings()
	started := false
	fromExtension := false

	// pi: finally { this._autoCompactionAbortController = undefined;
	// this._resolveIdleWaitIfIdle(); } — pig previously cleared the cancel
	// func at function start, leaving IsCompacting stuck true after a
	// completed auto-compaction; surfaced by the v0.85.1 isIdle change.
	defer func() {
		s.mu.Lock()
		s.autoCompactionCancel = nil
		s.mu.Unlock()
		s.resolveIdleWaitIfIdle()
	}()

	fail := func(errorMessage string) (bool, error) {
		if started {
			formattedErrorMessage := fmt.Sprintf("Auto-compaction failed: %s", errorMessage)
			if reason == "overflow" {
				formattedErrorMessage = fmt.Sprintf("Context overflow recovery failed: %s", errorMessage)
			}
			s.emit(&AgentSessionEvent{
				AgentEvent:   agent.AgentEvent{Type: AgentSessionEventCompactionEnd},
				Reason:       reason,
				ErrorMessage: formattedErrorMessage,
			})
			_ = s.emitSessionCompactFailed(ctx, &SessionCompactFailedEvent{
				Type:          "session_compact_failed",
				Reason:        reason,
				ErrorMessage:  formattedErrorMessage,
				FromExtension: fromExtension,
			})
		}
		return false, nil
	}

	model := s.Model()
	if model == nil {
		return false, nil
	}

	auth, err := s.getSummarizationRequestAuth(ctx, model)
	if err != nil {
		return fail(err.Error())
	}

	pathEntries := s.SessionManager.GetBranch("")

	preparation := PrepareCompaction(pathEntries, settings)
	if preparation == nil {
		return false, nil
	}

	s.emit(&AgentSessionEvent{AgentEvent: agent.AgentEvent{Type: AgentSessionEventCompactionStart}, Reason: reason})
	autoCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.mu.Lock()
	s.autoCompactionCancel = cancel
	s.mu.Unlock()
	started = true

	var extensionCompaction *CompactionResult

	if s.extensionRunner.HasHandlers("session_before_compact") {
		result, err := s.extensionRunner.Emit(autoCtx, &SessionBeforeCompactEvent{
			Type:          "session_before_compact",
			Preparation:   preparation,
			BranchEntries: pathEntries,
			Reason:        reason,
			WillRetry:     willRetry,
		})
		if err != nil {
			return fail(err.Error())
		}
		if beforeResult, ok := result.(*SessionBeforeCompactResult); ok && beforeResult != nil {
			if beforeResult.Cancel {
				s.emit(&AgentSessionEvent{
					AgentEvent: agent.AgentEvent{Type: AgentSessionEventCompactionEnd},
					Reason:     reason,
					Aborted:    true,
				})
				_ = s.emitSessionCompactFailed(ctx, &SessionCompactFailedEvent{
					Type:    "session_compact_failed",
					Reason:  reason,
					Aborted: true,
				})
				return false, nil
			}
			if beforeResult.Compaction != nil {
				extensionCompaction = beforeResult.Compaction
				fromExtension = true
			}
		}
	}

	var summary string
	var firstKeptEntryID string
	var tokensBefore int64
	var usage *types.Usage
	var details any

	if extensionCompaction != nil {
		// Extension provided compaction content
		summary = extensionCompaction.Summary
		firstKeptEntryID = extensionCompaction.FirstKeptEntryID
		tokensBefore = extensionCompaction.TokensBefore
		usage = extensionCompaction.Usage
		details = extensionCompaction.Details
	} else {
		// Shared default summary generator, also used by manual compaction.
		compactResult, err := s.runDefaultCompaction(autoCtx, preparation, auth.model, auth, "", reason)
		if err != nil {
			return fail(err.Error())
		}
		summary = compactResult.Summary
		firstKeptEntryID = compactResult.FirstKeptEntryID
		tokensBefore = compactResult.TokensBefore
		usage = compactResult.Usage
		details = compactResult.Details
	}

	if autoCtx.Err() != nil {
		s.emit(&AgentSessionEvent{
			AgentEvent: agent.AgentEvent{Type: AgentSessionEventCompactionEnd},
			Reason:     reason,
			Aborted:    true,
		})
		_ = s.emitSessionCompactFailed(ctx, &SessionCompactFailedEvent{
			Type:          "session_compact_failed",
			Reason:        reason,
			Aborted:       true,
			FromExtension: fromExtension,
		})
		return false, nil
	}

	if _, err := s.SessionManager.AppendCompaction(summary, firstKeptEntryID, tokensBefore, details, lo.ToPtr(fromExtension), usage); err != nil {
		return fail(err.Error())
	}
	newEntries := s.SessionManager.GetEntries()
	sessionContext := s.SessionManager.BuildSessionContext()
	s.Agent.SetMessages(sessionContext.Messages)
	estimatedTokensAfter := estimateMessagesTokens(sessionContext.Messages)

	// Get the saved compaction entry for the extension event
	var savedCompactionEntry *CompactionEntry
	for _, entry := range newEntries {
		if compactionEntry, ok := entry.(*CompactionEntry); ok && compactionEntry.Summary == summary {
			savedCompactionEntry = compactionEntry
			break
		}
	}

	if s.extensionRunner != nil && savedCompactionEntry != nil {
		_, _ = s.extensionRunner.Emit(ctx, &SessionCompactEvent{
			Type:            "session_compact",
			CompactionEntry: savedCompactionEntry,
			FromExtension:   fromExtension,
			Reason:          reason,
			WillRetry:       willRetry,
		})
	}

	result := &CompactionResult{
		Summary:              summary,
		FirstKeptEntryID:     firstKeptEntryID,
		TokensBefore:         tokensBefore,
		EstimatedTokensAfter: &estimatedTokensAfter,
		Usage:                usage,
		Details:              details,
	}
	s.emit(&AgentSessionEvent{
		AgentEvent:       agent.AgentEvent{Type: AgentSessionEventCompactionEnd},
		Reason:           reason,
		CompactionResult: result,
		WillRetry:        willRetry,
	})

	if willRetry {
		messages := s.Agent.State().Messages
		if len(messages) > 0 {
			if lastMsg, ok := messages[len(messages)-1].(types.AssistantMessage); ok &&
				(lastMsg.StopReason == types.StopReasonError || lastMsg.StopReason == types.StopReasonLength) {
				// The overflow response was persisted on message_end before checkCompaction() removed it
				// from agent state. Rebuilding state from the new compaction can restore that kept entry,
				// leaving an assistant as the final message. agent.continue() rejects that state, so remove
				// the retriable error or truncated-length response again before continuing the interrupted turn.
				s.Agent.SetMessages(messages[:len(messages)-1])
			}
		}
		return true, nil
	}

	// Auto-compaction can complete while follow-up/steering/custom messages are waiting.
	// Continue once so queued messages are delivered.
	return s.Agent.HasQueuedMessages(), nil
}

// SetAutoCompactionEnabled toggles the auto-compaction setting (pi:
// setAutoCompactionEnabled).
func (s *AgentSession) SetAutoCompactionEnabled(enabled bool) {
	s.SettingsManager.SetCompactionEnabled(enabled)
}

// AutoCompactionEnabled reports whether auto-compaction is enabled (pi:
// get autoCompactionEnabled).
func (s *AgentSession) AutoCompactionEnabled() bool {
	return s.SettingsManager.GetCompactionEnabled()
}

// ============================================================================
// Extension Bindings
// ============================================================================

// BindExtensions binds the extension host bindings and emits session_start
// (pi: bindExtensions).
func (s *AgentSession) BindExtensions(ctx context.Context, bindings *ExtensionBindings) error {
	if bindings != nil {
		if bindings.UIContext != nil {
			s.extensionUIContext = bindings.UIContext
		}
		if bindings.Mode != "" {
			s.extensionMode = bindings.Mode
		}
		if bindings.CommandContextActions != nil {
			s.extensionCommandContextActions = bindings.CommandContextActions
		}
		if bindings.AbortHandler != nil {
			s.extensionAbortHandler = bindings.AbortHandler
		}
		if bindings.ShutdownHandler != nil {
			s.extensionShutdownHandler = bindings.ShutdownHandler
		}
		if bindings.OnError != nil {
			s.extensionErrorListener = bindings.OnError
		}
	}

	s.applyExtensionBindings(s.extensionRunner)
	if _, err := s.extensionRunner.Emit(ctx, s.sessionStartEvent); err != nil {
		return err
	}
	reason := "startup"
	if s.sessionStartEvent.Reason == "reload" {
		reason = "reload"
	}
	return s.extendResourcesFromExtensions(ctx, reason)
}

func (s *AgentSession) extendResourcesFromExtensions(ctx context.Context, reason string) error {
	if !s.extensionRunner.HasHandlers("resources_discover") {
		return nil
	}

	result, err := s.extensionRunner.EmitResourcesDiscover(ctx, s.cwd, reason)
	if err != nil {
		return err
	}

	if len(result.SkillPaths) == 0 && len(result.PromptPaths) == 0 && len(result.ThemePaths) == 0 {
		return nil
	}

	extensionPaths := ResourceExtensionPaths{
		SkillPaths:  s.buildExtensionResourcePaths(result.SkillPaths),
		PromptPaths: s.buildExtensionResourcePaths(result.PromptPaths),
		ThemePaths:  s.buildExtensionResourcePaths(result.ThemePaths),
	}

	s.resourceLoader.ExtendResources(extensionPaths)
	basePrompt := s.rebuildSystemPrompt(s.GetActiveToolNames())
	s.mu.Lock()
	s.baseSystemPrompt = basePrompt
	s.mu.Unlock()
	s.Agent.State().SystemPrompt = basePrompt
	return nil
}

// buildExtensionResourcePaths adapts the discovered paths (pi: entries of
// { path, extensionPath }; pig's ResourcesDiscoverResult carries plain paths,
// so the source label and baseDir derive from the path itself — full
// extensionPath attribution arrives with the Phase 7 loader).
func (s *AgentSession) buildExtensionResourcePaths(paths []string) []ResourceExtensionPath {
	out := make([]ResourceExtensionPath, 0, len(paths))
	for _, path := range paths {
		source := s.getExtensionSourceLabel(path)
		baseDir := ""
		if !strings.HasPrefix(path, "<") {
			baseDir = filepath.Dir(path)
		}
		out = append(out, ResourceExtensionPath{
			Path: path,
			Metadata: ResourceExtensionPathMetadata{
				Source:  source,
				Scope:   "temporary",
				Origin:  "top-level",
				BaseDir: baseDir,
			},
		})
	}
	return out
}

func (s *AgentSession) getExtensionSourceLabel(extensionPath string) string {
	if strings.HasPrefix(extensionPath, "<") {
		name := strings.NewReplacer("<", "", ">", "").Replace(extensionPath)
		return fmt.Sprintf("extension:%s", name)
	}
	base := filepath.Base(extensionPath)
	name := regexp.MustCompile(`\.(ts|js)$`).ReplaceAllString(base, "")
	return fmt.Sprintf("extension:%s", name)
}

func (s *AgentSession) applyExtensionBindings(runner *ExtensionRunner) {
	runner.SetUIContext(s.extensionUIContext, s.extensionMode)
	runner.BindCommandContext(s.extensionCommandContextActions)

	if s.extensionErrorUnsubscriber != nil {
		s.extensionErrorUnsubscriber()
	}
	if s.extensionErrorListener != nil {
		s.extensionErrorUnsubscriber = runner.OnError(s.extensionErrorListener)
	} else {
		s.extensionErrorUnsubscriber = nil
	}
}

func (s *AgentSession) refreshCurrentModelFromRegistry() {
	currentModel := s.Model()
	if currentModel == nil {
		return
	}

	refreshedModel, ok := s.modelRuntime.GetModel(string(currentModel.Provider), currentModel.ID)
	if !ok || ai.ModelsAreEqual(&refreshedModel, currentModel) {
		return
	}

	s.Agent.State().Model = &refreshedModel
}

func (s *AgentSession) bindExtensionCore(runner *ExtensionRunner) {
	getCommands := func() []SlashCommandInfo {
		var extensionCommands []SlashCommandInfo
		for _, command := range runner.GetRegisteredCommands() {
			extensionCommands = append(extensionCommands, SlashCommandInfo{
				Name:        command.InvocationName,
				Description: command.Description,
				Source:      "extension",
				SourceInfo:  command.SourceInfo,
			})
		}

		var templates []SlashCommandInfo
		for _, template := range s.PromptTemplates() {
			templates = append(templates, SlashCommandInfo{
				Name:        template.Name,
				Description: template.Description,
				Source:      "prompt",
				SourceInfo:  template.SourceInfo,
			})
		}

		var skills []SlashCommandInfo
		loadedSkills, _ := s.resourceLoader.GetSkills()
		for _, skill := range loadedSkills {
			skills = append(skills, SlashCommandInfo{
				Name:        fmt.Sprintf("skill:%s", skill.Name),
				Description: skill.Description,
				Source:      "skill",
				SourceInfo:  skill.SourceInfo,
			})
		}

		out := append(extensionCommands, templates...)
		return append(out, skills...)
	}

	runner.BindCore(
		&ExtensionActions{
			SendMessage: func(ctx context.Context, message *CustomMessagePayload, options *SendMessageOptions) {
				if err := s.SendCustomMessage(ctx, message, options); err != nil {
					runner.EmitError(ExtensionError{ExtensionPath: "<runtime>", Event: "send_message", Error: err.Error()})
				}
			},
			SendUserMessage: func(ctx context.Context, content string, options *SendUserMessageOptions) {
				if err := s.SendUserMessage(ctx, content, options); err != nil {
					runner.EmitError(ExtensionError{ExtensionPath: "<runtime>", Event: "send_user_message", Error: err.Error()})
				}
			},
			AppendEntry: func(customType string, data any) string {
				entryID, _ := s.SessionManager.AppendCustomEntry(customType, data)
				entry := s.SessionManager.GetEntry(entryID)
				if entry != nil {
					s.emit(&AgentSessionEvent{AgentEvent: agent.AgentEvent{Type: AgentSessionEventEntryAppended}, Entry: entry})
				}
				return entryID
			},
			SetSessionName: func(name string) {
				s.SetSessionName(name)
			},
			GetSessionName: func() (string, bool) {
				return s.SessionManager.GetSessionName()
			},
			SetLabel: func(entryID string, label string) {
				_, _ = s.SessionManager.AppendLabelChange(entryID, &label)
			},
			GetActiveTools: s.GetActiveToolNames,
			GetAllTools:    s.GetAllTools,
			SetActiveTools: s.SetActiveToolsByName,
			RefreshTools:   func() { s.refreshToolRegistry(nil) },
			GetCommands:    getCommands,
			SetModel: func(ctx context.Context, model *types.Model) bool {
				if !s.modelRuntime.HasConfiguredAuth(string(model.Provider)) {
					return false
				}
				if err := s.SetModel(ctx, model, nil); err != nil {
					return false
				}
				return true
			},
			GetThinkingLevel: func() agent.ThinkingLevel {
				return s.ThinkingLevel()
			},
			SetThinkingLevel: func(level agent.ThinkingLevel) {
				s.SetThinkingLevel(level, nil)
			},
		},
		&ExtensionContextActions{
			GetModel:        s.Model,
			GetScopedModels: func() []ScopedModel { return s.scopedModels },
			IsIdle:          s.IsIdle,
			IsProjectTrusted: func() bool {
				return s.SettingsManager.IsProjectTrusted()
			},
			GetSignal: s.Agent.Signal,
			Abort: func() {
				if s.extensionAbortHandler != nil {
					s.extensionAbortHandler()
					return
				}
				_ = s.Abort(context.Background())
			},
			HasPendingMessages: func() bool {
				return s.PendingMessageCount() > 0
			},
			Shutdown: func() {
				if s.extensionShutdownHandler != nil {
					s.extensionShutdownHandler()
				}
			},
			GetContextUsage: s.GetContextUsage,
			Compact: func(options *CompactOptions) {
				go func() {
					var result *CompactionResult
					var err error
					defer func() {
						if recovered := recover(); recovered != nil {
							err = fmt.Errorf("%v", recovered)
						}
						if err != nil && options != nil && options.OnError != nil {
							options.OnError(err)
						}
					}()
					result, err = s.Compact(context.Background(), customInstructionsOf(options))
					if err == nil && options != nil && options.OnComplete != nil {
						options.OnComplete(result)
					}
				}()
			},
			GetSystemPrompt: s.SystemPrompt,
			GetSystemPromptOptions: func() BuildSystemPromptOptions {
				return s.baseSystemPromptOptionsSnapshot()
			},
		},
		&ExtensionProviderActions{
			RegisterProvider: func(name string, config ProviderConfig) {
				// pi's registerProvider takes a ProviderConfigInput; the
				// runner's simplified ProviderConfig shape converts here
				// (declared; the full input surface arrives with Phase 7).
				input := &ProviderConfigInput{
					Name:    config.Name,
					BaseURL: config.BaseURL,
					APIKey:  config.APIKey,
				}
				for _, model := range config.Models {
					input.Models = append(input.Models, ProviderConfigInputModel{
						ID:            model.ID,
						Name:          model.Name,
						Reasoning:     model.Reasoning,
						Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: model.Cost.Input, Output: model.Cost.Output, CacheRead: model.Cost.CacheRead, CacheWrite: model.Cost.CacheWrite}},
						ContextWindow: model.ContextWindow,
						MaxTokens:     model.MaxTokens,
					})
				}
				_ = s.modelRuntime.RegisterProvider(name, input)
				s.refreshCurrentModelFromRegistry()
			},
			RegisterNativeProvider: func(provider any) {
				if native, ok := provider.(ai.Provider); ok {
					_ = s.modelRuntime.RegisterNativeProvider(native)
					s.refreshCurrentModelFromRegistry()
				}
			},
			UnregisterProvider: func(name string) {
				s.modelRuntime.UnregisterProvider(name)
				s.refreshCurrentModelFromRegistry()
			},
		},
	)
}

// customInstructionsOf extracts CompactOptions.CustomInstructions ("" = none).
func customInstructionsOf(options *CompactOptions) string {
	if options == nil {
		return ""
	}
	return options.CustomInstructions
}

// refreshToolRegistryOptions mirror pi's optional _refreshToolRegistry
// argument (pi: { activeToolNames?, flagValues?, includeAllExtensionTools? }).
type refreshToolRegistryOptions struct {
	activeToolNames          []string
	includeAllExtensionTools bool
}

func (s *AgentSession) refreshToolRegistry(options *refreshToolRegistryOptions) {
	s.mu.Lock()
	previousRegistryNames := map[string]struct{}{}
	for name := range s.toolRegistry {
		previousRegistryNames[name] = struct{}{}
	}
	s.mu.Unlock()
	previousActiveToolNames := s.GetActiveToolNames()
	allowedToolNames := s.allowedToolNames
	excludedToolNames := s.excludedToolNames
	isAllowedTool := func(name string) bool {
		if allowedToolNames != nil {
			if _, ok := allowedToolNames[name]; !ok {
				return false
			}
		}
		if excludedToolNames != nil {
			if _, ok := excludedToolNames[name]; ok {
				return false
			}
		}
		return true
	}

	registeredTools := s.extensionRunner.GetAllRegisteredTools()
	allCustomTools := append([]RegisteredTool{}, registeredTools...)
	for _, definition := range s.customTools {
		allCustomTools = append(allCustomTools, RegisteredTool{
			Definition: definition,
			SourceInfo: CreateSyntheticSourceInfo(fmt.Sprintf("<sdk:%s>", definition.Name), SyntheticSourceInfoOptions{Source: "sdk"}),
		})
	}
	allCustomTools = slices.DeleteFunc(allCustomTools, func(tool RegisteredTool) bool {
		return !isAllowedTool(tool.Definition.Name)
	})

	definitionRegistry := map[string]toolDefinitionEntry{}
	var definitionRegistryOrder []string
	s.mu.Lock()
	for _, name := range s.baseToolDefinitionOrder {
		definition, ok := s.baseToolDefinitions[name]
		if !ok || !isAllowedTool(name) {
			continue
		}
		definitionRegistry[name] = toolDefinitionEntry{
			definition: definition,
			sourceInfo: CreateSyntheticSourceInfo(fmt.Sprintf("<builtin:%s>", name), SyntheticSourceInfoOptions{Source: "builtin"}),
		}
		definitionRegistryOrder = append(definitionRegistryOrder, name)
	}
	s.mu.Unlock()
	for _, tool := range allCustomTools {
		if _, exists := definitionRegistry[tool.Definition.Name]; !exists {
			definitionRegistryOrder = append(definitionRegistryOrder, tool.Definition.Name)
		}
		definitionRegistry[tool.Definition.Name] = toolDefinitionEntry{
			definition: tool.Definition,
			sourceInfo: tool.SourceInfo,
		}
	}

	toolDefinitions := definitionRegistry
	toolDefinitionOrder := definitionRegistryOrder
	toolPromptSnippets := map[string]string{}
	toolPromptGuidelines := map[string][]string{}
	for _, name := range definitionRegistryOrder {
		definition := definitionRegistry[name].definition
		if snippet := normalizePromptSnippet(definition.PromptSnippet); snippet != "" {
			toolPromptSnippets[definition.Name] = snippet
		}
		if guidelines := normalizePromptGuidelines(definition.PromptGuidelines); len(guidelines) > 0 {
			toolPromptGuidelines[definition.Name] = guidelines
		}
	}

	runner := s.extensionRunner
	var allCustomToolsForWrap []RegisteredTool
	allCustomToolsForWrap = append(allCustomToolsForWrap, allCustomTools...)
	wrappedExtensionTools := WrapRegisteredTools(allCustomToolsForWrap, runner)
	var builtinEntries []RegisteredTool
	s.mu.Lock()
	for _, name := range s.baseToolDefinitionOrder {
		definition, ok := s.baseToolDefinitions[name]
		if !ok || !isAllowedTool(name) {
			continue
		}
		builtinEntries = append(builtinEntries, RegisteredTool{
			Definition: definition,
			SourceInfo: CreateSyntheticSourceInfo(fmt.Sprintf("<builtin:%s>", name), SyntheticSourceInfoOptions{Source: "builtin"}),
		})
	}
	s.mu.Unlock()
	wrappedBuiltInTools := WrapRegisteredTools(builtinEntries, runner)

	toolRegistry := map[string]agent.AgentTool{}
	for _, tool := range wrappedBuiltInTools {
		toolRegistry[tool.Name] = tool
	}
	for _, tool := range wrappedExtensionTools {
		toolRegistry[tool.Name] = tool
	}

	s.mu.Lock()
	s.toolDefinitions = toolDefinitions
	s.toolDefinitionOrder = toolDefinitionOrder
	s.toolPromptSnippets = toolPromptSnippets
	s.toolPromptGuidelines = toolPromptGuidelines
	s.toolRegistry = toolRegistry
	s.mu.Unlock()

	nextActiveToolNames := []string{}
	if options != nil && options.activeToolNames != nil {
		nextActiveToolNames = append(nextActiveToolNames, options.activeToolNames...)
	} else {
		nextActiveToolNames = append(nextActiveToolNames, previousActiveToolNames...)
	}
	nextActiveToolNames = slices.DeleteFunc(nextActiveToolNames, func(name string) bool {
		return !isAllowedTool(name)
	})

	if allowedToolNames != nil {
		s.mu.Lock()
		for toolName := range s.toolRegistry {
			if _, ok := allowedToolNames[toolName]; ok {
				nextActiveToolNames = append(nextActiveToolNames, toolName)
			}
		}
		s.mu.Unlock()
	} else if options != nil && options.includeAllExtensionTools {
		for _, tool := range wrappedExtensionTools {
			nextActiveToolNames = append(nextActiveToolNames, tool.Name)
		}
	} else if options == nil || options.activeToolNames == nil {
		s.mu.Lock()
		for toolName := range s.toolRegistry {
			if _, ok := previousRegistryNames[toolName]; !ok {
				nextActiveToolNames = append(nextActiveToolNames, toolName)
			}
		}
		s.mu.Unlock()
	}

	// Deduplicate preserving first occurrence (pi: [...new Set(...)])
	seen := map[string]struct{}{}
	deduped := make([]string, 0, len(nextActiveToolNames))
	for _, name := range nextActiveToolNames {
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		deduped = append(deduped, name)
	}
	s.SetActiveToolsByName(deduped)
}

// buildRuntimeOptions mirror pi's _buildRuntime options.
type buildRuntimeOptions struct {
	activeToolNames          []string
	flagValues               map[string]any
	includeAllExtensionTools bool
}

func (s *AgentSession) buildRuntime(options buildRuntimeOptions) {
	autoResizeImages := s.SettingsManager.GetImageAutoResize()
	shellCommandPrefix, _ := s.SettingsManager.GetShellCommandPrefix()
	shellPath, _ := s.SettingsManager.GetShellPath()

	var baseToolDefinitions map[string]*tools.ToolDefinition
	var baseToolOrder []string
	if s.baseToolsOverride != nil {
		baseToolDefinitions = map[string]*tools.ToolDefinition{}
		// pi iterates Object.entries (insertion order); pig's map is
		// unordered, so sort by name for determinism (declared).
		names := make([]string, 0, len(s.baseToolsOverride))
		for name := range s.baseToolsOverride {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			baseToolDefinitions[name] = tools.CreateToolDefinitionFromAgentTool(s.baseToolsOverride[name])
			baseToolOrder = append(baseToolOrder, name)
		}
	} else {
		created := tools.CreateAllToolDefinitions(s.cwd, &tools.ToolsOptions{
			Read: &tools.ReadToolOptions{AutoResizeImages: lo.ToPtr(autoResizeImages)},
			Bash: &tools.BashToolOptions{CommandPrefix: shellCommandPrefix, ShellPath: shellPath},
		})
		// pi's object literal order (declaration order in createAllToolDefinitions).
		baseToolOrder = []string{"read", "bash", "powershell", "edit", "write", "grep", "find", "ls"}
		baseToolDefinitions = map[string]*tools.ToolDefinition{}
		for name, definition := range created {
			baseToolDefinitions[string(name)] = definition
		}
	}

	s.mu.Lock()
	s.baseToolDefinitions = baseToolDefinitions
	s.baseToolDefinitionOrder = baseToolOrder
	s.mu.Unlock()

	extensionsResult := s.resourceLoader.GetExtensions()
	if options.flagValues != nil {
		for name, value := range options.flagValues {
			extensionsResult.Runtime.FlagValues[name] = value
		}
	}

	runner := NewExtensionRunner(
		extensionsResult.Extensions,
		extensionsResult.Runtime,
		s.cwd,
		s.SessionManager,
		NewModelRegistry(s.modelRuntime),
	)
	s.extensionRunner = runner
	if s.extensionRunnerRef != nil {
		s.extensionRunnerRef.Current = runner
	}
	s.bindExtensionCore(runner)
	s.applyExtensionBindings(runner)

	defaultActiveToolNames := []string{"read", "bash", "edit", "write"}
	if s.baseToolsOverride != nil {
		defaultActiveToolNames = make([]string, 0, len(s.baseToolsOverride))
		for name := range s.baseToolsOverride {
			defaultActiveToolNames = append(defaultActiveToolNames, name)
		}
		slices.Sort(defaultActiveToolNames)
	}
	baseActiveToolNames := options.activeToolNames
	if baseActiveToolNames == nil {
		baseActiveToolNames = defaultActiveToolNames
	}
	s.refreshToolRegistry(&refreshToolRegistryOptions{
		activeToolNames:          baseActiveToolNames,
		includeAllExtensionTools: options.includeAllExtensionTools,
	})
}

// ReloadOptions carry the optional reload hooks (pi: reload options).
type ReloadOptions struct {
	// BeforeSessionStart runs before the reload session_start event.
	BeforeSessionStart func(ctx context.Context) error
}

// Reload rebuilds the extension runtime and resources (pi: reload).
func (s *AgentSession) Reload(ctx context.Context, options *ReloadOptions) error {
	oldRunner := s.extensionRunner
	previousFlagValues := oldRunner.GetFlagValues()
	if _, err := EmitSessionShutdownEvent(ctx, oldRunner, &SessionShutdownEvent{Type: "session_shutdown", Reason: "reload"}); err != nil {
		return err
	}
	oldRunner.Invalidate("This extension ctx is stale after session replacement or reload. Do not use a captured pig or command ctx after ctx.newSession(), ctx.fork(), ctx.switchSession(), or ctx.reload(). For newSession, fork, and switchSession, move post-replacement work into withSession and use the ctx passed to withSession. For reload, do not use the old ctx after await ctx.reload().")
	s.SettingsManager.Reload()
	s.syncQueueModesFromSettings()
	ai.ResetApiProviders()
	if err := s.resourceLoader.Reload(nil); err != nil {
		return err
	}
	s.buildRuntime(buildRuntimeOptions{
		activeToolNames:          s.GetActiveToolNames(),
		flagValues:               previousFlagValues,
		includeAllExtensionTools: true,
	})

	hasBindings := s.extensionUIContext != nil ||
		s.extensionCommandContextActions != nil ||
		s.extensionShutdownHandler != nil ||
		s.extensionErrorListener != nil
	if hasBindings {
		if options != nil && options.BeforeSessionStart != nil {
			if err := options.BeforeSessionStart(ctx); err != nil {
				return err
			}
		}
		if _, err := s.extensionRunner.Emit(ctx, &SessionStartEvent{Type: "session_start", Reason: "reload"}); err != nil {
			return err
		}
		if err := s.extendResourcesFromExtensions(ctx, "reload"); err != nil {
			return err
		}
	}
	return nil
}

// ============================================================================
// Auto-Retry
// ============================================================================

// isRetryableError checks if an error is retryable (overloaded, rate limit,
// server errors). Context overflow errors are NOT retryable (handled by
// compaction instead) (pi: _isRetryableError).
func (s *AgentSession) isRetryableError(message *types.AssistantMessage) bool {
	// Context overflow is handled by compaction, not retry.
	contextWindow := int64(0)
	if model := s.Model(); model != nil {
		contextWindow = model.ContextWindow
	}
	if aiutils.IsContextOverflow(message, contextWindow) {
		return false
	}
	return aiutils.IsRetryableAssistantError(message)
}

// prepareRetry prepares a retryable error for continuation with exponential
// backoff. Returns true if the caller should continue the agent (pi:
// _prepareRetry).
func (s *AgentSession) prepareRetry(ctx context.Context, message *types.AssistantMessage) (bool, error) {
	settings := s.SettingsManager.GetRetrySettings()
	if !settings.Enabled {
		return false, nil
	}

	s.mu.Lock()
	s.retryAttempt++
	if s.retryAttempt > int(settings.MaxRetries) {
		// Preserve the completed attempt count so post-run handling can emit
		// the final failure.
		s.retryAttempt--
		s.mu.Unlock()
		return false, nil
	}
	attempt := s.retryAttempt
	s.mu.Unlock()

	delayMs := settings.BaseDelayMs * (int64(1) << uint(attempt-1))

	s.emit(&AgentSessionEvent{
		AgentEvent:  agent.AgentEvent{Type: AgentSessionEventAutoRetryStart},
		Attempt:     attempt,
		MaxAttempts: int(settings.MaxRetries),
		DelayMs:     delayMs,
		FinalError:  firstNonEmpty(message.ErrorMessage, "Unknown error"),
	})

	// Remove error message from agent state (keep in session for history)
	state := s.Agent.State()
	if len(state.Messages) > 0 && state.Messages[len(state.Messages)-1].MessageRole() == types.RoleAssistant {
		s.Agent.SetMessages(state.Messages[:len(state.Messages)-1])
	}

	// Wait with exponential backoff (abortable)
	retryCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.mu.Lock()
	s.retryCancel = cancel
	s.mu.Unlock()
	err := utils.Sleep(retryCtx, delayMs)
	s.mu.Lock()
	s.retryCancel = nil
	s.mu.Unlock()
	if err != nil {
		// Aborted during sleep - emit end event so UI can clean up
		cancel()
		s.mu.Lock()
		attempt := s.retryAttempt
		s.retryAttempt = 0
		s.mu.Unlock()
		s.emit(&AgentSessionEvent{
			AgentEvent: agent.AgentEvent{Type: AgentSessionEventAutoRetryEnd},
			Attempt:    attempt,
			FinalError: "Retry cancelled",
		})
		return false, nil
	}
	return true, nil
}

// AbortRetry cancels the in-progress retry (pi: abortRetry).
func (s *AgentSession) AbortRetry() {
	s.mu.Lock()
	retryCancel := s.retryCancel
	s.mu.Unlock()
	if retryCancel != nil {
		retryCancel()
	}
}

// IsRetrying reports whether auto-retry is currently in progress (pi:
// get isRetrying).
func (s *AgentSession) IsRetrying() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retryCancel != nil
}

// AutoRetryEnabled reports whether auto-retry is enabled (pi:
// get autoRetryEnabled).
func (s *AgentSession) AutoRetryEnabled() bool {
	return s.SettingsManager.GetRetryEnabled()
}

// SetAutoRetryEnabled toggles the auto-retry setting (pi:
// setAutoRetryEnabled).
func (s *AgentSession) SetAutoRetryEnabled(enabled bool) {
	s.SettingsManager.SetRetryEnabled(enabled)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ============================================================================
// Bash Execution
// ============================================================================

// BashExecuteOptions are the executeBash options (pi: the third parameter).
type BashExecuteOptions struct {
	// ExcludeFromContext excludes the command output from the LLM context
	// (!! prefix).
	ExcludeFromContext bool
	// ID is an optional identifier included in bash execution update events.
	ID string
	// Operations customizes command execution for remote execution.
	Operations tools.BashOperations
	// HasOperations distinguishes a nil Operations from an unset one.
	HasOperations bool
}

// ExecuteBash executes a bash command and adds the result to agent context and
// session (pi: executeBash).
func (s *AgentSession) ExecuteBash(ctx context.Context, command string, onChunk func(chunk string), options *BashExecuteOptions) (BashResult, error) {
	bashCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.mu.Lock()
	s.bashCancelToken++
	token := s.bashCancelToken
	s.bashCancels[token] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.bashCancels, token)
		s.mu.Unlock()
	}()

	// Apply command prefix if configured (e.g., "shopt -s expand_aliases" for alias support)
	prefix, _ := s.SettingsManager.GetShellCommandPrefix()
	shellPath, _ := s.SettingsManager.GetShellPath()
	resolvedCommand := command
	if prefix != "" {
		resolvedCommand = fmt.Sprintf("%s\n%s", prefix, command)
	}

	var operations tools.BashOperations
	if options != nil && options.HasOperations {
		operations = options.Operations
	} else {
		operations = tools.CreateLocalBashOperations(s.SessionManager.GetCwd(), &tools.BashLocalOptions{ShellPath: shellPath})
	}

	var updateID *string
	if options != nil && options.ID != "" {
		updateID = &options.ID
	}

	executorOptions := &BashExecutorOptions{
		OnChunk: func(delta string) {
			if onChunk != nil {
				onChunk(delta)
			}
			s.emit(&AgentSessionEvent{AgentEvent: agent.AgentEvent{Type: AgentSessionEventBashExecutionUpdate}, ID: updateID, Delta: delta})
		},
	}
	result, err := ExecuteBashWithOperations(bashCtx, resolvedCommand, s.SessionManager.GetCwd(), operations, executorOptions)
	if err != nil {
		return BashResult{}, err
	}

	excludeFromContext := false
	if options != nil {
		excludeFromContext = options.ExcludeFromContext
	}
	s.RecordBashResult(command, result, excludeFromContext)
	return result, nil
}

// RecordBashResult records a bash execution result in session history. Used by
// ExecuteBash and by extensions that handle bash execution themselves (pi:
// recordBashResult).
func (s *AgentSession) RecordBashResult(command string, result BashResult, excludeFromContext bool) {
	bashMessage := harness.BashExecutionMessage{
		Role:           "bashExecution",
		Command:        command,
		Output:         result.Output,
		ExitCode:       result.ExitCode,
		Cancelled:      result.Cancelled,
		Truncated:      result.Truncated,
		FullOutputPath: result.FullOutputPath,
		Timestamp:      time.Now().UnixMilli(),
	}
	if excludeFromContext {
		bashMessage.ExcludeFromContext = lo.ToPtr(true)
	}

	// If agent is streaming, defer adding to avoid breaking tool_use/tool_result ordering
	if s.IsStreaming() {
		// Queue for later - will be flushed on agent_end
		s.mu.Lock()
		s.pendingBashMessages = append(s.pendingBashMessages, &bashMessage)
		s.mu.Unlock()
		return
	}
	// Add to agent state immediately
	state := s.Agent.State()
	state.Messages = append(state.Messages, bashMessage)

	// Save to session
	_, _ = s.SessionManager.AppendMessage(bashMessage)
}

// AbortBash cancels running bash commands (pi: abortBash).
func (s *AgentSession) AbortBash() {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.bashCancels))
	for _, cancel := range s.bashCancels {
		cancels = append(cancels, cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// IsBashRunning reports whether a bash command is currently running (pi:
// get isBashRunning).
func (s *AgentSession) IsBashRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bashCancels) > 0
}

// HasPendingBashMessages reports whether there are pending bash messages
// waiting to be flushed (pi: get hasPendingBashMessages).
func (s *AgentSession) HasPendingBashMessages() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pendingBashMessages) > 0
}

// flushPendingBashMessages flushes pending bash messages to agent state and
// session. Called after the agent turn completes to maintain proper message
// ordering (pi: _flushPendingBashMessages).
func (s *AgentSession) flushPendingBashMessages() {
	s.mu.Lock()
	pending := s.pendingBashMessages
	s.pendingBashMessages = nil
	s.mu.Unlock()
	if len(pending) == 0 {
		return
	}

	for _, bashMessage := range pending {
		// Add to agent state
		state := s.Agent.State()
		state.Messages = append(state.Messages, *bashMessage)

		// Save to session
		_, _ = s.SessionManager.AppendMessage(*bashMessage)
	}
}

// ============================================================================
// Session Management
// ============================================================================

// SetSessionName sets a display name for the current session (pi:
// setSessionName).
func (s *AgentSession) SetSessionName(name string) {
	_, _ = s.SessionManager.AppendSessionInfo(name)
	var eventName *SessionInfoChangedEvent
	if sessionName, ok := s.SessionManager.GetSessionName(); ok {
		eventName = &SessionInfoChangedEvent{Type: "session_info_changed", Name: &sessionName}
	} else {
		eventName = &SessionInfoChangedEvent{Type: "session_info_changed"}
	}
	s.emit(&AgentSessionEvent{AgentEvent: agent.AgentEvent{Type: AgentSessionEventSessionInfoChanged}, Name: eventName.Name})
	// pi fire-and-forgets (void); pig emits synchronously (declared in the
	// file header).
	_, _ = s.extensionRunner.Emit(context.Background(), eventName)
}

// ============================================================================
// Tree Navigation
// ============================================================================

// NavigateTreeOptions are the navigateTree options (pi: the options object).
type NavigateTreeOptions struct {
	// Summarize reports whether the user wants to summarize the abandoned
	// branch.
	Summarize bool
	// CustomInstructions are the custom instructions for the summarizer.
	CustomInstructions string
	// ReplaceInstructions replaces the default prompt with
	// CustomInstructions instead of appending.
	ReplaceInstructions bool
	// Label attaches to the branch summary entry.
	Label string
}

// NavigateTreeResult is the navigateTree result (pi: the result object).
type NavigateTreeResult struct {
	// EditorText carries the user message text to restore into the editor;
	// nil = none (pi: editorText?).
	EditorText *string
	// Cancelled reports extension cancellation.
	Cancelled bool
	// Aborted reports summarization abort; nil = absent (pi: aborted?).
	Aborted bool
	// SummaryEntry is the created branch summary entry; nil = none (pi:
	// summaryEntry?).
	SummaryEntry *BranchSummaryEntry
}

// NavigateTree navigates to a different node in the session tree. Unlike
// forking, which creates a new session file, this stays in the same file (pi:
// navigateTree).
func (s *AgentSession) NavigateTree(ctx context.Context, targetID string, options *NavigateTreeOptions) (*NavigateTreeResult, error) {
	if s.IsStreaming() {
		return nil, fmt.Errorf("Wait for the current response to finish before navigating the session tree.")
	}
	if options == nil {
		options = &NavigateTreeOptions{}
	}

	oldLeafID := s.SessionManager.GetLeafID()

	// No-op if already at target
	if oldLeafID != nil && targetID == *oldLeafID {
		return &NavigateTreeResult{Cancelled: false}, nil
	}
	if oldLeafID == nil && targetID == "" {
		return &NavigateTreeResult{Cancelled: false}, nil
	}

	// Model required for summarization
	if options.Summarize && s.Model() == nil {
		return nil, fmt.Errorf("No model available for summarization")
	}

	targetEntry := s.SessionManager.GetEntry(targetID)
	if targetEntry == nil {
		return nil, fmt.Errorf("Entry %s not found", targetID)
	}

	// Collect entries to summarize (from old leaf to common ancestor)
	collected := CollectEntriesForBranchSummary(s.SessionManager, oldLeafID, targetID)
	entriesToSummarize := collected.Entries
	commonAncestorID := collected.CommonAncestorID

	// Prepare event data - mutable so extensions can override
	customInstructions := options.CustomInstructions
	replaceInstructions := options.ReplaceInstructions
	label := options.Label

	preparation := TreePreparation{
		TargetID:            targetID,
		OldLeafID:           oldLeafID,
		CommonAncestorID:    commonAncestorID,
		EntriesToSummarize:  entriesToSummarize,
		UserWantsSummary:    options.Summarize,
		CustomInstructions:  customInstructions,
		ReplaceInstructions: replaceInstructions,
		Label:               label,
	}

	// Set up abort context for summarization
	branchCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.mu.Lock()
	s.branchSummaryCancel = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.branchSummaryCancel = nil
		s.mu.Unlock()
		// pi (v0.85.1): resolve the idle wait after branch summarization
		// settles.
		s.resolveIdleWaitIfIdle()
	}()

	var extensionSummary *SessionBeforeTreeSummary
	fromExtension := false

	// Emit session_before_tree event
	if s.extensionRunner.HasHandlers("session_before_tree") {
		result, err := s.extensionRunner.Emit(branchCtx, &SessionBeforeTreeEvent{
			Type:        "session_before_tree",
			Preparation: &preparation,
		})
		if err != nil {
			return nil, err
		}
		if beforeResult, ok := result.(*SessionBeforeTreeResult); ok && beforeResult != nil {
			if beforeResult.Cancel {
				return &NavigateTreeResult{Cancelled: true}, nil
			}
			if beforeResult.Summary != nil && options.Summarize {
				extensionSummary = beforeResult.Summary
				fromExtension = true
			}
			// Allow extensions to override instructions and label
			if beforeResult.CustomInstructions != nil {
				customInstructions = *beforeResult.CustomInstructions
			}
			if beforeResult.ReplaceInstructions != nil {
				replaceInstructions = *beforeResult.ReplaceInstructions
			}
			if beforeResult.Label != nil {
				label = *beforeResult.Label
			}
		}
	}

	// Run default summarizer if needed
	var summaryText string
	var summaryDetails any
	var summaryUsage *types.Usage
	if options.Summarize && len(entriesToSummarize) > 0 && extensionSummary == nil {
		model := s.Model()
		auth, err := s.getSummarizationRequestAuth(branchCtx, model)
		if err != nil {
			return nil, err
		}
		branchSummarySettings := s.SettingsManager.GetBranchSummarySettings()
		reserveTokens := int(branchSummarySettings.ReserveTokens)
		result, err := GenerateBranchSummary(branchCtx, entriesToSummarize, &GenerateBranchSummaryOptions{
			Model:               auth.model,
			APIKey:              auth.apiKey,
			Headers:             auth.headers,
			Env:                 auth.env,
			CustomInstructions:  customInstructions,
			ReplaceInstructions: replaceInstructions,
			ReserveTokens:       &reserveTokens,
			StreamFn:            s.Agent.StreamFunction,
			Retry:               retryPolicyFromSettings(s.SettingsManager.GetRetrySettings()),
			Callbacks:           s.summarizationRetryCallbacks(summaryRetrySourceBranchSummary, ""),
		})
		if err != nil {
			return nil, err
		}
		if result.Aborted {
			return &NavigateTreeResult{Cancelled: true, Aborted: true}, nil
		}
		if result.Error != "" {
			return nil, fmt.Errorf("%s", result.Error)
		}
		summaryText = result.Summary
		summaryUsage = result.Usage
		readFiles := result.ReadFiles
		if readFiles == nil {
			readFiles = []string{}
		}
		modifiedFiles := result.ModifiedFiles
		if modifiedFiles == nil {
			modifiedFiles = []string{}
		}
		summaryDetails = BranchSummaryDetails{
			ReadFiles:     readFiles,
			ModifiedFiles: modifiedFiles,
		}
	} else if extensionSummary != nil {
		summaryText = extensionSummary.Summary
		summaryDetails = extensionSummary.Details
		summaryUsage = extensionSummary.Usage
	}

	// Determine the new leaf position based on target type
	var newLeafID *string
	var editorText *string

	if messageEntry, ok := targetEntry.(*SessionMessageEntry); ok && messageEntry.Message.MessageRole() == types.RoleUser {
		// User message: leaf = parent (nil if root), text goes to editor
		newLeafID = messageEntry.EntryBase.ParentID
		text := messageTextContent(messageEntry.Message)
		editorText = &text
	} else if _, ok := targetEntry.(*CustomMessageEntry); ok {
		// Custom message: leaf = parent (nil if root), text goes to editor
		newLeafID = targetEntry.Base().ParentID
		text := customEntryText(targetEntry.(*CustomMessageEntry))
		editorText = &text
	} else {
		// Non-user message: leaf = selected node
		newLeafID = &targetID
	}

	// Switch leaf (with or without summary)
	// Summary is attached at the navigation target position (newLeafId), not the old branch
	var summaryEntry *BranchSummaryEntry
	if summaryText != "" {
		// Create summary at target position (can be nil for root)
		branchFromID := ""
		if newLeafID != nil {
			branchFromID = *newLeafID
		}
		summaryID, err := s.SessionManager.BranchWithSummary(branchFromID, summaryText, summaryDetails, lo.ToPtr(fromExtension), summaryUsage)
		if err != nil {
			return nil, err
		}
		if entry, ok := s.SessionManager.GetEntry(summaryID).(*BranchSummaryEntry); ok {
			summaryEntry = entry
		}

		// Attach label to the summary entry
		if label != "" {
			_, _ = s.SessionManager.AppendLabelChange(summaryID, &label)
		}
	} else if newLeafID == nil {
		// No summary, navigating to root - reset leaf
		s.SessionManager.ResetLeaf()
	} else {
		// No summary, navigating to non-root
		if err := s.SessionManager.Branch(*newLeafID); err != nil {
			return nil, err
		}
	}

	// Attach label to target entry when not summarizing (no summary entry to label)
	if label != "" && summaryText == "" {
		_, _ = s.SessionManager.AppendLabelChange(targetID, &label)
	}

	// Update agent state
	sessionContext := s.SessionManager.BuildSessionContext()
	s.Agent.SetMessages(sessionContext.Messages)

	// Emit session_tree event
	fromExtensionValue := fromExtension
	if summaryText == "" {
		fromExtensionValue = false
	}
	_, _ = s.extensionRunner.Emit(ctx, &SessionTreeEvent{
		Type:          "session_tree",
		NewLeafID:     s.SessionManager.GetLeafID(),
		OldLeafID:     oldLeafID,
		SummaryEntry:  summaryEntry,
		FromExtension: &fromExtensionValue,
	})

	return &NavigateTreeResult{EditorText: editorText, Cancelled: false, SummaryEntry: summaryEntry}, nil
}

// messageTextContent extracts the text of a message entry (pi:
// contentText(targetEntry.message.content, "")).
func messageTextContent(message agent.AgentMessage) string {
	switch m := message.(type) {
	case types.UserMessage:
		if m.Content.Parts == nil {
			return m.Content.String
		}
		return ai.ContentText(m.Content.Parts, "")
	case types.AssistantMessage:
		return ai.ContentText(m.Content, "")
	case types.ToolResultMessage:
		return ai.ContentText(m.Content, "")
	case harness.CustomMessage:
		return customContentText(m.Content)
	}
	return ""
}

// customEntryText extracts the text of a custom message entry (pi:
// contentText(targetEntry.content, "") over the custom message's string-or-
// blocks content).
func customEntryText(entry *CustomMessageEntry) string {
	if entry == nil {
		return ""
	}
	return customContentText(entry.Content)
}

func customContentText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []types.Content:
		return ai.ContentText(value, "")
	}
	return ""
}

// ForkUserMessage is one user message candidate for the fork selector (pi:
// getUserMessagesForForking's return members).
type ForkUserMessage struct {
	EntryID string
	Text    string
}

// GetUserMessagesForForking returns all user messages from the session for the
// fork selector (pi: getUserMessagesForForking).
func (s *AgentSession) GetUserMessagesForForking() []ForkUserMessage {
	entries := s.SessionManager.GetEntries()
	var result []ForkUserMessage

	for _, entry := range entries {
		messageEntry, ok := entry.(*SessionMessageEntry)
		if !ok {
			continue
		}
		if messageEntry.Message.MessageRole() != types.RoleUser {
			continue
		}
		text := messageTextContent(messageEntry.Message)
		if text != "" {
			result = append(result, ForkUserMessage{EntryID: entry.Base().ID, Text: text})
		}
	}
	return result
}

// GetSessionStats aggregates session statistics over ALL session entries
// (including history that was compacted away), so token/cost totals reflect
// what was actually billed across the session (pi: getSessionStats).
func (s *AgentSession) GetSessionStats() SessionStats {
	var userMessages, assistantMessages, toolResults, totalMessages, toolCalls int
	usageTotals := CreateUsageTotals()

	for _, entry := range s.SessionManager.GetEntries() {
		switch typed := entry.(type) {
		case *BranchSummaryEntry:
			if typed.Usage != nil {
				AddUsageToTotals(&usageTotals, *typed.Usage)
			}
		case *CompactionEntry:
			if typed.Usage != nil {
				AddUsageToTotals(&usageTotals, *typed.Usage)
			}
		}
		messageEntry, ok := entry.(*SessionMessageEntry)
		if !ok {
			continue
		}
		totalMessages++
		// The transcript holds both value messages (runtime appends) and
		// pointer messages (jsonl decode); both shapes are accepted.
		switch message := messageEntry.Message.(type) {
		case types.UserMessage:
			userMessages++
		case *types.UserMessage:
			userMessages++
		case types.ToolResultMessage:
			toolResults++
			if message.Usage != nil {
				AddUsageToTotals(&usageTotals, *message.Usage)
			}
		case *types.ToolResultMessage:
			toolResults++
			if message.Usage != nil {
				AddUsageToTotals(&usageTotals, *message.Usage)
			}
		case types.AssistantMessage:
			assistantMessages++
			for _, content := range message.Content {
				if _, isToolCall := content.(types.ToolCall); isToolCall {
					toolCalls++
				}
			}
			AddUsageToTotals(&usageTotals, message.Usage)
		case *types.AssistantMessage:
			assistantMessages++
			for _, content := range message.Content {
				if _, isToolCall := content.(types.ToolCall); isToolCall {
					toolCalls++
				}
			}
			AddUsageToTotals(&usageTotals, message.Usage)
		}
	}

	stats := SessionStats{
		SessionFile:       s.SessionFile(),
		SessionID:         s.SessionID(),
		UserMessages:      userMessages,
		AssistantMessages: assistantMessages,
		ToolCalls:         toolCalls,
		ToolResults:       toolResults,
		TotalMessages:     totalMessages,
		Cost:              usageTotals.Cost,
		ContextUsage:      s.GetContextUsage(),
	}
	stats.Tokens.Input = usageTotals.Input
	stats.Tokens.Output = usageTotals.Output
	stats.Tokens.CacheRead = usageTotals.CacheRead
	stats.Tokens.CacheWrite = usageTotals.CacheWrite
	stats.Tokens.Total = usageTotals.Input + usageTotals.Output + usageTotals.CacheRead + usageTotals.CacheWrite
	return stats
}

// GetContextUsage returns the current context usage; nil when no model is
// selected or the context window is unknown (pi: getContextUsage).
func (s *AgentSession) GetContextUsage() *ContextUsage {
	model := s.Model()
	if model == nil {
		return nil
	}

	contextWindow := model.ContextWindow
	if contextWindow <= 0 {
		return nil
	}

	// After compaction, the last assistant usage reflects pre-compaction context size.
	// We can only trust usage from an assistant that responded after the latest compaction.
	// If no such assistant exists, context token count is unknown until the next LLM response.
	branchEntries := s.SessionManager.GetBranch("")
	latestCompaction := GetLatestCompactionEntry(branchEntries)

	if latestCompaction != nil {
		// Check if there's a valid assistant usage after the compaction boundary
		compactionIndex := -1
		for i := len(branchEntries) - 1; i >= 0; i-- {
			if branchEntries[i] == SessionEntry(latestCompaction) {
				compactionIndex = i
				break
			}
		}
		hasPostCompactionUsage := false
		for i := len(branchEntries) - 1; i > compactionIndex; i-- {
			entry := branchEntries[i]
			if messageEntry, ok := entry.(*SessionMessageEntry); ok {
				if assistant, isAssistant := messageEntry.Message.(types.AssistantMessage); isAssistant {
					if assistant.StopReason != types.StopReasonAborted && assistant.StopReason != types.StopReasonError {
						contextTokens := CalculateContextTokens(&assistant.Usage)
						if contextTokens > 0 {
							hasPostCompactionUsage = true
							break
						}
					}
				}
			}
		}

		if !hasPostCompactionUsage {
			return &ContextUsage{Tokens: nil, ContextWindow: contextWindow, Percent: nil}
		}
	}

	estimate := EstimateContextTokens(s.Messages())
	percent := (float64(estimate.Tokens) / float64(contextWindow)) * 100

	return &ContextUsage{
		Tokens:        &estimate.Tokens,
		ContextWindow: contextWindow,
		Percent:       &percent,
	}
}

// entryTimestampMillis converts an entry's ISO timestamp to epoch millis (pi:
// new Date(compactionEntry.timestamp).getTime()).
func entryTimestampMillis(timestamp string) int64 {
	if parsed, ok := parseTimestamp(timestamp); ok {
		return parsed.UnixMilli()
	}
	return 0
}

// ============================================================================
// Utilities
// ============================================================================

// GetLastAssistantText returns the text content of the last assistant message,
// for the /copy command (pi: getLastAssistantText). Returns "" when no
// assistant message exists (pi: undefined).
func (s *AgentSession) GetLastAssistantText() string {
	messages := s.Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(types.AssistantMessage)
		if !ok {
			continue
		}
		// Skip aborted messages with no content
		if msg.StopReason == types.StopReasonAborted && len(msg.Content) == 0 {
			continue
		}

		var text strings.Builder
		for _, content := range msg.Content {
			if textContent, isText := content.(types.TextContent); isText {
				text.WriteString(textContent.Text)
			}
		}
		trimmed := strings.TrimSpace(text.String())
		if trimmed != "" {
			return trimmed
		}
		return ""
	}
	return ""
}

// ============================================================================
// Extension System
// ============================================================================

// CreateReplacedSessionContext builds the replacement session context for
// newSession/fork/switchSession flows (pi: createReplacedSessionContext).
func (s *AgentSession) CreateReplacedSessionContext() *ReplacedSessionContext {
	commandContext := s.extensionRunner.CreateCommandContext()
	replaced := &ReplacedSessionContext{ExtensionCommandContext: *commandContext}
	replaced.SendMessage = func(ctx context.Context, message *CustomMessagePayload, options *SendMessageOptions) {
		_ = s.SendCustomMessage(ctx, message, options)
	}
	replaced.SendUserMessage = func(ctx context.Context, content string, options *SendUserMessageOptions) {
		_ = s.SendUserMessage(ctx, content, options)
	}
	return replaced
}

// HasExtensionHandlers reports whether extensions have handlers for the event
// type (pi: hasExtensionHandlers).
func (s *AgentSession) HasExtensionHandlers(eventType string) bool {
	return s.extensionRunner.HasHandlers(eventType)
}

// GetExtensionRunner returns the extension runner (for setting UI context and
// error handlers) (pi: get extensionRunner).
func (s *AgentSession) GetExtensionRunner() *ExtensionRunner {
	return s.extensionRunner
}
