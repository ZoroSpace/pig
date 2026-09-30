// pi: api/openai-responses-shared.ts.
//
// Message/tool conversion and SSE event reduction shared by the
// openai-responses adapter (and reused by the Azure / Codex Responses
// variants as they arrive). The openai SDK's ResponseInput /
// ResponseStreamEvent types have no Go counterpart: input items are built as
// JSON maps and stream events are decoded locally. Reasoning items replay
// verbatim: the raw JSON received on output_item.done is stored as the
// thinking block's signature and pushed back into the input untouched (pi:
// JSON.parse(block.thinkingSignature) of JSON.stringify(item)).
//
// The stream reducer mirrors pi's shared-array invariant via the same
// snapshot mechanism as openai_completions_stream.go (declared deviation:
// events carry a content snapshot at push time, not a live view).
package api

import (
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// =============================================================================
// Utilities
// =============================================================================

// encodeTextSignatureV1 serializes the structured text signature payload (pi:
// encodeTextSignatureV1). An empty phase omits the key.
func encodeTextSignatureV1(id string, phase string) string {
	payload := types.TextSignatureV1{V: 1, ID: id}
	if phase != "" {
		payload.Phase = phase
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

// parseTextSignature decodes a text signature, accepting both the structured
// TextSignatureV1 JSON and the legacy plain-id string (pi:
// parseTextSignature). ok is false for an empty signature.
func parseTextSignature(signature string) (id string, phase string, ok bool) {
	if signature == "" {
		return "", "", false
	}
	if strings.HasPrefix(signature, "{") {
		var parsed types.TextSignatureV1
		if err := json.Unmarshal([]byte(signature), &parsed); err == nil && parsed.V == 1 && parsed.ID != "" {
			// pi also requires typeof parsed.id === "string"; JSON cannot
			// produce a non-string id, so the non-empty check stands in.
			if parsed.Phase == "commentary" || parsed.Phase == "final_answer" {
				return parsed.ID, parsed.Phase, true
			}
			return parsed.ID, "", true
		}
		// Fall through to legacy plain-string handling.
	}
	return signature, "", true
}

// convertResponsesToolResultOutput renders tool-result content as the
// function_call_output payload: a plain string, or an input_text/input_image
// part array when the model takes images (pi: convertToolResultOutput).
func convertResponsesToolResultOutput(model *types.Model, content []types.Content) any {
	var texts []string
	var images []types.ImageContent
	for _, c := range content {
		if t, ok := c.(types.TextContent); ok {
			texts = append(texts, t.Text)
		}
	}
	for _, c := range content {
		if c.ContentKind() == types.KindImage {
			images = append(images, c.(types.ImageContent))
		}
	}
	textResult := strings.Join(texts, "\n")
	hasText := textResult != ""

	if len(images) == 0 || !modelSupportsImages(model) {
		if hasText {
			return utils.SanitizeSurrogates(textResult)
		}
		if len(images) > 0 {
			return utils.SanitizeSurrogates("(see attached image)")
		}
		return utils.SanitizeSurrogates("(no tool output)")
	}

	var output []map[string]any
	if hasText {
		output = append(output, map[string]any{"type": "input_text", "text": utils.SanitizeSurrogates(textResult)})
	}
	for _, image := range images {
		output = append(output, map[string]any{
			"type":      "input_image",
			"detail":    "auto",
			"image_url": fmt.Sprintf("data:%s;base64,%s", image.MimeType, image.Data),
		})
	}
	return output
}

// OpenAIResponsesStreamOptions are the stream-reduction options (pi:
// OpenAIResponsesStreamOptions). ServiceTier is the request's service_tier
// value ("flex" | "priority" | ...).
type OpenAIResponsesStreamOptions struct {
	ServiceTier string
	// GrammarToolInputProperties maps grammar-constrained tool names to
	// their string input property (pi: grammarToolInputProperties).
	GrammarToolInputProperties map[string]string
	// ResolveServiceTier lets callers override the service tier applied to
	// pricing; nil uses the response tier, falling back to the request tier.
	ResolveServiceTier func(responseServiceTier, requestServiceTier string) string
	// ApplyServiceTierPricing adjusts usage cost for the served tier.
	ApplyServiceTierPricing func(usage *types.Usage, serviceTier string)
}

// ConvertResponsesMessagesOptions are the message-conversion options (pi:
// ConvertResponsesMessagesOptions).
type ConvertResponsesMessagesOptions struct {
	IncludeSystemPrompt        *bool
	GrammarToolInputProperties map[string]string
	// SupportsMidConvoSystemMessages: whether later system messages are sent
	// in place; otherwise they are folded into the leading prompt.
	SupportsMidConvoSystemMessages *bool
	SupportsAdditionalTools        *bool
	SupportsToolSearch             *bool
	ToolOptions                    *ConvertResponsesToolsOptions
}

// ConvertResponsesToolsOptions are the tool-conversion options (pi:
// ConvertResponsesToolsOptions). pi's strict member is boolean|null; the
// openai-responses adapters never pass it (nil resolves to false, pi's
// undefined path); the Codex adapter passes null via StrictNull.
type ConvertResponsesToolsOptions struct {
	Strict *bool
	// StrictNull expresses pi's strict: null: the default strict value is
	// null, serialized as JSON null on tools without constrained sampling
	// (constrained sampling still overrides with a boolean). Mutually
	// exclusive with Strict.
	StrictNull                 bool
	SupportsStrictMode         *bool
	SupportsOpenAIGrammarTools *bool
	ToolSearchResult           bool
}

// =============================================================================
// Message conversion
// =============================================================================

// ConvertResponsesMessages converts the LLM context into Responses API input
// items (pi: convertResponsesMessages). AllowedToolCallProviders carries the
// provider ids whose tool-call ids keep their call_id|item_id pair shape.
func ConvertResponsesMessages(model *types.Model, llmContext *types.TranscriptContext, allowedToolCallProviders map[string]struct{}, options *ConvertResponsesMessagesOptions) []any {
	messages := []any{}

	normalizeIDPart := func(part string) string {
		var b strings.Builder
		for _, r := range part {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				b.WriteRune(r)
			} else {
				b.WriteByte('_')
			}
		}
		sanitized := b.String()
		// OpenAI requires ids of at most 64 characters. pi slices UTF-16
		// code units; provider ids are ASCII, so byte slicing matches.
		if len(sanitized) > 64 {
			sanitized = sanitized[:64]
		}
		return strings.TrimRight(sanitized, "_")
	}

	buildForeignResponsesItemID := func(itemID string) string {
		normalized := "fc_" + utils.ShortHash(itemID)
		if len(normalized) > 64 {
			normalized = normalized[:64]
		}
		return normalized
	}

	normalizeToolCallID := func(id string, _ *types.Model, source *types.AssistantMessage) string {
		if _, allowed := allowedToolCallProviders[string(model.Provider)]; !allowed {
			return normalizeIDPart(id)
		}
		if !strings.Contains(id, "|") {
			return normalizeIDPart(id)
		}
		parts := strings.SplitN(id, "|", 2)
		callID, itemID := parts[0], parts[1]
		normalizedCallID := normalizeIDPart(callID)
		isForeignToolCall := source.Provider != model.Provider || source.API != model.API
		normalizedItemID := ""
		if isForeignToolCall {
			normalizedItemID = buildForeignResponsesItemID(itemID)
		} else {
			normalizedItemID = normalizeIDPart(itemID)
		}
		// OpenAI Responses API requires item id to start with "fc"
		if !strings.HasPrefix(normalizedItemID, "fc_") {
			normalizedItemID = normalizeIDPart("fc_" + normalizedItemID)
		}
		return normalizedCallID + "|" + normalizedItemID
	}

	normalizedContext := llmContext
	supportsMidConvo := false
	if options != nil && options.SupportsMidConvoSystemMessages != nil {
		supportsMidConvo = *options.SupportsMidConvoSystemMessages
	}
	normalizedContext = utils.ResolveTranscript(llmContext, supportsMidConvo)
	transformedMessages := TransformMessages(normalizedContext.Messages, model, normalizeToolCallID)
	transcriptTools := utils.ResolveTranscriptTools(
		normalizedContext.Messages,
		(options != nil && ((options.SupportsAdditionalTools != nil && *options.SupportsAdditionalTools) || (options.SupportsToolSearch != nil && *options.SupportsToolSearch))),
	)
	appendSystemToolAdditions := func(message *types.SystemMessage, seed string) {
		tools := []types.Tool{}
		if transcriptTools.AnchorsAdditions {
			tools = message.ToolsAdded
		}
		if len(tools) == 0 {
			return
		}
		if options != nil && options.SupportsAdditionalTools != nil && *options.SupportsAdditionalTools {
			messages = append(messages, map[string]any{
				"type":  "additional_tools",
				"role":  "developer",
				"tools": ConvertResponsesTools(tools, options.ToolOptions),
			})
			return
		}
		if options == nil || options.SupportsToolSearch == nil || !*options.SupportsToolSearch {
			return
		}
		names := make([]string, 0, len(tools))
		for _, tool := range tools {
			names = append(names, tool.Name)
		}
		callID := "pi_tool_load_" + utils.ShortHash(fmt.Sprintf("%s:%s", seed, strings.Join(names, ",")))
		messages = append(messages, map[string]any{
			"type":      "tool_search_call",
			"call_id":   callID,
			"execution": "client",
			"status":    "completed",
			"arguments": map[string]any{"query": strings.Join(names, " "), "limit": len(names)},
		})
		searchOptions := ConvertResponsesToolsOptions{}
		if options.ToolOptions != nil {
			searchOptions = *options.ToolOptions
		}
		searchOptions.ToolSearchResult = true
		messages = append(messages, map[string]any{
			"type":      "tool_search_output",
			"call_id":   callID,
			"execution": "client",
			"status":    "completed",
			"tools":     ConvertResponsesTools(tools, &searchOptions),
		})
	}
	includeInitialSystemMessage := true
	if options != nil && options.IncludeSystemPrompt != nil {
		includeInitialSystemMessage = *options.IncludeSystemPrompt
	}
	instructionRole := "system"
	supportsDeveloperRole := true
	if compat, ok := model.Compat.(*types.OpenAIResponsesCompat); ok && compat != nil &&
		compat.SupportsDeveloperRole != nil {
		supportsDeveloperRole = *compat.SupportsDeveloperRole
	}
	if model.Reasoning && supportsDeveloperRole {
		instructionRole = "developer"
	}

	msgIndex := 0
	sourceIndex := 0
	for _, msg := range transformedMessages {
		isLeadingSystemMessage := sourceIndex == 0 && msg.MessageRole() == types.RoleSystem
		sourceIndex++
		if system, ok := msg.(types.SystemMessage); ok {
			if !isLeadingSystemMessage {
				appendSystemToolAdditions(&system, fmt.Sprintf("system:%d", msgIndex))
			}
			if !isLeadingSystemMessage || includeInitialSystemMessage {
				text := ""
				if isLeadingSystemMessage {
					text = utils.GetSystemMessageText(&system)
				} else {
					text = utils.RenderSystemMessageUpdate(&system)
				}
				if len(text) > 0 {
					messages = append(messages, map[string]any{
						"role":    instructionRole,
						"content": utils.SanitizeSurrogates(text),
					})
				}
			}
		} else {
			switch m := msg.(type) {
			case types.UserMessage:
				if m.Content.Parts == nil {
					messages = append(messages, map[string]any{
						"role": "user",
						"content": []any{
							map[string]any{"type": "input_text", "text": utils.SanitizeSurrogates(m.Content.String)},
						},
					})
				} else {
					content := make([]any, 0, len(m.Content.Parts))
					for _, item := range m.Content.Parts {
						if text, ok := item.(types.TextContent); ok {
							content = append(content, map[string]any{
								"type": "input_text",
								"text": utils.SanitizeSurrogates(text.Text),
							})
							continue
						}
						image := item.(types.ImageContent)
						content = append(content, map[string]any{
							"type":      "input_image",
							"detail":    "auto",
							"image_url": fmt.Sprintf("data:%s;base64,%s", image.MimeType, image.Data),
						})
					}
					if len(content) == 0 {
						continue
					}
					messages = append(messages, map[string]any{"role": "user", "content": content})
				}
			case types.AssistantMessage:
				var output []any
				isSameProviderAndAPI := m.Provider == model.Provider && m.API == model.API
				isSameModel := isSameProviderAndAPI && m.Model == model.ID
				isDifferentModel := isSameProviderAndAPI && m.Model != model.ID
				textBlockIndex := 0

				for _, block := range m.Content {
					switch b := block.(type) {
					case types.ThinkingContent:
						if b.ThinkingSignature != "" {
							var reasoningItem any
							if err := json.Unmarshal([]byte(b.ThinkingSignature), &reasoningItem); err == nil {
								output = append(output, reasoningItem)
							}
						}
					case types.TextContent:
						sigID, sigPhase, hasSig := parseTextSignature(b.TextSignature)
						fallbackMessageID := fmt.Sprintf("msg_pi_%d", msgIndex)
						if textBlockIndex > 0 {
							fallbackMessageID = fmt.Sprintf("msg_pi_%d_%d", msgIndex, textBlockIndex)
						}
						textBlockIndex++
						// OpenAI requires id to be max 64 characters
						msgID := fallbackMessageID
						if hasSig && sigID != "" {
							msgID = sigID
						}
						if len(msgID) > 64 {
							msgID = "msg_" + utils.ShortHash(msgID)
						}
						messageItem := map[string]any{
							"type":    "message",
							"role":    "assistant",
							"content": []any{map[string]any{"type": "output_text", "text": utils.SanitizeSurrogates(b.Text), "annotations": []any{}}},
							"status":  "completed",
							"id":      msgID,
						}
						if hasSig && sigPhase != "" {
							messageItem["phase"] = sigPhase
						}
						output = append(output, messageItem)
					case types.ToolCall:
						toolCall := b
						parts := strings.SplitN(toolCall.ID, "|", 2)
						callID := parts[0]
						itemID := ""
						hasItemID := len(parts) > 1
						if hasItemID {
							itemID = parts[1]
						}
						var customInputProperty string
						hasCustomInputProperty := false
						if options != nil && options.GrammarToolInputProperties != nil {
							customInputProperty, hasCustomInputProperty = options.GrammarToolInputProperties[toolCall.Name]
						}

						// For different-model messages, set id to undefined to avoid pairing validation.
						// OpenAI tracks which fc_xxx IDs were paired with rs_xxx reasoning items.
						// By omitting the id, we avoid triggering that validation (like cross-provider does).
						// When replaying custom-tool calls as a function_call, also drop non-fc_* ids such as
						// ctc_* custom-tool ids because function_call item ids must be fc_*.
						itemIDIsFC := hasItemID && strings.HasPrefix(itemID, "fc_")
						if (isDifferentModel && itemIDIsFC) || (!hasCustomInputProperty && !itemIDIsFC) {
							hasItemID = false
							itemID = ""
						}

						// pi v0.86.0: namespace replay no longer keys off the
						// deferred-tools map; only same-model calls keep it.
						canReplayNamespace := isSameModel

						if hasCustomInputProperty {
							input, err := GetGrammarToolInput(toolCall.Name, toolCall.Arguments, customInputProperty)
							if err != nil {
								panic(err)
							}
							item := map[string]any{
								"type":    "custom_tool_call",
								"call_id": callID,
								"name":    toolCall.Name,
								"input":   utils.SanitizeSurrogates(input),
							}
							if hasItemID {
								item["id"] = itemID
							}
							if canReplayNamespace && toolCall.Namespace != "" {
								item["namespace"] = toolCall.Namespace
							}
							output = append(output, item)
						} else {
							item := map[string]any{
								"type":      "function_call",
								"call_id":   callID,
								"name":      toolCall.Name,
								"arguments": marshalToolCallArguments(toolCall.Arguments),
							}
							if hasItemID {
								item["id"] = itemID
							}
							if canReplayNamespace && toolCall.Namespace != "" {
								item["namespace"] = toolCall.Namespace
							}
							output = append(output, item)
						}
					}
				}
				if len(output) == 0 {
					continue
				}
				messages = append(messages, output...)
			case types.ToolResultMessage:
				parts := strings.SplitN(m.ToolCallID, "|", 2)
				callID := parts[0]
				output := convertResponsesToolResultOutput(model, m.Content)

				isGrammarTool := false
				if options != nil && options.GrammarToolInputProperties != nil {
					_, isGrammarTool = options.GrammarToolInputProperties[m.ToolName]
				}
				if isGrammarTool {
					messages = append(messages, map[string]any{
						"type":    "custom_tool_call_output",
						"call_id": callID,
						"output":  output,
					})
				} else {
					messages = append(messages, map[string]any{
						"type":    "function_call_output",
						"call_id": callID,
						"output":  output,
					})
				}
			}
			if !isLeadingSystemMessage {
				msgIndex++
			}
		}
	}

	return messages
}

// toolByName found a tool by name for the removed deferred-load path; it has
// no callers since #9548.

// marshalToolCallArguments serializes tool arguments (pi:
// JSON.stringify(toolCall.arguments)); a nil map serializes as {}.
func marshalToolCallArguments(arguments map[string]any) string {
	if arguments == nil {
		return "{}"
	}
	b, err := json.Marshal(arguments)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// =============================================================================
// Tool conversion
// =============================================================================

// ConvertResponsesTools converts tool definitions into Responses API tool
// objects: custom grammar tools or function tools (pi: convertResponsesTools).
func ConvertResponsesTools(tools []types.Tool, options *ConvertResponsesToolsOptions) []any {
	// pi: defaultStrict = options?.strict === undefined ? false :
	// options.strict — null propagates as the JSON null strict value.
	defaultStrict := false
	defaultStrictIsNull := false
	if options != nil {
		if options.StrictNull {
			defaultStrictIsNull = true
		} else if options.Strict != nil {
			defaultStrict = *options.Strict
		}
	}
	supportsStrictMode := true
	if options != nil && options.SupportsStrictMode != nil {
		supportsStrictMode = *options.SupportsStrictMode
	}
	supportsOpenAIGrammarTools := false
	if options != nil && options.SupportsOpenAIGrammarTools != nil {
		supportsOpenAIGrammarTools = *options.SupportsOpenAIGrammarTools
	}

	out := make([]any, 0, len(tools))
	for _, tool := range tools {
		grammar, err := ResolveGrammarConstrainedSampling(&tool, supportsOpenAIGrammarTools)
		if err != nil {
			panic(err)
		}
		if grammar != nil {
			item := map[string]any{
				"type":        "custom",
				"name":        tool.Name,
				"description": tool.Description,
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     grammar.Format,
					"definition": grammar.Definition,
				},
			}
			if options != nil && options.ToolSearchResult {
				item["defer_loading"] = true
			}
			out = append(out, item)
			continue
		}

		constrainedStrict, err := ResolveJSONSchemaStrictSampling(&tool, supportsStrictMode)
		if err != nil {
			panic(err)
		}
		strict := defaultStrict
		strictIsNull := defaultStrictIsNull
		if constrainedStrict != nil {
			strict = *constrainedStrict
			strictIsNull = false
		}
		parameters, err := GetJSONSchemaToolParameters(&tool, strict)
		if err != nil {
			panic(err)
		}
		functionTool := map[string]any{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  json.RawMessage(parameters),
		}
		if options != nil && options.ToolSearchResult {
			functionTool["defer_loading"] = true
		}
		if supportsStrictMode {
			if strictIsNull {
				functionTool["strict"] = nil
			} else {
				functionTool["strict"] = strict
			}
		}
		out = append(out, functionTool)
	}
	return out
}

// =============================================================================
// Stream processing
// =============================================================================

// responsesCustomInput is the scratch buffer of a streaming custom tool call
// (pi: StreamingToolCall.customInput).
type responsesCustomInput struct {
	property   string
	jsonBuffer GrammarToolInputJSONBuffer
}

// responsesStreamBlock is one block of the in-progress response (pi's
// ResponsesOutputSlot block members). Text and thinking blocks are
// value-complete; tool call blocks carry scratch buffers (partialJson /
// customInput) stripped before replay.
type responsesStreamBlock interface{ isResponsesBlock() }

type responsesTextStreamBlock struct{ content types.TextContent }

func (*responsesTextStreamBlock) isResponsesBlock() {}

type responsesThinkingStreamBlock struct{ content types.ThinkingContent }

func (*responsesThinkingStreamBlock) isResponsesBlock() {}

type responsesToolCallStreamBlock struct {
	content        types.ToolCall
	partialJSON    string
	hasPartialJSON bool
	customInput    *responsesCustomInput
}

func (*responsesToolCallStreamBlock) isResponsesBlock() {}

func getCustomToolCallInput(block *responsesToolCallStreamBlock) string {
	if block.customInput == nil {
		return ""
	}
	value, _ := block.content.Arguments[block.customInput.property].(string)
	return value
}

func appendCustomToolCallInput(block *responsesToolCallStreamBlock, nextInput string, close bool) *string {
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

// responsesTextPart is a reasoning summary/content part (pi:
// ResponseReasoningSummaryPart and reasoning text part).
type responsesTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// responsesContentPart is a message output content part (pi:
// ResponseOutputMessage.content members).
type responsesContentPart struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

// responsesOutputItem is the polymorphic output item carried by
// output_item.added/done events and response.output arrays (pi:
// ResponseOutputItem). Raw keeps the verbatim JSON of reasoning items for
// signature replay.
type responsesOutputItem struct {
	Raw              json.RawMessage        `json:"-"`
	Type             string                 `json:"type"`
	ID               string                 `json:"id"`
	Role             string                 `json:"role"`
	Status           string                 `json:"status"`
	Phase            string                 `json:"phase"`
	CallID           string                 `json:"call_id"`
	Name             string                 `json:"name"`
	Arguments        string                 `json:"arguments"`
	Input            string                 `json:"input"`
	Namespace        string                 `json:"namespace"`
	EncryptedContent string                 `json:"encrypted_content"`
	Summary          []responsesTextPart    `json:"summary"`
	Content          []responsesContentPart `json:"content"`
}

func (i *responsesOutputItem) UnmarshalJSON(data []byte) error {
	type alias responsesOutputItem
	if err := json.Unmarshal(data, (*alias)(i)); err != nil {
		return err
	}
	i.Raw = append(i.Raw, data...)
	return nil
}

// responsesResponseObject is the response payload of terminal events (pi:
// ResponseStreamEvent.response). ServiceTier is nil when the field is absent
// (pi: response?.service_tier ?? request tier).
type responsesResponseObject struct {
	ID                string                      `json:"id"`
	Status            string                      `json:"status"`
	Error             *responsesResponseError     `json:"error"`
	IncompleteDetails *responsesIncompleteDetails `json:"incomplete_details"`
	ServiceTier       *string                     `json:"service_tier"`
	Usage             *responsesUsage             `json:"usage"`
	Output            []*responsesOutputItem      `json:"output"`
	// EndTurn is the Codex backend's end_turn flag on terminal response
	// events (pi: response.end_turn, read by mapCodexEvents only).
	EndTurn *bool `json:"end_turn,omitempty"`
}

type responsesResponseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type responsesIncompleteDetails struct {
	Reason string `json:"reason"`
}

type responsesUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	InputTokensDetails *struct {
		CachedTokens     int64 `json:"cached_tokens"`
		CacheWriteTokens int64 `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails *struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

// responsesStreamEvent is one SSE event of the Responses stream (pi:
// ResponseStreamEvent). Fields not named by the event's type stay zero.
type responsesStreamEvent struct {
	Type           string                   `json:"type"`
	SequenceNumber *int64                   `json:"sequence_number"`
	Response       *responsesResponseObject `json:"response"`
	OutputIndex    int                      `json:"output_index"`
	ItemID         string                   `json:"item_id"`
	Item           *responsesOutputItem     `json:"item"`
	Delta          string                   `json:"delta"`
	Arguments      string                   `json:"arguments"`
	Input          string                   `json:"input"`
	Code           string                   `json:"code"`
	Message        string                   `json:"message"`
	// NestedError carries a top-level `error` object on Codex error events
	// (pi: event.error, read by extractCodexEventError only).
	NestedError *responsesResponseError `json:"error,omitempty"`
}

// responsesOutputSlot pairs one in-progress block with its output index (pi:
// the ResponsesOutputSlot union).
type responsesOutputSlot struct {
	kind         string // "thinking" | "text" | "toolCall"
	thinking     *responsesThinkingStreamBlock
	text         *responsesTextStreamBlock
	toolCall     *responsesToolCallStreamBlock
	contentIndex int
}

// responsesStopMapping is mapStopReason's return (pi:
// { stopReason, errorMessage? }).
type responsesStopMapping struct {
	stopReason   types.StopReason
	errorMessage string
}

// ProcessResponsesStream reduces the Responses SSE event sequence into
// AssistantMessageEvents (pi: processResponsesStream). events yields decoded
// events in wire order.
func ProcessResponsesStream(events iter.Seq[*responsesStreamEvent], output *types.AssistantMessage, stream *types.AssistantMessageEventStream, model *types.Model, options *OpenAIResponsesStreamOptions) error {
	sawTerminalResponseEvent := false
	outputSlots := map[int]*responsesOutputSlot{}
	reasoningBlocksByID := map[string]*responsesThinkingStreamBlock{}

	// blocks backs output.Content: every created block lands here and later
	// deltas mutate it in place (pi: the slot block members are the entries
	// in output.content). syncContent + push snapshot the content and the
	// partial with each event — pi's single event loop shares the live
	// output by reference, while pig's consumer runs on another goroutine
	// (declared deviation, see openai_completions_stream.go).
	var blocks []responsesStreamBlock
	syncContent := func() {
		content := make([]types.Content, len(blocks))
		for i, block := range blocks {
			switch b := block.(type) {
			case *responsesTextStreamBlock:
				content[i] = b.content
			case *responsesThinkingStreamBlock:
				content[i] = b.content
			case *responsesToolCallStreamBlock:
				content[i] = b.content
			}
		}
		output.Content = content
	}
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

	applyMessagePhaseStopReason := func(item *responsesOutputItem) {
		if item.Type == "message" && item.Phase == "final_answer" {
			output.StopReason = types.StopReasonStop
		}
	}
	getSlot := func(outputIndex int, kind string) *responsesOutputSlot {
		slot := outputSlots[outputIndex]
		if slot == nil || slot.kind != kind {
			return nil
		}
		return slot
	}
	pushToolCallDelta := func(slot *responsesOutputSlot, delta *string) {
		if delta == nil {
			return
		}
		push(types.AssistantMessageEvent{
			Type:         types.EventToolCallDelta,
			ContentIndex: slot.contentIndex,
			Delta:        *delta,
			Partial:      output,
		})
	}
	createSlot := func(outputIndex int, item *responsesOutputItem) *responsesOutputSlot {
		switch item.Type {
		case "reasoning":
			block := &responsesThinkingStreamBlock{content: types.NewThinkingContent("")}
			blocks = append(blocks, block)
			slot := &responsesOutputSlot{kind: "thinking", thinking: block, contentIndex: len(blocks) - 1}
			outputSlots[outputIndex] = slot
			push(types.AssistantMessageEvent{Type: types.EventThinkingStart, ContentIndex: slot.contentIndex, Partial: output})
			return slot
		case "message":
			applyMessagePhaseStopReason(item)
			block := &responsesTextStreamBlock{content: types.NewTextContent("")}
			blocks = append(blocks, block)
			slot := &responsesOutputSlot{kind: "text", text: block, contentIndex: len(blocks) - 1}
			outputSlots[outputIndex] = slot
			push(types.AssistantMessageEvent{Type: types.EventTextStart, ContentIndex: slot.contentIndex, Partial: output})
			return slot
		case "function_call":
			block := &responsesToolCallStreamBlock{
				content: types.ToolCall{
					Kind:      types.KindToolCall,
					ID:        item.CallID + "|" + item.ID,
					Name:      item.Name,
					Arguments: map[string]any{},
				},
				partialJSON:    item.Arguments,
				hasPartialJSON: true,
			}
			if item.Namespace != "" {
				block.content.Namespace = item.Namespace
			}
			blocks = append(blocks, block)
			slot := &responsesOutputSlot{kind: "toolCall", toolCall: block, contentIndex: len(blocks) - 1}
			outputSlots[outputIndex] = slot
			push(types.AssistantMessageEvent{Type: types.EventToolCallStart, ContentIndex: slot.contentIndex, Partial: output})
			return slot
		case "custom_tool_call":
			inputProperty := "input"
			if options != nil {
				if prop, ok := options.GrammarToolInputProperties[item.Name]; ok {
					inputProperty = prop
				}
			}
			block := &responsesToolCallStreamBlock{
				content: types.ToolCall{
					Kind:      types.KindToolCall,
					ID:        item.CallID + "|" + item.ID,
					Name:      item.Name,
					Arguments: map[string]any{inputProperty: item.Input},
				},
				customInput: &responsesCustomInput{
					property:   inputProperty,
					jsonBuffer: GrammarToolInputJSONBuffer{},
				},
			}
			if item.Namespace != "" {
				block.content.Namespace = item.Namespace
			}
			blocks = append(blocks, block)
			slot := &responsesOutputSlot{kind: "toolCall", toolCall: block, contentIndex: len(blocks) - 1}
			outputSlots[outputIndex] = slot
			push(types.AssistantMessageEvent{Type: types.EventToolCallStart, ContentIndex: slot.contentIndex, Partial: output})
			return slot
		}
		return nil
	}
	getOrCreateSlot := func(outputIndex int, item *responsesOutputItem) *responsesOutputSlot {
		if slot := outputSlots[outputIndex]; slot != nil {
			return slot
		}
		return createSlot(outputIndex, item)
	}
	// Azure OpenAI can omit reasoning.encrypted_content from response.output_item.done
	// and provide it only in response.completed.response.output. Backfill the
	// persisted reasoning signature from the terminal response to keep store:false
	// multi-turn replay stateless. See https://github.com/earendil-works/pi/issues/6409.
	backfillReasoningSignatures := func(responseOutput []*responsesOutputItem) {
		for _, item := range responseOutput {
			if item.Type != "reasoning" || item.EncryptedContent == "" {
				continue
			}
			block := reasoningBlocksByID[item.ID]
			if block == nil || block.content.ThinkingSignature == "" {
				continue
			}

			var storedItem map[string]any
			if err := json.Unmarshal([]byte(block.content.ThinkingSignature), &storedItem); err != nil {
				continue
			}
			if encrypted, present := storedItem["encrypted_content"]; present {
				if s, ok := encrypted.(string); ok && s != "" {
					continue
				}
			}
			storedItem["encrypted_content"] = item.EncryptedContent
			b, _ := json.Marshal(storedItem)
			block.content.ThinkingSignature = string(b)
		}
	}
	finalizeResponse := func(response *responsesResponseObject) {
		sawTerminalResponseEvent = true
		syncContent()
		if response != nil {
			backfillReasoningSignatures(response.Output)
			if response.ID != "" {
				output.ResponseID = response.ID
			}
			if response.Usage != nil {
				cachedTokens := int64(0)
				cacheWriteTokens := int64(0)
				if response.Usage.InputTokensDetails != nil {
					cachedTokens = response.Usage.InputTokensDetails.CachedTokens
					cacheWriteTokens = response.Usage.InputTokensDetails.CacheWriteTokens
				}
				usage := types.Usage{
					// OpenAI includes cached and cache-write tokens in input_tokens, so subtract both.
					Input:       maxInt64(0, response.Usage.InputTokens-cachedTokens-cacheWriteTokens),
					Output:      response.Usage.OutputTokens,
					CacheRead:   cachedTokens,
					CacheWrite:  cacheWriteTokens,
					TotalTokens: response.Usage.TotalTokens,
					Cost:        types.Cost{},
				}
				if response.Usage.OutputTokensDetails != nil {
					usage.Reasoning = lo.ToPtr(response.Usage.OutputTokensDetails.ReasoningTokens)
				}
				output.Usage = usage
			}
		}
		ai.CalculateCost(model, &output.Usage)
		if options != nil && options.ApplyServiceTierPricing != nil {
			var serviceTier string
			if response != nil && response.ServiceTier != nil {
				serviceTier = *response.ServiceTier
			} else {
				serviceTier = options.ServiceTier
			}
			if options.ResolveServiceTier != nil {
				responseTier := ""
				if response != nil && response.ServiceTier != nil {
					responseTier = *response.ServiceTier
				}
				serviceTier = options.ResolveServiceTier(responseTier, options.ServiceTier)
			}
			options.ApplyServiceTierPricing(&output.Usage, serviceTier)
		}
		// Map status to stop reason. For incomplete responses, retain the provider's
		// specific reason so max-output truncation and content filtering stay distinct.
		status := ""
		incompleteReason := ""
		if response != nil {
			status = response.Status
			if response.IncompleteDetails != nil {
				incompleteReason = response.IncompleteDetails.Reason
			}
		}
		if incompleteReason != "" {
			output.RawStopReason = status + "." + incompleteReason
		} else {
			output.RawStopReason = status
		}
		mappedStop := mapResponsesStopReason(status, incompleteReason)
		output.StopReason = mappedStop.stopReason
		output.ErrorMessage = mappedStop.errorMessage
		hasToolCall := false
		for _, b := range output.Content {
			if b.ContentKind() == types.KindToolCall {
				hasToolCall = true
				break
			}
		}
		if hasToolCall && output.StopReason == types.StopReasonStop {
			output.StopReason = types.StopReasonToolUse
		}
		// backfillReasoningSignatures mutated the thinking block's signature
		// after the last push; refresh the content snapshot so consumers see
		// the backfilled value (pi: the block is shared by reference).
		syncContent()
	}

	for event := range events {
		switch event.Type {
		case "response.created":
			if event.Response != nil {
				output.ResponseID = event.Response.ID
			}
		case "response.output_item.added":
			if event.Item != nil {
				createSlot(event.OutputIndex, event.Item)
			}
		case "response.reasoning_summary_text.delta":
			slot := getSlot(event.OutputIndex, "thinking")
			if slot == nil {
				continue
			}
			slot.thinking.content.Thinking += event.Delta
			push(types.AssistantMessageEvent{
				Type: types.EventThinkingDelta, ContentIndex: slot.contentIndex, Delta: event.Delta, Partial: output,
			})
		case "response.reasoning_summary_part.done":
			slot := getSlot(event.OutputIndex, "thinking")
			if slot == nil {
				continue
			}
			slot.thinking.content.Thinking += "\n\n"
			push(types.AssistantMessageEvent{
				Type: types.EventThinkingDelta, ContentIndex: slot.contentIndex, Delta: "\n\n", Partial: output,
			})
		case "response.reasoning_text.delta":
			slot := getSlot(event.OutputIndex, "thinking")
			if slot == nil {
				continue
			}
			slot.thinking.content.Thinking += event.Delta
			push(types.AssistantMessageEvent{
				Type: types.EventThinkingDelta, ContentIndex: slot.contentIndex, Delta: event.Delta, Partial: output,
			})
		case "response.output_text.delta":
			slot := getSlot(event.OutputIndex, "text")
			if slot == nil {
				continue
			}
			slot.text.content.Text += event.Delta
			push(types.AssistantMessageEvent{
				Type: types.EventTextDelta, ContentIndex: slot.contentIndex, Delta: event.Delta, Partial: output,
			})
		case "response.refusal.delta":
			slot := getSlot(event.OutputIndex, "text")
			if slot == nil {
				continue
			}
			slot.text.content.Text += event.Delta
			push(types.AssistantMessageEvent{
				Type: types.EventTextDelta, ContentIndex: slot.contentIndex, Delta: event.Delta, Partial: output,
			})
		case "response.function_call_arguments.delta":
			slot := getSlot(event.OutputIndex, "toolCall")
			if slot == nil || !slot.toolCall.hasPartialJSON {
				continue
			}
			slot.toolCall.partialJSON += event.Delta
			slot.toolCall.content.Arguments = utils.ParseStreamingJSON(&slot.toolCall.partialJSON)
			pushToolCallDelta(slot, &event.Delta)
		case "response.function_call_arguments.done":
			slot := getSlot(event.OutputIndex, "toolCall")
			if slot == nil || !slot.toolCall.hasPartialJSON {
				continue
			}
			previousPartialJSON := slot.toolCall.partialJSON
			slot.toolCall.partialJSON = event.Arguments
			slot.toolCall.content.Arguments = utils.ParseStreamingJSON(&slot.toolCall.partialJSON)

			if strings.HasPrefix(event.Arguments, previousPartialJSON) {
				delta := event.Arguments[len(previousPartialJSON):]
				if delta != "" {
					pushToolCallDelta(slot, &delta)
				}
			}
		case "response.custom_tool_call_input.delta":
			slot := getSlot(event.OutputIndex, "toolCall")
			if slot == nil || slot.toolCall.customInput == nil {
				continue
			}
			next := getCustomToolCallInput(slot.toolCall) + event.Delta
			pushToolCallDelta(slot, appendCustomToolCallInput(slot.toolCall, next, false))
		case "response.custom_tool_call_input.done":
			slot := getSlot(event.OutputIndex, "toolCall")
			if slot == nil || slot.toolCall.customInput == nil {
				continue
			}
			pushToolCallDelta(slot, appendCustomToolCallInput(slot.toolCall, event.Input, true))
		case "response.output_item.done":
			item := event.Item
			if item == nil {
				continue
			}
			applyMessagePhaseStopReason(item)
			slot := getOrCreateSlot(event.OutputIndex, item)

			if item.Type == "reasoning" && slot != nil && slot.kind == "thinking" {
				summaryText := joinTextParts(item.Summary, "\n\n")
				contentText := joinContentTexts(item.Content, "\n\n")
				if summaryText != "" {
					slot.thinking.content.Thinking = summaryText
				} else if contentText != "" {
					slot.thinking.content.Thinking = contentText
				}
				slot.thinking.content.ThinkingSignature = string(item.Raw)
				reasoningBlocksByID[item.ID] = slot.thinking
				push(types.AssistantMessageEvent{
					Type: types.EventThinkingEnd, ContentIndex: slot.contentIndex,
					Content: slot.thinking.content.Thinking, Partial: output,
				})
				delete(outputSlots, event.OutputIndex)
			} else if item.Type == "message" && slot != nil && slot.kind == "text" {
				var parts []string
				for _, c := range item.Content {
					if c.Type == "output_text" {
						parts = append(parts, c.Text)
					} else {
						parts = append(parts, c.Refusal)
					}
				}
				slot.text.content.Text = strings.Join(parts, "")
				slot.text.content.TextSignature = encodeTextSignatureV1(item.ID, item.Phase)
				push(types.AssistantMessageEvent{
					Type: types.EventTextEnd, ContentIndex: slot.contentIndex,
					Content: slot.text.content.Text, Partial: output,
				})
				delete(outputSlots, event.OutputIndex)
			} else if item.Type == "function_call" && slot != nil && slot.kind == "toolCall" && slot.toolCall.hasPartialJSON {
				args := item.Arguments
				if args == "" {
					args = slot.toolCall.partialJSON
				}
				if args == "" {
					args = "{}"
				}
				slot.toolCall.content.Arguments = utils.ParseStreamingJSON(&args)
				if item.Namespace != "" {
					slot.toolCall.content.Namespace = item.Namespace
				}
				// Finalize in-place and strip the scratch buffer so replay only
				// carries parsed arguments.
				slot.toolCall.hasPartialJSON = false
				slot.toolCall.partialJSON = ""
				toolCall := slot.toolCall.content
				push(types.AssistantMessageEvent{
					Type: types.EventToolCallEnd, ContentIndex: slot.contentIndex, ToolCall: &toolCall, Partial: output,
				})
				delete(outputSlots, event.OutputIndex)
			} else if item.Type == "custom_tool_call" && slot != nil && slot.kind == "toolCall" && slot.toolCall.customInput != nil {
				next := item.Input
				if next == "" {
					next = getCustomToolCallInput(slot.toolCall)
				}
				pushToolCallDelta(slot, appendCustomToolCallInput(slot.toolCall, next, true))
				if item.Namespace != "" {
					slot.toolCall.content.Namespace = item.Namespace
				}
				slot.toolCall.customInput = nil
				toolCall := slot.toolCall.content
				push(types.AssistantMessageEvent{
					Type: types.EventToolCallEnd, ContentIndex: slot.contentIndex, ToolCall: &toolCall, Partial: output,
				})
				delete(outputSlots, event.OutputIndex)
			}
		case "response.completed", "response.incomplete":
			finalizeResponse(event.Response)
		case "error":
			// pi: `Error Code ${event.code}: ${event.message}` — the template
			// string is always truthy, so the fallback never fires.
			return fmt.Errorf("Error Code %s: %s", event.Code, event.Message)
		case "response.failed":
			sawTerminalResponseEvent = true
			if event.Response != nil {
				output.RawStopReason = event.Response.Status
			}
			msg := "Unknown error (no error details in response)"
			if event.Response != nil && event.Response.Error != nil {
				code := event.Response.Error.Code
				if code == "" {
					code = "unknown"
				}
				message := event.Response.Error.Message
				if message == "" {
					message = "no message"
				}
				msg = code + ": " + message
			} else if event.Response != nil && event.Response.IncompleteDetails != nil &&
				event.Response.IncompleteDetails.Reason != "" {
				msg = "incomplete: " + event.Response.IncompleteDetails.Reason
			}
			return fmt.Errorf("%s", msg)
		}
	}
	if !sawTerminalResponseEvent {
		return fmt.Errorf("OpenAI Responses stream ended before a terminal response event")
	}
	// The agent runs every tool call in the final message. Refuse to hand
	// over calls whose output_item.done never arrived: their arguments may
	// be cut off or mixed up, e.g. when a non-compliant server omits
	// output_index (pi v0.99.1 1b2aa0ca0, #9974). Finished calls have their
	// scratch buffers removed.
	if output.StopReason == types.StopReasonToolUse {
		for _, block := range blocks {
			toolCall, ok := block.(*responsesToolCallStreamBlock)
			if !ok {
				continue
			}
			if toolCall.hasPartialJSON || toolCall.customInput != nil {
				return fmt.Errorf("OpenAI Responses stream completed with an unfinished tool call: %s (%s)",
					toolCall.content.Name, toolCall.content.ID)
			}
		}
	}
	return nil
}

// joinTextParts joins reasoning summary texts (pi: item.summary?.map(s =>
// s.text).join(sep) || "").
func joinTextParts(parts []responsesTextPart, sep string) string {
	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		texts = append(texts, p.Text)
	}
	return strings.Join(texts, sep)
}

// joinContentTexts joins reasoning content texts (pi: item.content?.map(c =>
// c.text).join(sep) || "").
func joinContentTexts(parts []responsesContentPart, sep string) string {
	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		texts = append(texts, p.Text)
	}
	return strings.Join(texts, sep)
}

// mapResponsesStopReason maps the response status to a stop reason (pi:
// mapStopReason). pi's exhaustive switch throws on unknown statuses; pig maps
// them to stop to keep the reducer total — the arms below cover every OpenAI
// Responses status today.
func mapResponsesStopReason(status string, incompleteReason string) responsesStopMapping {
	if status == "" {
		return responsesStopMapping{stopReason: types.StopReasonStop}
	}
	switch status {
	case "completed":
		return responsesStopMapping{stopReason: types.StopReasonStop}
	case "incomplete":
		if incompleteReason == "max_output_tokens" {
			return responsesStopMapping{stopReason: types.StopReasonLength}
		}
		message := "Response incomplete without a provider reason"
		if incompleteReason != "" {
			message = "Response incomplete: " + incompleteReason
		}
		return responsesStopMapping{stopReason: types.StopReasonError, errorMessage: message}
	case "failed", "cancelled":
		return responsesStopMapping{stopReason: types.StopReasonError}
	// These two are wonky ...
	case "in_progress", "queued":
		return responsesStopMapping{stopReason: types.StopReasonStop}
	default:
		return responsesStopMapping{stopReason: types.StopReasonStop}
	}
}
