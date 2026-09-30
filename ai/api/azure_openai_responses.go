// pi: api/azure-openai-responses.ts.
//
// The azure-openai-responses wire adapter: the openai-responses event
// protocol over Azure's deployment/api-version URL conventions. The AzureOpenAI
// SDK has no Go counterpart: the request body is built as a plain JSON map,
// the api-key header replaces the SDK's auth, and the SSE stream is parsed
// locally (shared reducer: ProcessResponsesStream).
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// defaultAzureAPIVersion is pi's DEFAULT_AZURE_API_VERSION.
const defaultAzureAPIVersion = "v1"

// azureToolCallProviders lists the providers whose tool-call ids keep their
// call_id|item_id pair shape on replay (pi: AZURE_TOOL_CALL_PROVIDERS).
var azureToolCallProviders = map[string]struct{}{
	"openai":                 {},
	"openai-codex":           {},
	"opencode":               {},
	"azure-openai-responses": {},
}

// AzureOpenAIResponsesOptions are the azure-openai-responses request options
// (pi: AzureOpenAIResponsesOptions extends StreamOptions).
type AzureOpenAIResponsesOptions struct {
	types.StreamOptions
	// ReasoningEffort is the reasoning effort level. Empty means unset.
	ReasoningEffort types.ModelThinkingLevel
	// ToolChoice is the tool_choice request value (string choice or
	// per-tool object); nil means unset.
	ToolChoice any
	// ReasoningSummary is the reasoning summary mode; pi also allows null,
	// which pig folds into "" (both default to "auto").
	ReasoningSummary string
	// AzureAPIVersion overrides the api-version query param. Default: env
	// AZURE_OPENAI_API_VERSION, else "v1".
	AzureAPIVersion string
	// AzureResourceName builds https://<name>.openai.azure.com/openai/v1
	// when no explicit base URL is available.
	AzureResourceName string
	// AzureBaseURL overrides the model's base URL.
	AzureBaseURL string
	// AzureDeploymentName overrides the deployment name (default: model id,
	// or the AZURE_OPENAI_DEPLOYMENT_NAME_MAP entry).
	AzureDeploymentName string
}

// AzureOpenAIResponses implements ProviderStreams for the azure-openai-responses
// API (pi: the { stream, streamSimple } module object).
type AzureOpenAIResponses struct{}

var _ types.ProviderStreams = AzureOpenAIResponses{}

// pi: compat.ts registers the builtin API implementations at module load;
// pig's layering rule (ai cannot import ai/api) puts the registration in the
// adapter package's init instead.
func init() {
	ai.RegisterBuiltInApiProvider(ai.ApiAzureOpenAIResponses, AzureOpenAIResponses{})
	ai.SetBuiltinApiRegistrar(func() {
		ai.RegisterBuiltInApiProvider(ai.ApiAzureOpenAIResponses, AzureOpenAIResponses{})
	})
}

// Stream dispatches one streaming request (pi: stream). ctx carries the
// caller's cancellation (pi: options.signal).
func (AzureOpenAIResponses) Stream(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.StreamOptions) *types.AssistantMessageEventStream {
	opts := &AzureOpenAIResponsesOptions{}
	if options != nil {
		opts.StreamOptions = *options
	}
	return streamAzureOpenAIResponses(ctx, model, llmContext, opts)
}

// StreamSimple dispatches one simple streaming request (pi: streamSimple).
func (AzureOpenAIResponses) StreamSimple(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
	if options.APIKey == "" {
		return immediateErrorStream(model, fmt.Errorf("No API key for provider: %s", model.Provider))
	}
	base := BuildBaseOptions(model, llmContext, options, options.APIKey)
	opts := &AzureOpenAIResponsesOptions{StreamOptions: base}
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
	return streamAzureOpenAIResponses(ctx, model, llmContext, opts)
}

// parseDeploymentNameMap parses the AZURE_OPENAI_DEPLOYMENT_NAME_MAP value
// ("modelId=deployment,..." pairs) (pi: parseDeploymentNameMap).
func parseDeploymentNameMap(value string) map[string]string {
	out := map[string]string{}
	if value == "" {
		return out
	}
	for _, entry := range strings.Split(value, ",") {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return out
}

// resolveDeploymentName resolves the Azure deployment for one model (pi:
// resolveDeploymentName).
func resolveDeploymentName(model *types.Model, options *AzureOpenAIResponsesOptions) string {
	if options != nil && options.AzureDeploymentName != "" {
		return options.AzureDeploymentName
	}
	envValue := ""
	if options != nil {
		envValue = utils.GetProviderEnvValue("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", options.Env)
	}
	if mapped, ok := parseDeploymentNameMap(envValue)[model.ID]; ok {
		return mapped
	}
	return model.ID
}

// formatAzureOpenAIError renders a provider failure (pi:
// formatAzureOpenAIError).
func formatAzureOpenAIError(err error) string {
	return utils.FormatProviderError(utils.NormalizeProviderError(err), "Azure OpenAI API error")
}

// streamAzureOpenAIResponses is the adapter's core stream implementation
// (pi: the stream StreamFunction body). ctx carries the caller's
// cancellation (pi: options.signal).
func streamAzureOpenAIResponses(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *AzureOpenAIResponsesOptions) *types.AssistantMessageEventStream {
	stream := types.NewAssistantMessageEventStream()
	normalizedContext := utils.ResolveTranscript(llmContext, azureSupportsMidConvoSystemMessages(model))

	go func() {
		deploymentName := resolveDeploymentName(model, options)

		output := &types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{},
			API:        types.ApiAzureOpenAIResponses,
			Provider:   model.Provider,
			Model:      model.ID,
			Usage:      types.Usage{},
			StopReason: types.StopReasonPending,
			Timestamp:  time.Now().UnixMilli(),
		}

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
			output.ErrorMessage = formatAzureOpenAIError(err)
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
			apiKey := options.APIKey
			if apiKey == "" {
				return fmt.Errorf("No API key for provider: %s", model.Provider)
			}
			client, err := createAzureResponsesClient(model, apiKey, options)
			if err != nil {
				return err
			}
			supportsOpenAIGrammarTools := false
			if compat, ok := model.Compat.(*types.OpenAIResponsesCompat); ok && compat != nil &&
				compat.SupportsOpenAIGrammarTools != nil {
				supportsOpenAIGrammarTools = *compat.SupportsOpenAIGrammarTools
			}
			grammarToolInputProperties := CreateGrammarToolInputProperties(utils.GetDeclaredTools(normalizedContext.Messages), supportsOpenAIGrammarTools)
			params := buildAzureResponsesParams(model, normalizedContext, options, deploymentName, grammarToolInputProperties)
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

			// Content snapshotting is owned by ProcessResponsesStream; the
			// failure path below only reads it (see openai_responses.go).
			return ProcessResponsesStream(decodeResponsesSSE(resp), output, stream, model, &OpenAIResponsesStreamOptions{
				GrammarToolInputProperties: grammarToolInputProperties,
			})
		}()

		if streamErr == nil {
			if ctx.Err() != nil {
				streamErr = errors.New("Request was aborted")
			} else if output.StopReason == types.StopReasonPending {
				streamErr = errors.New("Azure OpenAI Responses stream ended without a stop reason")
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

// normalizeAzureBaseURL validates and normalizes the Azure OpenAI base URL
// (pi: normalizeAzureBaseUrl): Azure hosts get the /openai/v1 base path so
// the deployment/api-version conventions resolve correctly.
func normalizeAzureBaseURL(baseURL string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("Invalid Azure OpenAI base URL: %s", baseURL)
	}

	host := parsed.Hostname()
	isAzureHost := strings.HasSuffix(host, ".openai.azure.com") ||
		strings.HasSuffix(host, ".cognitiveservices.azure.com") ||
		strings.HasSuffix(host, ".ai.azure.com")
	normalizedPath := strings.TrimRight(parsed.Path, "/")

	// Ensure Azure hosts have /openai/v1 as base path so the AzureOpenAI SDK
	// can append /deployments/<model>/... and ?api-version=v1 correctly.
	if isAzureHost {
		switch normalizedPath {
		case "", "/", "/openai", "/openai/v1/responses":
			parsed.Path = "/openai/v1"
			parsed.RawQuery = ""
		}
	}

	return strings.TrimRight(parsed.String(), "/"), nil
}

// buildAzureDefaultBaseURL builds the resource-name base URL (pi:
// buildDefaultBaseUrl).
func buildAzureDefaultBaseURL(resourceName string) string {
	return fmt.Sprintf("https://%s.openai.azure.com/openai/v1", resourceName)
}

// resolvedAzureConfig is the resolved base URL + api-version pair (pi:
// resolveAzureConfig's return).
type resolvedAzureConfig struct {
	baseURL    string
	apiVersion string
}

// resolveAzureConfig resolves the base URL and api-version (pi:
// resolveAzureConfig). Precedence: options, env, model.baseUrl; the model's
// base URL is the last fallback.
func resolveAzureConfig(model *types.Model, options *AzureOpenAIResponsesOptions) (*resolvedAzureConfig, error) {
	env := types.ProviderEnv(nil)
	if options != nil {
		env = options.Env
	}
	apiVersion := ""
	if options != nil {
		apiVersion = options.AzureAPIVersion
	}
	if apiVersion == "" {
		apiVersion = utils.GetProviderEnvValue("AZURE_OPENAI_API_VERSION", env)
	}
	if apiVersion == "" {
		apiVersion = defaultAzureAPIVersion
	}

	baseURL := ""
	if options != nil {
		baseURL = strings.TrimSpace(options.AzureBaseURL)
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(utils.GetProviderEnvValue("AZURE_OPENAI_BASE_URL", env))
	}
	resourceName := ""
	if options != nil && options.AzureResourceName != "" {
		resourceName = options.AzureResourceName
	} else {
		resourceName = utils.GetProviderEnvValue("AZURE_OPENAI_RESOURCE_NAME", env)
	}

	resolvedBaseURL := baseURL
	if resolvedBaseURL == "" && resourceName != "" {
		resolvedBaseURL = buildAzureDefaultBaseURL(resourceName)
	}
	if resolvedBaseURL == "" {
		resolvedBaseURL = model.BaseURL
	}
	if resolvedBaseURL == "" {
		return nil, errors.New(
			"Azure OpenAI base URL is required. Set AZURE_OPENAI_BASE_URL or AZURE_OPENAI_RESOURCE_NAME, or pass azureBaseUrl, azureResourceName, or model.baseUrl.")
	}

	normalized, err := normalizeAzureBaseURL(resolvedBaseURL)
	if err != nil {
		return nil, err
	}
	return &resolvedAzureConfig{baseURL: normalized, apiVersion: apiVersion}, nil
}

// azureResponsesClient is the AzureOpenAI SDK client replacement (pi:
// createClient returns an AzureOpenAI SDK instance; the SDK's responses
// endpoint is reproduced here).
type azureResponsesClient struct {
	baseURL    string
	apiVersion string
	apiKey     string
	headers    map[string]string
	fetch      types.FetchFunction
}

// createAzureResponsesClient builds the client (pi: createClient).
func createAzureResponsesClient(model *types.Model, apiKey string, options *AzureOpenAIResponsesOptions) (*azureResponsesClient, error) {
	headers := map[string]string{"User-Agent": utils.GetPiUserAgent()}
	for name, value := range model.Headers {
		headers[name] = value
	}
	if options != nil && options.Headers != nil {
		for name, value := range options.Headers {
			if value == nil {
				continue
			}
			headers[name] = *value
		}
	}

	config, err := resolveAzureConfig(model, options)
	if err != nil {
		return nil, err
	}

	return &azureResponsesClient{
		baseURL:    config.baseURL,
		apiVersion: config.apiVersion,
		apiKey:     apiKey,
		headers:    headers,
		fetch:      options.Fetch,
	}, nil
}

// createResponse performs the POST /responses request with the api-version
// query param and the api-key auth header (pi:
// client.responses.create with maxRetries 0). Non-2xx statuses surface as
// *utils.ProviderHTTPError for the retry helper and error formatter.
func (c *azureResponsesClient) createResponse(ctx context.Context, params map[string]any, timeout time.Duration) (*http.Response, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	requestURL := c.baseURL + "/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// pi: the AzureOpenAI SDK sends the api-version as a query param on
	// every request.
	query := req.URL.Query()
	query.Set("api-version", c.apiVersion)
	req.URL.RawQuery = query.Encode()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", c.apiKey)
	for name, value := range c.headers {
		// Direct map assignment preserves the header name case (see
		// openai_responses.go).
		req.Header[name] = []string{value}
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
		bodyBytes, readErr := io.ReadAll(resp.Body)
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

// buildAzureResponsesParams builds the Responses request body (pi:
// buildParams). The model field carries the deployment name; no session
// affinity/prompt-cache options beyond prompt_cache_key.
func buildAzureResponsesParams(model *types.Model, llmContext *types.TranscriptContext, options *AzureOpenAIResponsesOptions, deploymentName string, grammarToolInputProperties map[string]string) map[string]any {
	supportsAdditionalTools := false
	supportsToolSearch := false
	supportsStrictMode := true
	supportsOpenAIGrammarTools := false
	var supportsMidConvoSystemMessages bool
	if compat, ok := model.Compat.(*types.OpenAIResponsesCompat); ok && compat != nil {
		if compat.SupportsAdditionalTools != nil {
			supportsAdditionalTools = *compat.SupportsAdditionalTools
		}
		if compat.SupportsToolSearch != nil {
			supportsToolSearch = *compat.SupportsToolSearch
		}
		if compat.SupportsStrictMode != nil {
			supportsStrictMode = *compat.SupportsStrictMode
		}
		if compat.SupportsOpenAIGrammarTools != nil {
			supportsOpenAIGrammarTools = *compat.SupportsOpenAIGrammarTools
		}
		supportsMidConvoSystemMessages = compat.SupportsMidConvoSystemMessages != nil && *compat.SupportsMidConvoSystemMessages
	}
	transcriptTools := utils.ResolveTranscriptTools(llmContext.Messages, supportsAdditionalTools || supportsToolSearch)
	messages := ConvertResponsesMessages(model, llmContext, azureToolCallProviders, &ConvertResponsesMessagesOptions{
		GrammarToolInputProperties:     grammarToolInputProperties,
		SupportsMidConvoSystemMessages: lo.ToPtr(supportsMidConvoSystemMessages),
		SupportsAdditionalTools:        lo.ToPtr(supportsAdditionalTools),
		SupportsToolSearch:             lo.ToPtr(supportsToolSearch),
		ToolOptions: &ConvertResponsesToolsOptions{
			SupportsStrictMode:         lo.ToPtr(supportsStrictMode),
			SupportsOpenAIGrammarTools: lo.ToPtr(supportsOpenAIGrammarTools),
		},
	})

	params := map[string]any{
		"model":  deploymentName,
		"input":  messages,
		"stream": true,
		"store":  false,
	}
	// pi: prompt_cache_key: clampOpenAIPromptCacheKey(options?.sessionId) —
	// set unconditionally; clamping an empty key stays empty.
	params["prompt_cache_key"] = ClampOpenAIPromptCacheKey(options.SessionID)

	if options.MaxTokens != nil && *options.MaxTokens != 0 {
		params["max_output_tokens"] = maxInt64(*options.MaxTokens, openAIResponsesMinOutputTokens)
	}

	if options.Temperature != nil {
		params["temperature"] = *options.Temperature
	}

	if len(transcriptTools.RequestTools) > 0 {
		params["tools"] = ConvertResponsesTools(transcriptTools.RequestTools, &ConvertResponsesToolsOptions{
			SupportsStrictMode:         lo.ToPtr(supportsStrictMode),
			SupportsOpenAIGrammarTools: lo.ToPtr(supportsOpenAIGrammarTools),
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
		} else if thinkingLevelOffAllowed(model) {
			params["reasoning"] = map[string]any{"effort": thinkingLevelOffValue(model)}
		}
	}

	// Last so custom keys override the named request fields. Model defaults
	// apply first, per-request keys override them (pi: Object.assign(params,
	// model.samplingParams, options?.samplingParams), #9506).
	for key, value := range model.SamplingParams {
		params[key] = value
	}
	for key, value := range options.SamplingParams {
		params[key] = value
	}

	return params
}

// azureSupportsMidConvoSystemMessages resolves the Responses compat flag
// (pi: model.compat?.supportsMidConvoSystemMessages ?? false).
func azureSupportsMidConvoSystemMessages(model *types.Model) bool {
	if compat, ok := model.Compat.(*types.OpenAIResponsesCompat); ok && compat != nil &&
		compat.SupportsMidConvoSystemMessages != nil {
		return *compat.SupportsMidConvoSystemMessages
	}
	return false
}
