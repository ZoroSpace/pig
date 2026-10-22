// pi: api/openai-completions.ts (stream, streamSimple helpers, buildParams,
// SSE chunk processing). Same TS source file as openai_completions.go; split
// for readability, package api.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// streamingBlock is one block of the in-progress response (pi's
// StreamingBlock union). Text and thinking blocks are value-complete; tool
// call blocks carry scratch buffers (partialArgs/customInput/streamIndex)
// stripped before replay.
type streamingBlock interface{ isStreamingBlock() }

type streamingTextBlock struct{ content types.TextContent }

func (*streamingTextBlock) isStreamingBlock() {}

type streamingThinkingBlock struct{ content types.ThinkingContent }

func (*streamingThinkingBlock) isStreamingBlock() {}

type streamingCustomInput struct {
	property   string
	jsonBuffer GrammarToolInputJSONBuffer
}

type streamingToolBlock struct {
	content        types.ToolCall
	partialArgs    string
	hasPartialArgs bool
	customInput    *streamingCustomInput
	streamIndex    int
	hasStreamIndex bool
}

func (*streamingToolBlock) isStreamingBlock() {}

// thinkingLevelOffAllowed reports whether the model's off level is not
// explicitly nulled (pi: model.thinkingLevelMap?.off !== null). An absent key
// yields true (undefined !== null); only an explicit null suppresses.
func thinkingLevelOffAllowed(model *types.Model) bool {
	value, present := model.ThinkingLevelMap[types.ModelThinkingOff]
	if !present {
		return true
	}
	return value != nil
}

// thinkingLevelOffValue returns the model's off-level value, or "none" when
// unset (pi: model.thinkingLevelMap?.off ?? "none").
func thinkingLevelOffValue(model *types.Model) string {
	if value, present := model.ThinkingLevelMap[types.ModelThinkingOff]; present && value != nil {
		return *value
	}
	return "none"
}

// buildParams builds the chat completion request body (pi: buildParams).
func buildParams(model *types.Model, llmContext *types.TranscriptContext, options *OpenAICompletionsOptions, compat *resolvedOpenAICompletionsCompat, cacheRetention types.CacheRetention, grammarToolInputProperties map[string]string) map[string]any {
	transcriptTools := utils.ResolveTranscriptTools(
		llmContext.Messages,
		compat.supportsMidConvoSystemMessages && compat.supportsMidConvoToolAdditions,
	)
	messages := convertMessages(model, llmContext, compat, &ConvertCompletionsMessagesOptions{GrammarToolInputProperties: grammarToolInputProperties})
	cacheControl := getCompatCacheControl(compat, cacheRetention)

	params := map[string]any{
		"model":    model.ID,
		"messages": messages,
		"stream":   true,
	}
	if (model.BaseURL != "" && stringsContains(model.BaseURL, "api.openai.com") && cacheRetention != types.CacheRetentionNone) ||
		(cacheRetention == types.CacheRetentionLong && compat.supportsLongCacheRetention) {
		params["prompt_cache_key"] = ClampOpenAIPromptCacheKey(options.SessionID)
	}
	if cacheRetention == types.CacheRetentionLong && compat.supportsLongCacheRetention {
		params["prompt_cache_retention"] = "24h"
	}

	if compat.supportsUsageInStreaming {
		params["stream_options"] = map[string]any{"include_usage": true}
	}
	if compat.supportsStore {
		params["store"] = false
	}
	if options.MaxTokens != nil {
		if compat.maxTokensField == types.MaxTokensPlain {
			params["max_tokens"] = *options.MaxTokens
		} else {
			params["max_completion_tokens"] = *options.MaxTokens
		}
	}
	if options.Temperature != nil {
		params["temperature"] = *options.Temperature
	}

	if len(transcriptTools.RequestTools) > 0 {
		params["tools"] = convertTools(transcriptTools.RequestTools, compat)
		if compat.zaiToolStream {
			params["tool_stream"] = true
		}
	} else if hasToolHistory(llmContext.Messages) {
		// Anthropic (via LiteLLM/proxy) requires tools param when the
		// conversation has tool_calls/tool_results.
		params["tools"] = []any{}
	}

	if cacheControl != nil {
		tools, _ := params["tools"].([]map[string]any)
		applyAnthropicCacheControl(messages, tools, cacheControl)
		params["tools"] = tools
	}
	if options.ToolChoice != "" {
		params["tool_choice"] = string(options.ToolChoice)
	}

	if compat.vllmPriority != nil {
		params["priority"] = *compat.vllmPriority
	}

	thinkingTokenBudgetField := resolveThinkingTokenBudgetField(compat)
	thinkingBudget := resolveClampedThinkingBudget(model, options, params)

	if compat.thinkingFormat == types.ThinkingFormatZai && model.Reasoning {
		zaiParams := params
		if options.ReasoningEffort != "" {
			zaiParams["thinking"] = map[string]any{"type": "enabled", "clear_thinking": false}
		} else {
			zaiParams["thinking"] = map[string]any{"type": "disabled"}
		}
		if options.ReasoningEffort != "" && compat.supportsReasoningEffort {
			effort := model.ThinkingLevelMap[types.ModelThinkingLevel(options.ReasoningEffort)]
			if effort == nil {
				effort = lo.ToPtr(string(options.ReasoningEffort))
			}
			zaiParams["reasoning_effort"] = *effort
		}
	} else if compat.thinkingFormat == types.ThinkingFormatVolcengine && model.Reasoning {
		// pig-only (see ThinkingFormatVolcengine in ai/types/types.go).
		// Like zai but: no reasoning_effort ever, and "off" omits the
		// thinking param for models that reject "disabled" (off marked nil
		// in ThinkingLevelMap).
		if options.ReasoningEffort != "" {
			params["thinking"] = map[string]any{"type": "enabled", "clear_thinking": false}
		} else if thinkingLevelOffAllowed(model) {
			params["thinking"] = map[string]any{"type": "disabled"}
		}
	} else if compat.thinkingFormat == types.ThinkingFormatQwen && model.Reasoning {
		params["enable_thinking"] = options.ReasoningEffort != ""
		if options.ReasoningEffort != "" && compat.supportsReasoningEffort {
			effort := model.ThinkingLevelMap[types.ModelThinkingLevel(options.ReasoningEffort)]
			if effort == nil {
				effort = lo.ToPtr(string(options.ReasoningEffort))
			}
			params["reasoning_effort"] = *effort
		}
	} else if compat.thinkingFormat == types.ThinkingFormatQwenChatTemplate && model.Reasoning {
		params["chat_template_kwargs"] = map[string]any{
			"enable_thinking":   options.ReasoningEffort != "",
			"preserve_thinking": true,
		}
	} else if compat.thinkingFormat == types.ThinkingFormatChatTemplate && model.Reasoning {
		chatTemplateKwargs := buildChatTemplateValues(model, options, compat.chatTemplateKwargs, thinkingBudget)
		if chatTemplateKwargs != nil {
			params["chat_template_kwargs"] = chatTemplateKwargs
		}
	} else if compat.thinkingFormat == types.ThinkingFormatBaseten && model.Reasoning {
		chatTemplateArgs := buildChatTemplateValues(model, options, compat.chatTemplateArgs, thinkingBudget)
		if chatTemplateArgs != nil {
			params["chat_template_args"] = chatTemplateArgs
		}
		if compat.supportsReasoningEffort {
			requestedEffort := options.ReasoningEffort
			level := types.ModelThinkingLevel(requestedEffort)
			if level == "" {
				level = types.ModelThinkingOff
			}
			mappedEffort := model.ThinkingLevelMap[level]
			effort := string(requestedEffort)
			if mappedEffort != nil {
				effort = *mappedEffort
			}
			if effort != "" {
				params["reasoning_effort"] = effort
			}
		}
	} else if compat.thinkingFormat == types.ThinkingFormatDeepSeek && model.Reasoning {
		if options.ReasoningEffort != "" {
			params["thinking"] = map[string]any{"type": "enabled"}
		} else if thinkingLevelOffAllowed(model) {
			params["thinking"] = map[string]any{"type": "disabled"}
		}
		if options.ReasoningEffort != "" && compat.supportsReasoningEffort {
			effort := model.ThinkingLevelMap[types.ModelThinkingLevel(options.ReasoningEffort)]
			if effort == nil {
				effort = lo.ToPtr(string(options.ReasoningEffort))
			}
			params["reasoning_effort"] = *effort
		}
	} else if compat.thinkingFormat == types.ThinkingFormatOpenRouter && model.Reasoning {
		if options.ReasoningEffort != "" {
			effort := model.ThinkingLevelMap[types.ModelThinkingLevel(options.ReasoningEffort)]
			if effort == nil {
				effort = lo.ToPtr(string(options.ReasoningEffort))
			}
			params["reasoning"] = map[string]any{"effort": *effort}
		} else if thinkingLevelOffAllowed(model) {
			params["reasoning"] = map[string]any{"effort": thinkingLevelOffValue(model)}
		}
	} else if compat.thinkingFormat == types.ThinkingFormatAntLing && model.Reasoning && options.ReasoningEffort != "" {
		effort := model.ThinkingLevelMap[types.ModelThinkingLevel(options.ReasoningEffort)]
		if effort != nil {
			params["reasoning"] = map[string]any{"effort": *effort}
		}
	} else if compat.thinkingFormat == types.ThinkingFormatTogether && model.Reasoning {
		params["reasoning"] = map[string]any{"enabled": options.ReasoningEffort != ""}
		if options.ReasoningEffort != "" && compat.supportsReasoningEffort {
			effort := model.ThinkingLevelMap[types.ModelThinkingLevel(options.ReasoningEffort)]
			if effort == nil {
				effort = lo.ToPtr(string(options.ReasoningEffort))
			}
			params["reasoning_effort"] = *effort
		}
	} else if compat.thinkingFormat == types.ThinkingFormatStringThinking && model.Reasoning {
		if options.ReasoningEffort != "" {
			effort := model.ThinkingLevelMap[types.ModelThinkingLevel(options.ReasoningEffort)]
			if effort == nil {
				effort = lo.ToPtr(string(options.ReasoningEffort))
			}
			params["thinking"] = *effort
		} else if thinkingLevelOffAllowed(model) {
			params["thinking"] = thinkingLevelOffValue(model)
		}
	} else if options.ReasoningEffort != "" && model.Reasoning && compat.supportsReasoningEffort {
		// OpenAI-style reasoning_effort.
		effort := model.ThinkingLevelMap[types.ModelThinkingLevel(options.ReasoningEffort)]
		if effort == nil {
			effort = lo.ToPtr(string(options.ReasoningEffort))
		}
		params["reasoning_effort"] = *effort
	} else if options.ReasoningEffort == "" && model.Reasoning && compat.supportsReasoningEffort {
		offValue := model.ThinkingLevelMap[types.ModelThinkingOff]
		if offValue != nil {
			params["reasoning_effort"] = *offValue
		}
	}

	// Cap reasoning with a top-level budget field. Independent of
	// thinkingFormat: the same server can serve zai, qwen or chat-template
	// models. Reasoning and the answer share max_tokens here, so an uncapped
	// reasoning phase can consume the whole response and leave no answer and
	// no tool call.
	if thinkingTokenBudgetField != nil && thinkingBudget > 0 {
		params[string(*thinkingTokenBudgetField)] = thinkingBudget
	}

	// OpenRouter provider routing preferences.
	if compat.openRouterRouting != nil {
		params["provider"] = compat.openRouterRouting
	}
	// Vercel AI Gateway provider routing preferences.
	if compat.vercelGatewayRouting != nil {
		routing := compat.vercelGatewayRouting
		if len(routing.Only) > 0 || len(routing.Order) > 0 {
			gatewayOptions := map[string]any{}
			if len(routing.Only) > 0 {
				gatewayOptions["only"] = routing.Only
			}
			if len(routing.Order) > 0 {
				gatewayOptions["order"] = routing.Order
			}
			params["providerOptions"] = map[string]any{"gateway": gatewayOptions}
		}
	}

	// Last so model and request sampling parameters override the named
	// request fields, per key later wins (pi:
	// Object.assign(params, resolveSamplingParams(...)), v1.0.2 #9776).
	if samplingParams := ResolveSamplingParams(model, options.ReasoningEffort, options.SamplingParams); samplingParams != nil {
		for key, value := range samplingParams {
			params[key] = value
		}
	}

	return params
}

// streamOpenAICompletions is the adapter's core stream implementation (pi:
// the stream StreamFunction body). ctx carries the caller's cancellation
// (pi: options.signal — the SDK request options signal, the mid-stream
// signal.aborted checks, and the catch path's aborted-vs-error decision).
func streamOpenAICompletions(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *OpenAICompletionsOptions) *types.AssistantMessageEventStream {
	stream := types.NewAssistantMessageEventStream()
	normalizedContext := utils.ResolveTranscript(llmContext, getCompat(model).supportsMidConvoSystemMessages)

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

		var blocks []streamingBlock
		textBlock := (*streamingTextBlock)(nil)
		thinkingBlock := (*streamingThinkingBlock)(nil)
		toolCallBlocksByIndex := map[int]*streamingToolBlock{}
		toolCallBlocksByID := map[string]*streamingToolBlock{}
		hasFinishReason := false

		// `reasoning_details` are replay metadata, not user-visible stream
		// deltas. Keep them in memory during streaming and serialize once
		// when the block is finalized (pi: streamedReasoningDetails +
		// applyStreamedReasoningDetails).
		var streamedReasoningDetails []map[string]any
		applyStreamedReasoningDetails := func(block *streamingThinkingBlock) {
			if streamedReasoningDetails != nil {
				if b, err := json.Marshal(streamedReasoningDetails); err == nil {
					block.content.ThinkingSignature = string(b)
				}
			}
		}

		getContentIndex := func(block streamingBlock) int {
			for i, b := range blocks {
				if b == block {
					return i
				}
			}
			return -1
		}

		// assembleContent finalizes the blocks into output.Content for the
		// failure path: it is the counterpart of pi's catch block iterating
		// output.content to strip the scratch buffers (finalize per tool block),
		// since pi's blocks share the output.content array by reference. The
		// happy path keeps content current via syncContent/push below.
		assembleContent := func() {
			content := make([]types.Content, 0, len(blocks))
			for _, block := range blocks {
				switch b := block.(type) {
				case *streamingTextBlock:
					content = append(content, b.content)
				case *streamingThinkingBlock:
					applyStreamedReasoningDetails(b)
					content = append(content, b.content)
				case *streamingToolBlock:
					b.finalize()
					content = append(content, b.content)
				}
			}
			output.Content = content
		}

		// syncContent mirrors pi's shared-array invariant (`const blocks =
		// output.content`): in pi every block created by the ensure* helpers
		// lands in output.content immediately and later deltas mutate it
		// in place, so every event's partial carries the growing content.
		// Go value semantics cannot share the struct values, so pig rebuilds
		// the snapshot before each push instead (declared deviation: events
		// carry a snapshot of the content at push time, not a live view).
		syncContent := func() {
			content := make([]types.Content, len(blocks))
			for i, block := range blocks {
				switch b := block.(type) {
				case *streamingTextBlock:
					content[i] = b.content
				case *streamingThinkingBlock:
					content[i] = b.content
				case *streamingToolBlock:
					content[i] = b.content
				}
			}
			output.Content = content
		}
		// push serializes the content snapshot with each event (pi: the
		// partial's content array is shared by reference). It also snapshots
		// the partial itself: pi's single event loop lets the consumer read
		// the live partial safely, while pig's consumer runs on another
		// goroutine — handing it the shared pointer would race with the
		// producer's next chunk. A shallow copy is a safe immutable snapshot:
		// syncContent always allocates a fresh backing array for Content, and
		// every other field is only ever reassigned wholesale.
		push := func(event types.AssistantMessageEvent) {
			syncContent()
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
			assembleContent()
			// pi: output.stopReason = options?.signal?.aborted ? "aborted" : "error".
			if ctx.Err() != nil {
				output.StopReason = types.StopReasonAborted
			} else {
				output.StopReason = types.StopReasonError
			}
			output.ErrorMessage = utils.FormatProviderError(utils.NormalizeProviderError(err), "")
			// Some providers via OpenRouter give additional information in
			// error.metadata.raw; append it when not already present.
			rawMetadata := providerRawMetadata(err)
			if rawMetadata != "" && !stringsContains(output.ErrorMessage, rawMetadata) {
				output.ErrorMessage += "\n" + rawMetadata
			}
			stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: output.StopReason, Message: output})
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

		apiKey, err := getClientAPIKey(string(model.Provider), options.APIKey, options.Headers)
		if err != nil {
			fail(err)
			return
		}
		compat := getCompat(model)
		grammarToolInputProperties := CreateGrammarToolInputProperties(utils.GetDeclaredTools(normalizedContext.Messages), compat.supportsOpenAIGrammarTools)
		cacheRetention := resolveCacheRetention(options.CacheRetention, options.Env)
		cacheSessionID := ""
		if cacheRetention != types.CacheRetentionNone {
			cacheSessionID = options.SessionID
		}
		client := createClient(model, normalizedContext, apiKey, options.Headers, options.Identity, options.Fetch, cacheSessionID, compat)
		params := buildParams(model, normalizedContext, options, compat, cacheRetention, grammarToolInputProperties)
		if options.OnPayload != nil {
			if next := options.OnPayload(params, model); next != nil {
				if nextParams, ok := next.(map[string]any); ok {
					params = nextParams
				}
			}
		}
		timeout := time.Duration(options.TimeoutMs) * time.Millisecond
		maxRetries := 0
		if options.MaxRetries != nil {
			maxRetries = *options.MaxRetries
		}
		resp, err := utils.RetryProviderRequest(func() (*http.Response, error) {
			return client.createChatCompletion(ctx, params, timeout)
		}, utils.ProviderRetryOptions{MaxRetries: maxRetries, MaxRetryDelayMs: options.MaxRetryDelayMs, Ctx: ctx})
		if err != nil {
			fail(err)
			return
		}
		if options.OnResponse != nil {
			options.OnResponse(types.ProviderResponse{Status: resp.StatusCode, Headers: utils.HeadersToRecord(resp.Header)}, model)
		}
		push(types.AssistantMessageEvent{Type: types.EventStart, Partial: output})

		getCustomToolCallInput := func(block *streamingToolBlock) string {
			if block.customInput == nil {
				return ""
			}
			value, _ := block.content.Arguments[block.customInput.property].(string)
			return value
		}
		appendCustomToolCallInput := func(block *streamingToolBlock, nextInput string, close bool) *string {
			if block.customInput == nil {
				return nil
			}
			delta, err := AppendGrammarToolInputJSONDelta(&block.customInput.jsonBuffer, block.customInput.property, nextInput, close)
			if err != nil {
				panic(err)
			}
			block.content.Arguments = map[string]any{block.customInput.property: nextInput}
			return delta
		}

		ensureTextBlock := func() *streamingTextBlock {
			if textBlock == nil {
				textBlock = &streamingTextBlock{content: types.NewTextContent("")}
				blocks = append(blocks, textBlock)
				push(types.AssistantMessageEvent{Type: types.EventTextStart, ContentIndex: getContentIndex(textBlock), Partial: output})
			}
			return textBlock
		}
		ensureThinkingBlock := func(signature string) *streamingThinkingBlock {
			if thinkingBlock == nil {
				thinkingBlock = &streamingThinkingBlock{content: types.NewThinkingContent("")}
				thinkingBlock.content.ThinkingSignature = signature
				blocks = append(blocks, thinkingBlock)
				push(types.AssistantMessageEvent{Type: types.EventThinkingStart, ContentIndex: getContentIndex(thinkingBlock), Partial: output})
			}
			return thinkingBlock
		}
		ensureToolCallBlock := func(toolCall chatCompletionToolCallDelta) *streamingToolBlock {
			hasStreamIndex := toolCall.Index != nil
			streamIndex := 0
			if hasStreamIndex {
				streamIndex = *toolCall.Index
			}
			name := ""
			if toolCall.Function != nil {
				name = toolCall.Function.Name
			} else if toolCall.Custom != nil {
				name = toolCall.Custom.Name
			}
			var block *streamingToolBlock
			if hasStreamIndex {
				block = toolCallBlocksByIndex[streamIndex]
			}
			if block == nil && toolCall.ID != "" {
				block = toolCallBlocksByID[toolCall.ID]
			}
			if block == nil {
				// Note: the "input" fallback here should/must not be taken. In
				// case the LLM makes up a tool we don't know about, we at least
				// have a place to stash our stuff.
				customInputProperty := ""
				hasCustomInput := false
				if toolCall.Custom != nil && toolCall.Function == nil {
					customInputProperty = grammarToolInputProperties[name]
					if customInputProperty == "" {
						customInputProperty = "input"
					}
					hasCustomInput = true
				}
				block = &streamingToolBlock{
					content: types.NewToolCall(toolCall.ID, name, nil),
				}
				if hasCustomInput {
					block.customInput = &streamingCustomInput{
						property:   customInputProperty,
						jsonBuffer: GrammarToolInputJSONBuffer{},
					}
					block.content.Arguments = map[string]any{customInputProperty: ""}
				} else {
					block.hasPartialArgs = true
				}
				if hasStreamIndex {
					block.streamIndex = streamIndex
					block.hasStreamIndex = true
					toolCallBlocksByIndex[streamIndex] = block
				}
				if toolCall.ID != "" {
					toolCallBlocksByID[toolCall.ID] = block
				}
				blocks = append(blocks, block)
				push(types.AssistantMessageEvent{Type: types.EventToolCallStart, ContentIndex: getContentIndex(block), Partial: output})
			}
			if hasStreamIndex && !block.hasStreamIndex {
				block.streamIndex = streamIndex
				block.hasStreamIndex = true
				toolCallBlocksByIndex[streamIndex] = block
			}
			if toolCall.ID != "" {
				toolCallBlocksByID[toolCall.ID] = block
			}
			if block.content.Name == "" && name != "" {
				block.content.Name = name
			}
			if toolCall.Custom != nil && toolCall.Function == nil && block.customInput == nil {
				customInputProperty := grammarToolInputProperties[block.content.Name]
				if customInputProperty == "" {
					customInputProperty = "input"
				}
				block.content.Arguments = map[string]any{customInputProperty: ""}
				block.customInput = &streamingCustomInput{
					property:   customInputProperty,
					jsonBuffer: GrammarToolInputJSONBuffer{},
				}
				block.hasPartialArgs = false
			}
			return block
		}

		finishBlock := func(block streamingBlock) {
			contentIndex := getContentIndex(block)
			if contentIndex == -1 {
				return
			}
			switch b := block.(type) {
			case *streamingTextBlock:
				push(types.AssistantMessageEvent{Type: types.EventTextEnd, ContentIndex: contentIndex, Content: b.content.Text, Partial: output})
			case *streamingThinkingBlock:
				applyStreamedReasoningDetails(b)
				push(types.AssistantMessageEvent{Type: types.EventThinkingEnd, ContentIndex: contentIndex, Content: b.content.Thinking, Partial: output})
			case *streamingToolBlock:
				if b.customInput != nil {
					delta := appendCustomToolCallInput(b, getCustomToolCallInput(b), true)
					if delta != nil {
						push(types.AssistantMessageEvent{Type: types.EventToolCallDelta, ContentIndex: contentIndex, Delta: *delta, Partial: output})
					}
				} else if b.hasPartialArgs {
					b.content.Arguments, b.content.ArgumentsError = utils.ParseToolCallArguments(b.partialArgs)
				}
				b.finalize()
				toolCall := b.content
				push(types.AssistantMessageEvent{Type: types.EventToolCallEnd, ContentIndex: contentIndex, ToolCall: &toolCall, Partial: output})
			}
		}

		processChunk := func(chunk *chatCompletionChunk) {
			// OpenAI documents ChatCompletionChunk.id as the unique chat
			// completion identifier; each chunk in a streamed completion
			// carries the same id.
			if output.ResponseID == "" {
				output.ResponseID = chunk.ID
			}
			if chunk.Model != "" && chunk.Model != model.ID && output.ResponseModel == "" {
				output.ResponseModel = chunk.Model
			}
			if chunk.Usage != nil {
				output.Usage = parseChunkUsage(chunk.Usage, model)
			}

			var choice *chatCompletionChunkChoice
			if len(chunk.Choices) > 0 {
				choice = &chunk.Choices[0]
			}
			if choice == nil {
				return
			}
			// Fallback: some providers (e.g. Moonshot) return usage in
			// choice.usage instead of the standard chunk.usage.
			if chunk.Usage == nil && choice.Usage != nil {
				output.Usage = parseChunkUsage(choice.Usage, model)
			}
			if choice.FinishReason != nil {
				output.RawStopReason = *choice.FinishReason
				stopReason, errorMessage := mapStopReason(*choice.FinishReason)
				output.StopReason = stopReason
				if errorMessage != "" {
					output.ErrorMessage = errorMessage
				}
				hasFinishReason = true
			}

			delta := choice.Delta
			// Handle reasoning before content: some proxies (e.g. LiteLLM)
			// aggregate reasoning and answer into one delta, and the thinking
			// block must precede the text block.
			// Some endpoints return reasoning in reasoning_content
			// (llama.cpp), or reasoning (other openai compatible endpoints).
			// Use the first non-empty reasoning field to avoid duplication
			// (e.g. chutes.ai returns both with the same content).
			deltaFields := []struct {
				name  string
				value *string
			}{
				{"reasoning_content", delta.ReasoningContent},
				{"reasoning", delta.Reasoning},
				{"reasoning_text", delta.ReasoningText},
			}
			for _, field := range deltaFields {
				if field.value == nil || *field.value == "" {
					continue
				}
				thinkingSignature := field.name
				if model.Provider == "opencode-go" && field.name == "reasoning" {
					thinkingSignature = "reasoning_content"
				}
				block := ensureThinkingBlock(thinkingSignature)
				block.content.Thinking += *field.value
				push(types.AssistantMessageEvent{Type: types.EventThinkingDelta, ContentIndex: getContentIndex(block), Delta: *field.value, Partial: output})
				break
			}

			if len(delta.ReasoningDetails) > 0 {
				for _, detail := range delta.ReasoningDetails {
					if !isOpenAIReasoningDetail(detail) {
						continue
					}
					ensureThinkingBlock("")
					// Keep provider replay data in the existing signature slot.
					// OpenRouter streams reasoning_details as deltas: consecutive
					// text/summary deltas are merged into logical entries, while
					// encrypted entries remain opaque and discrete.
					streamedReasoningDetails = appendOpenAIReasoningDetail(streamedReasoningDetails, detail.(map[string]any))
				}
			}

			if delta.Content != nil && *delta.Content != "" {
				block := ensureTextBlock()
				block.content.Text += *delta.Content
				push(types.AssistantMessageEvent{Type: types.EventTextDelta, ContentIndex: getContentIndex(block), Delta: *delta.Content, Partial: output})
			}

			for _, toolCall := range delta.ToolCalls {
				block := ensureToolCallBlock(toolCall)
				if block.content.ID == "" && toolCall.ID != "" {
					block.content.ID = toolCall.ID
					toolCallBlocksByID[toolCall.ID] = block
				}
				name := ""
				if toolCall.Function != nil {
					name = toolCall.Function.Name
				} else if toolCall.Custom != nil {
					name = toolCall.Custom.Name
				}
				if block.content.Name == "" && name != "" {
					block.content.Name = name
				}
				var deltaStr string
				if toolCall.Function != nil && toolCall.Function.Arguments != "" {
					deltaStr = toolCall.Function.Arguments
					block.partialArgs += toolCall.Function.Arguments
					block.content.Arguments = utils.ParseStreamingJSON(&block.partialArgs)
				} else if toolCall.Custom != nil && toolCall.Custom.Input != "" {
					nextInput := getCustomToolCallInput(block) + toolCall.Custom.Input
					if d := appendCustomToolCallInput(block, nextInput, false); d != nil {
						deltaStr = *d
					}
				}
				push(types.AssistantMessageEvent{Type: types.EventToolCallDelta, ContentIndex: getContentIndex(block), Delta: deltaStr, Partial: output})
			}
		}

		err = streamSSEChunks(resp, func(data string) error {
			var chunk chatCompletionChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				return err
			}
			processChunk(&chunk)
			return nil
		})
		if err != nil {
			// The openai SDK surfaces a fired AbortSignal as its abort error
			// instead of the underlying read failure; ctx cancellation closing
			// the response body is the Go counterpart, so report the abort
			// error when the caller cancelled.
			if ctx.Err() != nil {
				fail(&utils.AbortError{})
			} else {
				fail(err)
			}
			return
		}

		for _, block := range blocks {
			finishBlock(block)
		}
		// pi: if (options?.signal?.aborted) throw new Error("Request was aborted").
		if ctx.Err() != nil {
			fail(&utils.AbortError{})
			return
		}
		// pi: if (output.stopReason === "aborted") throw new Error("Request was aborted").
		if output.StopReason == types.StopReasonAborted {
			fail(fmt.Errorf("Request was aborted"))
			return
		}

		if !hasFinishReason && !compat.supportsFinishReason {
			hasToolUse := false
			for _, c := range output.Content {
				if c.ContentKind() == types.KindToolCall {
					hasToolUse = true
					break
				}
			}
			if hasToolUse {
				output.StopReason = types.StopReasonToolUse
			} else {
				output.StopReason = types.StopReasonStop
			}
		}
		if output.StopReason == types.StopReasonError {
			message := output.ErrorMessage
			if message == "" {
				message = "Provider returned an error stop reason"
			}
			fail(fmt.Errorf("%s", message))
			return
		}
		if (compat.supportsFinishReason && !hasFinishReason) || output.StopReason == types.StopReasonPending {
			fail(fmt.Errorf("Stream ended without finish_reason"))
			return
		}

		// pi: blocks share output.content by reference, so nothing to assemble
		// here — the done event's push snapshot carries the finalized blocks.
		push(types.AssistantMessageEvent{Type: types.EventDone, Reason: output.StopReason, Message: output})
		stream.EndWith(*output)
	}()

	return stream
}

// finalize strips the scratch buffers from a tool block so replay only carries
// parsed arguments (pi: delete block.partialArgs/customInput/streamIndex).
func (b *streamingToolBlock) finalize() {
	b.hasPartialArgs = false
	b.partialArgs = ""
	b.customInput = nil
	b.hasStreamIndex = false
	b.streamIndex = 0
}

// providerRawMetadata extracts error.metadata.raw from a provider error
// surface (pi: (error as any)?.error?.metadata?.raw). Only *ProviderHTTPError
// with a raw JSON body of shape {error:{metadata:{raw}}} yields a value.
func providerRawMetadata(err error) string {
	httpErr, ok := err.(*utils.ProviderHTTPError)
	if !ok || httpErr.Body == "" {
		return ""
	}
	var parsed struct {
		Error struct {
			Metadata struct {
				Raw string `json:"raw"`
			} `json:"metadata"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(httpErr.Body), &parsed); err != nil {
		return ""
	}
	return parsed.Error.Metadata.Raw
}
