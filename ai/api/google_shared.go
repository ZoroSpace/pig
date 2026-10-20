// pi: api/google-shared.ts.
//
// Shared utilities for the Google Generative AI adapter (and, later, the
// Google Vertex adapter). The @google/genai SDK types (Content, Part,
// FinishReason, FunctionCallingConfigMode) have no Go counterpart: the wire
// structs below mirror the SDK shapes the adapter reads and writes, and the
// enum values are the SDK's string values.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
	"github.com/samber/lo"
)

// GoogleApiThinkingLevel is the thinking level for Gemini 3 models (pi:
// GoogleApiThinkingLevel — mirrors Google's ThinkingLevel enum values).
type GoogleApiThinkingLevel string

const (
	GoogleThinkingLevelUnspecified GoogleApiThinkingLevel = "THINKING_LEVEL_UNSPECIFIED"
	GoogleThinkingLevelMinimal     GoogleApiThinkingLevel = "MINIMAL"
	GoogleThinkingLevelLow         GoogleApiThinkingLevel = "LOW"
	GoogleThinkingLevelMedium      GoogleApiThinkingLevel = "MEDIUM"
	GoogleThinkingLevelHigh        GoogleApiThinkingLevel = "HIGH"
)

// FunctionCallingConfigMode enum values (pi: the SDK's
// FunctionCallingConfigMode).
const (
	googleFunctionCallingModeAuto      = "AUTO"
	googleFunctionCallingModeNone      = "NONE"
	googleFunctionCallingModeAny       = "ANY"
	googleFunctionCallingModeValidated = "VALIDATED"
)

// FinishReason enum values (pi: the SDK's FinishReason).
const (
	googleFinishReasonStop                   = "STOP"
	googleFinishReasonMaxTokens              = "MAX_TOKENS"
	googleFinishReasonBlocklist              = "BLOCKLIST"
	googleFinishReasonProhibitedContent      = "PROHIBITED_CONTENT"
	googleFinishReasonSPII                   = "SPII"
	googleFinishReasonSafety                 = "SAFETY"
	googleFinishReasonImageSafety            = "IMAGE_SAFETY"
	googleFinishReasonImageProhibitedContent = "IMAGE_PROHIBITED_CONTENT"
	googleFinishReasonImageRecitation        = "IMAGE_RECITATION"
	googleFinishReasonImageOther             = "IMAGE_OTHER"
	googleFinishReasonRecitation             = "RECITATION"
	googleFinishReasonUnspecified            = "FINISH_REASON_UNSPECIFIED"
	googleFinishReasonOther                  = "OTHER"
	googleFinishReasonLanguage               = "LANGUAGE"
	googleFinishReasonMalformedFunctionCall  = "MALFORMED_FUNCTION_CALL"
	googleFinishReasonUnexpectedToolCall     = "UNEXPECTED_TOOL_CALL"
	googleFinishReasonTooManyToolCalls       = "TOO_MANY_TOOL_CALLS"
	googleFinishReasonNoImage                = "NO_IMAGE"
)

// ResolveGoogleThinkingLevel resolves a supported pi level or model-specific
// Google mapping to a standard Google level (pi: resolveGoogleThinkingLevel).
// The returned level is one of minimal/low/medium/high. The "off" level is
// handled by the callers (v0.86.0: they disable thinking explicitly instead
// of silently mapping off → high).
func ResolveGoogleThinkingLevel(model *types.Model, level types.ModelThinkingLevel) (types.ModelThinkingLevel, error) {
	mapped := ""
	if model.ThinkingLevelMap != nil {
		if value, ok := model.ThinkingLevelMap[level]; ok && value != nil {
			mapped = strings.ToLower(*value)
		}
	}
	if mapped == "" {
		mapped = string(level)
	}
	switch mapped {
	case "minimal", "low", "medium", "high":
		return types.ModelThinkingLevel(mapped), nil
	default:
		// pi: String(mapped) — null maps to "null", missing to "undefined".
		displayed := "undefined"
		if model.ThinkingLevelMap != nil {
			if value, ok := model.ThinkingLevelMap[level]; ok {
				if value == nil {
					displayed = "null"
				} else {
					displayed = *value
				}
			}
		}
		return "", fmt.Errorf("Unsupported Google thinking level mapping for %s/%s: %s -> %s",
			model.Provider, model.ID, level, displayed)
	}
}

// googlePart is one Gemini response/request part (pi: the SDK Part fields the
// adapter reads and writes). Text is a pointer because the adapter
// distinguishes "part.text !== undefined" from an empty string.
type googlePart struct {
	// Text is the text content; nil when the part carries no text.
	Text *string `json:"text,omitempty"`
	// Thought marks thinking (thought summary) content.
	Thought bool `json:"thought,omitempty"`
	// ThoughtSignature is the encrypted reasoning-context signature; it can
	// appear on any part type.
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	FunctionCall     *googleFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *googleFunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *googleInlineData       `json:"inlineData,omitempty"`
}

// googleFunctionCall is one model tool call (pi: Part.functionCall).
type googleFunctionCall struct {
	// ID carries the tool call id; only sent for models that require it
	// (requiresToolCallId).
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	// Args is always a JSON object (pi: block.arguments ?? {}).
	Args map[string]any `json:"args"`
}

// googleFunctionResponse is one tool result part (pi: Part.functionResponse).
type googleFunctionResponse struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	// Response uses the "output" key for success and "error" key for errors
	// as per SDK documentation.
	Response map[string]any `json:"response"`
	// Parts nests multimodal function responses (Gemini 3+).
	Parts []googlePart `json:"parts,omitempty"`
}

// googleInlineData is one inline image (pi: Part.inlineData).
type googleInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// googleContent is one Gemini Content entry (pi: Content).
type googleContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []googlePart `json:"parts,omitempty"`
}

// googleUsageMetadata is one chunk's token accounting (pi: UsageMetadata).
type googleUsageMetadata struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	TotalTokenCount         int64 `json:"totalTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
}

// googleCandidate is one streamed candidate (pi: the Candidate fields the
// adapter reads; promptFeedback/modelVersion are never read by pi).
type googleCandidate struct {
	FinishReason string         `json:"finishReason,omitempty"`
	Content      *googleContent `json:"content,omitempty"`
}

// googleGenerateContentResponse is one streamed chunk (pi:
// GenerateContentResponse; responseId is output-only — the adapter keeps the
// first non-empty value from the stream).
type googleGenerateContentResponse struct {
	ResponseID    string               `json:"responseId,omitempty"`
	Candidates    []googleCandidate    `json:"candidates,omitempty"`
	UsageMetadata *googleUsageMetadata `json:"usageMetadata,omitempty"`
}

// googleGenerateContentParams is the request the SDK receives (pi:
// GenerateContentParameters — { model, contents, config }).
type googleGenerateContentParams struct {
	// Model goes into the request URL (the SDK's tModel mapping), not the
	// JSON body.
	Model    string                       `json:"-"`
	Contents []googleContent              `json:"contents,omitempty"`
	Config   *googleGenerateContentConfig `json:"config,omitempty"`
}

// googleGenerateContentConfig is the request config (pi:
// GenerateContentConfig; pi's config.abortSignal rides ctx per the
// AbortSignal → context rule).
type googleGenerateContentConfig struct {
	Temperature       *float64
	MaxOutputTokens   *int64
	SystemInstruction *googleContent
	Tools             []googleToolWrapper
	ToolConfig        *googleToolConfig
	ThinkingConfig    *googleThinkingConfig
}

// googleThinkingConfig is the thinking configuration (pi: ThinkingConfig;
// thinkingBudget 0 disables thinking for Gemini 2.x and must survive JSON
// round-trips — pointer, not value).
type googleThinkingConfig struct {
	IncludeThoughts bool                   `json:"includeThoughts,omitempty"`
	ThinkingBudget  *int64                 `json:"thinkingBudget,omitempty"`
	ThinkingLevel   GoogleApiThinkingLevel `json:"thinkingLevel,omitempty"`
}

// googleToolConfig is the tool selection config (pi: ToolConfig).
type googleToolConfig struct {
	FunctionCallingConfig *googleFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

// googleFunctionCallingConfig is one function-calling mode selection (pi:
// ToolConfig.functionCallingConfig; allowedFunctionNames is never set by pi).
type googleFunctionCallingConfig struct {
	Mode string `json:"mode,omitempty"`
}

// googleToolWrapper is the single-entry tools array (pi:
// { functionDeclarations: [...] }).
type googleToolWrapper struct {
	FunctionDeclarations []map[string]any `json:"functionDeclarations"`
}

// IsThinkingPart determines whether a streamed Gemini part should be treated
// as "thinking" (pi: isThinkingPart). thought === true is the definitive
// marker for thinking content (thought summaries); thoughtSignature is an
// encrypted representation of the model's internal thought process used to
// preserve reasoning context across multi-turn interactions, and it can
// appear on ANY part type (text, functionCall, ...) without making the part
// thinking content. See: https://ai.google.dev/gemini-api/docs/thought-signatures
func IsThinkingPart(part *googlePart) bool {
	return part.Thought
}

// RetainThoughtSignature retains thought signatures during streaming (pi:
// retainThoughtSignature): some backends only send the signature on the first
// delta for a given part/block; later deltas may omit it. This helper
// preserves the last non-empty signature for the current block without
// merging or moving signatures across distinct response parts.
func RetainThoughtSignature(existing string, incoming string) string {
	if incoming != "" {
		return incoming
	}
	return existing
}

// Thought signatures must be base64 for Google APIs (TYPE_BYTES).
var googleBase64SignaturePattern = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

func isValidThoughtSignature(signature string) bool {
	if signature == "" {
		return false
	}
	if len(signature)%4 != 0 {
		return false
	}
	return googleBase64SignaturePattern.MatchString(signature)
}

// resolveThoughtSignature only keeps signatures from the same provider/model
// with valid base64 (pi: resolveThoughtSignature).
func resolveThoughtSignature(isSameProviderAndModel bool, signature string) string {
	if isSameProviderAndModel && isValidThoughtSignature(signature) {
		return signature
	}
	return ""
}

// RequiresToolCallId reports whether a model via Google APIs requires
// explicit tool call IDs in function calls/responses (pi: requiresToolCallId).
func RequiresToolCallId(modelID string) bool {
	geminiMajorVersion := getGeminiMajorVersion(modelID)
	return strings.HasPrefix(modelID, "claude-") ||
		strings.HasPrefix(modelID, "gpt-oss-") ||
		(geminiMajorVersion != nil && *geminiMajorVersion >= 3)
}

// getGeminiMajorVersion extracts the major version of gemini-N ids (pi:
// getGeminiMajorVersion, /^gemini(?:-live)?-(\d+)/).
func getGeminiMajorVersion(modelID string) *int {
	lower := strings.ToLower(modelID)
	rest, ok := strings.CutPrefix(lower, "gemini")
	if !ok {
		return nil
	}
	rest, ok = strings.CutPrefix(rest, "-live")
	_ = ok
	rest, ok = strings.CutPrefix(rest, "-")
	if !ok {
		return nil
	}
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits == 0 {
		return nil
	}
	value := 0
	for _, ch := range rest[:digits] {
		value = value*10 + int(ch-'0')
	}
	return lo.ToPtr(value)
}

func supportsMultimodalFunctionResponse(modelID string) bool {
	geminiMajorVersion := getGeminiMajorVersion(modelID)
	if geminiMajorVersion != nil {
		return *geminiMajorVersion >= 3
	}
	return true
}

// googleNormalizeToolCallID is the id normalizer handed to TransformMessages
// (pi: the inline normalizeToolCallId in convertMessages): only models that
// require ids get sanitized.
func googleNormalizeToolCallID(model *types.Model) NormalizeToolCallIDFn {
	return func(id string, _ *types.Model, _ *types.AssistantMessage) string {
		if !RequiresToolCallId(model.ID) {
			return id
		}
		replaced := googleToolCallIDSanitizer.ReplaceAllString(id, "_")
		if len(replaced) > 64 {
			replaced = replaced[:64]
		}
		return replaced
	}
}

var googleToolCallIDSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// UsesGoogleThinkingLevel reports whether this model uses Gemini's discrete
// thinkingLevel control instead of the token-based thinkingBudget control.
// Supported levels come from the model's thinkingLevelMap; this only selects
// the Google wire format (pi: usesGoogleThinkingLevel).
func UsesGoogleThinkingLevel(model *types.Model) bool {
	id := strings.ToLower(model.ID)
	// Match Gemini 3 Pro/Flash IDs with or without a minor version, such as
	// gemini-3-flash-preview, gemini-3.1-pro-preview, and gemini-3.8-flash.
	if regexp.MustCompile(`gemini-3(?:\.\d+)?-(?:pro|flash)`).MatchString(id) {
		return true
	}
	// Match Gemini 4 and later, such as gemini-4-argon (#10890).
	if regexp.MustCompile(`gemini-(?:[4-9]|[1-9]\d)`).MatchString(id) {
		return true
	}
	if id == "gemini-flash-latest" || id == "gemini-flash-lite-latest" {
		return true
	}
	// Match both hosted Gemma 4 naming forms: gemma-4-* and gemma4-*.
	return regexp.MustCompile(`gemma-?4`).MatchString(id)
}

// ToGoogleThinkingLevel maps a resolved pi level to the Google API level
// (pi: toGoogleThinkingLevel).
func ToGoogleThinkingLevel(level types.ModelThinkingLevel) GoogleApiThinkingLevel {
	switch level {
	case types.ModelThinkingMinimal:
		return GoogleThinkingLevelMinimal
	case types.ModelThinkingLow:
		return GoogleThinkingLevelLow
	case types.ModelThinkingMedium:
		return GoogleThinkingLevelMedium
	case types.ModelThinkingHigh:
		return GoogleThinkingLevelHigh
	}
	panic(fmt.Sprintf("unhandled resolved Google thinking level: %s", level))
}

// GetDisabledGoogleThinkingConfig builds the thinking config used when
// thinking is disabled: level-based models fall back to the lowest supported
// level from their thinkingLevelMap; budget-based models disable via
// thinkingBudget 0 (pi: getDisabledGoogleThinkingConfig, v0.86.0 —
// "avoid unsupported Gemini thinking levels").
func GetDisabledGoogleThinkingConfig(model *types.Model) *googleThinkingConfig {
	if !UsesGoogleThinkingLevel(model) {
		return &googleThinkingConfig{ThinkingBudget: lo.ToPtr(int64(0))}
	}
	fallback := ai.ClampThinkingLevel(model, types.ModelThinkingOff)
	if fallback == types.ModelThinkingOff {
		return &googleThinkingConfig{ThinkingBudget: lo.ToPtr(int64(0))}
	}
	resolvedLevel, err := ResolveGoogleThinkingLevel(model, fallback)
	if err != nil {
		return &googleThinkingConfig{ThinkingBudget: lo.ToPtr(int64(0))}
	}
	apiLevel := ToGoogleThinkingLevel(resolvedLevel)
	return &googleThinkingConfig{ThinkingLevel: apiLevel}
}

// ConvertGoogleMessages converts internal messages to Gemini Content[] format
// (pi: convertMessages).
func ConvertGoogleMessages(model *types.Model, llmContext *types.TranscriptContext) []googleContent {
	var contents []googleContent

	// Gemini has no mid-conversation system messages; the leading prompt is
	// sent as systemInstruction.
	conversation := utils.WithoutInitialSystemMessage(utils.CollapseSystemMessages(llmContext).Messages)
	transformedMessages := TransformMessages(conversation, model, googleNormalizeToolCallID(model))

	for _, msg := range transformedMessages {
		switch message := msg.(type) {
		case types.UserMessage:
			var parts []googlePart
			if message.Content.Parts == nil {
				parts = append(parts, googlePart{Text: lo.ToPtr(utils.SanitizeSurrogates(message.Content.String))})
			} else {
				for _, item := range message.Content.Parts {
					switch block := item.(type) {
					case types.TextContent:
						parts = append(parts, googlePart{Text: lo.ToPtr(utils.SanitizeSurrogates(block.Text))})
					case types.ImageContent:
						parts = append(parts, googlePart{
							InlineData: &googleInlineData{MimeType: block.MimeType, Data: block.Data},
						})
					}
				}
			}
			if len(parts) == 0 {
				continue
			}
			contents = append(contents, googleContent{Role: "user", Parts: parts})

		case types.AssistantMessage:
			var parts []googlePart
			// Check if message is from same provider and model - only then
			// keep thinking blocks.
			isSameProviderAndModel := message.Provider == model.Provider && message.Model == model.ID

			for _, block := range message.Content {
				switch b := block.(type) {
				case types.TextContent:
					thoughtSignature := resolveThoughtSignature(isSameProviderAndModel, b.TextSignature)
					// Skip empty text blocks — unless they carry a thought
					// signature. Gemini can attach the signature to a part
					// whose visible text is empty and requires it echoed
					// back; dropping it breaks the reasoning chain (see the
					// signed-empty-blocks tests).
					if (b.Text == "" || strings.TrimSpace(b.Text) == "") && thoughtSignature == "" {
						continue
					}
					part := googlePart{Text: lo.ToPtr(utils.SanitizeSurrogates(b.Text))}
					if thoughtSignature != "" {
						part.ThoughtSignature = thoughtSignature
					}
					parts = append(parts, part)
				case types.ThinkingContent:
					if isSameProviderAndModel {
						thoughtSignature := resolveThoughtSignature(isSameProviderAndModel, b.ThinkingSignature)
						// Same rule as text blocks: an empty thinking block
						// is dropped only when it carries no signature
						// (mirrors the anthropic converter's handling).
						if (b.Thinking == "" || strings.TrimSpace(b.Thinking) == "") && thoughtSignature == "" {
							continue
						}
						part := googlePart{Thought: true, Text: lo.ToPtr(utils.SanitizeSurrogates(b.Thinking))}
						if thoughtSignature != "" {
							part.ThoughtSignature = thoughtSignature
						}
						parts = append(parts, part)
					} else {
						// Cross-provider/model: the signature is unusable,
						// empty blocks stay dropped.
						if b.Thinking == "" || strings.TrimSpace(b.Thinking) == "" {
							continue
						}
						parts = append(parts, googlePart{Text: lo.ToPtr(utils.SanitizeSurrogates(b.Thinking))})
					}
				case types.ToolCall:
					thoughtSignature := resolveThoughtSignature(isSameProviderAndModel, b.ThoughtSignature)
					args := b.Arguments
					if args == nil {
						args = map[string]any{}
					}
					call := &googleFunctionCall{Name: b.Name, Args: args}
					if RequiresToolCallId(model.ID) {
						call.ID = b.ID
					}
					part := googlePart{FunctionCall: call}
					if thoughtSignature != "" {
						part.ThoughtSignature = thoughtSignature
					}
					parts = append(parts, part)
				}
			}

			if len(parts) == 0 {
				continue
			}
			contents = append(contents, googleContent{Role: "model", Parts: parts})

		case types.ToolResultMessage:
			// Extract text and image content.
			var textParts []string
			var imageContent []types.ImageContent
			for _, block := range message.Content {
				switch b := block.(type) {
				case types.TextContent:
					textParts = append(textParts, b.Text)
				case types.ImageContent:
					for _, input := range model.Input {
						if input == types.ModelInputImage {
							imageContent = append(imageContent, b)
							break
						}
					}
				}
			}
			textResult := strings.Join(textParts, "\n")

			hasText := strings.TrimSpace(textResult) != ""
			hasImages := len(imageContent) > 0

			// Gemini 3+ models support multimodal function responses with
			// images nested inside functionResponse.parts. Claude and other
			// non-Gemini models behind Cloud Code Assist / Gemini < 3 still
			// need a separate user image turn.
			modelSupportsMultimodalFunctionResponse := supportsMultimodalFunctionResponse(model.ID)

			// Use "output" key for success, "error" key for errors as per SDK
			// documentation. Whitespace-only text counts as no text, so it
			// falls through to the image placeholder or an empty output.
			responseValue := ""
			if hasText {
				responseValue = utils.SanitizeSurrogates(textResult)
			} else if hasImages {
				responseValue = "(see attached image)"
			}

			response := map[string]any{"output": responseValue}
			if message.IsError {
				response = map[string]any{"error": responseValue}
			}

			var imageParts []googlePart
			for _, imageBlock := range imageContent {
				imageParts = append(imageParts, googlePart{
					InlineData: &googleInlineData{MimeType: imageBlock.MimeType, Data: imageBlock.Data},
				})
			}

			functionResponse := &googleFunctionResponse{
				Name:     message.ToolName,
				Response: response,
			}
			if hasImages && modelSupportsMultimodalFunctionResponse {
				functionResponse.Parts = imageParts
			}
			if RequiresToolCallId(model.ID) {
				functionResponse.ID = message.ToolCallID
			}
			functionResponsePart := googlePart{FunctionResponse: functionResponse}

			// Cloud Code Assist API requires all function responses to be in
			// a single user turn. Check if the last content is already a
			// user turn with function responses and merge.
			merged := false
			if len(contents) > 0 {
				lastContent := &contents[len(contents)-1]
				if lastContent.Role == "user" {
					for _, part := range lastContent.Parts {
						if part.FunctionResponse != nil {
							lastContent.Parts = append(lastContent.Parts, functionResponsePart)
							merged = true
							break
						}
					}
				}
			}
			if !merged {
				contents = append(contents, googleContent{Role: "user", Parts: []googlePart{functionResponsePart}})
			}

			// For Gemini < 3, add images in a separate user message.
			if hasImages && !modelSupportsMultimodalFunctionResponse {
				contents = append(contents, googleContent{
					Role:  "user",
					Parts: append([]googlePart{{Text: lo.ToPtr("Tool result image:")}}, imageParts...),
				})
			}
		}
	}

	return contents
}

// jsonSchemaMetaDeclarations are stripped from OpenAPI-style parameters (pi:
// JSON_SCHEMA_META_DECLARATIONS).
var jsonSchemaMetaDeclarations = map[string]struct{}{
	"$schema":        {},
	"$id":            {},
	"$anchor":        {},
	"$dynamicAnchor": {},
	"$vocabulary":    {},
	"$comment":       {},
	"$defs":          {},
	"definitions":    {}, // pre-draft-2019-09 equivalent of $defs
}

// sanitizeForOpenApi strips meta-declarations from a schema object (pi:
// sanitizeForOpenApi).
func sanitizeForOpenApi(schema any) any {
	switch value := schema.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			if _, ok := jsonSchemaMetaDeclarations[key]; ok {
				continue
			}
			result[key] = sanitizeForOpenApi(item)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			result[i] = sanitizeForOpenApi(item)
		}
		return result
	default:
		return schema
	}
}

// ConvertGoogleTools converts tools to Gemini function declarations format
// (pi: convertTools). By default uses `parametersJsonSchema` which supports
// full JSON Schema (including anyOf, oneOf, const, etc.); useParameters=true
// switches to the legacy `parameters` field (OpenAPI 3.03 Schema). This is
// needed for Cloud Code Assist with Claude models, where the API translates
// `parameters` into Anthropic's `input_schema`.
func ConvertGoogleTools(tools []types.Tool, useParameters bool, supportsStrictMode bool) ([]googleToolWrapper, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	declarations := make([]map[string]any, 0, len(tools))
	for i := range tools {
		tool := &tools[i]
		resolved, err := ResolveJSONSchemaStrictSampling(tool, supportsStrictMode, nil)
		if err != nil {
			return nil, err
		}
		strict := resolved != nil && *resolved
		parameters, err := GetJSONSchemaToolParameters(tool, strict)
		if err != nil {
			return nil, err
		}
		declaration := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
		}
		if useParameters {
			// The legacy parameters field is OpenAPI 3.03 Schema: strip the
			// JSON Schema meta keys.
			var decoded any
			if jsonErr := json.Unmarshal(parameters, &decoded); jsonErr != nil {
				return nil, jsonErr
			}
			declaration["parameters"] = sanitizeForOpenApi(decoded)
		} else {
			declaration["parametersJsonSchema"] = parameters
		}
		declarations = append(declarations, declaration)
	}
	return []googleToolWrapper{{FunctionDeclarations: declarations}}, nil
}

// SupportsGoogleStrictToolSampling reports whether Gemini 3+ enforces
// required function parameters in validated tool-calling modes (pi:
// supportsGoogleStrictToolSampling).
func SupportsGoogleStrictToolSampling(modelID string) bool {
	majorVersion := getGeminiMajorVersion(modelID)
	return majorVersion != nil && *majorVersion >= 3
}

// mapGoogleToolChoice maps a tool choice string to the Gemini
// FunctionCallingConfigMode (pi: mapToolChoice).
func mapGoogleToolChoice(choice string) string {
	switch choice {
	case "auto":
		return googleFunctionCallingModeAuto
	case "none":
		return googleFunctionCallingModeNone
	case "any":
		return googleFunctionCallingModeAny
	default:
		return googleFunctionCallingModeAuto
	}
}

// ResolveGoogleFunctionCallingMode resolves the tool config mode (pi:
// resolveGoogleFunctionCallingMode). The error mirrors pi's
// resolveJsonSchemaStrictSampling throw for strict tools on non-strict
// models.
func ResolveGoogleFunctionCallingMode(tools []types.Tool, toolChoice string, supportsStrictMode bool) (string, error) {
	useStrictMode := false
	for i := range tools {
		strict, err := ResolveJSONSchemaStrictSampling(&tools[i], supportsStrictMode, nil)
		if err != nil {
			return "", err
		}
		if strict != nil && *strict {
			useStrictMode = true
		}
	}
	if toolChoice == "none" || toolChoice == "any" {
		return mapGoogleToolChoice(toolChoice), nil
	}
	if useStrictMode {
		return googleFunctionCallingModeValidated, nil
	}
	if toolChoice != "" {
		return mapGoogleToolChoice(toolChoice), nil
	}
	return "", nil
}

// MapGoogleStopReason maps a Gemini FinishReason to our StopReason (pi:
// mapStopReason). An unhandled reason mirrors pi's exhaustive-switch throw,
// which the adapter's catch turns into the stream error.
func MapGoogleStopReason(reason string) (types.StopReason, error) {
	switch reason {
	case googleFinishReasonStop:
		return types.StopReasonStop, nil
	case googleFinishReasonMaxTokens:
		return types.StopReasonLength, nil
	case googleFinishReasonBlocklist,
		googleFinishReasonProhibitedContent,
		googleFinishReasonSPII,
		googleFinishReasonSafety,
		googleFinishReasonImageSafety,
		googleFinishReasonImageProhibitedContent,
		googleFinishReasonImageRecitation,
		googleFinishReasonImageOther,
		googleFinishReasonRecitation,
		googleFinishReasonUnspecified,
		googleFinishReasonOther,
		googleFinishReasonLanguage,
		googleFinishReasonMalformedFunctionCall,
		googleFinishReasonUnexpectedToolCall,
		googleFinishReasonTooManyToolCalls,
		googleFinishReasonNoImage:
		return types.StopReasonError, nil
	default:
		return "", fmt.Errorf("Unhandled stop reason: %s", reason)
	}
}

// MapGoogleStopReasonString maps a string finish reason to our StopReason for
// raw API responses (pi: mapStopReasonString).
func MapGoogleStopReasonString(reason string) types.StopReason {
	switch reason {
	case "STOP":
		return types.StopReasonStop
	case "MAX_TOKENS":
		return types.StopReasonLength
	default:
		return types.StopReasonError
	}
}

// GoogleRetryOptions are RetryGoogleRequest's knobs (pi: the
// Pick<StreamOptions, "maxRetries" | "maxRetryDelayMs" | "signal"> shape;
// signal → ctx).
type GoogleRetryOptions struct {
	MaxRetries      int
	MaxRetryDelayMs *int64
	Ctx             context.Context
}

// RetryGoogleRequest runs a Google request with the shared provider retry
// policy (pi: retryGoogleRequest — 408/409/429/5xx with backoff, honoring
// retry-after), mirroring how the Anthropic and OpenAI adapters wrap their
// initial request in retryProviderRequest. pi normalizes the SDK's
// headers-less ApiError by adding a missing `headers` field so
// retryProviderRequest classifies it; pig's transport already returns
// *utils.ProviderHTTPError (Headers may be nil, which the classifier
// accepts), so the wrapper is a direct pass-through.
func RetryGoogleRequest[T any](request func() (T, error), options GoogleRetryOptions) (T, error) {
	return utils.RetryProviderRequest(request, utils.ProviderRetryOptions{
		MaxRetries:      options.MaxRetries,
		MaxRetryDelayMs: options.MaxRetryDelayMs,
		Ctx:             options.Ctx,
	})
}
