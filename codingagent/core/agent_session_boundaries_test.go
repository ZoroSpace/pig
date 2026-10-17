// pi: test/suite/agent-session-boundaries.test.ts — AgentSession actionable
// boundaries (pi v0.87.0): turn_end / agent_before_settle boundary drafts,
// canonical projection consumption, recovery omissions, and durable length
// recovery. The scripted harness mirrors pi's createHarness +
// registerFauxProvider (test/suite/harness.ts + packages/ai faux provider):
// responses carry a chars/4 usage estimate computed from the serialized
// request context, which is what drives the compaction-threshold cases.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

// ----------------------------------------------------------------------------
// faux harness (pi: createHarness + registerFauxProvider)
// ----------------------------------------------------------------------------

var fauxToolCallSeq atomic.Int64

// fauxResponseStep is one scripted response (pi: FauxResponseStep =
// AssistantMessage | FauxResponseFactory); the factory receives the provider
// request context.
type fauxResponseStep func(llmContext *types.TranscriptContext) *types.AssistantMessage

// fauxAssistantMessage builds a scripted assistant response (pi:
// fauxAssistantMessage). content is a string, a single types.Content, or a
// []types.Content.
func fauxAssistantMessage(content any, overrides ...func(*types.AssistantMessage)) fauxResponseStep {
	var blocks []types.Content
	switch value := content.(type) {
	case string:
		blocks = []types.Content{types.NewTextContent(value)}
	case types.Content:
		blocks = []types.Content{value}
	case []types.Content:
		blocks = value
	default:
		panic(fmt.Sprintf("unsupported faux content %T", content))
	}
	return func(*types.TranscriptContext) *types.AssistantMessage {
		message := &types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    blocks,
			StopReason: types.StopReasonStop,
			Timestamp:  time.Now().UnixMilli(),
		}
		for _, override := range overrides {
			override(message)
		}
		return message
	}
}

// fauxToolCall builds a tool-call content block (pi: fauxToolCall).
func fauxToolCall(name string, arguments map[string]any) types.ToolCall {
	return types.ToolCall{
		Kind:      types.KindToolCall,
		ID:        fmt.Sprintf("toolcall:%d", fauxToolCallSeq.Add(1)),
		Name:      name,
		Arguments: arguments,
	}
}

// fauxWithTimestamp overrides the response timestamp (pi: fauxAssistantMessage
// options.timestamp).
func fauxWithTimestamp(ts int64) func(*types.AssistantMessage) {
	return func(message *types.AssistantMessage) { message.Timestamp = ts }
}

func fauxEstimateTokens(text string) int64 {
	// pi: Math.ceil(text.length / 4).
	return int64((len(text) + 3) / 4)
}

func fauxContentToText(content types.UserContent) string {
	if len(content.Parts) == 0 {
		return content.String
	}
	parts := make([]string, 0, len(content.Parts))
	for _, block := range content.Parts {
		if text, ok := block.(types.TextContent); ok {
			parts = append(parts, text.Text)
			continue
		}
		if image, ok := block.(types.ImageContent); ok {
			parts = append(parts, fmt.Sprintf("[image:%s:%d]", image.MimeType, len(image.Data)))
		}
	}
	return strings.Join(parts, "\n")
}

func fauxAssistantContentToText(content []types.Content) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		switch value := block.(type) {
		case types.TextContent:
			parts = append(parts, value.Text)
		case types.ThinkingContent:
			parts = append(parts, value.Thinking)
		case types.ToolCall:
			encoded, _ := json.Marshal(value.Arguments)
			parts = append(parts, value.Name+":"+string(encoded))
		}
	}
	return strings.Join(parts, "\n")
}

func fauxSerializeContext(context *types.TranscriptContext) string {
	if context == nil {
		return ""
	}
	lines := make([]string, 0, len(context.Messages))
	for _, message := range context.Messages {
		lines = append(lines, string(message.MessageRole())+":"+fauxMessageToText(message))
	}
	return strings.Join(lines, "\n\n")
}

func fauxMessageToText(message types.Message) string {
	switch value := message.(type) {
	case types.SystemMessage:
		parts := []string{}
		if text := value.Content.String; text != "" {
			parts = append(parts, text)
		}
		for _, tool := range value.ToolsRemoved {
			encoded, _ := json.Marshal(tool)
			parts = append(parts, "tool-:"+string(encoded))
		}
		for _, tool := range value.ToolsAdded {
			encoded, _ := json.Marshal(tool)
			parts = append(parts, "tool+:"+string(encoded))
		}
		return strings.Join(parts, "\n")
	case types.UserMessage:
		return fauxContentToText(value.Content)
	case types.AssistantMessage:
		return fauxAssistantContentToText(value.Content)
	case types.ToolResultMessage:
		return value.ToolName + "\n" + fauxContentToText(types.UserContent{Parts: value.Content})
	}
	return ""
}

func fauxCommonPrefixLength(a, b string) int {
	length := len(a)
	if len(b) < length {
		length = len(b)
	}
	index := 0
	for index < length && a[index] == b[index] {
		index++
	}
	return index
}

// fauxWithUsageEstimate mirrors the faux provider's usage estimate: tokens
// are chars/4 over the serialized context and the response text, with a
// per-session prefix cache simulation (pi: withUsageEstimate).
func fauxWithUsageEstimate(message *types.AssistantMessage, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions, promptCache map[string]string) {
	promptText := fauxSerializeContext(llmContext)
	promptTokens := fauxEstimateTokens(promptText)
	outputTokens := fauxEstimateTokens(fauxAssistantContentToText(message.Content))
	input := promptTokens
	cacheRead, cacheWrite := int64(0), int64(0)
	sessionID := ""
	if options != nil {
		sessionID = options.SessionID
	}
	if sessionID != "" {
		if previous, ok := promptCache[sessionID]; ok {
			cachedChars := fauxCommonPrefixLength(previous, promptText)
			cacheRead = fauxEstimateTokens(previous[:cachedChars])
			cacheWrite = fauxEstimateTokens(promptText[cachedChars:])
			input = promptTokens - cacheRead
			if input < 0 {
				input = 0
			}
		} else {
			cacheWrite = promptTokens
		}
		promptCache[sessionID] = promptText
	}
	message.Usage = types.Usage{
		Input:       input,
		Output:      outputTokens,
		CacheRead:   cacheRead,
		CacheWrite:  cacheWrite,
		TotalTokens: input + outputTokens + cacheRead + cacheWrite,
	}
}

// fauxHarness is the local test harness (pi: Harness).
type fauxHarness struct {
	session         *AgentSession
	sessionManager  *SessionManager
	settingsManager *SettingsManager
	model           *types.Model

	mu          sync.Mutex
	callCount   int
	responses   []fauxResponseStep
	promptCache map[string]string
	events      []*AgentSessionEvent
}

// fauxHarnessOptions parameterizes createFauxHarness (pi: HarnessOptions).
type fauxHarnessOptions struct {
	modelID       string
	contextWindow int64
	maxTokens     int64
	settings      Settings
	tools         []agent.AgentTool
	extensions    []*Extension
}

func createFauxHarness(t *testing.T, options *fauxHarnessOptions) *fauxHarness {
	t.Helper()
	cfg := options
	if cfg == nil {
		cfg = &fauxHarnessOptions{}
	}
	modelID := cfg.modelID
	if modelID == "" {
		modelID = "faux-1"
	}
	contextWindow := cfg.contextWindow
	if contextWindow == 0 {
		// pi: FauxModelDefinition.contextWindow ?? 128_000.
		contextWindow = 128000
	}
	model := &types.Model{
		ID:            modelID,
		Name:          "Faux Model",
		API:           types.ApiOpenAICompletions,
		Provider:      "anthropic",
		BaseURL:       "http://localhost:0",
		ContextWindow: contextWindow,
		MaxTokens:     cfg.maxTokens,
		Input:         []ai.ModelInput{ai.ModelInputText},
	}

	h := &fauxHarness{model: model, promptCache: map[string]string{}}
	streamFn := func(ctx context.Context, streamModel *types.Model, llmContext *types.TranscriptContext, streamOptions *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		h.mu.Lock()
		h.callCount++
		var step fauxResponseStep
		if len(h.responses) > 0 {
			step = h.responses[0]
			h.responses = h.responses[1:]
		}
		h.mu.Unlock()
		stream := newMockAssistantStream()
		if step == nil {
			message := &types.AssistantMessage{
				Role:         types.RoleAssistant,
				Content:      []types.Content{},
				StopReason:   types.StopReasonError,
				ErrorMessage: "No faux response queued",
				Timestamp:    time.Now().UnixMilli(),
			}
			message.API = streamModel.API
			message.Provider = streamModel.Provider
			message.Model = streamModel.ID
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: message})
			stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonError, Message: message})
			stream.End()
			return stream.AssistantMessageEventStream
		}
		response := step(llmContext)
		response.API = streamModel.API
		response.Provider = streamModel.Provider
		response.Model = streamModel.ID
		fauxWithUsageEstimate(response, llmContext, streamOptions, h.promptCache)
		stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: response})
		if response.StopReason == types.StopReasonError {
			stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonError, Message: response})
		} else {
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: response.StopReason, Message: response})
		}
		stream.End()
		return stream.AssistantMessageEventStream
	}

	baseToolsOverride := map[string]agent.AgentTool{}
	if cfg.tools != nil {
		for _, tool := range cfg.tools {
			baseToolsOverride[tool.Name] = tool
		}
	} else {
		// pi: baseToolsOverride stays undefined, so the session keeps its
		// default base tools (read/bash/edit/write) and the built prompt
		// includes their snippets.
		baseToolsOverride = nil
	}
	extensions := cfg.extensions
	if extensions == nil {
		extensions = []*Extension{}
	}
	session, sessionManager, settingsManager := createAgentTestSession(t, &createAgentTestSessionOptions{
		Model:             model,
		StreamFn:          streamFn,
		GetAPIKey:         true,
		SettingsOverrides: cfg.settings,
		ExtensionsResult:  []*LoadExtensionsResult{{Extensions: extensions, Runtime: CreateExtensionRuntime()}},
		BaseToolsOverride: baseToolsOverride,
	})
	h.session = session
	h.sessionManager = sessionManager
	h.settingsManager = settingsManager
	session.Subscribe(func(event *AgentSessionEvent) {
		h.mu.Lock()
		h.events = append(h.events, event)
		h.mu.Unlock()
	})
	return h
}

// setResponses replaces the scripted response queue (pi: setResponses).
func (h *fauxHarness) setResponses(responses ...fauxResponseStep) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.responses = responses
}

// callCountRead returns the provider request count (pi: faux.state.callCount).
func (h *fauxHarness) callCountRead() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.callCount
}

// eventsOfType returns the recorded session events of one type (pi:
// eventsOfType).
func (h *fauxHarness) eventsOfType(eventType agent.AgentEventType) []*AgentSessionEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*AgentSessionEvent
	for _, event := range h.events {
		if event.Type == eventType {
			out = append(out, event)
		}
	}
	return out
}

// fauxToolsOverride reports whether custom tools were configured (pi: an
// empty BaseToolsOverride map means the default base tools stay).
func (h *fauxHarness) noopTool() agent.AgentTool {
	return agent.AgentTool{
		Tool: types.Tool{
			Name:        "noop",
			Description: "Noop",
			Parameters:  rawJSON(`{"type":"object","properties":{}}`),
		},
		Label: "Noop",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{Content: []types.Content{types.NewTextContent("done")}, Details: map[string]any{}}, nil
		},
	}
}

// ----------------------------------------------------------------------------
// AgentSession actionable boundaries
// ----------------------------------------------------------------------------

func TestBoundaryCommitsRetainNoneTurnEndCompactionAndContinuesOnce(t *testing.T) {
	var handled bool
	var observedIDs []string
	var requests []string
	var mu sync.Mutex
	handler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		turnEnd, ok := event.(*TurnEndEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		mu.Lock()
		observedIDs = append(observedIDs, turnEnd.MessageEntryID)
		alreadyHandled := handled
		handled = true
		mu.Unlock()
		if alreadyHandled {
			return nil, nil
		}
		return &BoundaryResult{
			Entries: []SessionBoundaryDraft{
				&CompactionEntryDraft{Type: "compaction", Summary: "exact handoff", Details: map[string]any{"source": "test"}},
			},
			Continue: lo.ToPtr(true),
		}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{{Path: "boundary-ext", Handlers: map[string][]ExtensionHandler{"turn_end": {handler}}}},
	})
	var requestCapture sync.Mutex
	harness.setResponses(
		fauxAssistantMessage("discarded response"),
		func(llmContext *types.TranscriptContext) *types.AssistantMessage {
			encoded, _ := json.Marshal(llmContext.Messages)
			requestCapture.Lock()
			requests = append(requests, string(encoded))
			requestCapture.Unlock()
			return fauxAssistantMessage("continued from handoff")(llmContext)
		},
	)

	if err := harness.session.Prompt(context.Background(), "discarded prompt", nil); err != nil {
		t.Fatal(err)
	}

	var compaction *CompactionEntry
	for _, entry := range harness.sessionManager.GetEntries() {
		if typed, ok := entry.(*CompactionEntry); ok {
			compaction = typed
			break
		}
	}
	if compaction == nil {
		t.Fatal("expected a compaction entry")
	}
	if compaction.Summary != "exact handoff" {
		t.Fatalf("summary = %q", compaction.Summary)
	}
	if compaction.FirstKeptEntryID != compaction.ID {
		t.Fatalf("firstKeptEntryId = %q, want self %q", compaction.FirstKeptEntryID, compaction.ID)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	if !strings.Contains(requests[0], "exact handoff") {
		t.Fatal("continuation request missing the compaction summary")
	}
	if strings.Contains(requests[0], "discarded prompt") || strings.Contains(requests[0], "discarded response") {
		t.Fatal("continuation request leaked the omitted turn")
	}
	if len(observedIDs) != 2 {
		t.Fatalf("observedIds = %v, want 2 entries", observedIDs)
	}
	if len(harness.eventsOfType(AgentSessionEventAgentSettled)) != 1 {
		t.Fatal("expected exactly one agent_settled")
	}
}

func TestBoundaryPreservesQueueSchedulingAroundTurnEndHandoff(t *testing.T) {
	for _, queueKind := range []string{"steering", "follow-up", "both"} {
		t.Run(queueKind, func(t *testing.T) {
			var handled bool
			var requests []string
			var mu sync.Mutex
			handler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
				if _, ok := event.(*TurnEndEvent); !ok {
					return nil, fmt.Errorf("unexpected event %T", event)
				}
				mu.Lock()
				alreadyHandled := handled
				handled = true
				mu.Unlock()
				if alreadyHandled {
					return nil, nil
				}
				if queueKind == "steering" || queueKind == "both" {
					extCtx.SendUserMessage(ctx, "queued steering", &SendUserMessageOptions{DeliverAs: "steer"})
				}
				if queueKind == "follow-up" || queueKind == "both" {
					extCtx.SendUserMessage(ctx, "queued follow-up", &SendUserMessageOptions{DeliverAs: "followUp"})
				}
				return &BoundaryResult{
					Entries:  []SessionBoundaryDraft{&CompactionEntryDraft{Type: "compaction", Summary: "exact handoff"}},
					Continue: lo.ToPtr(true),
				}, nil
			}
			harness := createFauxHarness(t, &fauxHarnessOptions{
				extensions: []*Extension{{Path: "boundary-ext", Handlers: map[string][]ExtensionHandler{"turn_end": {handler}}}},
			})
			first := true
			harness.setResponses(
				fauxAssistantMessage("first"),
				func(llmContext *types.TranscriptContext) *types.AssistantMessage {
					encoded, _ := json.Marshal(llmContext.Messages)
					mu.Lock()
					requests = append(requests, string(encoded))
					mu.Unlock()
					return fauxAssistantMessage("second")(llmContext)
				},
				func(llmContext *types.TranscriptContext) *types.AssistantMessage {
					encoded, _ := json.Marshal(llmContext.Messages)
					mu.Lock()
					requests = append(requests, string(encoded))
					mu.Unlock()
					_ = first
					return fauxAssistantMessage("third")(llmContext)
				},
			)

			if err := harness.session.Prompt(context.Background(), "start", nil); err != nil {
				t.Fatal(err)
			}

			mu.Lock()
			defer mu.Unlock()
			if !strings.Contains(requests[0], "exact handoff") {
				t.Fatal("first request missing the handoff summary")
			}
			if queueKind == "steering" {
				if harness.callCountRead() != 2 {
					t.Fatalf("callCount = %d, want 2", harness.callCountRead())
				}
				if !strings.Contains(requests[0], "queued steering") || strings.Contains(requests[0], "queued follow-up") {
					t.Fatal("steering delivery mismatch")
				}
			} else if queueKind == "follow-up" {
				if harness.callCountRead() != 2 {
					t.Fatalf("callCount = %d, want 2", harness.callCountRead())
				}
				if !strings.Contains(requests[0], "queued follow-up") {
					t.Fatal("follow-up delivery mismatch")
				}
			} else {
				if harness.callCountRead() != 3 {
					t.Fatalf("callCount = %d, want 3", harness.callCountRead())
				}
				if !strings.Contains(requests[0], "queued steering") || strings.Contains(requests[0], "queued follow-up") {
					t.Fatal("steering should deliver first")
				}
				if !strings.Contains(requests[1], "queued follow-up") {
					t.Fatal("follow-up should deliver second")
				}
			}
		})
	}
}

func TestBoundaryKeepsReplacementVerbatimThroughThresholdCompaction(t *testing.T) {
	var handled bool
	var requests []string
	var mu sync.Mutex
	instruction := strings.Repeat("EXACT-REPLACEMENT-INSTRUCTION ", 100)
	compactionHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		beforeCompact, ok := event.(*SessionBeforeCompactEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		return &SessionBeforeCompactResult{Compaction: &CompactionResult{
			Summary:          "older history summary",
			FirstKeptEntryID: beforeCompact.Preparation.FirstKeptEntryID,
			TokensBefore:     beforeCompact.Preparation.TokensBefore,
		}}, nil
	}
	boundaryHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		turnEnd, ok := event.(*TurnEndEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		mu.Lock()
		alreadyHandled := handled
		handled = true
		mu.Unlock()
		if alreadyHandled {
			return nil, nil
		}
		var userEntry *SessionMessageEntry
		branch := extCtx.SessionManager.GetBranch("")
		for i := len(branch) - 1; i >= 0; i-- {
			if typed, ok := branch[i].(*SessionMessageEntry); ok && typed.Message.MessageRole() == types.RoleUser {
				userEntry = typed
				break
			}
		}
		if userEntry == nil {
			return nil, fmt.Errorf("missing user entry")
		}
		return &BoundaryResult{
			Entries: []SessionBoundaryDraft{
				&ContextEditEntryDraft{Type: "context_edit", TargetID: userEntry.ID, Replacement: &ContextEditReplacement{Content: instruction}},
				&ContextEditEntryDraft{Type: "context_edit", TargetID: turnEnd.MessageEntryID},
				&CustomEntryDraft{Type: "custom", CustomType: "bookkeeping", Data: map[string]any{"source": "test"}},
			},
			Continue: lo.ToPtr(true),
		}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		// contextWindow 2000 in pi: the system prompt there includes a docs
		// section (~250 tokens); pig dropped it, so the threshold moves down.
		// 1500 sits well below the post-removal critical range while keeping
		// the test's intent (threshold compaction fires on these short msgs).
		contextWindow: 1500,
		maxTokens:     100,
		settings:      Settings{"compaction": map[string]any{"enabled": true, "keepRecentTokens": float64(1), "reserveTokens": float64(0)}},
		extensions: []*Extension{
			{Path: "compact-ext", Handlers: map[string][]ExtensionHandler{"session_before_compact": {compactionHandler}}},
			{Path: "boundary-ext", Handlers: map[string][]ExtensionHandler{"turn_end": {boundaryHandler}}},
		},
	})
	if _, err := harness.sessionManager.AppendMessage(types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "older input"}, Timestamp: time.Now().UnixMilli() - 2}); err != nil {
		t.Fatal(err)
	}
	older := fauxAssistantMessage("older answer")(nil)
	older.Timestamp = time.Now().UnixMilli() - 1
	if _, err := harness.sessionManager.AppendMessage(*older); err != nil {
		t.Fatal(err)
	}
	harness.session.RefreshContext()
	harness.setResponses(
		fauxAssistantMessage("answered original input"),
		func(llmContext *types.TranscriptContext) *types.AssistantMessage {
			encoded, _ := json.Marshal(llmContext.Messages)
			mu.Lock()
			requests = append(requests, string(encoded))
			mu.Unlock()
			return fauxAssistantMessage("answered replacement")(llmContext)
		},
	)

	if err := harness.session.Prompt(context.Background(), "original input", nil); err != nil {
		t.Fatal(err)
	}

	if len(harness.eventsOfType(AgentSessionEventCompactionStart)) == 0 {
		t.Fatal("expected threshold compaction to run")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	if !strings.Contains(requests[0], "EXACT-REPLACEMENT-INSTRUCTION") {
		t.Fatal("replacement instruction lost through compaction")
	}
}

func TestBoundaryKeepsUnsentInputVerbatimThroughThresholdCompaction(t *testing.T) {
	var handled bool
	var requests []string
	var mu sync.Mutex
	instruction := strings.Repeat("EXACT-UNSENT-INSTRUCTION ", 100)
	compactionHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		beforeCompact, ok := event.(*SessionBeforeCompactEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		return &SessionBeforeCompactResult{Compaction: &CompactionResult{
			Summary:          "older history summary",
			FirstKeptEntryID: beforeCompact.Preparation.FirstKeptEntryID,
			TokensBefore:     beforeCompact.Preparation.TokensBefore,
		}}, nil
	}
	boundaryHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		if _, ok := event.(*TurnEndEvent); !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		mu.Lock()
		alreadyHandled := handled
		handled = true
		mu.Unlock()
		if alreadyHandled {
			return nil, nil
		}
		return &BoundaryResult{
			Entries: []SessionBoundaryDraft{
				&CustomMessageEntryDraft{Type: "custom_message", CustomType: "next-work", Content: instruction},
				&CustomEntryDraft{Type: "custom", CustomType: "bookkeeping", Data: map[string]any{"source": "test"}},
			},
			Continue: lo.ToPtr(true),
		}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		contextWindow: 2000,
		maxTokens:     100,
		settings:      Settings{"compaction": map[string]any{"enabled": true, "keepRecentTokens": float64(1), "reserveTokens": float64(0)}},
		extensions: []*Extension{
			{Path: "compact-ext", Handlers: map[string][]ExtensionHandler{"session_before_compact": {compactionHandler}}},
			{Path: "boundary-ext", Handlers: map[string][]ExtensionHandler{"turn_end": {boundaryHandler}}},
		},
	})
	harness.setResponses(
		fauxAssistantMessage("first"),
		func(llmContext *types.TranscriptContext) *types.AssistantMessage {
			encoded, _ := json.Marshal(llmContext.Messages)
			mu.Lock()
			requests = append(requests, string(encoded))
			mu.Unlock()
			return fauxAssistantMessage("second")(llmContext)
		},
	)

	if err := harness.session.Prompt(context.Background(), strings.Repeat("old input ", 500), nil); err != nil {
		t.Fatal(err)
	}

	if len(harness.eventsOfType(AgentSessionEventCompactionStart)) == 0 {
		t.Fatal("expected threshold compaction to run")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	if !strings.Contains(requests[0], "EXACT-UNSENT-INSTRUCTION") {
		t.Fatal("boundary custom message lost through compaction")
	}
}

func TestBoundaryRefreshesContextBeforeEntryNotifications(t *testing.T) {
	var handled bool
	var snapshots []string
	var mu sync.Mutex
	handler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		if _, ok := event.(*TurnEndEvent); !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		mu.Lock()
		alreadyHandled := handled
		handled = true
		mu.Unlock()
		if alreadyHandled {
			return nil, nil
		}
		return &BoundaryResult{
			Entries: []SessionBoundaryDraft{
				&CustomEntryDraft{Type: "custom", CustomType: "metadata", Data: true},
				&CustomMessageEntryDraft{Type: "custom_message", CustomType: "visible-context", Content: "committed context", Display: true},
			},
		}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{{Path: "boundary-ext", Handlers: map[string][]ExtensionHandler{"turn_end": {handler}}}},
	})
	harness.session.Subscribe(func(event *AgentSessionEvent) {
		if event.Type == AgentSessionEventEntryAppended {
			encoded, _ := json.Marshal(harness.session.Messages())
			mu.Lock()
			snapshots = append(snapshots, string(encoded))
			mu.Unlock()
		}
	})
	harness.setResponses(fauxAssistantMessage("done"))

	if err := harness.session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(snapshots) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(snapshots))
	}
	for _, snapshot := range snapshots {
		if !strings.Contains(snapshot, "committed context") {
			t.Fatal("entry notification published before the context refresh saw the draft")
		}
	}
}

func TestBoundaryContinuesFromBeforeSettleCustomMessage(t *testing.T) {
	var requested bool
	var requests []string
	var mu sync.Mutex
	handler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		if _, ok := event.(*AgentBeforeSettleEvent); !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		mu.Lock()
		alreadyRequested := requested
		requested = true
		mu.Unlock()
		if alreadyRequested {
			return nil, nil
		}
		return &BoundaryResult{
			Entries: []SessionBoundaryDraft{
				&CustomMessageEntryDraft{Type: "custom_message", CustomType: "test-continuation", Content: "continue now"},
			},
			Continue: lo.ToPtr(true),
		}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{{Path: "settle-ext", Handlers: map[string][]ExtensionHandler{"agent_before_settle": {handler}}}},
	})
	harness.setResponses(
		fauxAssistantMessage("first"),
		func(llmContext *types.TranscriptContext) *types.AssistantMessage {
			encoded, _ := json.Marshal(llmContext.Messages)
			mu.Lock()
			requests = append(requests, string(encoded))
			mu.Unlock()
			return fauxAssistantMessage("second")(llmContext)
		},
	)

	if err := harness.session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) == 0 || !strings.Contains(requests[0], "continue now") {
		t.Fatal("continuation request missing the before-settle custom message")
	}
	found := false
	for _, entry := range harness.sessionManager.GetEntries() {
		if typed, ok := entry.(*CustomMessageEntry); ok && typed.CustomType == "test-continuation" && !typed.Display {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the persisted custom_message entry")
	}
	if len(harness.eventsOfType(agent.AgentEventAgentStart)) != 2 {
		t.Fatalf("agent_start events = %d, want 2", len(harness.eventsOfType(agent.AgentEventAgentStart)))
	}
	if len(harness.eventsOfType(AgentSessionEventAgentSettled)) != 1 {
		t.Fatal("expected exactly one agent_settled")
	}
}

func TestBoundaryPersistsAgentEndQueuedContextBeforePresettleContinuation(t *testing.T) {
	firstRun := true
	continued := false
	var requests []string
	var mu sync.Mutex
	agentEndHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		mu.Lock()
		isFirst := firstRun
		firstRun = false
		mu.Unlock()
		if !isFirst {
			return nil, nil
		}
		extCtx.SendMessage(ctx, &CustomMessagePayload{
			CustomType: "agent-end-context",
			Content:    "queued after agent end",
			Display:    false,
		}, &SendMessageOptions{TriggerTurn: lo.ToPtr(false)})
		return nil, nil
	}
	settleHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		beforeSettle, ok := event.(*AgentBeforeSettleEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		mu.Lock()
		alreadyContinued := continued
		continued = true
		mu.Unlock()
		if alreadyContinued {
			return nil, nil
		}
		pendingEncoded, _ := json.Marshal(beforeSettle.Context.PendingMessages)
		if !strings.Contains(string(pendingEncoded), "queued after agent end") {
			return nil, fmt.Errorf("pendingMessages missing the agent_end custom message: %s", pendingEncoded)
		}
		contextEncoded, _ := json.Marshal(beforeSettle.Context.ContextMessages)
		if strings.Contains(string(contextEncoded), "queued after agent end") {
			return nil, fmt.Errorf("contextMessages already contains the queued message: %s", contextEncoded)
		}
		return &BoundaryResult{Continue: lo.ToPtr(true)}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{{Path: "settle-ext", Handlers: map[string][]ExtensionHandler{
			"agent_end":           {agentEndHandler},
			"agent_before_settle": {settleHandler},
		}}},
	})
	harness.setResponses(
		fauxAssistantMessage("first"),
		func(llmContext *types.TranscriptContext) *types.AssistantMessage {
			encoded, _ := json.Marshal(llmContext.Messages)
			mu.Lock()
			requests = append(requests, string(encoded))
			mu.Unlock()
			return fauxAssistantMessage("second")(llmContext)
		},
	)

	if err := harness.session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) == 0 || !strings.Contains(requests[0], "queued after agent end") {
		t.Fatal("continuation request missing the agent_end queued context")
	}
	found := false
	for _, entry := range harness.sessionManager.GetEntries() {
		if typed, ok := entry.(*CustomMessageEntry); ok && typed.CustomType == "agent-end-context" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the persisted agent-end custom_message entry")
	}
}

func TestBoundaryDefersFollowUpUntilExplicitContinuationStops(t *testing.T) {
	var handled bool
	var requests []string
	var mu sync.Mutex
	handler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		if _, ok := event.(*AgentBeforeSettleEvent); !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		mu.Lock()
		alreadyHandled := handled
		handled = true
		mu.Unlock()
		if alreadyHandled {
			return nil, nil
		}
		extCtx.SendUserMessage(ctx, "queued follow-up", &SendUserMessageOptions{DeliverAs: "followUp"})
		return &BoundaryResult{
			Entries: []SessionBoundaryDraft{
				&CustomMessageEntryDraft{Type: "custom_message", CustomType: "boundary", Content: "boundary context"},
			},
			Continue: lo.ToPtr(true),
		}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{{Path: "settle-ext", Handlers: map[string][]ExtensionHandler{"agent_before_settle": {handler}}}},
	})
	harness.setResponses(
		fauxAssistantMessage("first"),
		func(llmContext *types.TranscriptContext) *types.AssistantMessage {
			encoded, _ := json.Marshal(llmContext.Messages)
			mu.Lock()
			requests = append(requests, string(encoded))
			mu.Unlock()
			return fauxAssistantMessage("second")(llmContext)
		},
		func(llmContext *types.TranscriptContext) *types.AssistantMessage {
			encoded, _ := json.Marshal(llmContext.Messages)
			mu.Lock()
			requests = append(requests, string(encoded))
			mu.Unlock()
			return fauxAssistantMessage("follow-up response")(llmContext)
		},
	)

	if err := harness.session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	if harness.callCountRead() != 3 {
		t.Fatalf("callCount = %d, want 3", harness.callCountRead())
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(requests[0], "boundary context") || strings.Contains(requests[0], "queued follow-up") {
		t.Fatal("first continuation must not deliver the follow-up")
	}
	if !strings.Contains(requests[1], "queued follow-up") {
		t.Fatal("second request must deliver the follow-up")
	}
}

func TestBoundaryDefersRunsStartedByAgentSettledHandlers(t *testing.T) {
	triggered := false
	var lifecycle []string
	var mu sync.Mutex
	startHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		mu.Lock()
		lifecycle = append(lifecycle, "start")
		mu.Unlock()
		return nil, nil
	}
	firstSettled := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		mu.Lock()
		alreadyTriggered := triggered
		triggered = true
		lifecycle = append(lifecycle, fmt.Sprintf("settled-first:%v", extCtx.IsIdle()))
		mu.Unlock()
		if alreadyTriggered {
			return nil, nil
		}
		extCtx.SendMessage(ctx, &CustomMessagePayload{
			CustomType: "settled-trigger",
			Content:    "start later",
			Display:    false,
		}, &SendMessageOptions{TriggerTurn: lo.ToPtr(true)})
		return nil, nil
	}
	secondSettled := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		mu.Lock()
		lifecycle = append(lifecycle, fmt.Sprintf("settled-second:%v", extCtx.IsIdle()))
		mu.Unlock()
		return nil, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{{Path: "settle-ext", Handlers: map[string][]ExtensionHandler{
			"agent_start":   {startHandler},
			"agent_settled": {firstSettled, secondSettled},
		}}},
	})
	harness.setResponses(fauxAssistantMessage("first"), fauxAssistantMessage("second"))

	if err := harness.session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"start",
		"settled-first:true",
		"settled-second:true",
		"start",
		"settled-first:true",
		"settled-second:true",
	}
	if strings.Join(lifecycle, "|") != strings.Join(want, "|") {
		t.Fatalf("lifecycle = %v", lifecycle)
	}
}

func TestBoundaryInvalidContinuationDoesNotSuppressNaturalToolContinuation(t *testing.T) {
	tool := agent.AgentTool{
		Tool: types.Tool{
			Name:        "noop",
			Description: "Noop",
			Parameters:  rawJSON(`{"type":"object","properties":{}}`),
		},
		Label: "Noop",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{Content: []types.Content{types.NewTextContent("done")}, Details: map[string]any{}}, nil
		},
	}
	handler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		turnEnd, ok := event.(*TurnEndEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		var userEntry *SessionMessageEntry
		branch := extCtx.SessionManager.GetBranch("")
		for i := len(branch) - 1; i >= 0; i-- {
			if typed, ok := branch[i].(*SessionMessageEntry); ok && typed.Message.MessageRole() == types.RoleUser {
				userEntry = typed
				break
			}
		}
		if userEntry == nil {
			return nil, fmt.Errorf("missing user entry")
		}
		targets := []string{userEntry.ID, turnEnd.MessageEntryID}
		targets = append(targets, turnEnd.ToolResultEntryIDs...)
		drafts := make([]SessionBoundaryDraft, 0, len(targets))
		for _, targetID := range targets {
			drafts = append(drafts, &ContextEditEntryDraft{Type: "context_edit", TargetID: targetID})
		}
		return &BoundaryResult{Entries: drafts, Continue: lo.ToPtr(true)}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		tools: []agent.AgentTool{tool},
		extensions: []*Extension{{Path: "boundary-ext", Handlers: map[string][]ExtensionHandler{
			"turn_end": {handler},
		}}},
	})
	harness.setResponses(
		fauxAssistantMessage([]types.Content{fauxToolCall("noop", map[string]any{})}, withStopReason(types.StopReasonToolUse)),
		fauxAssistantMessage("must not run"),
	)

	if err := harness.session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	if harness.callCountRead() != 2 {
		t.Fatalf("callCount = %d, want 2 (natural tool continuation)", harness.callCountRead())
	}
}

func TestBoundaryDispatchesTurnEndForSyntheticRunFailures(t *testing.T) {
	turnEnds := 0
	var mu sync.Mutex
	handler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		turnEnd, ok := event.(*TurnEndEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		mu.Lock()
		turnEnds++
		outcome := turnEnd.Outcome
		mu.Unlock()
		if outcome != AgentActivityError {
			return nil, fmt.Errorf("outcome = %q, want error", outcome)
		}
		return &BoundaryResult{Entries: []SessionBoundaryDraft{&CustomEntryDraft{Type: "custom", CustomType: "failure-boundary", Data: true}}}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{{Path: "boundary-ext", Handlers: map[string][]ExtensionHandler{"turn_end": {handler}}}},
	})
	harness.session.Agent.PrepareRequest = func(ctx context.Context, request agent.PrepareRequestContext) *agent.AgentRequestUpdate {
		panic("request preparation failed")
	}
	harness.setResponses(fauxAssistantMessage("unreachable"))

	if err := harness.session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if turnEnds != 1 {
		t.Fatalf("turnEnds = %d, want 1", turnEnds)
	}
	if harness.callCountRead() != 0 {
		t.Fatalf("callCount = %d, want 0", harness.callCountRead())
	}
	found := false
	for _, entry := range harness.sessionManager.GetEntries() {
		if typed, ok := entry.(*CustomEntry); ok && typed.CustomType == "failure-boundary" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the failure-boundary custom entry")
	}
}

func TestBoundaryNoCompactionFromOmittedAssistantUsage(t *testing.T) {
	var handled bool
	var mu sync.Mutex
	messageEndHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		messageEnd, ok := event.(*MessageEndEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		assistant, isAssistant := messageEnd.Message.(types.AssistantMessage)
		if !isAssistant {
			return nil, nil
		}
		replacement := assistant
		replacement.Usage.Input = 9800
		replacement.Usage.Output = 1
		replacement.Usage.TotalTokens = 9801
		return &MessageEndEventResult{Message: replacement}, nil
	}
	turnEndHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		typedEvent, ok := event.(*TurnEndEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		mu.Lock()
		alreadyHandled := handled
		handled = true
		mu.Unlock()
		if alreadyHandled {
			return nil, nil
		}
		return &BoundaryResult{
			Entries: []SessionBoundaryDraft{&ContextEditEntryDraft{Type: "context_edit", TargetID: typedEvent.MessageEntryID}},
		}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		contextWindow: 10000,
		maxTokens:     100,
		settings:      Settings{"compaction": map[string]any{"enabled": true, "keepRecentTokens": float64(1), "reserveTokens": float64(300)}},
		extensions: []*Extension{{Path: "boundary-ext", Handlers: map[string][]ExtensionHandler{
			"message_end": {messageEndHandler},
			"turn_end":    {turnEndHandler},
		}}},
	})
	harness.setResponses(fauxAssistantMessage("short response"))

	if err := harness.session.Prompt(context.Background(), "small prompt", nil); err != nil {
		t.Fatal(err)
	}

	if len(harness.eventsOfType(AgentSessionEventCompactionStart)) != 0 {
		t.Fatal("usage from an omitted assistant must not trigger compaction")
	}
	if usage := harness.session.GetContextUsage(); usage == nil || usage.Tokens == nil || *usage.Tokens >= 2000 {
		t.Fatalf("context usage = %+v, want < 2000", usage)
	}
}

func TestBoundaryNoOverflowFromUsageBeforeBoundaryEdit(t *testing.T) {
	var handled bool
	var mu sync.Mutex
	messageEndHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		messageEnd, ok := event.(*MessageEndEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		assistant, isAssistant := messageEnd.Message.(types.AssistantMessage)
		if !isAssistant {
			return nil, nil
		}
		replacement := assistant
		replacement.Usage.Input = 5100
		replacement.Usage.Output = 1
		replacement.Usage.TotalTokens = 5101
		return &MessageEndEventResult{Message: replacement}, nil
	}
	turnEndHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		if _, ok := event.(*TurnEndEvent); !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		mu.Lock()
		alreadyHandled := handled
		handled = true
		mu.Unlock()
		if alreadyHandled {
			return nil, nil
		}
		var userEntry *SessionMessageEntry
		branch := extCtx.SessionManager.GetBranch("")
		for i := len(branch) - 1; i >= 0; i-- {
			if typed, ok := branch[i].(*SessionMessageEntry); ok && typed.Message.MessageRole() == types.RoleUser {
				userEntry = typed
				break
			}
		}
		if userEntry == nil {
			return nil, fmt.Errorf("missing user entry")
		}
		return &BoundaryResult{
			Entries: []SessionBoundaryDraft{&ContextEditEntryDraft{Type: "context_edit", TargetID: userEntry.ID}},
		}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		contextWindow: 5000,
		maxTokens:     100,
		settings:      Settings{"compaction": map[string]any{"enabled": true, "keepRecentTokens": float64(1), "reserveTokens": float64(0)}},
		extensions: []*Extension{{Path: "boundary-ext", Handlers: map[string][]ExtensionHandler{
			"message_end": {messageEndHandler},
			"turn_end":    {turnEndHandler},
		}}},
	})
	harness.setResponses(fauxAssistantMessage("done"))

	if err := harness.session.Prompt(context.Background(), "large input that is later omitted", nil); err != nil {
		t.Fatal(err)
	}

	if len(harness.eventsOfType(AgentSessionEventCompactionStart)) != 0 {
		t.Fatal("usage captured before a boundary edit must not trigger overflow compaction")
	}
	if usage := harness.session.GetContextUsage(); usage == nil || usage.Tokens == nil || *usage.Tokens >= 2000 {
		t.Fatalf("context usage = %+v, want < 2000", usage)
	}
}

func TestBoundaryNoThresholdCompactionFromPostEditUsageBeforeCompaction(t *testing.T) {
	harness := createFauxHarness(t, &fauxHarnessOptions{
		contextWindow: 10000,
		maxTokens:     100,
		settings:      Settings{"compaction": map[string]any{"enabled": true, "keepRecentTokens": float64(1), "reserveTokens": float64(0)}},
	})
	userID, err := harness.sessionManager.AppendMessage(types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{String: "small input"},
		Timestamp: time.Now().UnixMilli() - 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.sessionManager.AppendContextEdit(userID, &ContextEditReplacement{Content: "edited input"}); err != nil {
		t.Fatal(err)
	}
	response := fauxAssistantMessage("answer")(nil)
	response.Timestamp = time.Now().UnixMilli() - 2
	response.Usage = types.Usage{Input: 50000, Output: 1, TotalTokens: 50001}
	if _, err := harness.sessionManager.AppendMessage(*response); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.sessionManager.AppendCompaction("small summary", &userID, 50001, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	harness.session.RefreshContext()

	runAutoCompactionCalled := false
	previous := harness.session.runAutoCompactionFn
	harness.session.runAutoCompactionFn = func(ctx context.Context, reason string, willRetry bool) (bool, error) {
		runAutoCompactionCalled = true
		return previous(ctx, reason, willRetry)
	}
	errorMessage := fauxAssistantMessage("")(nil)
	errorMessage.StopReason = types.StopReasonError
	errorMessage.ErrorMessage = "invalid_api_key"
	errorMessage.Timestamp = time.Now().UnixMilli() + 1000

	if _, err := harness.session.checkCompaction(context.Background(), errorMessage, true, nil); err != nil {
		t.Fatal(err)
	}
	if runAutoCompactionCalled {
		t.Fatal("stale pre-edit usage must not trigger compaction")
	}
}

func TestBoundaryRetainedPreCompactionUsageIsNotPostCompactionUsage(t *testing.T) {
	harness := createFauxHarness(t, nil)
	retained := fauxAssistantMessage("retained")(nil)
	retained.Usage.Input = 10000
	retained.Usage.TotalTokens = 10001
	retainedID, err := harness.sessionManager.AppendMessage(*retained)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.sessionManager.AppendCompaction("summary", &retainedID, 10001, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	harness.session.RefreshContext()

	usage := harness.session.GetContextUsage()
	if usage == nil || usage.Tokens != nil {
		t.Fatalf("context usage = %+v, want nil tokens", usage)
	}
}

func TestBoundaryPersistsCustomContextSentDuringPresettle(t *testing.T) {
	var handled bool
	var requests []string
	var mu sync.Mutex
	handler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		if _, ok := event.(*AgentBeforeSettleEvent); !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		mu.Lock()
		alreadyHandled := handled
		handled = true
		mu.Unlock()
		if alreadyHandled {
			return nil, nil
		}
		extCtx.SendMessage(ctx, &CustomMessagePayload{
			CustomType: "pending-boundary",
			Content:    "persist before continue",
			Display:    false,
		}, &SendMessageOptions{TriggerTurn: lo.ToPtr(false)})
		return &BoundaryResult{Continue: lo.ToPtr(true)}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{{Path: "settle-ext", Handlers: map[string][]ExtensionHandler{"agent_before_settle": {handler}}}},
	})
	harness.setResponses(
		fauxAssistantMessage("first"),
		func(llmContext *types.TranscriptContext) *types.AssistantMessage {
			encoded, _ := json.Marshal(llmContext.Messages)
			mu.Lock()
			requests = append(requests, string(encoded))
			mu.Unlock()
			return fauxAssistantMessage("second")(llmContext)
		},
	)

	if err := harness.session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	if harness.callCountRead() != 2 {
		t.Fatalf("callCount = %d, want 2", harness.callCountRead())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) == 0 || !strings.Contains(requests[0], "persist before continue") {
		t.Fatal("continuation request missing the pre-settlement custom context")
	}
	found := false
	for _, entry := range harness.sessionManager.GetEntries() {
		if typed, ok := entry.(*CustomMessageEntry); ok && typed.CustomType == "pending-boundary" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the persisted pending-boundary entry")
	}
}

func TestBoundaryDoesNotConsumeQueuedInputOnSystemOnlyContext(t *testing.T) {
	var handled bool
	var mu sync.Mutex
	handler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		if _, ok := event.(*AgentBeforeSettleEvent); !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		mu.Lock()
		alreadyHandled := handled
		handled = true
		mu.Unlock()
		if alreadyHandled {
			return nil, nil
		}
		extCtx.SendUserMessage(ctx, "still queued", &SendUserMessageOptions{DeliverAs: "followUp"})
		var targets []string
		for _, entry := range extCtx.SessionManager.GetBranch("") {
			if typed, ok := entry.(*SessionMessageEntry); ok {
				role := typed.Message.MessageRole()
				if role == types.RoleUser || role == types.RoleAssistant || role == types.RoleToolResult {
					targets = append(targets, typed.ID)
				}
			}
		}
		drafts := make([]SessionBoundaryDraft, 0, len(targets))
		for _, targetID := range targets {
			drafts = append(drafts, &ContextEditEntryDraft{Type: "context_edit", TargetID: targetID})
		}
		return &BoundaryResult{Entries: drafts, Continue: lo.ToPtr(true)}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{{Path: "settle-ext", Handlers: map[string][]ExtensionHandler{"agent_before_settle": {handler}}}},
	})
	harness.setResponses(fauxAssistantMessage("first"), fauxAssistantMessage("must not run"))

	if err := harness.session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	if harness.callCountRead() != 1 {
		t.Fatalf("callCount = %d, want 1", harness.callCountRead())
	}
	if harness.session.PendingMessageCount() != 1 {
		t.Fatalf("pendingMessageCount = %d, want 1", harness.session.PendingMessageCount())
	}
}

func TestBoundaryCommitsDraftsButSuppressesContinuationWhenAborted(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var closeOnce sync.Once
	handler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		if _, ok := event.(*AgentBeforeSettleEvent); !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		closeOnce.Do(func() { close(started) })
		<-release
		return &BoundaryResult{
			Entries:  []SessionBoundaryDraft{&CustomEntryDraft{Type: "custom", CustomType: "committed-after-abort", Data: true}},
			Continue: lo.ToPtr(true),
		}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{{Path: "settle-ext", Handlers: map[string][]ExtensionHandler{"agent_before_settle": {handler}}}},
	})
	harness.setResponses(fauxAssistantMessage("first"), fauxAssistantMessage("must not run"))

	promptErr := make(chan error, 1)
	go func() {
		promptErr <- harness.session.Prompt(context.Background(), "start", nil)
	}()
	<-started
	abortErr := make(chan error, 1)
	go func() {
		abortErr <- harness.session.Abort(context.Background())
	}()
	close(release)
	if err := <-promptErr; err != nil {
		t.Fatalf("prompt error: %v", err)
	}
	if err := <-abortErr; err != nil {
		t.Fatalf("abort error: %v", err)
	}

	if harness.callCountRead() != 1 {
		t.Fatalf("callCount = %d, want 1", harness.callCountRead())
	}
	found := false
	for _, entry := range harness.sessionManager.GetEntries() {
		if typed, ok := entry.(*CustomEntry); ok && typed.CustomType == "committed-after-abort" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the draft committed during the abort to persist")
	}
	if len(harness.eventsOfType(AgentSessionEventAgentSettled)) != 1 {
		t.Fatal("expected exactly one agent_settled")
	}
}

// ----------------------------------------------------------------------------
// durable length recovery
// ----------------------------------------------------------------------------

func TestLengthRecoveryKeepsTruncatedToolAttemptsInContext(t *testing.T) {
	var executed bool
	var requests []string
	var mu sync.Mutex
	tool := agent.AgentTool{
		Tool: types.Tool{
			Name:        "unsafe_truncated_tool",
			Description: "Must not execute from a length response",
			Parameters:  rawJSON(`{"type":"object","properties":{"value":{"type":"string"}}}`),
		},
		Label: "Unsafe truncated tool",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			mu.Lock()
			executed = true
			mu.Unlock()
			return agent.AgentToolResult{Content: []types.Content{types.NewTextContent("executed")}, Details: map[string]any{}}, nil
		},
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{tools: []agent.AgentTool{tool}})
	harness.setResponses(
		fauxAssistantMessage([]types.Content{fauxToolCall("unsafe_truncated_tool", map[string]any{"value": "partial"})}, withStopReason(types.StopReasonLength)),
		func(llmContext *types.TranscriptContext) *types.AssistantMessage {
			encoded, _ := json.Marshal(llmContext.Messages)
			mu.Lock()
			requests = append(requests, string(encoded))
			mu.Unlock()
			return fauxAssistantMessage("completed natural continuation")(llmContext)
		},
	)

	if err := harness.session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if executed {
		t.Fatal("truncated tool call must not execute")
	}
	if harness.callCountRead() != 2 {
		t.Fatalf("callCount = %d, want 2", harness.callCountRead())
	}
	if len(requests) == 0 || !strings.Contains(requests[0], "may be truncated") {
		t.Fatal("the failed tool result must explain the truncation")
	}
	for _, entry := range harness.sessionManager.GetEntries() {
		if _, ok := entry.(*ContextEditEntry); ok {
			t.Fatal("length recovery via natural continuation must not write context edits")
		}
	}
}

func TestLengthRecoveryResetsAfterSuccessfulIntermediateTurn(t *testing.T) {
	tool := agent.AgentTool{
		Tool:  types.Tool{Name: "noop", Description: "Noop", Parameters: rawJSON(`{"type":"object","properties":{}}`)},
		Label: "Noop",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{Content: []types.Content{types.NewTextContent("done")}, Details: map[string]any{}}, nil
		},
	}
	compactionHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		beforeCompact, ok := event.(*SessionBeforeCompactEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		return &SessionBeforeCompactResult{Compaction: &CompactionResult{
			Summary:          "recovered input",
			FirstKeptEntryID: beforeCompact.Preparation.FirstKeptEntryID,
			TokensBefore:     beforeCompact.Preparation.TokensBefore,
		}}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		contextWindow: 1000,
		maxTokens:     100,
		settings:      Settings{"compaction": map[string]any{"keepRecentTokens": float64(1), "reserveTokens": float64(0)}},
		tools:         []agent.AgentTool{tool},
		extensions:    []*Extension{{Path: "compact-ext", Handlers: map[string][]ExtensionHandler{"session_before_compact": {compactionHandler}}}},
	})
	now := time.Now().UnixMilli()
	harness.setResponses(
		fauxAssistantMessage("first partial", withStopReason(types.StopReasonLength)),
		fauxAssistantMessage([]types.Content{fauxToolCall("noop", map[string]any{})}, withStopReason(types.StopReasonToolUse)),
		func(llmContext *types.TranscriptContext) *types.AssistantMessage {
			return fauxAssistantMessage("second partial", withStopReason(types.StopReasonLength), fauxWithTimestamp(now+1000))(llmContext)
		},
		func(llmContext *types.TranscriptContext) *types.AssistantMessage {
			return fauxAssistantMessage("completed second recovery", fauxWithTimestamp(now+2000))(llmContext)
		},
	)

	if err := harness.session.Prompt(context.Background(), strings.Repeat("x", 5000), nil); err != nil {
		t.Fatal(err)
	}

	var omittedIDs []string
	var lengthIDs []string
	for _, entry := range harness.sessionManager.GetEntries() {
		switch typed := entry.(type) {
		case *ContextEditEntry:
			omittedIDs = append(omittedIDs, typed.TargetID)
		case *SessionMessageEntry:
			if assistant, ok := typed.Message.(types.AssistantMessage); ok && assistant.StopReason == types.StopReasonLength {
				lengthIDs = append(lengthIDs, typed.ID)
			}
		}
	}
	if len(lengthIDs) != 2 {
		t.Fatalf("length responses = %d, want 2", len(lengthIDs))
	}
	for _, id := range lengthIDs {
		if !containsString(omittedIDs, id) {
			t.Fatalf("length response %s not omitted", id)
		}
	}
	if harness.callCountRead() != 3 {
		t.Fatalf("callCount = %d, want 3", harness.callCountRead())
	}
}

func TestLengthRecoveryDistinctFollowUpGetsOwnBudget(t *testing.T) {
	queued := false
	var mu sync.Mutex
	compactionHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		beforeCompact, ok := event.(*SessionBeforeCompactEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		return &SessionBeforeCompactResult{Compaction: &CompactionResult{
			Summary:          "recovered input",
			FirstKeptEntryID: beforeCompact.Preparation.FirstKeptEntryID,
			TokensBefore:     beforeCompact.Preparation.TokensBefore,
		}}, nil
	}
	agentEndHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		agentEnd, ok := event.(*AgentEndEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		for _, message := range agentEnd.Messages {
			if assistant, ok := message.(types.AssistantMessage); ok && strings.Contains(agentMessageText(assistant), "first recovered") {
				mu.Lock()
				alreadyQueued := queued
				queued = true
				mu.Unlock()
				if !alreadyQueued {
					extCtx.SendUserMessage(ctx, "distinct follow-up", &SendUserMessageOptions{DeliverAs: "followUp"})
				}
			}
		}
		return nil, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		contextWindow: 1000,
		maxTokens:     100,
		settings:      Settings{"compaction": map[string]any{"keepRecentTokens": float64(1), "reserveTokens": float64(0)}},
		extensions: []*Extension{{Path: "compact-ext", Handlers: map[string][]ExtensionHandler{
			"session_before_compact": {compactionHandler},
			"agent_end":              {agentEndHandler},
		}}},
	})
	now := time.Now().UnixMilli()
	harness.setResponses(
		fauxAssistantMessage("first partial", withStopReason(types.StopReasonLength)),
		fauxAssistantMessage("first recovered"),
		func(llmContext *types.TranscriptContext) *types.AssistantMessage {
			return fauxAssistantMessage("follow-up partial", withStopReason(types.StopReasonLength), fauxWithTimestamp(now+1000))(llmContext)
		},
		func(llmContext *types.TranscriptContext) *types.AssistantMessage {
			return fauxAssistantMessage("follow-up recovered", fauxWithTimestamp(now+2000))(llmContext)
		},
	)

	if err := harness.session.Prompt(context.Background(), strings.Repeat("x", 5000), nil); err != nil {
		t.Fatal(err)
	}

	var omittedIDs []string
	var lengthIDs []string
	for _, entry := range harness.sessionManager.GetEntries() {
		switch typed := entry.(type) {
		case *ContextEditEntry:
			omittedIDs = append(omittedIDs, typed.TargetID)
		case *SessionMessageEntry:
			if assistant, ok := typed.Message.(types.AssistantMessage); ok && assistant.StopReason == types.StopReasonLength {
				lengthIDs = append(lengthIDs, typed.ID)
			}
		}
	}
	if len(lengthIDs) != 2 {
		t.Fatalf("length responses = %d, want 2", len(lengthIDs))
	}
	for _, id := range lengthIDs {
		if !containsString(omittedIDs, id) {
			t.Fatalf("length response %s not omitted", id)
		}
	}
	if harness.callCountRead() != 4 {
		t.Fatalf("callCount = %d, want 4", harness.callCountRead())
	}
}

func TestLengthRecoveryFinishRetryBookkeepingOnNonretryableError(t *testing.T) {
	harness := createFauxHarness(t, &fauxHarnessOptions{
		settings: Settings{"retry": map[string]any{"enabled": true, "maxRetries": float64(2), "baseDelayMs": float64(1)}},
	})
	harness.setResponses(
		fauxAssistantMessage("", withStopReason(types.StopReasonError), withErrorMessage("overloaded_error")),
		fauxAssistantMessage("", withStopReason(types.StopReasonError), withErrorMessage("invalid_api_key")),
	)

	if err := harness.session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	if harness.callCountRead() != 2 {
		t.Fatalf("callCount = %d, want 2", harness.callCountRead())
	}
	found := false
	for _, event := range harness.eventsOfType(AgentSessionEventAutoRetryEnd) {
		if !event.Success && event.Attempt == 1 && event.FinalError == "invalid_api_key" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected auto_retry_end(success=false, attempt=1, invalid_api_key)")
	}
}

func TestLengthRecoveryOmitsRecoverableProjectedReplacementBySourceID(t *testing.T) {
	compactionHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		return &SessionBeforeCompactResult{Cancel: true}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		contextWindow: 1000,
		maxTokens:     100,
		settings:      Settings{"compaction": map[string]any{"enabled": true, "keepRecentTokens": float64(1), "reserveTokens": float64(0)}},
		extensions:    []*Extension{{Path: "compact-ext", Handlers: map[string][]ExtensionHandler{"session_before_compact": {compactionHandler}}}},
	})
	if _, err := harness.sessionManager.AppendMessage(types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{String: strings.Repeat("x", 5000)},
		Timestamp: time.Now().UnixMilli() - 2,
	}); err != nil {
		t.Fatal(err)
	}
	partial := fauxAssistantMessage("original partial", withStopReason(types.StopReasonLength))(nil)
	partial.Timestamp = time.Now().UnixMilli() - 1
	partial.API = harness.model.API
	partial.Provider = harness.model.Provider
	partial.Model = harness.model.ID
	partialID, err := harness.sessionManager.AppendMessage(*partial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.sessionManager.AppendContextEdit(partialID, &ContextEditReplacement{
		Content: []types.Content{types.NewTextContent("edited partial")},
	}); err != nil {
		t.Fatal(err)
	}
	harness.session.RefreshContext()
	harness.setResponses(fauxAssistantMessage("new answer"))

	if err := harness.session.Prompt(context.Background(), "next prompt", nil); err != nil {
		t.Fatal(err)
	}

	var lastReplacement *ContextEditReplacement
	for _, entry := range harness.sessionManager.GetEntries() {
		if typed, ok := entry.(*ContextEditEntry); ok && typed.TargetID == partialID {
			lastReplacement = typed.Replacement
		}
	}
	if lastReplacement != nil {
		// pi: the last edit for the partial is the recovery omission (null
		// replacement), replacing the earlier boundary replacement.
		t.Fatal("expected the recovery omission to replace the earlier boundary replacement")
	}
	projection := harness.sessionManager.BuildSessionProjection()
	for _, message := range projection.Messages {
		if strings.Contains(fauxMessageToText(fauxAsTypesMessage(message)), "edited partial") {
			t.Fatal("the replaced content must be omitted from the projection")
		}
	}
}

func TestLengthRecoveryRecoversExplicitOverflowAfterRetainedReplacement(t *testing.T) {
	replaced := false
	var overflowID string
	var mu sync.Mutex
	compactionHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		beforeCompact, ok := event.(*SessionBeforeCompactEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		return &SessionBeforeCompactResult{Compaction: &CompactionResult{
			Summary:          "recovered overflow",
			FirstKeptEntryID: beforeCompact.Preparation.FirstKeptEntryID,
			TokensBefore:     beforeCompact.Preparation.TokensBefore,
		}}, nil
	}
	turnEndHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		turnEnd, ok := event.(*TurnEndEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		mu.Lock()
		alreadyReplaced := replaced || turnEnd.Outcome != AgentActivityError
		replaced = true
		overflowID = turnEnd.MessageEntryID
		mu.Unlock()
		if alreadyReplaced {
			return nil, nil
		}
		return &BoundaryResult{
			Entries: []SessionBoundaryDraft{&ContextEditEntryDraft{
				Type:        "context_edit",
				TargetID:    turnEnd.MessageEntryID,
				Replacement: &ContextEditReplacement{Content: []types.Content{types.NewTextContent("retained error")}},
			}},
		}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		contextWindow: 1000,
		maxTokens:     100,
		settings:      Settings{"compaction": map[string]any{"enabled": true, "keepRecentTokens": float64(1), "reserveTokens": float64(0)}},
		extensions: []*Extension{{Path: "boundary-ext", Handlers: map[string][]ExtensionHandler{
			"turn_end":               {turnEndHandler},
			"session_before_compact": {compactionHandler},
		}}},
	})
	harness.setResponses(
		fauxAssistantMessage("retained error", withStopReason(types.StopReasonError), withErrorMessage("prompt is too long")),
		fauxAssistantMessage("recovered"),
	)

	if err := harness.session.Prompt(context.Background(), strings.Repeat("x", 5000), nil); err != nil {
		t.Fatal(err)
	}

	if harness.callCountRead() != 2 {
		t.Fatalf("callCount = %d, want 2", harness.callCountRead())
	}
	mu.Lock()
	defer mu.Unlock()
	if overflowID == "" {
		t.Fatal("expected the error turn's boundary dispatch")
	}
	var lastReplacement *ContextEditReplacement
	for _, entry := range harness.sessionManager.GetEntries() {
		if typed, ok := entry.(*ContextEditEntry); ok && typed.TargetID == overflowID {
			lastReplacement = typed.Replacement
		}
	}
	if lastReplacement != nil {
		t.Fatal("the retained error replacement must be followed by an omission")
	}
}

func TestLengthRecoveryKeepsFollowUpBehindAutomaticErrorRetry(t *testing.T) {
	queued := false
	var requests []string
	var lifecycle []string
	var mu sync.Mutex
	turnEndHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		turnEnd, ok := event.(*TurnEndEvent)
		if !ok {
			return nil, fmt.Errorf("unexpected event %T", event)
		}
		mu.Lock()
		shouldQueue := !queued && turnEnd.Outcome == AgentActivityError
		queued = true
		mu.Unlock()
		if !shouldQueue {
			return nil, nil
		}
		extCtx.SendUserMessage(ctx, "queued follow-up", &SendUserMessageOptions{DeliverAs: "followUp"})
		return nil, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		settings: Settings{"retry": map[string]any{"enabled": true, "maxRetries": float64(2), "baseDelayMs": float64(1)}},
		extensions: []*Extension{{Path: "boundary-ext", Handlers: map[string][]ExtensionHandler{
			"turn_end": {turnEndHandler},
		}}},
	})
	harness.session.Subscribe(func(event *AgentSessionEvent) {
		if event.Type == agent.AgentEventAgentEnd || event.Type == AgentSessionEventAutoRetryStart {
			mu.Lock()
			lifecycle = append(lifecycle, string(event.Type))
			mu.Unlock()
		}
	})
	harness.setResponses(
		fauxAssistantMessage("", withStopReason(types.StopReasonError), withErrorMessage("overloaded_error")),
		func(llmContext *types.TranscriptContext) *types.AssistantMessage {
			encoded, _ := json.Marshal(llmContext.Messages)
			mu.Lock()
			requests = append(requests, string(encoded))
			mu.Unlock()
			return fauxAssistantMessage("retry recovered")(llmContext)
		},
		func(llmContext *types.TranscriptContext) *types.AssistantMessage {
			encoded, _ := json.Marshal(llmContext.Messages)
			mu.Lock()
			requests = append(requests, string(encoded))
			mu.Unlock()
			return fauxAssistantMessage("follow-up completed")(llmContext)
		},
	)

	if err := harness.session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	if harness.callCountRead() != 3 {
		t.Fatalf("callCount = %d, want 3", harness.callCountRead())
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(requests[0], "queued follow-up") {
		t.Fatal("the retry request must not deliver the follow-up")
	}
	if !strings.Contains(requests[1], "queued follow-up") {
		t.Fatal("the post-retry continuation must deliver the follow-up")
	}
	if len(lifecycle) < 2 || lifecycle[0] != "agent_end" || lifecycle[1] != string(AgentSessionEventAutoRetryStart) {
		t.Fatalf("lifecycle head = %v", lifecycle)
	}
}

func TestLengthRecoveryExhaustedRetryRunIsFinal(t *testing.T) {
	harness := createFauxHarness(t, &fauxHarnessOptions{
		settings: Settings{"retry": map[string]any{"enabled": true, "maxRetries": float64(1), "baseDelayMs": float64(1)}},
	})
	harness.setResponses(
		fauxAssistantMessage("", withStopReason(types.StopReasonError), withErrorMessage("overloaded_error")),
		fauxAssistantMessage("", withStopReason(types.StopReasonError), withErrorMessage("overloaded_error")),
	)

	if err := harness.session.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}

	var willRetryFlags []bool
	for _, event := range harness.eventsOfType(agent.AgentEventAgentEnd) {
		willRetryFlags = append(willRetryFlags, event.WillRetry)
	}
	if len(willRetryFlags) != 2 || !willRetryFlags[0] || willRetryFlags[1] {
		t.Fatalf("agent_end willRetry = %v, want [true false]", willRetryFlags)
	}
	found := false
	for _, event := range harness.eventsOfType(AgentSessionEventAutoRetryEnd) {
		if !event.Success && event.Attempt == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("expected auto_retry_end(success=false, attempt=1)")
	}
}

func TestLengthRecoveryKeepsOmissionsAndDoesNotRetryWhenCompactionFails(t *testing.T) {
	harness := createFauxHarness(t, &fauxHarnessOptions{
		contextWindow: 1000,
		maxTokens:     100,
		settings: Settings{
			"compaction": map[string]any{"keepRecentTokens": float64(1), "reserveTokens": float64(0)},
			"retry":      map[string]any{"enabled": false, "maxRetries": float64(0), "baseDelayMs": float64(1)},
		},
	})
	harness.setResponses(
		fauxAssistantMessage("partial response", withStopReason(types.StopReasonLength)),
		fauxAssistantMessage("summary failed", withStopReason(types.StopReasonError), withErrorMessage("summary failed")),
		fauxAssistantMessage("must not retry"),
	)

	if err := harness.session.Prompt(context.Background(), strings.Repeat("x", 5000), nil); err != nil {
		t.Fatal(err)
	}

	entries := harness.sessionManager.GetEntries()
	hasContextEdit := false
	hasCompaction := false
	hasRawPartial := false
	for _, entry := range entries {
		switch typed := entry.(type) {
		case *ContextEditEntry:
			hasContextEdit = true
		case *CompactionEntry:
			hasCompaction = true
		case *SessionMessageEntry:
			if assistant, ok := typed.Message.(types.AssistantMessage); ok && strings.Contains(agentMessageText(assistant), "partial response") {
				hasRawPartial = true
			}
		}
	}
	if !hasContextEdit {
		t.Fatal("expected the recovery omission to persist")
	}
	if hasCompaction {
		t.Fatal("failed recovery must not append a compaction")
	}
	if !hasRawPartial {
		t.Fatal("the failed attempt must remain in raw history")
	}
	for _, message := range harness.sessionManager.BuildSessionProjection().Messages {
		if strings.Contains(fauxMessageToText(fauxAsTypesMessage(message)), "partial response") {
			t.Fatal("the failed attempt must be omitted from the projection")
		}
	}
	if harness.callCountRead() != 2 {
		t.Fatalf("callCount = %d, want 2", harness.callCountRead())
	}
}

// fauxAsTypesMessage narrows an AgentMessage to its LLM-message form when
// possible (pig-only helper for the projection text assertions; nil falls
// through fauxMessageToText's default case).
func fauxAsTypesMessage(message agent.AgentMessage) types.Message {
	if typed, ok := message.(types.Message); ok {
		return typed
	}
	return nil
}
