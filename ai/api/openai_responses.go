// pi: api/openai-responses.ts.
//
// The openai-responses wire adapter. It covers OpenAI, xAI, OpenRouter,
// proxies, and any Responses-shaped endpoint. The openai SDK (responses
// namespace, HTTP transport, SSE parsing) has no Go counterpart: the request
// body is built as a plain JSON map and the SSE response stream is parsed
// locally. Responses SSE frames are `event:`/`data:` pairs whose data JSON
// carries the discriminating `type` field, so the data line alone drives the
// reducer (shared with openai_completions' SSE framing).
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
	"strings"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// openAIResponsesToolCallProviders lists the providers whose tool-call ids
// keep their call_id|item_id pair shape on replay (pi:
// OPENAI_TOOL_CALL_PROVIDERS).
var openAIResponsesToolCallProviders = map[string]struct{}{
	"openai":       {},
	"openai-codex": {},
	"opencode":     {},
}

// OpenAI Responses rejects max_output_tokens below 16:
// https://github.com/earendil-works/pi/issues/6265
const openAIResponsesMinOutputTokens = int64(16)

// OpenAIResponsesOptions are the openai-responses request options (pi:
// OpenAIResponsesOptions extends StreamOptions).
type OpenAIResponsesOptions struct {
	types.StreamOptions
	// ReasoningEffort is the reasoning effort level. Empty means unset.
	ReasoningEffort types.ModelThinkingLevel
	// ReasoningSummary is the reasoning summary mode ("auto" | "detailed" |
	// "concise"); pi also allows null, which pig folds into "" (both take
	// the else-if branch / default to "auto").
	ReasoningSummary string
	// ServiceTier is the service_tier request value ("auto" | "default" |
	// "flex" | "priority" | ...); empty means unset (pi: undefined).
	ServiceTier string
	// ToolChoice is the tool_choice request value: a string choice or a
	// per-tool object (pi: ResponseCreateParamsStreaming["tool_choice"]).
	ToolChoice any
}

// OpenAIResponses implements ProviderStreams for the openai-responses API
// (pi: the { stream, streamSimple } module object).
type OpenAIResponses struct{}

var _ types.ProviderStreams = OpenAIResponses{}

// pi: compat.ts registers the builtin API implementations at module load;
// pig's layering rule (ai cannot import ai/api) puts the registration in the
// adapter package's init instead.
func init() {
	ai.RegisterBuiltInApiProvider(ai.ApiOpenAIResponses, OpenAIResponses{})
	ai.SetBuiltinApiRegistrar(func() {
		ai.RegisterBuiltInApiProvider(ai.ApiOpenAIResponses, OpenAIResponses{})
	})
}

// Stream dispatches one streaming request (pi: stream). ctx carries the
// caller's cancellation; pi passes options.signal into the SDK request
// options and the retry helper (pig: AbortSignal → context rule).
func (OpenAIResponses) Stream(ctx context.Context, model *types.Model, llmContext *types.Context, options *types.StreamOptions) *types.AssistantMessageEventStream {
	opts := &OpenAIResponsesOptions{}
	if options != nil {
		opts.StreamOptions = *options
	}
	return streamOpenAIResponses(ctx, model, llmContext, opts)
}

// StreamSimple dispatches one simple streaming request (pi: streamSimple).
func (OpenAIResponses) StreamSimple(ctx context.Context, model *types.Model, llmContext *types.Context, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
	if _, err := getClientAPIKey(string(model.Provider), options.APIKey, options.Headers); err != nil {
		return immediateErrorStream(model, err)
	}
	base := BuildBaseOptions(model, llmContext, options, options.APIKey)
	opts := &OpenAIResponsesOptions{StreamOptions: base}
	if options.ToolChoice != "" {
		opts.ToolChoice = string(options.ToolChoice)
	}
	reasoningEffort := types.ModelThinkingLevel("")
	if options.Reasoning != "" {
		clamped := ai.ClampThinkingLevel(model, types.ModelThinkingLevel(options.Reasoning))
		if clamped != types.ModelThinkingOff {
			reasoningEffort = clamped
		}
	}
	opts.ReasoningEffort = reasoningEffort
	return streamOpenAIResponses(ctx, model, llmContext, opts)
}

// detectSessionAffinityFormat picks the session-affinity header format from
// the endpoint URL (pi: detectSessionAffinityFormat).
func detectSessionAffinityFormat(model *types.Model) types.SessionAffinityFormat {
	if model.Provider == types.ProviderOpenRouter || strings.Contains(model.BaseURL, "openrouter.ai") {
		return types.SessionAffinityOpenRouter
	}
	return types.SessionAffinityOpenAI
}

// resolvedOpenAIResponsesCompat is the fully-resolved compatibility settings
// (pi: getCompat's Required<...> return).
type resolvedOpenAIResponsesCompat struct {
	supportsDeveloperRole           bool
	sessionAffinityFormat           types.SessionAffinityFormat
	supportsLongCacheRetention      bool
	supportsStrictMode              bool
	supportsOpenAIGrammarTools      bool
	supportsAdditionalTools         bool
	supportsToolSearch              bool
	supportsExplicitPromptCacheMode bool
	supportsMaxOutputTokens         bool
}

// getResponsesCompat resolves the model's openai-responses compat with
// defaults (pi: getCompat).
func getResponsesCompat(model *types.Model) resolvedOpenAIResponsesCompat {
	compat, _ := model.Compat.(*types.OpenAIResponsesCompat)
	resolved := resolvedOpenAIResponsesCompat{
		supportsDeveloperRole:           true,
		sessionAffinityFormat:           detectSessionAffinityFormat(model),
		supportsLongCacheRetention:      true,
		supportsStrictMode:              false,
		supportsOpenAIGrammarTools:      false,
		supportsAdditionalTools:         false,
		supportsToolSearch:              false,
		supportsExplicitPromptCacheMode: false,
		supportsMaxOutputTokens:         true,
	}
	if compat == nil {
		return resolved
	}
	if compat.SupportsDeveloperRole != nil {
		resolved.supportsDeveloperRole = *compat.SupportsDeveloperRole
	}
	if compat.SessionAffinityFormat != nil {
		resolved.sessionAffinityFormat = *compat.SessionAffinityFormat
	}
	if compat.SupportsLongCacheRetention != nil {
		resolved.supportsLongCacheRetention = *compat.SupportsLongCacheRetention
	}
	if compat.SupportsStrictMode != nil {
		resolved.supportsStrictMode = *compat.SupportsStrictMode
	}
	if compat.SupportsOpenAIGrammarTools != nil {
		resolved.supportsOpenAIGrammarTools = *compat.SupportsOpenAIGrammarTools
	}
	if compat.SupportsAdditionalTools != nil {
		resolved.supportsAdditionalTools = *compat.SupportsAdditionalTools
	}
	if compat.SupportsToolSearch != nil {
		resolved.supportsToolSearch = *compat.SupportsToolSearch
	}
	if compat.SupportsExplicitPromptCacheMode != nil {
		resolved.supportsExplicitPromptCacheMode = *compat.SupportsExplicitPromptCacheMode
	}
	if compat.SupportsMaxOutputTokens != nil {
		resolved.supportsMaxOutputTokens = *compat.SupportsMaxOutputTokens
	}
	return resolved
}

// getResponsesPromptCacheRetention resolves prompt_cache_retention (pi:
// getPromptCacheRetention). Empty means omit the field.
func getResponsesPromptCacheRetention(compat resolvedOpenAIResponsesCompat, cacheRetention types.CacheRetention) string {
	if cacheRetention == types.CacheRetentionLong && compat.supportsLongCacheRetention && !compat.supportsExplicitPromptCacheMode {
		return "24h"
	}
	return ""
}

// getResponsesPromptCacheOptions resolves prompt_cache_options (pi:
// getPromptCacheOptions). nil means omit the field.
func getResponsesPromptCacheOptions(compat resolvedOpenAIResponsesCompat, cacheRetention types.CacheRetention) map[string]any {
	if !compat.supportsExplicitPromptCacheMode {
		return nil
	}
	if cacheRetention == types.CacheRetentionNone {
		return map[string]any{"mode": "explicit"}
	}
	if cacheRetention == types.CacheRetentionLong && compat.supportsLongCacheRetention {
		return map[string]any{"ttl": "30m"}
	}
	return nil
}

// formatOpenAIResponsesError renders a provider failure (pi:
// formatOpenAIResponsesError).
func formatOpenAIResponsesError(err error) string {
	return utils.FormatProviderError(utils.NormalizeProviderError(err), "OpenAI API error")
}

// streamOpenAIResponses is the adapter's core stream implementation (pi: the
// stream StreamFunction body). ctx carries the caller's cancellation (pi:
// options.signal — the SDK request options signal, the post-stream
// signal.aborted check, and the catch path's aborted-vs-error decision).
func streamOpenAIResponses(ctx context.Context, model *types.Model, llmContext *types.Context, options *OpenAIResponsesOptions) *types.AssistantMessageEventStream {
	stream := types.NewAssistantMessageEventStream()

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

		// Content snapshotting is owned by ProcessResponsesStream: its block
		// list backs output.Content and every push re-syncs it. The stream
		// level only pushes start/done/error without touching content (pi:
		// output.content is shared; the catch path only strips scratch
		// fields, which pig's value snapshots never carried).
		push := func(event types.AssistantMessageEvent) {
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
			// "error"; errorMessage always carries the formatted message.
			if ctx.Err() != nil {
				output.StopReason = types.StopReasonAborted
			} else {
				output.StopReason = types.StopReasonError
			}
			output.ErrorMessage = formatOpenAIResponsesError(err)
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

		streamErr := func() error {
			apiKey, err := getClientAPIKey(string(model.Provider), options.APIKey, options.Headers)
			if err != nil {
				return err
			}
			cacheRetention := resolveCacheRetention(options.CacheRetention, options.Env)
			cacheSessionID := ""
			if cacheRetention != types.CacheRetentionNone {
				cacheSessionID = options.SessionID
			}
			compat := getResponsesCompat(model)
			grammarToolInputProperties := CreateGrammarToolInputProperties(llmContext.Tools, compat.supportsOpenAIGrammarTools)
			client := createResponsesClient(model, llmContext, apiKey, options.Headers, options.Fetch, cacheSessionID, compat)
			params := buildResponsesParams(model, llmContext, options, compat, grammarToolInputProperties)
			if options.OnPayload != nil {
				if next := options.OnPayload(params, model); next != nil {
					if nextParams, ok := next.(map[string]any); ok {
						params = nextParams
					}
				}
			}
			timeout := 10 * time.Minute
			if options.TimeoutMs != 0 {
				timeout = time.Duration(options.TimeoutMs) * time.Millisecond
			}
			maxRetries := 0
			if options.MaxRetries != nil {
				maxRetries = *options.MaxRetries
			}
			resp, err := utils.RetryProviderRequest(func() (*http.Response, error) {
				return client.createResponse(ctx, params, timeout)
			}, utils.ProviderRetryOptions{MaxRetries: maxRetries, MaxRetryDelayMs: options.MaxRetryDelayMs, Ctx: ctx})
			if err != nil {
				return err
			}
			if options.OnResponse != nil {
				options.OnResponse(types.ProviderResponse{Status: resp.StatusCode, Headers: utils.HeadersToRecord(resp.Header)}, model)
			}
			push(types.AssistantMessageEvent{Type: types.EventStart, Partial: output})

			// The reducer appends its own blocks and keeps output.Content
			// current through every push; the failure path below only reads it.
			return ProcessResponsesStream(decodeResponsesSSE(resp), output, stream, model, &OpenAIResponsesStreamOptions{
				ServiceTier:                options.ServiceTier,
				GrammarToolInputProperties: grammarToolInputProperties,
				ApplyServiceTierPricing: func(usage *types.Usage, serviceTier string) {
					applyResponsesServiceTierPricing(usage, serviceTier, model)
				},
			})
		}()

		if streamErr == nil {
			// pi: if (options?.signal?.aborted) throw new Error("Request was aborted").
			if ctx.Err() != nil {
				streamErr = errors.New("Request was aborted")
			} else if output.StopReason == types.StopReasonPending {
				streamErr = errors.New("OpenAI Responses stream ended without a stop reason")
			} else if output.StopReason == types.StopReasonAborted || output.StopReason == types.StopReasonError {
				if output.ErrorMessage != "" {
					streamErr = errors.New(output.ErrorMessage)
				} else {
					streamErr = errors.New("An unknown error occurred")
				}
			}
		}
		if streamErr != nil {
			fail(streamErr)
			return
		}

		push(types.AssistantMessageEvent{Type: types.EventDone, Reason: output.StopReason, Message: output})
		stream.EndWith(*output)
	}()

	return stream
}

// decodeResponsesSSE parses an SSE response body into decoded stream events
// (pi: the SDK's AsyncIterable<ResponseStreamEvent>). Data lines carry the
// event JSON including its discriminating `type`; [DONE] ends the stream.
func decodeResponsesSSE(resp *http.Response) func(yield func(*responsesStreamEvent) bool) {
	return func(yield func(*responsesStreamEvent) bool) {
		defer resp.Body.Close()
		reader := bufio.NewReader(resp.Body)
		var pending []string
		flush := func() bool {
			if len(pending) == 0 {
				return true
			}
			data := strings.Join(pending, "\n")
			pending = nil
			if data == "[DONE]" {
				return true
			}
			var event responsesStreamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				// pi: the SDK throws on malformed event JSON; surface it as a
				// synthetic error event so the reducer reports it.
				event = responsesStreamEvent{Type: "error", Code: "parse_error", Message: err.Error()}
			}
			return yield(&event)
		}
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				trimmed := strings.TrimRight(line, "\r\n")
				if strings.HasPrefix(trimmed, "data:") {
					pending = append(pending, strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")))
				} else if trimmed == "" {
					if !flush() {
						return
					}
				}
			}
			if err != nil {
				if err == io.EOF {
					flush()
				}
				return
			}
		}
	}
}

// openAIResponsesClient is the openai SDK client replacement (pi:
// createClient returns an OpenAI SDK instance; the SDK's responses endpoint
// is reproduced here).
type openAIResponsesClient struct {
	baseURL string
	apiKey  string
	headers map[string]string
	fetch   types.FetchFunction
}

// createResponsesClient builds the client (pi: createClient).
func createResponsesClient(model *types.Model, llmContext *types.Context, apiKey string, optionsHeaders types.ProviderHeaders, fetch types.FetchFunction, sessionID string, compat resolvedOpenAIResponsesCompat) *openAIResponsesClient {
	headers := map[string]string{"User-Agent": utils.GetPiUserAgent()}
	for name, value := range model.Headers {
		headers[name] = value
	}
	if model.Provider == "github-copilot" {
		hasImages := HasCopilotVisionInput(llmContext.Messages)
		for name, value := range BuildCopilotDynamicHeaders(llmContext.Messages, hasImages) {
			headers[name] = value
		}
	}

	if sessionID != "" {
		if compat.sessionAffinityFormat == types.SessionAffinityOpenRouter {
			headers["x-session-id"] = sessionID
		} else {
			if compat.sessionAffinityFormat == types.SessionAffinityOpenAI {
				headers["session_id"] = sessionID
			}
			headers["x-client-request-id"] = sessionID
		}
	}

	// Merge options headers last so they can override defaults
	for name, value := range optionsHeaders {
		if value == nil {
			continue
		}
		headers[name] = *value
	}
	return &openAIResponsesClient{
		baseURL: model.BaseURL,
		apiKey:  apiKey,
		headers: headers,
		fetch:   fetch,
	}
}

// createResponse performs the POST /responses request (pi:
// client.responses.create with maxRetries 0). Non-2xx statuses surface as
// *utils.ProviderHTTPError for the retry helper and error formatter.
func (c *openAIResponsesClient) createResponse(ctx context.Context, params map[string]any, timeout time.Duration) (*http.Response, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	url := strings.TrimSuffix(c.baseURL, "/") + "/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	authProvided := false
	for name, value := range c.headers {
		lower := strings.ToLower(name)
		if lower == "authorization" || lower == "cf-aig-authorization" {
			authProvided = true
		}
		// Direct map assignment preserves the header name case the openai SDK
		// would send (undici does not canonicalize; Go's Set upper-cases).
		req.Header[name] = []string{value}
	}
	if !authProvided {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
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

// buildResponsesParams builds the Responses request body (pi: buildParams).
func buildResponsesParams(model *types.Model, llmContext *types.Context, options *OpenAIResponsesOptions, compat resolvedOpenAIResponsesCompat, grammarToolInputProperties map[string]string) map[string]any {
	deferredToolsMode := ""
	if compat.supportsAdditionalTools {
		deferredToolsMode = "additional-tools"
	} else if compat.supportsToolSearch {
		deferredToolsMode = "tool-search"
	}
	toolPlacement := utils.SplitDeferredTools(llmContext, deferredToolsMode != "", nil)
	messages := ConvertResponsesMessages(model, llmContext, openAIResponsesToolCallProviders, &ConvertResponsesMessagesOptions{
		GrammarToolInputProperties: grammarToolInputProperties,
		DeferredTools:              toolPlacement.Deferred,
		DeferredToolNames:          toolPlacement.DeferredNames,
		DeferredToolsMode:          deferredToolsMode,
		ToolOptions: &ConvertResponsesToolsOptions{
			SupportsStrictMode:         &compat.supportsStrictMode,
			SupportsOpenAIGrammarTools: &compat.supportsOpenAIGrammarTools,
		},
	})

	cacheRetention := resolveCacheRetention(options.CacheRetention, options.Env)
	params := map[string]any{
		"model":  model.ID,
		"input":  messages,
		"stream": true,
		"store":  false,
	}
	if cacheRetention != types.CacheRetentionNone {
		params["prompt_cache_key"] = ClampOpenAIPromptCacheKey(options.SessionID)
	}
	if retention := getResponsesPromptCacheRetention(compat, cacheRetention); retention != "" {
		params["prompt_cache_retention"] = retention
	}
	if cacheOptions := getResponsesPromptCacheOptions(compat, cacheRetention); cacheOptions != nil {
		params["prompt_cache_options"] = cacheOptions
	}

	if options.MaxTokens != nil && *options.MaxTokens != 0 && compat.supportsMaxOutputTokens {
		params["max_output_tokens"] = maxInt64(*options.MaxTokens, openAIResponsesMinOutputTokens)
	}

	if options.Temperature != nil {
		params["temperature"] = *options.Temperature
	}

	if options.ServiceTier != "" {
		params["service_tier"] = options.ServiceTier
	}

	if len(toolPlacement.Immediate) > 0 {
		params["tools"] = ConvertResponsesTools(toolPlacement.Immediate, &ConvertResponsesToolsOptions{
			SupportsStrictMode:         &compat.supportsStrictMode,
			SupportsOpenAIGrammarTools: &compat.supportsOpenAIGrammarTools,
		})
	}

	if options.ToolChoice != nil {
		params["tool_choice"] = options.ToolChoice
	}

	if model.Reasoning {
		if options.ReasoningEffort != "" || options.ReasoningSummary != "" {
			effort := "medium"
			if options.ReasoningEffort != "" {
				effort = string(options.ReasoningEffort)
				if mapped, ok := model.ThinkingLevelMap[options.ReasoningEffort]; ok && mapped != nil {
					effort = *mapped
				}
			}
			summary := options.ReasoningSummary
			if summary == "" {
				summary = "auto"
			}
			params["reasoning"] = map[string]any{"effort": effort, "summary": summary}
			params["include"] = []any{"reasoning.encrypted_content"}
		} else if model.Provider != "github-copilot" && thinkingLevelOffAllowed(model) {
			params["reasoning"] = map[string]any{"effort": thinkingLevelOffValue(model)}
		}
		if model.Provider == types.ProviderXAI {
			params["include"] = []any{"reasoning.encrypted_content"}
		}
	}

	// Last so custom keys override the named request fields.
	for key, value := range options.SamplingParams {
		params[key] = value
	}

	return params
}

// getResponsesServiceTierCostMultiplier maps the served service tier to a
// cost multiplier (pi: getServiceTierCostMultiplier).
func getResponsesServiceTierCostMultiplier(model *types.Model, serviceTier string) float64 {
	switch serviceTier {
	case "flex":
		return 0.5
	case "priority":
		if model.ID == "gpt-5.5" {
			return 2.5
		}
		return 2
	default:
		return 1
	}
}

// applyResponsesServiceTierPricing rescales usage cost for the served tier
// (pi: applyServiceTierPricing).
func applyResponsesServiceTierPricing(usage *types.Usage, serviceTier string, model *types.Model) {
	multiplier := getResponsesServiceTierCostMultiplier(model, serviceTier)
	if multiplier == 1 {
		return
	}

	usage.Cost.Input *= multiplier
	usage.Cost.Output *= multiplier
	usage.Cost.CacheRead *= multiplier
	usage.Cost.CacheWrite *= multiplier
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
}
