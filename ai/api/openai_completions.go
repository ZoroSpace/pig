// pi: api/openai-completions.ts.
//
// The openai-completions wire adapter. It covers openai, openrouter, deepseek,
// and any OpenAI-shaped endpoint. The openai npm SDK (chat completions
// request/response types, HTTP transport, SSE parsing) has no Go counterpart:
// the request body is built as a plain JSON map and the SSE response stream is
// parsed locally.
package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// OpenAICompletionsOptions are the openai-completions request options (pi:
// OpenAICompletionsOptions extends StreamOptions).
type OpenAICompletionsOptions struct {
	types.StreamOptions
	ToolChoice types.ToolChoice
	// ReasoningEffort is a model thinking level other than "off" (pi: the
	// Exclude<ModelThinkingLevel, "off"> union). Empty means unset.
	ReasoningEffort types.ModelThinkingLevel
	// ThinkingBudgets are token budgets per thinking level; used when compat
	// thinkingTokenBudgetField is set, or by { "$var": "thinking.budget" }.
	ThinkingBudgets *types.ThinkingBudgets
}

// OpenAICompletions implements ProviderStreams for the openai-completions API
// (pi: the { stream, streamSimple } module object).
type OpenAICompletions struct{}

var _ types.ProviderStreams = OpenAICompletions{}

// pi: compat.ts registers the builtin API implementations at module load;
// pig's layering rule (ai cannot import ai/api) puts the registration in the
// adapter package's init instead.
func init() {
	ai.RegisterBuiltInApiProvider(ai.ApiOpenAICompletions, OpenAICompletions{})
	ai.SetBuiltinApiRegistrar(func() {
		ai.RegisterBuiltInApiProvider(ai.ApiOpenAICompletions, OpenAICompletions{})
	})
}

// Stream dispatches one streaming request (pi: stream). ctx carries the
// caller's cancellation; pi passes options.signal into the SDK request
// options and the retry helper (pig: AbortSignal → context rule, README
// §3.4).
func (OpenAICompletions) Stream(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.StreamOptions) *types.AssistantMessageEventStream {
	opts := OpenAICompletionsOptions{}
	if options != nil {
		opts.StreamOptions = *options
	}
	return streamOpenAICompletions(ctx, model, llmContext, &opts)
}

// StreamSimple dispatches one simple streaming request (pi: streamSimple).
func (OpenAICompletions) StreamSimple(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
	_, err := getClientAPIKey(string(model.Provider), options.APIKey, options.Headers)
	if err != nil {
		return immediateErrorStream(model, err)
	}
	base := BuildBaseOptions(model, llmContext, options, options.APIKey)
	var reasoningEffort types.ModelThinkingLevel
	if options.Reasoning != "" {
		clamped := ai.ClampThinkingLevel(model, types.ModelThinkingLevel(options.Reasoning))
		if clamped != types.ModelThinkingOff {
			reasoningEffort = clamped
		}
	}
	opts := &OpenAICompletionsOptions{
		StreamOptions:   base,
		ToolChoice:      options.ToolChoice,
		ReasoningEffort: reasoningEffort,
		ThinkingBudgets: options.ThinkingBudgets,
	}
	return streamOpenAICompletions(ctx, model, llmContext, opts)
}

// immediateErrorStream terminates a stream with a single error event (used
// where pi throws synchronously before returning a stream).
func immediateErrorStream(model *types.Model, err error) *types.AssistantMessageEventStream {
	stream := types.NewAssistantMessageEventStream()
	message := &types.AssistantMessage{
		Role:         types.RoleAssistant,
		Content:      []types.Content{},
		API:          model.API,
		Provider:     model.Provider,
		Model:        model.ID,
		Usage:        types.Usage{},
		StopReason:   types.StopReasonError,
		ErrorMessage: err.Error(),
		Timestamp:    time.Now().UnixMilli(),
	}
	stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonError, Message: message})
	stream.EndWith(*message)
	return stream
}

// hasHeader reports whether a header with the given name (case-insensitive)
// has a non-empty value (pi: hasHeader).
func hasHeader(headers types.ProviderHeaders, name string) bool {
	if headers == nil {
		return false
	}
	expected := strings.ToLower(name)
	for key, value := range headers {
		if strings.ToLower(key) == expected && value != nil && strings.TrimSpace(*value) != "" {
			return true
		}
	}
	return false
}

// getClientAPIKey resolves the API key for the request, tolerating
// header-supplied auth (pi: getClientApiKey).
func getClientAPIKey(provider string, apiKey string, headers types.ProviderHeaders) (string, error) {
	if apiKey != "" {
		return apiKey, nil
	}
	if hasHeader(headers, "authorization") || hasHeader(headers, "cf-aig-authorization") {
		return "unused", nil
	}
	return "", fmt.Errorf("No API key for provider: %s", provider)
}

// hasToolHistory reports whether the messages contain tool calls or tool
// results (pi: hasToolHistory). Needed because Anthropic (via proxy) requires
// the tools param when messages include tool_calls or tool role messages.
func hasToolHistory(messages []types.Message) bool {
	for _, msg := range messages {
		switch m := msg.(type) {
		case types.ToolResultMessage:
			return true
		case types.AssistantMessage:
			for _, block := range m.Content {
				if block.ContentKind() == types.KindToolCall {
					return true
				}
			}
		}
	}
	return false
}

// getDeferredToolNames/getToolsByName were removed upstream with #9548:
// deferred tool placement moved to transcript system messages.

// openAIReasoningDetail models the OpenAI reasoning detail objects carried in
// reasoning_details (pi: OpenAIReasoningDetailBase and its three variants).
type openAIReasoningDetail struct {
	ID     string
	Format string
	Index  *float64
	Type   string
	Detail map[string]any
}

func isReasoningDetailObject(value any) bool {
	_, ok := value.(map[string]any)
	return ok
}

func hasValidCommonReasoningDetailFields(candidate map[string]any) bool {
	if id, present := candidate["id"]; present && id != nil {
		if _, ok := id.(string); !ok {
			return false
		}
	}
	if format, present := candidate["format"]; present {
		if _, ok := format.(string); !ok {
			return false
		}
	}
	if index, present := candidate["index"]; present {
		if _, ok := index.(float64); !ok {
			return false
		}
	}
	return true
}

func isOpenAIReasoningDetail(value any) bool {
	if !isReasoningDetailObject(value) || !hasValidCommonReasoningDetailFields(value.(map[string]any)) {
		return false
	}
	candidate := value.(map[string]any)
	switch candidate["type"] {
	case "reasoning.summary":
		_, ok := candidate["summary"].(string)
		return ok
	case "reasoning.encrypted":
		_, ok := candidate["data"].(string)
		return ok
	case "reasoning.text":
		text, ok := candidate["text"].(string)
		if !ok {
			return false
		}
		_ = text
		if signature, present := candidate["signature"]; present && signature != nil {
			if _, ok := signature.(string); !ok {
				return false
			}
		}
		return true
	}
	return false
}

// parseOpenAIReasoningDetails parses a thinkingSignature into a validated
// reasoning detail list (pi: parseOpenAIReasoningDetails).
func parseOpenAIReasoningDetails(signature string) []map[string]any {
	if signature == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(signature), &parsed); err != nil {
		return nil
	}
	arr, ok := parsed.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	var out []map[string]any
	for _, detail := range arr {
		if !isOpenAIReasoningDetail(detail) {
			return nil
		}
		out = append(out, detail.(map[string]any))
	}
	return out
}

// parseLegacyEncryptedReasoningDetail parses a tool call thoughtSignature as a
// single encrypted reasoning detail (pi: parseLegacyEncryptedReasoningDetail).
func parseLegacyEncryptedReasoningDetail(signature string) map[string]any {
	if signature == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(signature), &parsed); err != nil {
		return nil
	}
	detail, ok := parsed.(map[string]any)
	if !ok || !isOpenAIReasoningDetail(detail) || detail["type"] != "reasoning.encrypted" {
		return nil
	}
	id, idOK := detail["id"].(string)
	data, dataOK := detail["data"].(string)
	if !idOK || id == "" || !dataOK || data == "" {
		return nil
	}
	return detail
}

// fillMissingCommonReasoningDetailFields fills id/format/index on target
// from source when missing (pi: fillMissingCommonReasoningDetailFields;
// pi's ??=/||= collapse to nil/empty checks, and a value TS would drop as
// undefined is deleted from the map so json.Marshal matches
// JSON.stringify).
func fillMissingCommonReasoningDetailFields(target, source map[string]any) {
	if id, ok := target["id"]; !ok || id == nil {
		if sid, ok := source["id"]; ok && sid != nil {
			target["id"] = sid
		}
	}
	if format, ok := target["format"]; !ok || format == "" {
		if sformat, ok := source["format"].(string); ok && sformat != "" {
			target["format"] = sformat
		} else {
			delete(target, "format")
		}
	}
	if index, ok := target["index"]; !ok || index == nil {
		if sindex, ok := source["index"]; ok && sindex != nil {
			target["index"] = sindex
		}
	}
}

// appendOpenAIReasoningDetail appends one streamed reasoning detail to the
// accumulating list, merging consecutive text/summary deltas into logical
// entries while encrypted entries remain opaque and discrete (pi:
// appendOpenAIReasoningDetail; OpenRouter streams reasoning_details as
// deltas).
func appendOpenAIReasoningDetail(details []map[string]any, detail map[string]any) []map[string]any {
	var lastDetail map[string]any
	if len(details) > 0 {
		lastDetail = details[len(details)-1]
	}
	if detail["type"] == "reasoning.text" && lastDetail != nil && lastDetail["type"] == "reasoning.text" {
		lastDetail["text"] = lastDetail["text"].(string) + detail["text"].(string)
		if sig, ok := lastDetail["signature"]; !ok || sig == nil || sig == "" {
			if dsig, ok := detail["signature"]; ok && dsig != nil && dsig != "" {
				lastDetail["signature"] = dsig
			} else {
				delete(lastDetail, "signature")
			}
		}
		fillMissingCommonReasoningDetailFields(lastDetail, detail)
		return details
	}
	if detail["type"] == "reasoning.summary" && lastDetail != nil && lastDetail["type"] == "reasoning.summary" {
		lastDetail["summary"] = lastDetail["summary"].(string) + detail["summary"].(string)
		fillMissingCommonReasoningDetailFields(lastDetail, detail)
		return details
	}
	clone := make(map[string]any, len(detail))
	for key, value := range detail {
		clone[key] = value
	}
	return append(details, clone)
}

var openAICompletionsReasoningFields = []string{"reasoning", "reasoning_content", "reasoning_text"}

func isOpenAICompletionsReasoningField(field string) bool {
	for _, f := range openAICompletionsReasoningFields {
		if f == field {
			return true
		}
	}
	return false
}

// resolveCacheRetention resolves the cache retention preference (pi:
// resolveCacheRetention): the option wins, else the PI_CACHE_RETENTION env,
// else "short".
func resolveCacheRetention(cacheRetention types.CacheRetention, env types.ProviderEnv) types.CacheRetention {
	if cacheRetention != "" {
		return cacheRetention
	}
	if utils.GetProviderEnvValue("PI_CACHE_RETENTION", env) == "long" {
		return types.CacheRetentionLong
	}
	return types.CacheRetentionShort
}

// openAICacheControl is the OpenAI-compatible cache_control object (pi:
// OpenAICompatCacheControl).
type openAICacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

// resolvedOpenAICompletionsCompat is the fully-resolved compatibility settings
// (pi: ResolvedOpenAICompletionsCompat — Required except the five optional
// members).
type resolvedOpenAICompletionsCompat struct {
	supportsStore                               bool
	supportsDeveloperRole                       bool
	supportsReasoningEffort                     bool
	supportsUsageInStreaming                    bool
	supportsFinishReason                        bool
	maxTokensField                              types.MaxTokensField
	requiresToolResultName                      bool
	requiresAssistantAfterToolResult            bool
	requiresThinkingAsText                      bool
	requiresReasoningContentOnAssistantMessages bool
	thinkingFormat                              types.ThinkingFormat
	openRouterRouting                           *types.OpenRouterRouting
	vercelGatewayRouting                        *types.VercelGatewayRouting
	chatTemplateKwargs                          map[string]types.ChatTemplateKwargValue
	chatTemplateArgs                            map[string]types.ChatTemplateKwargValue
	zaiToolStream                               bool
	supportsThinkingTokenBudget                 bool
	thinkingTokenBudgetField                    *types.ThinkingTokenBudgetField
	supportsStrictMode                          bool
	supportsOpenAIGrammarTools                  bool
	supportsMidConvoSystemMessages              bool
	supportsMidConvoToolAdditions               bool
	cacheControlFormat                          *types.CacheControlFormat
	sendSessionAffinityHeaders                  bool
	sessionAffinityFormat                       types.SessionAffinityFormat
	supportsLongCacheRetention                  bool
	vllmPriority                                *float64
}

// chatCompletionChunk is the streamed chat completion chunk (pi: openai SDK's
// ChatCompletionChunk).
type chatCompletionChunk struct {
	ID      string                      `json:"id"`
	Model   string                      `json:"model"`
	Choices []chatCompletionChunkChoice `json:"choices"`
	Usage   *chatCompletionUsage        `json:"usage"`
}

type chatCompletionChunkChoice struct {
	Index        int                      `json:"index"`
	Delta        chatCompletionChunkDelta `json:"delta"`
	FinishReason *string                  `json:"finish_reason"`
	Usage        *chatCompletionUsage     `json:"usage"`
}

type chatCompletionChunkDelta struct {
	Content          *string                       `json:"content"`
	ReasoningContent *string                       `json:"reasoning_content"`
	Reasoning        *string                       `json:"reasoning"`
	ReasoningText    *string                       `json:"reasoning_text"`
	ReasoningDetails []any                         `json:"reasoning_details"`
	ToolCalls        []chatCompletionToolCallDelta `json:"tool_calls"`
}

type chatCompletionToolCallDelta struct {
	Index    *int                         `json:"index"`
	ID       string                       `json:"id"`
	Type     string                       `json:"type"`
	Function *chatCompletionFunctionDelta `json:"function"`
	Custom   *chatCompletionCustomDelta   `json:"custom"`
}

type chatCompletionFunctionDelta struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatCompletionCustomDelta struct {
	Name  string `json:"name"`
	Input string `json:"input"`
}

type chatCompletionUsage struct {
	PromptTokens         int `json:"prompt_tokens"`
	CompletionTokens     int `json:"completion_tokens"`
	CachedTokens         int `json:"cached_tokens"`
	PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
	PromptTokensDetails  *struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// openaiCompletionsClient is the openai SDK client replacement (pi:
// createClient returns an OpenAI SDK instance; the SDK's chat.completions
// endpoint is reproduced here).
type openaiCompletionsClient struct {
	baseURL string
	apiKey  string
	headers map[string]string
	fetch   types.FetchFunction
	timeout time.Duration
}

// createClient builds the client (pi: createClient).
func createClient(model *types.Model, llmContext *types.TranscriptContext, apiKey string, optionsHeaders types.ProviderHeaders, identity *types.ClientIdentity, fetch types.FetchFunction, sessionID string, compat *resolvedOpenAICompletionsCompat) *openaiCompletionsClient {
	headers := utils.ProviderHeadersToRecord(utils.GetIdentityHeaders(types.ClientIdentityTarget{Provider: string(model.Provider), BaseURL: model.BaseURL}, identity))
	for name, value := range model.Headers {
		headers[name] = value
	}
	// pi: model.provider === "github-copilot". The constant arrives when the
	// github-copilot provider is ported (README Phase 2); string compare is
	// equivalent for now.
	if model.Provider == "github-copilot" {
		hasImages := HasCopilotVisionInput(llmContext.Messages)
		for name, value := range BuildCopilotDynamicHeaders(llmContext.Messages, hasImages) {
			headers[name] = value
		}
	}
	if sessionID != "" && compat.sendSessionAffinityHeaders {
		if compat.sessionAffinityFormat == types.SessionAffinityOpenRouter {
			headers["x-session-id"] = sessionID
		} else {
			if compat.sessionAffinityFormat == types.SessionAffinityOpenAI {
				headers["session_id"] = sessionID
			}
			headers["x-client-request-id"] = sessionID
			headers["x-session-affinity"] = sessionID
		}
	}
	// Merge options headers last so they can override defaults.
	for name, value := range optionsHeaders {
		if value == nil {
			delete(headers, name)
			continue
		}
		headers[name] = *value
	}
	return &openaiCompletionsClient{
		baseURL: model.BaseURL,
		apiKey:  apiKey,
		headers: headers,
		fetch:   fetch,
	}
}

// createChatCompletion performs the POST /chat/completions request (pi:
// client.chat.completions.create with maxRetries 0). It returns the raw
// response; non-2xx statuses surface as *utils.ProviderHTTPError for the retry
// helper and error formatter.
func (c *openaiCompletionsClient) createChatCompletion(ctx context.Context, params map[string]any, timeout time.Duration) (*http.Response, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	url := strings.TrimSuffix(c.baseURL, "/") + "/chat/completions"
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
		req.Header.Set(name, value)
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

// requestOptions is the per-request SDK options (pi: the inline requestOptions
// object: signal is dropped per the AbortSignal → context rule).
type requestOptions struct {
	timeout time.Duration
}

// streamSSEChunks parses an SSE response body, invoking onData for each
// event's data payload (the JSON chunk or [DONE]). It matches the openai SDK's
// SSE framing: data lines accumulate until a blank line.
func streamSSEChunks(resp *http.Response, onData func(data string) error) error {
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	var pending []string
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		data := strings.Join(pending, "\n")
		pending = nil
		if data == "[DONE]" {
			return nil
		}
		return onData(data)
	}
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(trimmed, "data:") {
				pending = append(pending, strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")))
			} else if trimmed == "" {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return flush()
			}
			return err
		}
	}
}
