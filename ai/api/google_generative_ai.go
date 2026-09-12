// pi: api/google-generative-ai.ts.
//
// The Google Generative AI (Gemini API) wire adapter. pi goes through the
// @google/genai SDK (client.models.generateContentStream); the SDK has no Go
// counterpart, so the transport below is a native rewrite of the SDK surface
// pi exercises (pi: createClient + generateContentStream):
//
//   - POST {baseUrl}[/{apiVersion}]/models/{model}:streamGenerateContent?alt=sse
//     (pi: ApiClient.constructUrl + tModel — the model id is prefixed with
//     "models/" unless it already starts with models/|tunedModels/, and the
//     apiVersion segment is skipped when empty, which is how a model.baseUrl
//     that already includes the version composes);
//   - headers: x-goog-api-key from the apiKey (pi: ApiKeyAuth
//     addAuthHeaders) plus the httpOptions.headers set (User-Agent + model
//     headers + options headers) and Content-Type: application/json;
//   - body: the mldev mapping of GenerateContentParameters (pi:
//     generateContentParametersToMldev — contents pass through; config's
//     systemInstruction/tools/toolConfig hoist to the top level, the rest go
//     under generationConfig);
//   - response: SSE data lines, each a GenerateContentResponse JSON chunk.
package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
	"github.com/samber/lo"
)

// GoogleOptions are the google-generative-ai request options (pi:
// GoogleOptions extends StreamOptions).
type GoogleOptions struct {
	types.StreamOptions
	// ToolChoice is "auto", "none", or "any".
	ToolChoice string
	// Thinking configures thinking; BudgetTokens is -1 for dynamic, 0 to
	// disable; Level mirrors Google's ThinkingLevel enum values.
	Thinking *GoogleThinkingOptions
}

// GoogleThinkingOptions is the thinking member of GoogleOptions (pi: the
// inline thinking object).
type GoogleThinkingOptions struct {
	Enabled      bool
	BudgetTokens *int64
	Level        GoogleApiThinkingLevel
}

// googleGenAIClient is the native counterpart of the SDK client pi builds in
// createClient: base URL, apiVersion, headers, and the api key.
type googleGenAIClient struct {
	baseURL    string
	apiVersion string
	headers    map[string]string
	apiKey     string
}

// googleChunkSource is a pull-based iterator of streamed chunks (pi: the
// async iterator the SDK returns).
type googleChunkSource interface {
	Next() (*googleGenerateContentResponse, error)
	Close()
}

// googleGenerateContentStream is the transport seam for
// client.models.generateContentStream (pi: vi.mock("@google/genai") stubs the
// SDK; tests override this var). The default implementation performs the
// native HTTP request and returns a pull-based chunk stream.
var googleGenerateContentStream = func(ctx context.Context, client *googleGenAIClient, params *googleGenerateContentParams) (googleChunkSource, error) {
	return client.generateContentStream(ctx, params)
}

// googleChunkStream is a pull-based iterator of streamed chunks (pi: the
// async iterator the SDK returns).
type googleChunkStream struct {
	resp   *http.Response
	body   *bufio.Reader
	closed bool
}

// Next returns the next chunk; io.EOF marks a clean end.
func (s *googleChunkStream) Next() (*googleGenerateContentResponse, error) {
	if s.closed {
		return nil, io.EOF
	}
	for {
		line, err := s.body.ReadBytes('\n')
		if err != nil {
			s.Close()
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			return nil, err
		}
		trimmed := strings.TrimRight(string(line), "\r\n")
		if trimmed == "" {
			continue
		}
		data, ok := strings.CutPrefix(trimmed, "data:")
		if !ok {
			continue
		}
		payload := strings.TrimSpace(data)
		if payload == "" {
			continue
		}
		var chunk googleGenerateContentResponse
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			s.Close()
			return nil, err
		}
		return &chunk, nil
	}
}

// Close releases the response body.
func (s *googleChunkStream) Close() {
	if !s.closed {
		s.closed = true
		if s.resp != nil && s.resp.Body != nil {
			_ = s.resp.Body.Close()
		}
	}
}

// googleDefaultBaseURL and googleDefaultAPIVersion reproduce the SDK's
// defaults for the Gemini API (pi: _defaultBaseGeminiUrl + initHttpOptions
// apiVersion).
const (
	googleDefaultBaseURL    = "https://generativelanguage.googleapis.com"
	googleDefaultAPIVersion = "v1beta"
)

// generateContentStream performs the native request (pi:
// client.models.generateContentStream).
func (c *googleGenAIClient) generateContentStream(ctx context.Context, params *googleGenerateContentParams) (googleChunkSource, error) {
	url, body, err := c.buildRequest(params)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, value := range c.headers {
		request.Header.Set(key, value)
	}
	request.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		request.Header.Set("x-goog-api-key", c.apiKey)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("Request was aborted")
		}
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		bodyText, _ := io.ReadAll(response.Body)
		return nil, &utils.ProviderHTTPError{
			Status:  response.StatusCode,
			Headers: response.Header,
			Body:    string(bodyText),
			Message: fmt.Sprintf("%d %s", response.StatusCode, http.StatusText(response.StatusCode)),
		}
	}
	return &googleChunkStream{resp: response, body: bufio.NewReader(response.Body)}, nil
}

// buildRequest composes the URL and the mldev body (pi: ApiClient.constructUrl
// + generateContentParametersToMldev; the body serialization is shared with
// the vertex transport — the two mappings coincide for the surface pi uses).
func (c *googleGenAIClient) buildRequest(params *googleGenerateContentParams) (string, []byte, error) {
	baseURL := c.baseURL
	if baseURL == "" {
		baseURL = googleDefaultBaseURL
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	urlElements := []string{baseURL}
	if c.apiVersion != "" {
		urlElements = append(urlElements, c.apiVersion)
	}
	urlElements = append(urlElements, tGoogleModel(params.Model)+":streamGenerateContent")
	url := strings.Join(urlElements, "/") + "?alt=sse"

	encoded, err := marshalGoogleGenerateContentBody(params)
	if err != nil {
		return "", nil, err
	}
	return url, encoded, nil
}

// tGoogleModel is the SDK's mldev tModel mapping: prefix with models/ unless
// the id already carries a models/|tunedModels/ prefix; reject ids with
// path-escaping characters.
func tGoogleModel(model string) string {
	if model == "" {
		return ""
	}
	if strings.Contains(model, "..") || strings.Contains(model, "?") || strings.Contains(model, "&") {
		// pi: the SDK throws 'invalid model parameter'; pig lets the request
		// fail on the URL instead (declared deviation — pi never hits this
		// with catalog ids).
		return model
	}
	if strings.HasPrefix(model, "models/") || strings.HasPrefix(model, "tunedModels/") {
		return model
	}
	return "models/" + model
}

// googleToolCallCounter counts generated tool call ids (pi: toolCallCounter).
var googleToolCallCounter atomic.Int64

// GoogleGenerativeAI implements ProviderStreams for the google-generative-ai
// API (pi: the { stream, streamSimple } module object).
type GoogleGenerativeAI struct{}

var _ types.ProviderStreams = GoogleGenerativeAI{}

// pi: compat.ts registers the builtin API implementations at module load;
// pig's layering rule (ai cannot import ai/api) puts the registration in the
// adapter package's init instead.
func init() {
	ai.RegisterBuiltInApiProvider(ai.ApiGoogleGenerativeAI, GoogleGenerativeAI{})
	ai.SetBuiltinApiRegistrar(func() {
		ai.RegisterBuiltInApiProvider(ai.ApiGoogleGenerativeAI, GoogleGenerativeAI{})
	})
}

// Stream dispatches one streaming request (pi: stream). ctx carries the
// caller's cancellation (pi: options.signal into the SDK request and the
// retry helper).
func (GoogleGenerativeAI) Stream(ctx context.Context, model *types.Model, llmContext *types.Context, options *types.StreamOptions) *types.AssistantMessageEventStream {
	opts := &GoogleOptions{}
	if options != nil {
		opts.StreamOptions = *options
	}
	return streamGoogleGenerativeAI(ctx, model, llmContext, opts)
}

// StreamSimple dispatches one simple streaming request (pi: streamSimple).
func (GoogleGenerativeAI) StreamSimple(ctx context.Context, model *types.Model, llmContext *types.Context, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
	if options.APIKey == "" {
		return immediateErrorStream(model, fmt.Errorf("No API key for provider: %s", model.Provider))
	}
	base := BuildBaseOptions(model, llmContext, options, options.APIKey)
	opts := &GoogleOptions{StreamOptions: base, ToolChoice: string(options.ToolChoice)}
	if options.Reasoning == "" {
		opts.Thinking = &GoogleThinkingOptions{Enabled: false}
		return streamGoogleGenerativeAI(ctx, model, llmContext, opts)
	}

	clampedReasoning := ai.ClampThinkingLevel(model, types.ModelThinkingLevel(options.Reasoning))
	resolvedLevel, err := ResolveGoogleThinkingLevel(model, clampedReasoning)
	if err != nil {
		return immediateErrorStream(model, err)
	}

	if isGemini3ProModel(model.ID) || isGemini3FlashModel(model.ID) || isGemma4Model(model.ID) {
		opts.Thinking = &GoogleThinkingOptions{
			Enabled: true,
			Level:   getGoogleThinkingLevel(resolvedLevel, model),
		}
		return streamGoogleGenerativeAI(ctx, model, llmContext, opts)
	}

	opts.Thinking = &GoogleThinkingOptions{
		Enabled:      true,
		BudgetTokens: lo.ToPtr(getGoogleBudget(model, resolvedLevel, options.ThinkingBudgets)),
	}
	return streamGoogleGenerativeAI(ctx, model, llmContext, opts)
}

// streamGoogleGenerativeAI is the adapter's core stream implementation (pi:
// stream).
func streamGoogleGenerativeAI(ctx context.Context, model *types.Model, llmContext *types.Context, options *GoogleOptions) *types.AssistantMessageEventStream {
	stream := types.NewAssistantMessageEventStream()
	output := &types.AssistantMessage{
		Content:  []types.Content{},
		API:      ai.ApiGoogleGenerativeAI,
		Provider: model.Provider,
		Model:    model.ID,
		Usage: types.Usage{
			Cost: types.Cost{},
		},
		StopReason: types.StopReasonPending,
		Timestamp:  time.Now().UnixMilli(),
	}

	// push keeps the stream's partial pointer a snapshot of the mutable
	// output (pi pushes the same object; pig's event consumers treat Partial
	// as a value snapshot).
	push := func(event types.AssistantMessageEvent) {
		partial := *output
		event.Partial = &partial
		stream.Push(event)
	}

	go func() {
		defer stream.EndWith(*output)
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok {
					failGoogleStream(ctx, output, stream, err)
				} else {
					failGoogleStream(ctx, output, stream, fmt.Errorf("%v", r))
				}
			}
		}()

		if err := runGoogleGenerativeAIStream(ctx, model, llmContext, options, output, push); err != nil {
			failGoogleStream(ctx, output, stream, err)
		}
	}()

	return stream
}

// failGoogleStream is pi's catch block: aborts normalize to the aborted stop
// reason, everything else to error, with the formatted provider message.
func failGoogleStream(ctx context.Context, output *types.AssistantMessage, stream *types.AssistantMessageEventStream, err error) {
	if ctx.Err() != nil {
		output.StopReason = types.StopReasonAborted
	} else {
		output.StopReason = types.StopReasonError
	}
	output.ErrorMessage = utils.FormatProviderError(utils.NormalizeProviderError(err), "")
	stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: output.StopReason, Message: output})
}

// runGoogleGenerativeAIStream is pi's try block (the streaming loop).
func runGoogleGenerativeAIStream(ctx context.Context, model *types.Model, llmContext *types.Context, options *GoogleOptions, output *types.AssistantMessage, push func(types.AssistantMessageEvent)) error {
	if options.Fetch != nil {
		return errors.New("Custom fetch is not supported by the Google Generative AI adapter")
	}
	apiKey := options.APIKey
	if apiKey == "" {
		return fmt.Errorf("No API key for provider: %s", model.Provider)
	}
	client := createGoogleClient(model, apiKey, options.Headers)
	params := buildGoogleParams(model, llmContext, options)
	if options.OnPayload != nil {
		if next := options.OnPayload(params, model); next != nil {
			if nextParams, ok := next.(*googleGenerateContentParams); ok {
				params = nextParams
			}
		}
	}
	maxRetries := 0
	if options.MaxRetries != nil {
		maxRetries = *options.MaxRetries
	}
	chunkStream, err := RetryGoogleRequest(func() (googleChunkSource, error) {
		return googleGenerateContentStream(ctx, client, params)
	}, GoogleRetryOptions{MaxRetries: maxRetries, MaxRetryDelayMs: options.MaxRetryDelayMs, Ctx: ctx})
	if err != nil {
		return err
	}
	defer chunkStream.Close()

	push(types.AssistantMessageEvent{Type: types.EventStart, Partial: output})
	var currentBlock *googleStreamBlock
	blockIndex := func() int { return len(output.Content) - 1 }

	for {
		chunk, err := chunkStream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		// @google/genai documents responseId as an output-only field used to
		// identify each response; keep the first non-empty one.
		if output.ResponseID == "" {
			output.ResponseID = chunk.ResponseID
		}
		if len(chunk.Candidates) > 0 {
			candidate := &chunk.Candidates[0]
			if candidate.Content != nil {
				for i := range candidate.Content.Parts {
					part := &candidate.Content.Parts[i]
					if part.Text != nil {
						isThinking := IsThinkingPart(part)
						if currentBlock == nil ||
							(isThinking && currentBlock.kind != "thinking") ||
							(!isThinking && currentBlock.kind != "text") {
							if currentBlock != nil {
								if currentBlock.kind == "text" {
									push(types.AssistantMessageEvent{
										Type: types.EventTextEnd, ContentIndex: blockIndex(), Content: currentBlock.text, Partial: output,
									})
								} else {
									push(types.AssistantMessageEvent{
										Type: types.EventThinkingEnd, ContentIndex: blockIndex(), Content: currentBlock.thinking, Partial: output,
									})
								}
							}
							if isThinking {
								currentBlock = &googleStreamBlock{kind: "thinking"}
								output.Content = append(output.Content, types.NewThinkingContent(""))
								push(types.AssistantMessageEvent{Type: types.EventThinkingStart, ContentIndex: blockIndex(), Partial: output})
							} else {
								currentBlock = &googleStreamBlock{kind: "text"}
								output.Content = append(output.Content, types.NewTextContent(""))
								push(types.AssistantMessageEvent{Type: types.EventTextStart, ContentIndex: blockIndex(), Partial: output})
							}
						}
						if currentBlock.kind == "thinking" {
							currentBlock.thinking += *part.Text
							currentBlock.signature = RetainThoughtSignature(currentBlock.signature, part.ThoughtSignature)
							entry := types.NewThinkingContent(currentBlock.thinking)
							entry.ThinkingSignature = currentBlock.signature
							output.Content[blockIndex()] = entry
							push(types.AssistantMessageEvent{
								Type: types.EventThinkingDelta, ContentIndex: blockIndex(), Delta: *part.Text, Partial: output,
							})
						} else {
							currentBlock.text += *part.Text
							currentBlock.signature = RetainThoughtSignature(currentBlock.signature, part.ThoughtSignature)
							entry := types.NewTextContent(currentBlock.text)
							entry.TextSignature = currentBlock.signature
							output.Content[blockIndex()] = entry
							push(types.AssistantMessageEvent{
								Type: types.EventTextDelta, ContentIndex: blockIndex(), Delta: *part.Text, Partial: output,
							})
						}
					}

					if part.FunctionCall != nil {
						if currentBlock != nil {
							if currentBlock.kind == "text" {
								push(types.AssistantMessageEvent{
									Type: types.EventTextEnd, ContentIndex: blockIndex(), Content: currentBlock.text, Partial: output,
								})
							} else {
								push(types.AssistantMessageEvent{
									Type: types.EventThinkingEnd, ContentIndex: blockIndex(), Content: currentBlock.thinking, Partial: output,
								})
							}
							currentBlock = nil
						}

						// Generate a unique ID if not provided or if it's a duplicate.
						providedID := part.FunctionCall.ID
						needsNewID := providedID == ""
						if !needsNewID {
							for _, block := range output.Content {
								if call, ok := block.(types.ToolCall); ok && call.ID == providedID {
									needsNewID = true
									break
								}
							}
						}
						toolCallID := providedID
						if needsNewID {
							toolCallID = fmt.Sprintf("%s_%d_%d", part.FunctionCall.Name, time.Now().UnixMilli(), googleToolCallCounter.Add(1))
						}

						args := part.FunctionCall.Args
						if args == nil {
							args = map[string]any{}
						}
						toolCall := types.ToolCall{
							Kind:      types.KindToolCall,
							ID:        toolCallID,
							Name:      part.FunctionCall.Name,
							Arguments: args,
						}
						if part.ThoughtSignature != "" {
							toolCall.ThoughtSignature = part.ThoughtSignature
						}

						output.Content = append(output.Content, toolCall)
						push(types.AssistantMessageEvent{Type: types.EventToolCallStart, ContentIndex: blockIndex(), Partial: output})
						argumentsJSON, _ := json.Marshal(toolCall.Arguments)
						push(types.AssistantMessageEvent{
							Type: types.EventToolCallDelta, ContentIndex: blockIndex(), Delta: string(argumentsJSON), Partial: output,
						})
						push(types.AssistantMessageEvent{Type: types.EventToolCallEnd, ContentIndex: blockIndex(), ToolCall: &toolCall, Partial: output})
					}
				}
			}

			if candidate.FinishReason != "" {
				output.RawStopReason = candidate.FinishReason
				stopReason, mapErr := MapGoogleStopReason(candidate.FinishReason)
				if mapErr != nil {
					return mapErr
				}
				output.StopReason = stopReason
				hasToolCall := false
				for _, block := range output.Content {
					if _, ok := block.(types.ToolCall); ok {
						hasToolCall = true
						break
					}
				}
				if hasToolCall && output.StopReason == types.StopReasonStop {
					output.StopReason = types.StopReasonToolUse
				}
			}
		}

		if chunk.UsageMetadata != nil {
			usage := chunk.UsageMetadata
			output.Usage = types.Usage{
				Input:       usage.PromptTokenCount - usage.CachedContentTokenCount,
				Output:      usage.CandidatesTokenCount + usage.ThoughtsTokenCount,
				CacheRead:   usage.CachedContentTokenCount,
				CacheWrite:  0,
				Reasoning:   lo.ToPtr(usage.ThoughtsTokenCount),
				TotalTokens: usage.TotalTokenCount,
				Cost:        types.Cost{},
			}
			ai.CalculateCost(model, &output.Usage)
		}
	}

	if currentBlock != nil {
		if currentBlock.kind == "text" {
			push(types.AssistantMessageEvent{
				Type:         types.EventTextEnd,
				ContentIndex: blockIndex(),
				Content:      currentBlock.text,
				Partial:      output,
			})
		} else {
			push(types.AssistantMessageEvent{
				Type:         types.EventThinkingEnd,
				ContentIndex: blockIndex(),
				Content:      currentBlock.thinking,
				Partial:      output,
			})
		}
	}

	if ctx.Err() != nil {
		return errors.New("Request was aborted")
	}

	if output.StopReason == types.StopReasonPending {
		return errors.New("Google stream ended without a finish reason")
	}
	if output.StopReason == types.StopReasonAborted || output.StopReason == types.StopReasonError {
		if output.RawStopReason != "" {
			return fmt.Errorf("Provider stopped with: %s", output.RawStopReason)
		}
		return errors.New("An unknown error occurred")
	}

	push(types.AssistantMessageEvent{Type: types.EventDone, Reason: output.StopReason, Message: output})
	return nil
}

// googleStreamBlock tracks the current text/thinking block during streaming
// (pi: the currentBlock local). kind is "text" or "thinking".
type googleStreamBlock struct {
	kind      string
	text      string
	thinking  string
	signature string
}

// backingBlock returns the output.Content entry the accumulator mirrors (pi
// mutates the block objects in place; pig's blocks are values, so the local
// tracks the text and the backing entry is patched through its pointer).
func (b *googleStreamBlock) backingBlock(content []types.Content, index int) types.Content {
	if index < 0 || index >= len(content) {
		return nil
	}
	return content[index]
}

// createGoogleClient assembles the native client (pi: createClient). A
// model.baseUrl already includes the version path, so the apiVersion segment
// stays empty.
func createGoogleClient(model *types.Model, apiKey string, optionsHeaders types.ProviderHeaders) *googleGenAIClient {
	client := &googleGenAIClient{apiKey: apiKey, apiVersion: googleDefaultAPIVersion}
	if model.BaseURL != "" {
		client.baseURL = model.BaseURL
		client.apiVersion = ""
	}
	headers := map[string]string{"User-Agent": utils.GetPiUserAgent()}
	for key, value := range model.Headers {
		headers[key] = value
	}
	for key, value := range optionsHeaders {
		if value != nil {
			headers[key] = *value
		} else {
			// pi: a null header suppresses the default value.
			delete(headers, key)
		}
	}
	client.headers = headers
	return client
}

// buildGoogleParams builds the request the SDK receives (pi: buildParams).
func buildGoogleParams(model *types.Model, llmContext *types.Context, options *GoogleOptions) *googleGenerateContentParams {
	contents := ConvertGoogleMessages(model, llmContext)

	config := &googleGenerateContentConfig{}
	if options.Temperature != nil {
		config.Temperature = options.Temperature
	}
	if options.MaxTokens != nil {
		config.MaxOutputTokens = options.MaxTokens
	}

	supportsStrictMode := SupportsGoogleStrictToolSampling(model.ID)
	var functionCallingMode string
	if len(llmContext.Tools) > 0 {
		mode, err := ResolveGoogleFunctionCallingMode(llmContext.Tools, options.ToolChoice, supportsStrictMode)
		if err != nil {
			panic(err)
		}
		functionCallingMode = mode
	}
	if llmContext.SystemPrompt != "" {
		config.SystemInstruction = &googleContent{
			Parts: []googlePart{{Text: lo.ToPtr(utils.SanitizeSurrogates(llmContext.SystemPrompt))}},
		}
	}
	if len(llmContext.Tools) > 0 {
		tools, err := ConvertGoogleTools(llmContext.Tools, false, supportsStrictMode)
		if err != nil {
			panic(err)
		}
		config.Tools = tools
	}
	if functionCallingMode != "" {
		config.ToolConfig = &googleToolConfig{
			FunctionCallingConfig: &googleFunctionCallingConfig{Mode: functionCallingMode},
		}
	}

	if options.Thinking != nil && options.Thinking.Enabled && model.Reasoning {
		thinkingConfig := &googleThinkingConfig{IncludeThoughts: true}
		if options.Thinking.Level != "" {
			thinkingConfig.ThinkingLevel = options.Thinking.Level
		} else if options.Thinking.BudgetTokens != nil {
			thinkingConfig.ThinkingBudget = options.Thinking.BudgetTokens
		}
		config.ThinkingConfig = thinkingConfig
	} else if model.Reasoning && options.Thinking != nil && !options.Thinking.Enabled {
		config.ThinkingConfig = getDisabledGoogleThinkingConfig(model)
	}

	return &googleGenerateContentParams{
		Model:    model.ID,
		Contents: contents,
		Config:   config,
	}
}

func isGemma4Model(modelID string) bool {
	// pi: /gemma-?4/.test(model.id.toLowerCase())
	return regexp.MustCompile(`gemma-?4`).MatchString(strings.ToLower(modelID))
}

func isGemini3ProModel(modelID string) bool {
	return regexp.MustCompile(`gemini-3(?:\.\d+)?-pro`).MatchString(strings.ToLower(modelID))
}

func isGemini3FlashModel(modelID string) bool {
	id := strings.ToLower(modelID)
	if regexp.MustCompile(`gemini-3(?:\.\d+)?-flash`).MatchString(id) {
		return true
	}
	return id == "gemini-flash-latest" || id == "gemini-flash-lite-latest"
}

// getDisabledGoogleThinkingConfig mirrors pi's getDisabledThinkingConfig:
// Gemini 3.1 Pro cannot disable thinking, and Gemini 3 Flash / Flash-Lite do
// not support full thinking-off either — use the lowest supported
// thinkingLevel without includeThoughts so hidden thinking remains invisible.
// Gemini 2.x disables via thinkingBudget = 0.
func getDisabledGoogleThinkingConfig(model *types.Model) *googleThinkingConfig {
	if isGemini3ProModel(model.ID) {
		return &googleThinkingConfig{ThinkingLevel: GoogleThinkingLevelLow}
	}
	if isGemini3FlashModel(model.ID) {
		return &googleThinkingConfig{ThinkingLevel: GoogleThinkingLevelMinimal}
	}
	if isGemma4Model(model.ID) {
		return &googleThinkingConfig{ThinkingLevel: GoogleThinkingLevelMinimal}
	}
	return &googleThinkingConfig{ThinkingBudget: lo.ToPtr(int64(0))}
}

// getGoogleThinkingLevel maps the resolved level to Google's ThinkingLevel
// enum for level-based models (pi: getThinkingLevel).
func getGoogleThinkingLevel(effort types.ModelThinkingLevel, model *types.Model) GoogleApiThinkingLevel {
	if isGemini3ProModel(model.ID) {
		switch effort {
		case types.ModelThinkingMinimal, types.ModelThinkingLow:
			return GoogleThinkingLevelLow
		case types.ModelThinkingMedium, types.ModelThinkingHigh:
			return GoogleThinkingLevelHigh
		}
	}
	if isGemma4Model(model.ID) {
		switch effort {
		case types.ModelThinkingMinimal, types.ModelThinkingLow:
			return GoogleThinkingLevelMinimal
		case types.ModelThinkingMedium, types.ModelThinkingHigh:
			return GoogleThinkingLevelHigh
		}
	}
	switch effort {
	case types.ModelThinkingMinimal:
		return GoogleThinkingLevelMinimal
	case types.ModelThinkingLow:
		return GoogleThinkingLevelLow
	case types.ModelThinkingMedium:
		return GoogleThinkingLevelMedium
	case types.ModelThinkingHigh:
		return GoogleThinkingLevelHigh
	}
	return GoogleThinkingLevelUnspecified
}

// getGoogleBudget resolves the thinking budget for token-based models (pi:
// getGoogleBudget).
func getGoogleBudget(model *types.Model, level types.ModelThinkingLevel, customBudgets *types.ThinkingBudgets) int64 {
	if customBudgets != nil {
		var budget *int64
		switch level {
		case types.ModelThinkingMinimal:
			budget = customBudgets.Minimal
		case types.ModelThinkingLow:
			budget = customBudgets.Low
		case types.ModelThinkingMedium:
			budget = customBudgets.Medium
		case types.ModelThinkingHigh:
			budget = customBudgets.High
		}
		if budget != nil {
			return *budget
		}
	}

	switch {
	case strings.Contains(model.ID, "2.5-pro"):
		switch level {
		case types.ModelThinkingMinimal:
			return 128
		case types.ModelThinkingLow:
			return 2048
		case types.ModelThinkingMedium:
			return 8192
		case types.ModelThinkingHigh:
			return 32768
		}
	case strings.Contains(model.ID, "2.5-flash-lite"):
		switch level {
		case types.ModelThinkingMinimal:
			return 512
		case types.ModelThinkingLow:
			return 2048
		case types.ModelThinkingMedium:
			return 8192
		case types.ModelThinkingHigh:
			return 24576
		}
	case strings.Contains(model.ID, "2.5-flash"):
		switch level {
		case types.ModelThinkingMinimal:
			return 128
		case types.ModelThinkingLow:
			return 2048
		case types.ModelThinkingMedium:
			return 8192
		case types.ModelThinkingHigh:
			return 24576
		}
	}
	return -1
}
