// pi: api/anthropic-messages.ts.
//
// The anthropic-messages wire adapter. It covers Anthropic and
// Anthropic-compatible endpoints (DeepSeek /anthropic, kimi-coding, minimax,
// fireworks claude routes, ...). The @anthropic-ai/sdk (beta messages
// namespace, HTTP transport, SSE event:/data: parsing) has no Go counterpart:
// the request body is built as a plain JSON map and the SSE response stream
// is parsed locally. The SDK's betas create-param travels as the
// anthropic-beta request header, exactly like the SDK sends it.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// anthropicSDKVersion is the anthropic-version header value the SDK sends by
// default; pig reproduces it because real Anthropic endpoints require it.
const anthropicSDKVersion = "2023-06-01"

// AnthropicEffort is the effort level of adaptive thinking models (pi:
// AnthropicEffort).
type AnthropicEffort string

const (
	AnthropicEffortLow    AnthropicEffort = "low"
	AnthropicEffortMedium AnthropicEffort = "medium"
	AnthropicEffortHigh   AnthropicEffort = "high"
	AnthropicEffortXHigh  AnthropicEffort = "xhigh"
	AnthropicEffortMax    AnthropicEffort = "max"
)

// AnthropicThinkingDisplay controls how thinking content is returned (pi:
// AnthropicThinkingDisplay).
type AnthropicThinkingDisplay string

const (
	// AnthropicThinkingDisplaySummarized: thinking blocks carry summarized
	// thinking text.
	AnthropicThinkingDisplaySummarized AnthropicThinkingDisplay = "summarized"
	// AnthropicThinkingDisplayOmitted: thinking text is empty; the signature
	// still travels back for multi-turn continuity.
	AnthropicThinkingDisplayOmitted AnthropicThinkingDisplay = "omitted"
)

// AnthropicToolChoice is Anthropic tool-choice behavior (pi: the
// "auto" | "any" | "none" | { type: "tool", name } union — string choices are
// modeled as Type with an empty Name).
type AnthropicToolChoice struct {
	Type string `json:"type"` // "auto" | "any" | "none" | "tool"
	Name string `json:"name,omitempty"`
}

const (
	fineGrainedToolStreamingBeta    = "fine-grained-tool-streaming-2025-05-14"
	interleavedThinkingBeta         = "interleaved-thinking-2025-05-14"
	serverSideFallbackBeta          = "server-side-fallback-2026-07-01"
	midConversationOutputConfigBeta = "mid-conversation-output-config-2026-07-01"
	thinkingBindingControlsBeta     = "thinking-binding-controls-2026-08-01"
	inlineToolsBeta                 = "inline-tools-2026-09-15"
)

// deferredToolPlaceholderName is the stable deferred tool declared whenever
// native tool changes are in use. Anthropic adds hidden prompt scaffolding for
// mid-conversation tool changes; declaring this placeholder from the first
// request keeps that scaffolding in the cached prefix, so the first tool
// change does not invalidate the cache (measured: full miss without it). It
// is never activated and the model cannot see it (pi:
// DEFERRED_TOOL_PLACEHOLDER).
const deferredToolPlaceholderName = "__pi_deferred_placeholder__"

// AnthropicOptions are the anthropic-messages request options (pi:
// AnthropicOptions extends StreamOptions).
type AnthropicOptions struct {
	types.StreamOptions
	// ThinkingEnabled enables extended thinking. For adaptive thinking
	// models: the model decides when/how much to think. For older models:
	// uses budget-based thinking with ThinkingBudgetTokens. Default: nil
	// (thinking is omitted unless StreamSimple maps a simple reasoning level
	// to it, or callers set it explicitly).
	ThinkingEnabled *bool
	// ThinkingBudgetTokens is the token budget for extended thinking (older
	// models only). Ignored for adaptive thinking models. Default: 1024
	// when ThinkingEnabled is true and no budget is provided.
	ThinkingBudgetTokens *int64
	// Effort is the effort level for adaptive thinking models:
	// "max" (Opus 4.6 only), "xhigh" (Opus 4.7+, Fable 5), "high" (always
	// thinks, deep reasoning), "medium" (may skip for simple queries),
	// "low" (minimal thinking). Ignored for older models. Default: empty =
	// omitted unless StreamSimple maps a reasoning level to it.
	Effort AnthropicEffort
	// ThinkingDisplay controls how thinking content is returned:
	// "summarized" (default when thinking is enabled) or "omitted" (empty
	// thinking text, faster time-to-first-text-token).
	ThinkingDisplay AnthropicThinkingDisplay
	// InterleavedThinking requests the interleaved thinking beta header for
	// non-adaptive thinking models. Adaptive thinking models have it built
	// in, so the header is skipped for them regardless. Default: true.
	InterleavedThinking *bool
	// ToolChoice is Anthropic tool choice behavior. Default: nil (provider
	// default, currently equivalent to auto).
	ToolChoice *AnthropicToolChoice
	// pi's pre-built `client` member is missing by design: it injects an
	// alternative Anthropic SDK instance (AnthropicVertex); pig replaces the
	// transport via StreamOptions.Fetch instead, which covers the same
	// injection surface for tests and alternative endpoints.
}

// AnthropicMessages implements ProviderStreams for the anthropic-messages API
// (pi: the { stream, streamSimple } module object).
type AnthropicMessages struct{}

var _ types.ProviderStreams = AnthropicMessages{}

// pi: compat.ts registers the builtin API implementations at module load;
// pig's layering rule (ai cannot import ai/api) puts the registration in the
// adapter package's init instead.
func init() {
	ai.RegisterBuiltInApiProvider(ai.ApiAnthropicMessages, AnthropicMessages{})
	ai.SetBuiltinApiRegistrar(func() {
		ai.RegisterBuiltInApiProvider(ai.ApiAnthropicMessages, AnthropicMessages{})
	})
}

// Stream dispatches one streaming request (pi: stream). ctx carries the
// caller's cancellation; pi passes options.signal into the SDK request
// options and the retry helper (pig: AbortSignal → context rule).
func (AnthropicMessages) Stream(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.StreamOptions) *types.AssistantMessageEventStream {
	opts := &AnthropicOptions{}
	if options != nil {
		opts.StreamOptions = *options
	}
	return streamAnthropicMessages(ctx, model, llmContext, opts)
}

// StreamSimple dispatches one simple streaming request (pi: streamSimple).
func (AnthropicMessages) StreamSimple(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
	if getAnthropicFederation(model, options.APIKey, options.Headers, options.Env) == nil {
		if err := assertAnthropicRequestAuth(string(model.Provider), options.APIKey, options.Headers); err != nil {
			return immediateErrorStream(model, err)
		}
	}
	base := BuildBaseOptions(model, llmContext, options, options.APIKey)
	opts := &AnthropicOptions{StreamOptions: base}
	if options.ToolChoice != "" {
		opts.ToolChoice = &AnthropicToolChoice{Type: string(options.ToolChoice)}
	}
	if options.Reasoning == "" {
		// pi: { ...base, thinkingEnabled: false }.
		opts.ThinkingEnabled = lo.ToPtr(false)
		return streamAnthropicMessages(ctx, model, llmContext, opts)
	}

	// For models with adaptive thinking: use an effort level. For older
	// models: use budget-based thinking.
	if compat, ok := model.Compat.(*types.AnthropicMessagesCompat); ok && compat != nil &&
		compat.ForceAdaptiveThinking != nil && *compat.ForceAdaptiveThinking {
		opts.ThinkingEnabled = lo.ToPtr(true)
		opts.Effort = mapThinkingLevelToEffort(model, options.Reasoning)
		return streamAnthropicMessages(ctx, model, llmContext, opts)
	}

	// pi passes base.maxTokens (buildBaseOptions always resolves it) as the
	// explicit cap and lets adjustMaxTokensForThinking fit the thinking
	// budget inside it. Do not coerce to 0 here, or the thinking budget
	// would become the entire max_tokens value.
	adjustedMaxTokens, adjustedThinkingBudget := AdjustMaxTokensForThinking(base.MaxTokens, model.MaxTokens, options.Reasoning, options.ThinkingBudgets)
	maxTokens := ClampMaxTokensToContext(model, llmContext, adjustedMaxTokens)

	opts.MaxTokens = lo.ToPtr(maxTokens)
	opts.ThinkingEnabled = lo.ToPtr(true)
	opts.ThinkingBudgetTokens = lo.ToPtr(min64(adjustedThinkingBudget, max64(0, maxTokens-1024)))
	return streamAnthropicMessages(ctx, model, llmContext, opts)
}

// anthropicCacheControl is the cache_control object (pi:
// CacheControlEphemeral).
type anthropicCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

// getCacheControl resolves the cache retention preference into a
// cache_control marker (pi: getCacheControl). A nil marker means none.
func getCacheControl(model *types.Model, cacheRetention types.CacheRetention, env types.ProviderEnv) (types.CacheRetention, *anthropicCacheControl) {
	retention := resolveCacheRetention(cacheRetention, env)
	if retention == types.CacheRetentionNone {
		return retention, nil
	}
	ttl := ""
	if retention == types.CacheRetentionLong && getAnthropicCompat(model).supportsLongCacheRetention {
		ttl = "1h"
	}
	marker := &anthropicCacheControl{Type: "ephemeral"}
	if ttl != "" {
		marker.TTL = ttl
	}
	return retention, marker
}

// Stealth mode: Mimic Claude Code's tool naming exactly.
const claudeCodeVersion = "2.1.280"

// Claude Code 2.x tool names (canonical casing).
// Source: https://cchistory.mariozechner.at/data/prompts-2.1.11.md
// To update: https://github.com/badlogic/cchistory
var claudeCodeTools = []string{
	"Read", "Write", "Edit", "Bash", "Grep", "Glob", "AskUserQuestion",
	"EnterPlanMode", "ExitPlanMode", "KillShell", "NotebookEdit", "Skill",
	"Task", "TaskOutput", "TodoWrite", "WebFetch", "WebSearch",
}

var ccToolLookup = func() map[string]string {
	m := make(map[string]string, len(claudeCodeTools))
	for _, t := range claudeCodeTools {
		m[strings.ToLower(t)] = t
	}
	return m
}()

// toClaudeCodeName converts a tool name to CC canonical casing when it
// matches case-insensitively (pi: toClaudeCodeName).
func toClaudeCodeName(name string) string {
	if canonical, ok := ccToolLookup[strings.ToLower(name)]; ok {
		return canonical
	}
	return name
}

// fromClaudeCodeName converts a CC-cased tool name back to the context
// tool's original casing (pi: fromClaudeCodeName). This is a simple
// case-insensitive lookup, NOT a mapping of different names.
func fromClaudeCodeName(name string, tools []types.Tool) string {
	if len(tools) > 0 {
		lower := strings.ToLower(name)
		for _, tool := range tools {
			if strings.ToLower(tool.Name) == lower {
				return tool.Name
			}
		}
	}
	return name
}

// convertImageBlock converts one image content block to Anthropic's image
// source shape (pi: convertImageBlock).
func convertImageBlock(image types.ImageContent) map[string]any {
	return map[string]any{
		"type": "image",
		"source": map[string]any{
			"type":       "base64",
			"media_type": image.MimeType,
			"data":       image.Data,
		},
	}
}

// convertContentBlocks converts text/image content to Anthropic API format
// (pi: convertContentBlocks): a plain string when there are no images, else a
// content block array with a placeholder text block when there are no text
// blocks.
func convertContentBlocks(content []types.Content) any {
	hasImages := false
	for _, c := range content {
		if c.ContentKind() == types.KindImage {
			hasImages = true
			break
		}
	}
	if !hasImages {
		texts := make([]string, 0, len(content))
		for _, c := range content {
			texts = append(texts, c.(types.TextContent).Text)
		}
		return utils.SanitizeSurrogates(strings.Join(texts, "\n"))
	}

	var blocks []map[string]any
	hasText := false
	for _, block := range content {
		if b, ok := block.(types.TextContent); ok {
			blocks = append(blocks, map[string]any{"type": "text", "text": utils.SanitizeSurrogates(b.Text)})
			hasText = true
			continue
		}
		image := block.(types.ImageContent)
		blocks = append(blocks, convertImageBlock(image))
	}
	// If only images (no text), add a placeholder text block.
	if !hasText {
		blocks = append([]map[string]any{{"type": "text", "text": "(see attached image)"}}, blocks...)
	}
	return blocks
}

// resolvedAnthropicCompat is the fully-resolved compatibility settings (pi:
// getAnthropicCompat's Required<...> return).
type resolvedAnthropicCompat struct {
	supportsEagerToolInputStreaming bool
	supportsLongCacheRetention      bool
	sendSessionAffinityHeaders      bool
	sessionAffinityFormat           string
	supportsCacheControlOnTools     bool
	supportsTemperature             bool
	allowEmptySignature             bool
	supportsStrictTools             bool
	supportsMidConvoSystemMessages  bool
	supportsMidConvoToolChanges     bool
}

// getAnthropicCompat resolves the model's anthropic-messages compat with
// defaults (pi: getAnthropicCompat).
func getAnthropicCompat(model *types.Model) resolvedAnthropicCompat {
	compat, _ := model.Compat.(*types.AnthropicMessagesCompat)
	isOpenRouter := model.Provider == types.ProviderOpenRouter || strings.Contains(model.BaseURL, "openrouter.ai")
	resolved := resolvedAnthropicCompat{
		supportsEagerToolInputStreaming: true,
		supportsLongCacheRetention:      true,
		sendSessionAffinityHeaders:      isOpenRouter,
		supportsCacheControlOnTools:     true,
		supportsTemperature:             true,
		allowEmptySignature:             false,
		supportsStrictTools:             false,
		supportsMidConvoSystemMessages:  false,
		supportsMidConvoToolChanges:     false,
	}
	if isOpenRouter {
		resolved.sessionAffinityFormat = "openrouter"
	}
	if compat == nil {
		return resolved
	}
	if compat.SupportsEagerToolInputStreaming != nil {
		resolved.supportsEagerToolInputStreaming = *compat.SupportsEagerToolInputStreaming
	}
	if compat.SupportsLongCacheRetention != nil {
		resolved.supportsLongCacheRetention = *compat.SupportsLongCacheRetention
	}
	if compat.SendSessionAffinityHeaders != nil {
		resolved.sendSessionAffinityHeaders = *compat.SendSessionAffinityHeaders
	}
	if compat.SessionAffinityFormat != nil {
		resolved.sessionAffinityFormat = *compat.SessionAffinityFormat
	}
	if compat.SupportsCacheControlOnTools != nil {
		resolved.supportsCacheControlOnTools = *compat.SupportsCacheControlOnTools
	}
	if compat.SupportsTemperature != nil {
		resolved.supportsTemperature = *compat.SupportsTemperature
	}
	if compat.AllowEmptySignature != nil {
		resolved.allowEmptySignature = *compat.AllowEmptySignature
	}
	if compat.SupportsStrictTools != nil {
		resolved.supportsStrictTools = *compat.SupportsStrictTools
	}
	if compat.SupportsMidConvoSystemMessages != nil {
		resolved.supportsMidConvoSystemMessages = *compat.SupportsMidConvoSystemMessages
	}
	if compat.SupportsMidConvoToolChanges != nil {
		resolved.supportsMidConvoToolChanges = *compat.SupportsMidConvoToolChanges
	}
	return resolved
}

func parsePositiveInt(s string) int64 {
	var n int64
	for i := 0; i < len(s); i++ {
		n = n*10 + int64(s[i]-'0')
	}
	return n
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// mergeHeaders merges header sources; later sources win (pi: mergeHeaders).
func mergeHeaders(headerSources ...types.ProviderHeaders) types.ProviderHeaders {
	merged := types.ProviderHeaders{}
	for _, headers := range headerSources {
		for name, value := range headers {
			merged[name] = value
		}
	}
	return merged
}

// mergeClientHeaders prepends the request identity's headers to the merge
// (pi: mergeHeaders(getIdentityHeaders(model, identity), ...)).
func mergeClientHeaders(model *types.Model, identity *types.ClientIdentity, headerSources ...types.ProviderHeaders) types.ProviderHeaders {
	all := make([]types.ProviderHeaders, 0, len(headerSources)+1)
	all = append(all, utils.GetIdentityHeaders(types.ClientIdentityTarget{Provider: string(model.Provider), BaseURL: model.BaseURL}, identity))
	all = append(all, headerSources...)
	return mergeHeaders(all...)
}

// assertAnthropicRequestAuth fails when no API key or auth header is present
// (pi: assertRequestAuth).
func assertAnthropicRequestAuth(provider string, apiKey string, headers types.ProviderHeaders) error {
	if hasAnthropicRequestAuth(apiKey, headers) {
		return nil
	}
	return fmt.Errorf("No API key for provider: %s", provider)
}

// ServerSentEvent is one decoded SSE event (pi: ServerSentEvent). Event is
// empty when the frame carried no event line (pi: null).
type ServerSentEvent struct {
	Event string
	Data  string
	Raw   []string
}

// sseDecoderState accumulates fields across lines (pi: SseDecoderState).
type sseDecoderState struct {
	event string
	data  []string
	raw   []string
}

var anthropicMessageEvents = map[string]struct{}{
	"message_start":       {},
	"message_delta":       {},
	"message_stop":        {},
	"content_block_start": {},
	"content_block_delta": {},
	"content_block_stop":  {},
}

// flushSseEvent returns the accumulated event, or nil when nothing was
// accumulated (pi: flushSseEvent).
func flushSseEvent(state *sseDecoderState) *ServerSentEvent {
	if state.event == "" && len(state.data) == 0 {
		return nil
	}
	event := &ServerSentEvent{
		Event: state.event,
		Data:  strings.Join(state.data, "\n"),
		Raw:   append([]string(nil), state.raw...),
	}
	state.event = ""
	state.data = nil
	state.raw = nil
	return event
}

// decodeSseLine feeds one line into the decoder, returning a completed event
// on blank lines (pi: decodeSseLine).
func decodeSseLine(line string, state *sseDecoderState) *ServerSentEvent {
	if line == "" {
		return flushSseEvent(state)
	}

	state.raw = append(state.raw, line)
	if strings.HasPrefix(line, ":") {
		return nil
	}

	delimiterIndex := strings.Index(line, ":")
	fieldName := line
	value := ""
	if delimiterIndex != -1 {
		fieldName = line[:delimiterIndex]
		value = line[delimiterIndex+1:]
	}
	if strings.HasPrefix(value, " ") {
		value = value[1:]
	}

	switch fieldName {
	case "event":
		state.event = value
	case "data":
		state.data = append(state.data, value)
	}
	return nil
}

// consumeLine splits one line off text, handling \n, \r and \r\n (pi:
// consumeLine). ok is false when no line break remains.
func consumeLine(text string) (line string, rest string, ok bool) {
	carriageReturnIndex := strings.IndexByte(text, '\r')
	newlineIndex := strings.IndexByte(text, '\n')
	lineBreakIndex := -1
	switch {
	case carriageReturnIndex != -1 && newlineIndex != -1:
		lineBreakIndex = min(carriageReturnIndex, newlineIndex)
	case carriageReturnIndex != -1:
		lineBreakIndex = carriageReturnIndex
	default:
		lineBreakIndex = newlineIndex
	}
	if lineBreakIndex == -1 {
		return "", "", false
	}

	nextIndex := lineBreakIndex + 1
	if text[lineBreakIndex] == '\r' && nextIndex < len(text) && text[nextIndex] == '\n' {
		nextIndex++
	}
	return text[:lineBreakIndex], text[nextIndex:], true
}

// iterateSseMessages parses an SSE body into events (pi: iterateSseMessages).
// Abort travels via ctx: cancellation closes the response body, ending the
// read loop with an abort error.
func iterateSseMessages(ctx context.Context, body io.Reader, yield func(ServerSentEvent) error) error {
	buf := make([]byte, 64*1024)
	var buffer string
	state := &sseDecoderState{}
	process := func() error {
		for {
			line, rest, ok := consumeLine(buffer)
			if !ok {
				return nil
			}
			buffer = rest
			if event := decodeSseLine(line, state); event != nil {
				if err := yield(*event); err != nil {
					return err
				}
			}
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return &utils.AbortError{}
		}
		n, readErr := body.Read(buf)
		if n > 0 {
			buffer += string(buf[:n])
			if err := process(); err != nil {
				return err
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
	}
	// Tail without a trailing line break still feeds the decoder; a blank
	// tail flushes through decodeSseLine.
	if event := decodeSseLine(buffer, state); event != nil {
		if err := yield(*event); err != nil {
			return err
		}
	}
	if event := flushSseEvent(state); event != nil {
		return yield(*event)
	}
	return nil
}

// anthropicStreamUsage is the message usage payload (pi: the SDK's usage
// shapes). Pointer fields preserve the "absent vs zero" distinction that
// message_delta null handling relies on.
type anthropicStreamUsage struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheCreation            *struct {
		Ephemeral1hInputTokens *int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	OutputTokensDetails *struct {
		ThinkingTokens *int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

// anthropicInputTransformation is one dropped-input transformation entry (pi:
// BetaThinkingDroppedInputTransformation).
type anthropicInputTransformation struct {
	Type   *string `json:"type"`
	Path   *string `json:"path"`
	Reason *string `json:"reason"`
}

// anthropicStreamMessage is the message payload of message_start (pi: the
// SDK's BetaMessage).
type anthropicStreamMessage struct {
	ID                   string                         `json:"id"`
	Model                string                         `json:"model"`
	Usage                anthropicStreamUsage           `json:"usage"`
	InputTransformations []anthropicInputTransformation `json:"input_transformations"`
}

// anthropicStopDetails carries refusal details (pi: RefusalStopDetails).
type anthropicStopDetails struct {
	Type        string `json:"type"`
	Category    string `json:"category"`
	Explanation string `json:"explanation"`
}

// iterateAnthropicEvents parses the SSE body into beta message events (pi:
// iterateAnthropicEvents), yielding JSON-decoded events as generic maps.
func iterateAnthropicEvents(ctx context.Context, resp *http.Response, yield func(event map[string]any) error) error {
	if resp.Body == nil {
		return fmt.Errorf("Attempted to iterate over an Anthropic response with no body")
	}
	defer resp.Body.Close()

	sawMessageStart := false
	sawMessageEnd := false

	err := iterateSseMessages(ctx, resp.Body, func(sse ServerSentEvent) error {
		if sse.Event == "error" {
			return fmt.Errorf("%s", sse.Data)
		}
		if _, isMessageEvent := anthropicMessageEvents[sse.Event]; !isMessageEvent {
			return nil
		}

		parsed, err := utils.ParseJSONWithRepair(sse.Data)
		if err != nil {
			return fmt.Errorf("Could not parse Anthropic SSE event %s: %s; data=%s; raw=%s",
				sse.Event, err.Error(), sse.Data, strings.Join(sse.Raw, `\n`))
		}
		event, ok := parsed.(map[string]any)
		if !ok {
			return fmt.Errorf("Could not parse Anthropic SSE event %s: not an object; data=%s; raw=%s",
				sse.Event, sse.Data, strings.Join(sse.Raw, `\n`))
		}
		switch event["type"] {
		case "message_start":
			sawMessageStart = true
		case "message_stop":
			sawMessageEnd = true
		}
		return yield(event)
	})
	if err != nil {
		return err
	}
	if sawMessageStart && !sawMessageEnd {
		return fmt.Errorf("Anthropic stream ended before message_stop")
	}
	return nil
}

// anthropicBlock is one block of the in-progress response (pi: the Block
// intersection type). partialJSON is a tool-call scratch buffer stripped
// before replay.
type anthropicBlock struct {
	index   int
	content types.Content
	// partialJSON accumulates input_json_delta for tool calls.
	partialJSON string
}

// streamAnthropicMessages is the adapter's core stream implementation (pi:
// the stream StreamFunction body). ctx carries the caller's cancellation
// (pi: options.signal — the SDK request options signal, the mid-stream
// signal.aborted checks, and the catch path's aborted-vs-error decision).
func streamAnthropicMessages(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *AnthropicOptions) *types.AssistantMessageEventStream {
	stream := types.NewAssistantMessageEventStream()
	normalizedContext := utils.ResolveTranscript(llmContext, getAnthropicCompat(model).supportsMidConvoSystemMessages)
	currentTools := utils.GetCurrentTools(normalizedContext.Messages)

	go func() {
		output := &types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{},
			API:        model.API,
			Provider:   model.Provider,
			Model:      model.ID,
			Usage:      types.Usage{},
			StopReason: types.StopReasonPending,
			Timestamp:  time.Now().UnixMilli(),
		}

		// blocks backs output.Content: every created block lands here and
		// later deltas mutate it in place (pi: `const blocks =
		// output.content` shares one array). push snapshots the content and
		// the partial with each event — pi's single event loop shares the
		// live output by reference, while pig's consumer runs on another
		// goroutine and would race with the producer (declared deviation,
		// see openai_completions_stream.go).
		var blocks []*anthropicBlock
		var inputTransformations []anthropicInputTransformation

		push := func(event types.AssistantMessageEvent) {
			content := make([]types.Content, len(blocks))
			for i, b := range blocks {
				content[i] = b.content
			}
			output.Content = content
			if event.Partial != nil {
				partial := *event.Partial
				event.Partial = &partial
			}
			if event.Message != nil {
				message := *event.Message
				event.Message = &message
			}
			stream.Push(event)
		}
		fail := func(err error) {
			// pi: output.stopReason = options?.signal?.aborted ? "aborted" :
			// "error"; errorMessage always carries the thrown message.
			if ctx.Err() != nil {
				output.StopReason = types.StopReasonAborted
			} else {
				output.StopReason = types.StopReasonError
			}
			output.ErrorMessage = utils.FormatProviderError(utils.NormalizeProviderError(err), "")
			push(types.AssistantMessageEvent{Type: types.EventError, Reason: output.StopReason, Message: output})
			stream.EndWith(*output)
		}

		// pi's try/catch: internal logic errors panic and are recovered here.
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok {
					fail(err)
				} else {
					fail(fmt.Errorf("%v", r))
				}
			}
		}()

		requestErr := func() error {
			apiKey := ""
			if options != nil {
				apiKey = options.APIKey
			}
			// Workload identity federation replaces the auth assert when the
			// ANTHROPIC_* federation env vars are configured (pi: the same
			// getAnthropicFederation guard around assertRequestAuth).
			federation := getAnthropicFederation(model, apiKey, options.Headers, options.Env)
			if federation == nil {
				if err := assertAnthropicRequestAuth(string(model.Provider), apiKey, options.Headers); err != nil {
					return err
				}
			}

			var copilotDynamicHeaders map[string]string
			if model.Provider == types.ProviderID("github-copilot") {
				hasImages := HasCopilotVisionInput(normalizedContext.Messages)
				copilotDynamicHeaders = BuildCopilotDynamicHeaders(normalizedContext.Messages, hasImages)
			}

			cacheRetention, _ := getCacheControl(model, options.CacheRetention, options.Env)
			cacheSessionID := ""
			if cacheRetention != types.CacheRetentionNone {
				cacheSessionID = options.SessionID
			}

			created := createAnthropicClient(model, apiKey, options.Headers, options.Identity, options.Fetch, copilotDynamicHeaders, cacheSessionID, federation)

			timeout := 10 * time.Minute
			if options.TimeoutMs != 0 {
				timeout = time.Duration(options.TimeoutMs) * time.Millisecond
			}
			maxRetries := 0
			if options.MaxRetries != nil {
				maxRetries = *options.MaxRetries
			}

			// Strict-tool capacity rejections (400 invalid_request_error) retry
			// once with preferred tools downgraded to their non-strict schemas
			// (#10409). The loop runs before the stream starts, so a started
			// stream is never retried.
			disablePreferredStrictTools := false
			var resp *http.Response
			for {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				params, hasPreferredStrictTools, providerThinkingLevel, err := buildAnthropicParams(model, normalizedContext, created.isOAuthToken, options, disablePreferredStrictTools)
				if err != nil {
					return err
				}
				if providerThinkingLevel != "" {
					output.ProviderThinkingLevel = providerThinkingLevel
				}
				if options.OnPayload != nil {
					if next := options.OnPayload(params, model); next != nil {
						if nextParams, ok := next.(map[string]any); ok {
							params = nextParams
						}
					}
				}
				// pi: params = { ...nextParams, stream: true }.
				params["stream"] = true

				resp, err = utils.RetryProviderRequest(func() (*http.Response, error) {
					return created.client.createMessage(ctx, params, timeout)
				}, utils.ProviderRetryOptions{MaxRetries: maxRetries, MaxRetryDelayMs: options.MaxRetryDelayMs, Ctx: ctx})
				if err == nil {
					break
				}
				if ctx.Err() != nil || disablePreferredStrictTools || !hasPreferredStrictTools ||
					!isAnthropicStrictToolsLimitError(err) {
					return err
				}
				disablePreferredStrictTools = true
				utils.AppendAssistantMessageDiagnostic(output, utils.CreateAssistantMessageDiagnostic(
					"anthropic_strict_tools_fallback", err, map[string]any{"reason": "strict_tool_limits_exceeded"}))
			}
			if options.OnResponse != nil {
				options.OnResponse(types.ProviderResponse{Status: resp.StatusCode, Headers: utils.HeadersToRecord(resp.Header)}, model)
			}
			push(types.AssistantMessageEvent{Type: types.EventStart, Partial: output})

			usageModel := model
			return iterateAnthropicEvents(ctx, resp, func(event map[string]any) error {
				switch event["type"] {
				case "message_start":
					var message anthropicStreamMessage
					if err := remarshal(event["message"], &message); err != nil {
						panic(err)
					}
					output.ResponseID = message.ID
					if message.InputTransformations != nil {
						inputTransformations = message.InputTransformations
					}
					output.Model = message.Model
					if message.Model != model.ID {
						if compat, ok := model.Compat.(*types.AnthropicMessagesCompat); ok && compat != nil {
							for _, fallback := range compat.AllowedFallbackModels {
								if fallback.Provider == model.Provider && fallback.Model == output.Model {
									usageModel = &types.Model{}
									*usageModel = *model
									usageModel.ID = output.Model
									usageModel.Cost = fallback.Cost
									break
								}
							}
						}
					}
					// Capture initial token usage from message_start event.
					// This ensures we have input token counts even if the
					// stream is aborted early.
					output.Usage.Input = derefOrZero(message.Usage.InputTokens)
					output.Usage.Output = derefOrZero(message.Usage.OutputTokens)
					output.Usage.CacheRead = derefOrZero(message.Usage.CacheReadInputTokens)
					output.Usage.CacheWrite = derefOrZero(message.Usage.CacheCreationInputTokens)
					cacheWrite1h := int64(0)
					if message.Usage.CacheCreation != nil && message.Usage.CacheCreation.Ephemeral1hInputTokens != nil {
						cacheWrite1h = *message.Usage.CacheCreation.Ephemeral1hInputTokens
					}
					output.Usage.CacheWrite1h = lo.ToPtr(cacheWrite1h)
					// Anthropic doesn't provide total_tokens, compute from
					// components.
					output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output + output.Usage.CacheRead + output.Usage.CacheWrite
					ai.CalculateCost(usageModel, &output.Usage)
				case "content_block_start":
					index := int(numberField(event, "index"))
					contentBlock, _ := event["content_block"].(map[string]any)
					blockType, _ := contentBlock["type"].(string)
					switch blockType {
					case "fallback":
						if len(output.Content) > 0 {
							panic(fmt.Errorf("Anthropic performed an unsupported mid-output model fallback"))
						}
					case "text":
						text, _ := contentBlock["text"].(string)
						block := &anthropicBlock{index: index, content: types.NewTextContent(text)}
						blocks = append(blocks, block)
						push(types.AssistantMessageEvent{Type: types.EventTextStart, ContentIndex: len(blocks) - 1, Partial: output})
					case "thinking":
						thinking, _ := contentBlock["thinking"].(string)
						signature, _ := contentBlock["signature"].(string)
						block := &anthropicBlock{index: index, content: types.ThinkingContent{Kind: types.KindThinking, Thinking: thinking, ThinkingSignature: signature}}
						blocks = append(blocks, block)
						push(types.AssistantMessageEvent{Type: types.EventThinkingStart, ContentIndex: len(blocks) - 1, Partial: output})
					case "redacted_thinking":
						data, _ := contentBlock["data"].(string)
						block := &anthropicBlock{index: index, content: types.ThinkingContent{Kind: types.KindThinking, Thinking: "[Reasoning redacted]", ThinkingSignature: data, Redacted: true}}
						blocks = append(blocks, block)
						push(types.AssistantMessageEvent{Type: types.EventThinkingStart, ContentIndex: len(blocks) - 1, Partial: output})
					case "tool_use":
						id, _ := contentBlock["id"].(string)
						name, _ := contentBlock["name"].(string)
						toolName := name
						if created.isOAuthToken {
							toolName = fromClaudeCodeName(name, currentTools)
						}
						arguments, _ := contentBlock["input"].(map[string]any)
						toolCall := types.NewToolCall(id, toolName, arguments)
						block := &anthropicBlock{index: index, content: toolCall}
						blocks = append(blocks, block)
						push(types.AssistantMessageEvent{Type: types.EventToolCallStart, ContentIndex: len(blocks) - 1, Partial: output})
					}
				case "content_block_delta":
					index := int(numberField(event, "index"))
					delta, _ := event["delta"].(map[string]any)
					deltaType, _ := delta["type"].(string)
					blockIndex := -1
					for i, b := range blocks {
						if b.index == index {
							blockIndex = i
							break
						}
					}
					if blockIndex == -1 {
						return nil
					}
					block := blocks[blockIndex]
					switch deltaType {
					case "text_delta":
						if text, ok := block.content.(types.TextContent); ok {
							deltaText, _ := delta["text"].(string)
							text.Text += deltaText
							block.content = text
							push(types.AssistantMessageEvent{Type: types.EventTextDelta, ContentIndex: blockIndex, Delta: deltaText, Partial: output})
						}
					case "thinking_delta":
						if thinking, ok := block.content.(types.ThinkingContent); ok {
							deltaThinking, _ := delta["thinking"].(string)
							thinking.Thinking += deltaThinking
							block.content = thinking
							push(types.AssistantMessageEvent{Type: types.EventThinkingDelta, ContentIndex: blockIndex, Delta: deltaThinking, Partial: output})
						}
					case "input_json_delta":
						if toolCall, ok := block.content.(types.ToolCall); ok {
							partialJSON, _ := delta["partial_json"].(string)
							block.partialJSON += partialJSON
							toolCall.Arguments = utils.ParseStreamingJSON(&block.partialJSON)
							block.content = toolCall
							push(types.AssistantMessageEvent{Type: types.EventToolCallDelta, ContentIndex: blockIndex, Delta: partialJSON, Partial: output})
						}
					case "signature_delta":
						if thinking, ok := block.content.(types.ThinkingContent); ok {
							signature, _ := delta["signature"].(string)
							thinking.ThinkingSignature += signature
							block.content = thinking
						}
					}
				case "content_block_stop":
					index := int(numberField(event, "index"))
					blockIndex := -1
					for i, b := range blocks {
						if b.index == index {
							blockIndex = i
							break
						}
					}
					if blockIndex == -1 {
						return nil
					}
					block := blocks[blockIndex]
					switch b := block.content.(type) {
					case types.TextContent:
						push(types.AssistantMessageEvent{Type: types.EventTextEnd, ContentIndex: blockIndex, Content: b.Text, Partial: output})
					case types.ThinkingContent:
						push(types.AssistantMessageEvent{Type: types.EventThinkingEnd, ContentIndex: blockIndex, Content: b.Thinking, Partial: output})
					case types.ToolCall:
						b.Arguments, b.ArgumentsError = utils.ParseToolCallArguments(block.partialJSON)
						// Finalize in place and strip the scratch buffer so
						// replay only carries parsed arguments (pi: delete
						// block.partialJson).
						block.partialJSON = ""
						block.content = b
						toolCall := b
						push(types.AssistantMessageEvent{Type: types.EventToolCallEnd, ContentIndex: blockIndex, ToolCall: &toolCall, Partial: output})
					}
				case "message_delta":
					if transformations, ok := event["input_transformations"].([]any); ok {
						parsed := make([]anthropicInputTransformation, 0, len(transformations))
						for _, raw := range transformations {
							if m, ok := raw.(map[string]any); ok {
								var transformation anthropicInputTransformation
								if err := remarshal(m, &transformation); err == nil {
									parsed = append(parsed, transformation)
								}
							}
						}
						inputTransformations = parsed
					}
					delta, _ := event["delta"].(map[string]any)
					if delta != nil {
						if stopReason, hasStopReason := delta["stop_reason"].(string); hasStopReason {
							output.RawStopReason = stopReason
							var stopDetails *anthropicStopDetails
							if details, ok := delta["stop_details"].(map[string]any); ok {
								stopDetails = &anthropicStopDetails{}
								_ = remarshal(details, stopDetails)
							}
							mapped, errorMessage, mapErr := mapAnthropicStopReason(stopReason, stopDetails)
							if mapErr != nil {
								// pi throws; the stream catch surfaces it.
								return mapErr
							}
							output.StopReason = mapped
							if errorMessage != "" {
								output.ErrorMessage = errorMessage
							}
						}
					}
					// Only update usage fields if present (not null).
					// Preserves input_tokens from message_start when proxies
					// omit it in message_delta.
					if usage, ok := event["usage"].(map[string]any); ok {
						var parsed anthropicStreamUsage
						if err := remarshal(usage, &parsed); err == nil {
							if parsed.InputTokens != nil {
								output.Usage.Input = *parsed.InputTokens
							}
							if parsed.OutputTokens != nil {
								output.Usage.Output = *parsed.OutputTokens
							}
							if parsed.CacheReadInputTokens != nil {
								output.Usage.CacheRead = *parsed.CacheReadInputTokens
							}
							if parsed.CacheCreationInputTokens != nil {
								output.Usage.CacheWrite = *parsed.CacheCreationInputTokens
							}
							// Vercel AI Gateway includes the TTL breakdown in
							// deltas, though the SDK only types it on
							// message_start (pi v0.99.1 667fc3dd3, #9210).
							if parsed.CacheCreation != nil && parsed.CacheCreation.Ephemeral1hInputTokens != nil {
								output.Usage.CacheWrite1h = lo.ToPtr(*parsed.CacheCreation.Ephemeral1hInputTokens)
							}
							// Anthropic reports reasoning tokens as a subset
							// of output tokens.
							if parsed.OutputTokensDetails != nil && parsed.OutputTokensDetails.ThinkingTokens != nil {
								output.Usage.Reasoning = lo.ToPtr(*parsed.OutputTokensDetails.ThinkingTokens)
							}
						}
					}
					// Anthropic doesn't provide total_tokens, compute from
					// components.
					output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output + output.Usage.CacheRead + output.Usage.CacheWrite
					ai.CalculateCost(usageModel, &output.Usage)
				}
				return nil
			})
		}()
		if requestErr != nil {
			fail(requestErr)
			return
		}

		// pi: if (options?.signal?.aborted) throw new Error("Request was
		// aborted").
		if ctx.Err() != nil {
			fail(&utils.AbortError{})
			return
		}
		if output.StopReason == types.StopReasonPending {
			fail(fmt.Errorf("Anthropic stream ended without a stop reason"))
			return
		}
		if output.StopReason == types.StopReasonAborted || output.StopReason == types.StopReasonError {
			message := output.ErrorMessage
			if message == "" {
				message = "An unknown error occurred"
			}
			fail(fmt.Errorf("%s", message))
			return
		}
		if len(inputTransformations) > 0 {
			transformations := make([]map[string]any, 0, len(inputTransformations))
			for _, transformation := range inputTransformations {
				entry := map[string]any{}
				if transformation.Type != nil {
					entry["type"] = *transformation.Type
				}
				if transformation.Path != nil {
					entry["path"] = *transformation.Path
				}
				if transformation.Reason != nil {
					entry["reason"] = *transformation.Reason
				}
				transformations = append(transformations, entry)
			}
			utils.AppendAssistantMessageDiagnostic(output, types.AssistantMessageDiagnostic{
				Type:      "anthropic_input_transformations",
				Timestamp: time.Now().UnixMilli(),
				Details:   map[string]any{"transformations": transformations},
			})
		}

		push(types.AssistantMessageEvent{Type: types.EventDone, Reason: output.StopReason, Message: output})
		stream.EndWith(*output)
	}()

	return stream
}

// derefOrZero reads an optional number as 0 (pi: value || 0 on message_start
// usage fields).
func derefOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// numberField reads a JSON number field as float64 (the encoding/json
// generic form) and widens to int64.
func numberField(event map[string]any, name string) float64 {
	value, _ := event[name].(float64)
	return value
}

// remarshal round-trips a decoded JSON map into a typed struct.
func remarshal(source any, target any) error {
	encoded, err := json.Marshal(source)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

// mapThinkingLevelToEffort maps ThinkingLevel to Anthropic effort levels for
// adaptive thinking (pi: mapThinkingLevelToEffort). Note: effort "max" is
// available on all adaptive-thinking Claude models, while native "xhigh" is
// only available on Opus 4.7/4.8, Sonnet 5, and Fable 5.
func mapThinkingLevelToEffort(model *types.Model, level types.ThinkingLevel) AnthropicEffort {
	if level != "" && model.ThinkingLevelMap != nil {
		if mapped, present := model.ThinkingLevelMap[types.ModelThinkingLevel(level)]; present && mapped != nil {
			return AnthropicEffort(*mapped)
		}
	}
	switch level {
	case types.ThinkingLevelMinimal, types.ThinkingLevelLow:
		return AnthropicEffortLow
	case types.ThinkingLevelMedium:
		return AnthropicEffortMedium
	case types.ThinkingLevelHigh:
		return AnthropicEffortHigh
	default:
		return AnthropicEffortHigh
	}
}

func isOAuthToken(apiKey string) bool {
	return strings.Contains(apiKey, "sk-ant-oat")
}

// anthropicClient is the Anthropic SDK client replacement (pi: createClient
// returns an Anthropic SDK instance; the SDK's beta.messages endpoint is
// reproduced here).
type anthropicClient struct {
	baseURL string
	apiKey  string
	// authToken goes out as Authorization: Bearer (pi: SDK authToken; set
	// for OAuth and github-copilot instead of x-api-key).
	authToken string
	headers   map[string]string
	fetch     types.FetchFunction
	timeout   time.Duration
	// federation carries the workload identity config; when set, the client
	// bearer-authenticates with an access token exchanged (and cached) from
	// the identity token file (pi: the SDK's config-based credentials path).
	federation      *anthropicFederationConfig
	federationKey   string
	federationCache *anthropicFederationTokenCache
}

// anthropicCreatedClient is createAnthropicClient's result (pi: the
// { client, isOAuthToken } return).
type anthropicCreatedClient struct {
	client       *anthropicClient
	isOAuthToken bool
}

// createAnthropicClient builds the client (pi: createClient).
func createAnthropicClient(model *types.Model, apiKey string, optionsHeaders types.ProviderHeaders, identity *types.ClientIdentity, fetch types.FetchFunction, dynamicHeaders map[string]string, sessionId string, federation *anthropicFederationConfig) anthropicCreatedClient {
	// Copilot: Bearer auth.
	if model.Provider == types.ProviderID("github-copilot") {
		defaults := types.ProviderHeaders{
			"accept": lo.ToPtr("application/json"),
			"anthropic-dangerous-direct-browser-access": lo.ToPtr("true"),
		}
		merged := mergeClientHeaders(
			model,
			identity,
			defaults,
			modelHeadersAsProviderHeaders(model),
			dynamicHeadersAsProviderHeaders(dynamicHeaders),
			optionsHeaders,
		)
		return anthropicCreatedClient{
			client: &anthropicClient{
				baseURL:   model.BaseURL,
				authToken: apiKey,
				headers:   resolveAnthropicHeaders(merged),
				fetch:     fetch,
			},
			isOAuthToken: false,
		}
	}

	// OAuth: Bearer auth, Claude Code identity headers.
	if apiKey != "" && isOAuthToken(apiKey) {
		defaults := types.ProviderHeaders{
			"accept": lo.ToPtr("application/json"),
			"anthropic-dangerous-direct-browser-access": lo.ToPtr("true"),
			"user-agent": lo.ToPtr("claude-cli/" + claudeCodeVersion),
			"x-app":      lo.ToPtr("cli"),
		}
		merged := mergeClientHeaders(
			model,
			identity,
			defaults,
			modelHeadersAsProviderHeaders(model),
			optionsHeaders,
		)
		return anthropicCreatedClient{
			client: &anthropicClient{
				baseURL:   model.BaseURL,
				authToken: apiKey,
				headers:   resolveAnthropicHeaders(merged),
				fetch:     fetch,
			},
			isOAuthToken: true,
		}
	}

	// API key, header-owned auth, or workload identity federation.
	compat := getAnthropicCompat(model)
	sessionAffinityHeaders := types.ProviderHeaders{}
	if sessionId != "" && compat.sendSessionAffinityHeaders {
		header := "x-session-affinity"
		if compat.sessionAffinityFormat == "openrouter" {
			header = "x-session-id"
		}
		sessionAffinityHeaders[header] = lo.ToPtr(sessionId)
	}
	defaults := types.ProviderHeaders{
		"accept": lo.ToPtr("application/json"),
		"anthropic-dangerous-direct-browser-access": lo.ToPtr("true"),
	}
	merged := mergeClientHeaders(
		model,
		identity,
		defaults,
		sessionAffinityHeaders,
		modelHeadersAsProviderHeaders(model),
		optionsHeaders,
	)
	if federation != nil {
		// pi hands the SDK a federation-config client (apiKey/authToken null)
		// and clones it per request with withOptions({defaultHeaders}), which
		// shares the SDK token cache; pig stores the shared token cache in a
		// process-level entry keyed like pi's federationClient.
		return anthropicCreatedClient{
			client: &anthropicClient{
				baseURL:         model.BaseURL,
				headers:         resolveAnthropicHeaders(merged),
				fetch:           fetch,
				federation:      federation,
				federationKey:   federationCacheKey(model.BaseURL, federation),
				federationCache: &theAnthropicFederationTokenCache,
			},
			isOAuthToken: false,
		}
	}
	return anthropicCreatedClient{
		client: &anthropicClient{
			baseURL: model.BaseURL,
			apiKey:  apiKey,
			headers: resolveAnthropicHeaders(merged),
			fetch:   fetch,
		},
		isOAuthToken: false,
	}
}

// modelHeadersAsProviderHeaders lifts Model.Headers (map[string]string) into
// the merge form.
func modelHeadersAsProviderHeaders(model *types.Model) types.ProviderHeaders {
	headers := types.ProviderHeaders{}
	for name, value := range model.Headers {
		v := value
		headers[name] = &v
	}
	return headers
}

// dynamicHeadersAsProviderHeaders lifts the copilot dynamic headers into the
// merge form.
func dynamicHeadersAsProviderHeaders(dynamic map[string]string) types.ProviderHeaders {
	headers := types.ProviderHeaders{}
	for name, value := range dynamic {
		v := value
		headers[name] = &v
	}
	return headers
}

// resolveAnthropicHeaders flattens the ProviderHeaders merge into the wire
// map. A nil entry suppresses a default header with the same name (pi: the
// SDK skips null default headers).
func resolveAnthropicHeaders(merged types.ProviderHeaders) map[string]string {
	headers := map[string]string{}
	for name, value := range merged {
		if value == nil {
			delete(headers, name)
			continue
		}
		headers[name] = *value
	}
	return headers
}

// createMessage performs the POST /v1/messages request (pi:
// client.beta.messages.create with maxRetries 0). It returns the raw
// response; non-2xx statuses surface as *utils.ProviderHTTPError for the
// retry helper and error formatter. The SDK's betas create-param goes out as
// the anthropic-beta header.
func (c *anthropicClient) createMessage(ctx context.Context, params map[string]any, timeout time.Duration) (*http.Response, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	url := strings.TrimSuffix(c.baseURL, "/") + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", anthropicSDKVersion)
	for name, value := range c.headers {
		req.Header.Set(name, value)
	}
	// Auth: x-api-key by default, Authorization: Bearer for OAuth/Copilot.
	if c.federation != nil {
		// Workload identity federation: bearer-authenticate with an access
		// token exchanged from the identity token file (pi: the SDK's
		// credentials path).
		token, err := c.federationCache.getToken(ctx, c.federationKey, c.fetch, func(requestCtx context.Context) (*anthropicFederatedToken, error) {
			return exchangeAnthropicFederatedToken(requestCtx, c.baseURL, c.federation, c.fetch)
		})
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	} else if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	} else if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}
	if betas, ok := params["betas"].([]string); ok && len(betas) > 0 {
		req.Header.Set("anthropic-beta", strings.Join(betas, ","))
	}
	if c.federation != nil {
		// pi's SDK appends oauth-2025-04-20 to any existing anthropic-beta on
		// token-cache-authenticated requests (prepareRequest).
		if existing := req.Header.Get("anthropic-beta"); existing != "" {
			if !anthropicBetaListContains(existing, anthropicOAuthAPIBetaHeader) {
				req.Header.Set("anthropic-beta", existing+","+anthropicOAuthAPIBetaHeader)
			}
		} else {
			req.Header.Set("anthropic-beta", anthropicOAuthAPIBetaHeader)
		}
	}

	var resp *http.Response
	if c.fetch != nil {
		resp, err = c.fetch(req)
	} else {
		client := http.DefaultClient
		if timeout > 0 {
			client = &http.Client{Timeout: timeout}
		}
		resp, err = client.Do(req)
	}
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		return nil, &utils.ProviderHTTPError{
			Status:  resp.StatusCode,
			Headers: resp.Header,
			Body:    string(bodyBytes),
			Message: fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode)),
		}
	}
	return resp, nil
}

// getBetaFeatures resolves the beta features list (pi: getBetaFeatures). An
// anthropic-beta header value of null suppresses all beta features; a value
// replaces the default feature set.
func getBetaFeatures(model *types.Model, llmContext *types.TranscriptContext, isOAuthToken bool, nativeToolChanges bool, options *AnthropicOptions) []string {
	configuredFeatures := (*string)(nil)
	configuredSeen := false
	for _, headers := range []types.ProviderHeaders{modelHeadersAsProviderHeaders(model), options.Headers} {
		for name, value := range headers {
			if strings.ToLower(name) == "anthropic-beta" {
				configuredFeatures = value
				configuredSeen = true
			}
		}
	}
	if configuredSeen && configuredFeatures == nil {
		return []string{}
	}
	if configuredSeen && configuredFeatures != nil {
		seen := map[string]struct{}{}
		var features []string
		for _, feature := range strings.Split(*configuredFeatures, ",") {
			feature = strings.TrimSpace(feature)
			if feature == "" {
				continue
			}
			if _, dup := seen[feature]; dup {
				continue
			}
			seen[feature] = struct{}{}
			features = append(features, feature)
		}
		return features
	}

	var features []string
	if isOAuthToken {
		features = append(features, "claude-code-20250219", "oauth-2025-04-20")
	}
	if shouldUseFineGrainedToolStreamingBeta(model, llmContext) {
		features = append(features, fineGrainedToolStreamingBeta)
	}
	compat, _ := model.Compat.(*types.AnthropicMessagesCompat)
	forcedAdaptive := compat != nil && compat.ForceAdaptiveThinking != nil && *compat.ForceAdaptiveThinking
	if model.Reasoning && options.ThinkingEnabled != nil && *options.ThinkingEnabled &&
		(options.InterleavedThinking == nil || *options.InterleavedThinking) && !forcedAdaptive {
		features = append(features, interleavedThinkingBeta)
	}
	if shouldUseServerSideFallbackBeta(model) {
		features = append(features, serverSideFallbackBeta)
	}
	if compat != nil && compat.SupportsMidConvoEffort != nil && *compat.SupportsMidConvoEffort {
		features = append(features, midConversationOutputConfigBeta, thinkingBindingControlsBeta)
	}
	if nativeToolChanges {
		features = append(features, inlineToolsBeta)
	}
	seen := map[string]struct{}{}
	deduped := make([]string, 0, len(features))
	for _, feature := range features {
		if _, dup := seen[feature]; dup {
			continue
		}
		seen[feature] = struct{}{}
		deduped = append(deduped, feature)
	}
	return deduped
}

// shouldUseServerSideFallbackBeta reports whether the server-side fallback
// beta applies (pi: shouldUseServerSideFallbackBeta).
func shouldUseServerSideFallbackBeta(model *types.Model) bool {
	compat, _ := model.Compat.(*types.AnthropicMessagesCompat)
	return compat != nil && len(compat.AllowedFallbackModels) > 0
}

// shouldUseFineGrainedToolStreamingBeta reports whether the legacy
// fine-grained tool streaming beta applies (pi:
// shouldUseFineGrainedToolStreamingBeta).
func shouldUseFineGrainedToolStreamingBeta(model *types.Model, llmContext *types.TranscriptContext) bool {
	return len(utils.GetCurrentTools(llmContext.Messages)) > 0 && !getAnthropicCompat(model).supportsEagerToolInputStreaming
}

// buildAnthropicParams builds the message create request body (pi:
// buildParams). The params are plain JSON maps so optional fields serialize
// on demand and OnPayload consumers can inspect the wire shape.
func buildAnthropicParams(model *types.Model, llmContext *types.TranscriptContext, isOAuthToken bool, options *AnthropicOptions, disablePreferredStrictTools bool) (map[string]any, bool, string, error) {
	_, cacheControl := getCacheControl(model, options.CacheRetention, options.Env)
	compat := getAnthropicCompat(model)
	compatModel, _ := model.Compat.(*types.AnthropicMessagesCompat)
	midConvoEffort := compatModel != nil && compatModel.SupportsMidConvoEffort != nil && *compatModel.SupportsMidConvoEffort
	// Effort cannot change between messages while thinking is disabled, so
	// "off" sends no effort markers.
	thinkingDisabled := model.Reasoning && options.ThinkingEnabled != nil && !*options.ThinkingEnabled &&
		thinkingLevelOffAllowed(model)
	effortMessages := midConvoEffort && !thinkingDisabled
	initialSystemMessage := utils.GetInitialSystemMessage(llmContext.Messages)
	initialSystemText := ""
	if initialSystemMessage != nil {
		initialSystemText = utils.GetSystemMessageText(initialSystemMessage)
	}
	normalizer := func(id string, m *types.Model, source *types.AssistantMessage) string {
		return normalizeAnthropicToolCallID(id)
	}
	transformedMessages := TransformMessages(llmContext.Messages, model, normalizer)
	conversationMessages := transformedMessages
	if initialSystemMessage != nil {
		conversationMessages = transformedMessages[1:]
	}
	// Native tool changes keep the request-level tool list fixed and define
	// every later tool by value in a tool_addition block, which also expresses
	// same-name redefinitions. Anthropic rejects a tool list where every tool
	// is deferred, so there must be an initial active tool to anchor the
	// placeholder. Otherwise the current tool list is sent.
	var initialTools []types.Tool
	if initialSystemMessage != nil {
		initialTools = initialSystemMessage.ToolsAdded
	}
	nativeToolChanges := compat.supportsMidConvoSystemMessages && compat.supportsMidConvoToolChanges &&
		len(initialTools) > 0
	hasPreferredStrictTools := false
	convertToolDefinitions := func(tools []types.Tool, toolCacheControl *anthropicCacheControl) ([]map[string]any, error) {
		definitions, err := convertAnthropicTools(tools, isOAuthToken, compat.supportsEagerToolInputStreaming, compat.supportsStrictTools, toolCacheControl, disablePreferredStrictTools)
		if err != nil {
			return nil, err
		}
		// A converted definition stays strict only when the input preferred it
		// and strict mode was available (pi: definition.strict === true &&
		// config.strict === "prefer").
		for index, definition := range definitions {
			if strict, _ := definition["strict"].(bool); strict && index < len(tools) && isPreferredJSONSchemaStrictSampling(tools[index].ConstrainedSampling) {
				hasPreferredStrictTools = true
			}
		}
		return definitions, nil
	}
	var messageToolConverter func([]types.Tool) ([]map[string]any, error)
	if nativeToolChanges {
		messageToolConverter = func(tools []types.Tool) ([]map[string]any, error) {
			return convertToolDefinitions(tools, nil)
		}
	}
	converted, err := convertAnthropicMessages(conversationMessages, isOAuthToken, cacheControl, compat.allowEmptySignature, managedEffortProvider(model, effortMessages), messageToolConverter)
	if err != nil {
		return nil, false, "", err
	}
	activeEffort := options.Effort
	if activeEffort == "" {
		activeEffort = AnthropicEffortHigh
	}
	betaFeatures := getBetaFeatures(model, llmContext, isOAuthToken, nativeToolChanges, options)
	params := map[string]any{
		"model":      model.ID,
		"messages":   converted.messages,
		"max_tokens": maxTokensValue(options, model),
		"stream":     true,
	}
	if effortMessages {
		params["messages"] = insertThinkingLevelMessages(converted, activeEffort)
	}
	if len(betaFeatures) > 0 {
		params["betas"] = betaFeatures
	}

	// For OAuth tokens, we MUST include Claude Code identity.
	if isOAuthToken {
		system := []map[string]any{{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."}}
		if cacheControl != nil {
			system[0]["cache_control"] = cacheControl
		}
		if initialSystemText != "" {
			block := map[string]any{"type": "text", "text": utils.SanitizeSurrogates(initialSystemText)}
			if cacheControl != nil {
				block["cache_control"] = cacheControl
			}
			system = append(system, block)
		}
		params["system"] = system
	} else if initialSystemText != "" {
		// Add cache control to system prompt for non-OAuth tokens.
		block := map[string]any{"type": "text", "text": utils.SanitizeSurrogates(initialSystemText)}
		if cacheControl != nil {
			block["cache_control"] = cacheControl
		}
		params["system"] = []map[string]any{block}
	}

	// Temperature is incompatible with extended thinking and unsupported on
	// Claude Opus 4.7+.
	if options.Temperature != nil && (options.ThinkingEnabled == nil || !*options.ThinkingEnabled) &&
		!midConvoEffort && compat.supportsTemperature {
		params["temperature"] = *options.Temperature
	}

	toolCacheControl := (*anthropicCacheControl)(nil)
	if compat.supportsCacheControlOnTools {
		toolCacheControl = cacheControl
	}
	if nativeToolChanges {
		// Initial tools stay active with the cache breakpoint on the last one,
		// followed by the placeholder. The list never changes afterwards: later
		// tools are defined by value in tool_addition blocks and withdrawn by
		// tool_removal, so the cached prefix survives every tool change.
		initialConverted, err := convertToolDefinitions(initialTools, toolCacheControl)
		if err != nil {
			return nil, false, "", err
		}
		placeholder := map[string]any{
			"name":        deferredToolPlaceholderName,
			"description": "Reserved placeholder. Never available. Never call this.",
			"input_schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
				"required":   []any{},
			},
			"defer_loading": true,
		}
		tools := append(append([]map[string]any{}, initialConverted...), placeholder)
		params["tools"] = tools
	} else {
		tools := utils.GetCurrentTools(llmContext.Messages)
		if len(tools) > 0 {
			convertedTools, err := convertToolDefinitions(tools, toolCacheControl)
			if err != nil {
				return nil, false, "", err
			}
			params["tools"] = convertedTools
		}
	}

	// Managed effort models use adaptive thinking so prefix mismatches can be
	// dropped instead of surfacing as persistent 400 responses.
	if thinkingDisabled {
		// No effort is sent, so the model default applies. Anthropic rejects
		// `block_binding` with disabled thinking.
		params["thinking"] = map[string]any{"type": "disabled"}
	} else if effortMessages {
		params["thinking"] = map[string]any{
			"type":          "adaptive",
			"display":       thinkingDisplayValue(options),
			"block_binding": map[string]any{"prefix_mismatch_behavior": "drop_block"},
		}
		params["output_config"] = map[string]any{"effort": "high"}
	} else if model.Reasoning && options.ThinkingEnabled != nil && *options.ThinkingEnabled {
		// Default to "summarized" so Opus 4.7 and Mythos Preview behave
		// like older Claude 4 models (whose API default is also
		// "summarized").
		display := thinkingDisplayValue(options)
		if compatModel != nil && compatModel.ForceAdaptiveThinking != nil && *compatModel.ForceAdaptiveThinking {
			// Adaptive thinking: Claude decides when and how much to
			// think.
			params["thinking"] = map[string]any{"type": "adaptive", "display": display}
			if options.Effort != "" {
				params["output_config"] = map[string]any{"effort": string(options.Effort)}
			}
		} else {
			// Budget-based thinking for older models.
			budget := int64(1024)
			if options.ThinkingBudgetTokens != nil && *options.ThinkingBudgetTokens != 0 {
				budget = *options.ThinkingBudgetTokens
			}
			params["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget, "display": display}
		}
	}

	if options.Metadata != nil {
		if userId, ok := options.Metadata["user_id"].(string); ok {
			params["metadata"] = map[string]any{"user_id": userId}
		}
	}

	if options.ToolChoice != nil {
		choice := map[string]any{"type": options.ToolChoice.Type}
		if options.ToolChoice.Name != "" {
			choice["name"] = options.ToolChoice.Name
		}
		params["tool_choice"] = choice
	}

	if compatModel, ok := model.Compat.(*types.AnthropicMessagesCompat); ok && compatModel != nil &&
		len(compatModel.AllowedFallbackModels) > 0 {
		fallbacks := make([]map[string]any, 0, len(compatModel.AllowedFallbackModels))
		for _, fallback := range compatModel.AllowedFallbackModels {
			fallbacks = append(fallbacks, map[string]any{"model": fallback.Model})
		}
		params["fallbacks"] = fallbacks
	}

	providerThinkingLevel := ""
	if effortMessages {
		providerThinkingLevel = string(activeEffort)
	}
	return params, hasPreferredStrictTools, providerThinkingLevel, nil
}

// anthropicStrictToolLimitMessages are the API error messages that mean the
// request exceeded Anthropic's strict-tool capacity (pi: the same three
// substrings, #10409).
var anthropicStrictToolLimitMessages = []string{
	"The compiled grammar is too large",
	"Too many strict tools (",
	"Schemas contains too many parameters with union types (",
}

// isAnthropicStrictToolsLimitError reports whether err is a 400
// invalid_request_error whose message names a strict-tool capacity limit
// (pi: error instanceof Anthropic.APIError && error.status === 400 &&
// error.type === "invalid_request_error" && message.includes(...)).
func isAnthropicStrictToolsLimitError(err error) bool {
	norm := utils.NormalizeProviderError(err)
	if norm.Status != 400 {
		return false
	}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(norm.Body), &body) != nil {
		return false
	}
	if body.Error.Type != "invalid_request_error" {
		return false
	}
	message := body.Error.Message
	if message == "" {
		message = norm.Message
	}
	for _, marker := range anthropicStrictToolLimitMessages {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// maxTokensValue resolves the max_tokens param (pi: options?.maxTokens ??
// model.maxTokens).
func maxTokensValue(options *AnthropicOptions, model *types.Model) int64 {
	if options.MaxTokens != nil {
		return *options.MaxTokens
	}
	return model.MaxTokens
}

// thinkingDisplayValue resolves the thinking display (pi:
// options.thinkingDisplay ?? "summarized").
func thinkingDisplayValue(options *AnthropicOptions) string {
	if options.ThinkingDisplay != "" {
		return string(options.ThinkingDisplay)
	}
	return string(AnthropicThinkingDisplaySummarized)
}

// managedEffortProvider returns the provider for mid-conversation effort
// marker reconstruction (pi: effortMessages ? model.provider : undefined).
func managedEffortProvider(model *types.Model, effortMessages bool) string {
	if !effortMessages {
		return ""
	}
	return string(model.Provider)
}

// normalizeAnthropicToolCallID normalizes tool call IDs to match Anthropic's
// required pattern and length (pi: normalizeToolCallId in
// anthropic-messages.ts; openai-completions has its own variant).
func normalizeAnthropicToolCallID(id string) string {
	var b strings.Builder
	for _, c := range id {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	normalized := b.String()
	if len(normalized) > 64 {
		normalized = normalized[:64]
	}
	return normalized
}
