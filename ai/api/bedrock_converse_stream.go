// pi: api/bedrock-converse-stream.ts.
//
// The AWS Bedrock Converse Stream wire adapter. pi goes through
// @aws-sdk/client-bedrock-runtime (BedrockRuntimeClient + ConverseStreamCommand);
// the SDK has no Go counterpart, so pig rewrites the exercised SDK surface
// natively on aws-sdk-go-v2 (C3/C4 "SDK rewrite" precedent):
//
//   - transport: BedrockRuntimeClient.ConverseStream with SigV4 signing, the
//     AWS credential chain, and the vnd.amazon.eventstream binary framing are
//     carried by aws-sdk-go-v2. The client config pi puts into
//     BedrockRuntimeClientConfig (profile / region / endpoint / static
//     credentials / bearer token / proxy handler) is mirrored field-for-field
//     into bedrockClientConfig; tests capture it through the bedrockNewClient
//     seam exactly like pi's vi.mock of the SDK module.
//
//   - region: ARN-embedded > explicit option/env > endpoint-derived > the
//     us-east-1 fallback (pi's Node branch; the browser branch is unreachable
//     in pig). Standard bedrock-runtime endpoints are only pinned when no
//     region or ambient AWS_PROFILE is configured (pi #3402/#6957).
//
//   - auth: bearer token (options.bearerToken / apiKey / AWS_BEARER_TOKEN_BEDROCK,
//     httpBearerAuth), AWS_BEDROCK_SKIP_AUTH dummy credentials, static env
//     keys, or the SDK default chain. A profile configured through pi (the
//     profile option or scoped AWS_PROFILE) must beat ambient env keys (pi
//     #6957); the Go SDK chain prefers env keys, so static-key profiles are
//     re-resolved from the shared profile (role/SSO profiles fall back to the
//     SDK chain — declared deviation, see bedrockProfileCredentialsProvider).
//
//   - middleware: caller headers ride a Smithy build-step middleware (signed;
//     reserved headers skipped); onResponse receives the raw Smithy response
//     at the deserialize step, with the $metadata fallback pi implements.
//
//   - retry: pi relies on the JS SDK's default retry mode (3 attempts); pig
//     relies on the Go SDK's default retryer (3 attempts) — equal counts, no
//     pi-level options.MaxRetries wiring (pi also ignores it for bedrock).
//
//   - AWS_BEDROCK_FORCE_HTTP1 is accepted for parity but is a no-op: the Go
//     SDK speaks HTTP/1.1 by default (pi forces NodeHttpHandler because the JS
//     SDK switched to HTTP/2 in 3.798.0).
//
//   - errors: the Go SDK folds the raw HTTP body into the smithy APIError
//     message, so formatBedrockError always stays on the message path (pi's
//     status:body branch reads $response.body, which the Go SDK does not
//     retain on the error).
package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	smithy "github.com/aws/smithy-go"
	smithybearer "github.com/aws/smithy-go/auth/bearer"
	smithymiddleware "github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/samber/lo"
)

// BedrockThinkingDisplay controls how Claude thinking content is returned
// (pi: BedrockThinkingDisplay):
//   - "summarized": thinking blocks contain summarized thinking text (default).
//   - "omitted": thinking is redacted but the signature still travels back for
//     multi-turn continuity, reducing time-to-first-text-token.
//
// pi's note carries over: Anthropic's API default for Claude Opus 4.8 and
// Mythos Preview is "omitted"; pi defaults to "summarized" for consistency
// with older Claude 4 models. Only applies to Claude models on Bedrock.
type BedrockThinkingDisplay string

const (
	BedrockThinkingDisplaySummarized BedrockThinkingDisplay = "summarized"
	BedrockThinkingDisplayOmitted    BedrockThinkingDisplay = "omitted"
)

// BedrockToolChoice is the bedrock tool-choice behavior (pi: BedrockOptions
// toolChoice, the "auto" | "any" | "none" | { type: "tool", name } union —
// string choices are modeled as Type with an empty Name).
type BedrockToolChoice struct {
	Type string `json:"type"` // "auto" | "any" | "none" | "tool"
	Name string `json:"name,omitempty"`
}

// BedrockOptions are the bedrock-converse-stream request options (pi:
// BedrockOptions extends StreamOptions).
type BedrockOptions struct {
	types.StreamOptions
	// Region is the AWS region for the request; falls back to AWS_REGION /
	// AWS_DEFAULT_REGION / the model endpoint / the SDK default chain.
	Region string
	// Profile selects a shared-config profile; also settable through the
	// scoped AWS_PROFILE (pi: profile).
	Profile string
	// ToolChoice is "auto", "any", "none", or a named tool.
	ToolChoice *BedrockToolChoice
	// Reasoning requests thinking for models that support it (pi: reasoning).
	// See https://docs.aws.amazon.com/bedrock/latest/userguide/inference-reasoning.html
	// for supported models.
	Reasoning types.ThinkingLevel
	// ThinkingBudgets are custom token budgets per thinking level; overrides
	// the default budgets (pi: thinkingBudgets).
	ThinkingBudgets *types.ThinkingBudgets
	// InterleavedThinking requests the interleaved-thinking beta for
	// non-adaptive Claude thinking. Only supported by Claude 4.x models, see
	// https://docs.aws.amazon.com/bedrock/latest/userguide/claude-messages-extended-thinking.html#claude-messages-extended-thinking-tool-use-interleaved.
	// Default: true.
	InterleavedThinking *bool
	// ThinkingDisplay controls how Claude's thinking content is returned (pi:
	// thinkingDisplay). Default: "summarized".
	ThinkingDisplay BedrockThinkingDisplay
	// RequestMetadata attaches key-value pairs to the inference request for
	// cost allocation tagging. Keys: max 64 chars, no `aws:` prefix. Values:
	// max 256 chars. Max 50 pairs. Tags appear in AWS Cost Explorer split
	// cost allocation data.
	// @see https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ConverseStream.html
	RequestMetadata map[string]string
	// BearerToken is the Bedrock API key for bearer authentication. When set,
	// SigV4 signing is bypassed and Authorization: Bearer <token> is sent
	// instead; requires `bedrock:CallWithBearerToken` IAM permission on the
	// token's identity. Set via AWS_BEARER_TOKEN_BEDROCK env var or passed
	// directly.
	// @see https://docs.aws.amazon.com/service-authorization/latest/reference/list_amazonbedrock.html
	BearerToken string
}

// bedrockBlock is the streaming scratch state of one output content block
// (pi: Block = content & { index, partialJson, redactedChunks }). pig keeps
// the scratch beside output.Content instead of on it, so it can never reach a
// persisted message.
type bedrockBlock struct {
	// index is the provider content block index (pi: Block.index).
	index int32
	// pos is the block's position in output.Content (pi reads the fields off
	// output.content entries directly; pig stores scratch beside them).
	pos int
	// partialJson accumulates streamed tool arguments (pi: Block.partialJson);
	// never persisted.
	partialJson string
	// redactedChunks buffer encrypted reasoning deltas, joined into
	// thinkingSignature on finalize (pi: Block.redactedChunks).
	redactedChunks [][]byte
}

const bedrockEmptyTextPlaceholder = "<empty>"

// bedrockRedactedThinkingPlaceholder matches the placeholder the Anthropic API
// path uses for redacted thinking.
const bedrockRedactedThinkingPlaceholder = "[Reasoning redacted]"

// bedrockThinkingBindingControlsBeta is sent alongside
// thinking.block_binding for adaptive Claude models (pi:
// THINKING_BINDING_CONTROLS_BETA).
const thinkingBindingControlsBetaBedrock = "thinking-binding-controls-2026-08-01"

// BedrockConverseStream implements ProviderStreams for the
// bedrock-converse-stream API (pi: the { stream, streamSimple } module
// object).
type BedrockConverseStream struct{}

var _ types.ProviderStreams = BedrockConverseStream{}

// pi: compat.ts registers the builtin API implementations at module load;
// pig's layering rule (ai cannot import ai/api) puts the registration in the
// adapter package's init instead.
func init() {
	ai.RegisterBuiltInApiProvider(ai.ApiBedrockConverseStream, BedrockConverseStream{})
	ai.SetBuiltinApiRegistrar(func() {
		ai.RegisterBuiltInApiProvider(ai.ApiBedrockConverseStream, BedrockConverseStream{})
	})
}

// Stream dispatches one streaming request (pi: stream). ctx carries the
// caller's cancellation (pi: options.signal into client.send).
func (BedrockConverseStream) Stream(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.StreamOptions) *types.AssistantMessageEventStream {
	opts := &BedrockOptions{}
	if options != nil {
		opts.StreamOptions = *options
	}
	return streamBedrockConverse(ctx, model, llmContext, opts)
}

// StreamSimple dispatches one simple streaming request (pi: streamSimple).
func (BedrockConverseStream) StreamSimple(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
	// pi: { ...buildBaseOptions(model, context, options, undefined), toolChoice } —
	// the undefined apiKey falls through to options.apiKey.
	base := BuildBaseOptions(model, llmContext, options, "")
	opts := &BedrockOptions{StreamOptions: base}
	if options != nil {
		opts.ToolChoice = simpleBedrockToolChoice(options.ToolChoice)
	}
	if options == nil || options.Reasoning == "" {
		// pi: { ...base, reasoning: undefined }.
		return streamBedrockConverse(ctx, model, llmContext, opts)
	}

	if isAnthropicClaudeModel(model) {
		if supportsAdaptiveThinking(model.ID, model.Name) {
			opts.Reasoning = options.Reasoning
			opts.ThinkingBudgets = options.ThinkingBudgets
			return streamBedrockConverse(ctx, model, llmContext, opts)
		}

		// Undefined means the caller did not request an output cap; let the
		// helper use the model cap. Do not coerce to 0 here, or the thinking
		// budget would become the entire maxTokens value.
		adjustedMaxTokens, adjustedBudget := AdjustMaxTokensForThinking(
			base.MaxTokens, model.MaxTokens, options.Reasoning, options.ThinkingBudgets)

		maxTokens := ClampMaxTokensToContext(model, llmContext, adjustedMaxTokens)

		budgets := types.ThinkingBudgets{}
		if options.ThinkingBudgets != nil {
			budgets = *options.ThinkingBudgets
		}
		setThinkingBudget(&budgets, ClampReasoning(options.Reasoning),
			minInt64(adjustedBudget, maxInt64(0, maxTokens-MinAnswerTokens)))

		opts.MaxTokens = &maxTokens
		opts.Reasoning = options.Reasoning
		opts.ThinkingBudgets = &budgets
		return streamBedrockConverse(ctx, model, llmContext, opts)
	}

	opts.Reasoning = options.Reasoning
	opts.ThinkingBudgets = options.ThinkingBudgets
	return streamBedrockConverse(ctx, model, llmContext, opts)
}

// simpleBedrockToolChoice maps the simple "auto"|"none" choice onto the
// bedrock union (pi: toolChoice passes through as the narrow union).
func simpleBedrockToolChoice(choice types.ToolChoice) *BedrockToolChoice {
	if choice == "" {
		return nil
	}
	return &BedrockToolChoice{Type: string(choice)}
}

// setThinkingBudget sets budgets[level] (pi: thinkingBudgets[level] = ...).
func setThinkingBudget(budgets *types.ThinkingBudgets, level types.ThinkingLevel, budget int64) {
	switch level {
	case types.ThinkingLevelMinimal:
		budgets.Minimal = lo.ToPtr(budget)
	case types.ThinkingLevelLow:
		budgets.Low = lo.ToPtr(budget)
	case types.ThinkingLevelMedium:
		budgets.Medium = lo.ToPtr(budget)
	case types.ThinkingLevelHigh:
		budgets.High = lo.ToPtr(budget)
	}
}

// streamBedrockConverse is the adapter's core stream implementation (pi:
// stream).
func streamBedrockConverse(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *BedrockOptions) *types.AssistantMessageEventStream {
	stream := types.NewAssistantMessageEventStream()
	// Bedrock has no mid-conversation system messages; fold them into the
	// leading prompt.
	normalizedContext := utils.CollapseSystemMessages(llmContext)
	output := &types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{},
		API:        ai.ApiBedrockConverseStream,
		Provider:   model.Provider,
		Model:      model.ID,
		Usage:      types.Usage{Cost: types.Cost{}},
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
		blocks := map[int32]*bedrockBlock{}
		// Kept outside the try so the catch can still correlate a mid-stream
		// failure: exceptions delivered as stream events carry no HTTP
		// metadata of their own (pi: responseRequestId closure variable).
		responseRequestID := ""
		defer stream.EndWith(*output)
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok {
					failBedrockStream(ctx, output, stream, push, err, blocks, &responseRequestID)
				} else {
					failBedrockStream(ctx, output, stream, push, fmt.Errorf("%v", r), blocks, &responseRequestID)
				}
			}
		}()

		if err := runBedrockConverseStream(ctx, model, normalizedContext, options, output, push, blocks, &responseRequestID); err != nil {
			failBedrockStream(ctx, output, stream, push, err, blocks, &responseRequestID)
		}
	}()

	return stream
}

// failBedrockStream is pi's catch block: blocks are finalized, aborts
// normalize to the aborted stop reason, everything else to error with the
// formatted provider message and a failure diagnostic.
func failBedrockStream(ctx context.Context, output *types.AssistantMessage, stream *types.AssistantMessageEventStream, push func(types.AssistantMessageEvent), err error, blocks map[int32]*bedrockBlock, responseRequestID *string) {
	finalizeAllBedrockBlocks(output, blocks)
	if ctx.Err() != nil {
		output.StopReason = types.StopReasonAborted
	} else {
		output.StopReason = types.StopReasonError
	}
	output.ErrorMessage = formatBedrockError(err)
	if output.StopReason == types.StopReasonError {
		appendBedrockFailureDiagnostic(output, err, responseRequestID)
	}
	push(types.AssistantMessageEvent{Type: types.EventError, Reason: output.StopReason, Message: output})
}

// runBedrockConverseStream is pi's try block (request setup + streaming loop).
func runBedrockConverseStream(ctx context.Context, model *types.Model, normalizedContext *types.TranscriptContext, options *BedrockOptions, output *types.AssistantMessage, push func(types.AssistantMessageEvent), blocks map[int32]*bedrockBlock, responseRequestID *string) error {
	// A profile explicitly configured through pi's auth flow (the `profile`
	// option or scoped `AWS_PROFILE` on the stored credential's env) must win
	// over ambient AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY. The SDK default
	// chain already prefers a configured profile over env keys, but only when
	// `credentials` is not set on the client config. See #6957.
	optionsProfile := options.Profile
	if optionsProfile == "" {
		optionsProfile = options.Env["AWS_PROFILE"]
	}
	cfg := bedrockClientConfig{
		Profile:   firstNonEmpty(optionsProfile, utils.GetProviderEnvValue("AWS_PROFILE", options.Env)),
		UserAgent: bedrockUserAgent(options.Identity),
	}
	configuredRegion := getConfiguredBedrockRegion(options)
	hasAmbientConfiguredProfile := utils.GetProviderEnvValue("AWS_PROFILE", nil) != ""
	endpointRegion := getStandardBedrockEndpointRegion(model.BaseURL)
	useExplicitEndpoint := shouldUseExplicitBedrockEndpoint(model.BaseURL, configuredRegion, hasAmbientConfiguredProfile)

	// Only pin standard AWS Bedrock runtime endpoints when no region or
	// ambient AWS_PROFILE is configured. This preserves custom endpoints
	// (VPC/proxy) from #3402 without forcing built-in catalog defaults such
	// as us-east-1 to override AWS_REGION/AWS_PROFILE.
	if useExplicitEndpoint {
		cfg.Endpoint = model.BaseURL
	}

	// Resolve bearer token for Bedrock API key auth.
	skipAuth := utils.GetProviderEnvValue("AWS_BEDROCK_SKIP_AUTH", options.Env) == "1"
	bearerToken := firstNonEmpty(options.BearerToken, options.APIKey,
		utils.GetProviderEnvValue("AWS_BEARER_TOKEN_BEDROCK", options.Env))
	useBearerToken := bearerToken != "" && !skipAuth

	// Region resolution: ARN-embedded > explicit option > env vars > SDK
	// default chain. When the model ID is an inference profile ARN, extract
	// the region from it. This avoids conflicts with AWS_REGION set for other
	// services.
	var arnRegion string
	if match := bedrockArnRegionRe.FindStringSubmatch(model.ID); match != nil {
		arnRegion = match[1]
	}
	switch {
	case arnRegion != "":
		cfg.Region = arnRegion
	case configuredRegion != "":
		cfg.Region = configuredRegion
	case endpointRegion != "" && useExplicitEndpoint:
		cfg.Region = endpointRegion
	case !hasAmbientConfiguredProfile:
		cfg.Region = "us-east-1"
	}

	// Support proxies that don't need authentication.
	if skipAuth {
		cfg.Credentials = &bedrockStaticCredentials{
			AccessKeyID:     "dummy-access-key",
			SecretAccessKey: "dummy-secret-key",
		}
	}

	if creds := getConfiguredBedrockCredentials(options.Env); !skipAuth && creds != nil && optionsProfile == "" {
		cfg.Credentials = creds
	}

	proxyURL, proxyErr := utils.ResolveHTTPProxyURLForTarget(model.BaseURL, options.Env)
	if proxyErr != nil {
		return proxyErr
	}
	if proxyURL != nil {
		cfg.ProxyURL = proxyURL.String()
	} else if utils.GetProviderEnvValue("AWS_BEDROCK_FORCE_HTTP1", options.Env) == "1" {
		// Some custom endpoints require HTTP/1.1 instead of HTTP/2. The Go
		// SDK speaks HTTP/1.1 by default; the flag is accepted for parity and
		// otherwise ignored (see the file header).
		cfg.ForceHTTP1 = true
	}

	if useBearerToken {
		cfg.Token = bearerToken
		cfg.AuthSchemePreference = []string{"httpBearerAuth"}
	}

	supportsStrictMode := false
	if compat, ok := model.Compat.(*types.BedrockCompat); ok && compat != nil && compat.SupportsStrictMode != nil {
		supportsStrictMode = *compat.SupportsStrictMode
	}

	customHeaders := utils.ProviderHeadersToRecord(options.Headers)
	client, err := bedrockNewClient(ctx, bedrockClientParams{
		config:        cfg,
		customHeaders: customHeaders,
		onResponse:    options.OnResponse,
		model:         model,
	})
	if err != nil {
		return err
	}

	cacheRetention := bedrockResolveCacheRetention(options.CacheRetention, options.Env)
	inferenceMaxTokens := options.MaxTokens
	if inferenceMaxTokens == nil && isAnthropicClaudeModel(model) {
		maxTokens := model.MaxTokens
		inferenceMaxTokens = &maxTokens
	}
	toolConfig, err := convertBedrockToolConfig(utils.GetCurrentTools(normalizedContext.Messages), options.ToolChoice, supportsStrictMode)
	if err != nil {
		return err
	}
	initialSystemMessage := utils.GetInitialSystemMessage(normalizedContext.Messages)
	initialSystemPrompt := ""
	if initialSystemMessage != nil {
		initialSystemPrompt = utils.GetSystemMessageText(initialSystemMessage)
	}
	commandInput := &bedrockConverseInput{
		ModelID:  model.ID,
		Messages: convertBedrockMessages(normalizedContext, model, cacheRetention, options.Env),
		System:   buildBedrockSystemPrompt(initialSystemPrompt, model, cacheRetention, options.Env),
		InferenceConfig: &bedrockInferenceConfig{
			MaxTokens:   inferenceMaxTokens,
			Temperature: options.Temperature,
		},
		ToolConfig:                   toolConfig,
		AdditionalModelRequestFields: buildAdditionalModelRequestFields(model, options),
		RequestMetadata:              options.RequestMetadata,
	}
	if options.OnPayload != nil {
		if next := options.OnPayload(commandInput, model); next != nil {
			if nextInput, ok := next.(*bedrockConverseInput); ok {
				commandInput = nextInput
			}
		}
	}

	response, err := client.ConverseStream(ctx, commandInput)
	if err != nil {
		return err
	}
	defer response.Events.Close()

	normalizedRequestID := normalizeBedrockDiagnosticValue(response.Metadata.RequestID)
	if normalizedRequestID != "" {
		*responseRequestID = normalizedRequestID
	}
	if options.OnResponse != nil && !response.Metadata.Observed && response.Metadata.HTTPStatusCode != 0 {
		responseHeaders := map[string]string{}
		if response.Metadata.RequestID != "" {
			responseHeaders["x-amzn-requestid"] = response.Metadata.RequestID
		}
		options.OnResponse(types.ProviderResponse{Status: response.Metadata.HTTPStatusCode, Headers: responseHeaders}, model)
	}

	for {
		event, err := response.Events.Next(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// pi throws the exception item (internalServerException /
			// modelStreamErrorException / validationException /
			// throttlingException / serviceUnavailableException); the Go
			// event stream surfaces them as errors from Next instead.
			return err
		}
		switch item := event.(type) {
		case *brtypes.ConverseStreamOutputMemberMessageStart:
			if item.Value.Role != brtypes.ConversationRoleAssistant {
				return errors.New("Unexpected assistant message start but got user message start instead")
			}
			push(types.AssistantMessageEvent{Type: types.EventStart, Partial: output})
		case *brtypes.ConverseStreamOutputMemberContentBlockStart:
			handleBedrockContentBlockStart(&item.Value, output, blocks, push)
		case *brtypes.ConverseStreamOutputMemberContentBlockDelta:
			handleBedrockContentBlockDelta(&item.Value, output, blocks, push)
		case *brtypes.ConverseStreamOutputMemberContentBlockStop:
			handleBedrockContentBlockStop(&item.Value, output, blocks, push)
		case *brtypes.ConverseStreamOutputMemberMessageStop:
			output.RawStopReason = string(item.Value.StopReason)
			stopReason, errorMessage := mapBedrockStopReason(string(item.Value.StopReason))
			output.StopReason = stopReason
			if errorMessage != "" {
				output.ErrorMessage = errorMessage
			}
		case *brtypes.ConverseStreamOutputMemberMetadata:
			handleBedrockMetadata(&item.Value, model, output)
		}
	}

	if ctx.Err() != nil {
		return errors.New("Request was aborted")
	}

	if output.StopReason == types.StopReasonPending {
		return errors.New("Bedrock stream ended without a stop reason")
	}
	if output.StopReason == types.StopReasonError || output.StopReason == types.StopReasonAborted {
		if output.ErrorMessage != "" {
			return errors.New(output.ErrorMessage)
		}
		return errors.New("An unknown error occurred")
	}

	// A stream can settle without stopping every block, so finalize here too.
	finalizeAllBedrockBlocks(output, blocks)
	push(types.AssistantMessageEvent{Type: types.EventDone, Reason: output.StopReason, Message: output})
	return nil
}

// bedrockArnRegionRe matches inference profile ARNs (pi:
// /^arn:aws(?:-[a-z0-9-]+)?:bedrock:([a-z0-9-]+):/).
var bedrockArnRegionRe = regexp.MustCompile(`^arn:aws(?:-[a-z0-9-]+)?:bedrock:([a-z0-9-]+):`)

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// --- error formatting (pi: formatBedrockError + failure diagnostics) ---

// bedrockErrorPrefixes are human-readable prefixes for Bedrock SDK exception
// names. The downstream retry logic in agent-session matches patterns like
// `server.?error` and `service.?unavailable`, so the legacy prefix format is
// preserved rather than using the raw SDK exception name.
var bedrockErrorPrefixes = map[string]string{
	"InternalServerException":     "Internal server error",
	"ModelStreamErrorException":   "Model stream error",
	"ValidationException":         "Validation error",
	"ThrottlingException":         "Throttling error",
	"ServiceUnavailableException": "Service unavailable",
}

// bedrockDataRetentionDocsURL points at the AWS docs explaining how to
// configure a supported data retention mode, for models that reject the
// account/profile's configured one (e.g. "data retention mode 'default' is not
// available for this model").
const bedrockDataRetentionDocsURL = "https://docs.aws.amazon.com/bedrock/latest/userguide/data-retention.html"

// bedrockDataRetentionRe matches pi's /data retention mode/i probe.
var bedrockDataRetentionRe = regexp.MustCompile(`(?i)data retention mode`)

// formatBedrockError formats a Bedrock error with a human-readable prefix.
// AWS SDK exceptions (both from client.send and from stream events) map their
// code to a stable human-readable prefix so downstream consumers (retry
// logic, context-overflow detection) can distinguish error categories via
// simple string matching (pi: formatBedrockError).
func formatBedrockError(err error) string {
	if err == nil {
		return ""
	}
	// pi: normalizeProviderError(error).message — the SDK exception message
	// (the deserialized server text). pig reads the smithy APIError message;
	// plain errors fall back to Error(). See the file header for the missing
	// status:body branch.
	core := bedrockCoreErrorMessage(err)
	dataRetentionHint := ""
	if bedrockDataRetentionRe.MatchString(core) {
		dataRetentionHint = fmt.Sprintf(" See %s for supported data retention modes.", bedrockDataRetentionDocsURL)
	}
	if code, _, modeled, ok := extractBedrockAPIError(err); ok && modeled {
		prefix, known := bedrockErrorPrefixes[code]
		if !known {
			prefix = code
		}
		return fmt.Sprintf("%s: %s%s", prefix, core, dataRetentionHint)
	}
	return fmt.Sprintf("%s%s", core, dataRetentionHint)
}

// bedrockCoreErrorMessage is the display message of one provider failure.
func bedrockCoreErrorMessage(err error) string {
	if _, message, _, ok := extractBedrockAPIError(err); ok && message != "" {
		return message
	}
	return err.Error()
}

// extractBedrockAPIError finds the smithy APIError in the error chain. ok is
// false when no APIError is present; modeled distinguishes modeled Bedrock
// exceptions (pi: instanceof BedrockRuntimeServiceException) from unmodeled
// ones (*smithy.GenericAPIError — the SDK's shape for unmodeled error frames,
// covering transport names such as TimeoutError and the Unknown placeholder).
func extractBedrockAPIError(err error) (code, message string, modeled, ok bool) {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return "", "", false, false
	}
	code = apiErr.ErrorCode()
	message = apiErr.ErrorMessage()
	modeled = true
	if generic, isGeneric := apiErr.(*smithy.GenericAPIError); isGeneric && generic != nil {
		modeled = false
	}
	return code, message, modeled, true
}

// maxBedrockDiagnosticValueChars caps header-derived diagnostic values;
// over-long values are dropped rather than truncated, because a truncated
// request id is not a request id (pi: normalizeDiagnosticValue).
const maxBedrockDiagnosticValueChars = 200

func normalizeBedrockDiagnosticValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || len(trimmed) > maxBedrockDiagnosticValueChars {
		return ""
	}
	return trimmed
}

// appendBedrockFailureDiagnostic adds structured metadata alongside
// errorMessage, which stays byte-identical because isRetryableAssistantError
// matches against it. Unknown fields are omitted, never guessed. `details`
// only, as the throw is not always an Error (pi: appendBedrockFailureDiagnostic).
func appendBedrockFailureDiagnostic(output *types.AssistantMessage, err error, fallbackRequestID *string) {
	details := map[string]any{}

	var responseErr *smithyhttp.ResponseError
	if errors.As(err, &responseErr) && responseErr.Response != nil && responseErr.Response.Response != nil {
		details["status"] = responseErr.Response.StatusCode
	}

	// The SDK puts the modeled code on the APIError for service exceptions and
	// unmodeled stream errors alike, so do not narrow to modeled exceptions
	// here. Modeled Bedrock errors all end in "Exception", unlike transport
	// names such as TimeoutError (pi: extractBedrockErrorCode's
	// name.endsWith("Exception") check, which also drops the SDK's
	// Unknown/UnknownError placeholder).
	if code, _, _, ok := extractBedrockAPIError(err); ok && strings.HasSuffix(code, "Exception") {
		if normalized := normalizeBedrockDiagnosticValue(code); normalized != "" {
			details["errorCode"] = normalized
		}
	}

	// pi: metadata?.requestId off the thrown error, falling back to the
	// send-result's request id for mid-stream failures. The smithy response
	// error carries the header when the HTTP response is available.
	requestID := ""
	var responseErr2 *smithyhttp.ResponseError
	if errors.As(err, &responseErr2) && responseErr2.Response != nil && responseErr2.Response.Response != nil {
		requestID = responseErr2.Response.Header.Get("x-amzn-requestid")
	}
	if requestID == "" && fallbackRequestID != nil {
		requestID = *fallbackRequestID
	}
	if normalized := normalizeBedrockDiagnosticValue(requestID); normalized != "" {
		details["requestId"] = normalized
	}

	if len(details) == 0 {
		return
	}

	utils.AppendAssistantMessageDiagnostic(output, types.AssistantMessageDiagnostic{
		Type:      "bedrock_response_failure",
		Timestamp: time.Now().UnixMilli(),
		Details:   details,
	})
}

// --- custom headers middleware (pi: addCustomHeadersMiddleware) ---

// bedrockReservedHeaderExact are header keys that must never be overwritten by
// caller-supplied headers: `host` and `authorization` participate in the
// SigV4 canonical request / bearer auth. Compared case-insensitively.
var bedrockReservedHeaderExact = map[string]bool{"authorization": true, "host": true}

func isReservedBedrockHeader(key string) bool {
	lower := strings.ToLower(key)
	return strings.HasPrefix(lower, "x-amz-") || bedrockReservedHeaderExact[lower]
}

// applyBedrockCustomHeaders injects caller headers into the outgoing request
// at the Smithy build step, after serialization but before SigV4 signing, so
// they are covered by the signature. Reserved SigV4 / auth headers are
// silently skipped; all other caller headers override same-named headers.
func applyBedrockCustomHeaders(header http.Header, custom map[string]string) {
	for key, value := range custom {
		if !isReservedBedrockHeader(key) {
			header.Set(key, value)
		}
	}
}

// --- event handling (pi: handleContentBlockStart/Delta/Stop, handleMetadata) ---

func handleBedrockContentBlockStart(event *brtypes.ContentBlockStartEvent, output *types.AssistantMessage, blocks map[int32]*bedrockBlock, push func(types.AssistantMessageEvent)) {
	index := lo.FromPtr(event.ContentBlockIndex)
	if toolUseStart, ok := event.Start.(*brtypes.ContentBlockStartMemberToolUse); ok {
		toolUse := toolUseStart.Value
		id := ""
		if toolUse.ToolUseId != nil {
			id = *toolUse.ToolUseId
		}
		name := ""
		if toolUse.Name != nil {
			name = *toolUse.Name
		}
		output.Content = append(output.Content, types.ToolCall{
			Kind:      types.KindToolCall,
			ID:        id,
			Name:      name,
			Arguments: map[string]any{},
		})
		blocks[index] = &bedrockBlock{index: index, pos: len(output.Content) - 1}
		push(types.AssistantMessageEvent{Type: types.EventToolCallStart, ContentIndex: len(output.Content) - 1, Partial: output})
	}
}

func handleBedrockContentBlockDelta(event *brtypes.ContentBlockDeltaEvent, output *types.AssistantMessage, blocks map[int32]*bedrockBlock, push func(types.AssistantMessageEvent)) {
	contentBlockIndex := lo.FromPtr(event.ContentBlockIndex)
	block, haveBlock := blocks[contentBlockIndex]

	switch delta := event.Delta.(type) {
	case *brtypes.ContentBlockDeltaMemberText:
		// If no text block exists yet, create one, as handleContentBlockStart
		// is not sent for text blocks.
		var pos int
		if !haveBlock {
			output.Content = append(output.Content, types.NewTextContent(""))
			pos = len(output.Content) - 1
			block = &bedrockBlock{index: contentBlockIndex, pos: pos}
			blocks[contentBlockIndex] = block
			push(types.AssistantMessageEvent{Type: types.EventTextStart, ContentIndex: pos, Partial: output})
		} else {
			pos = block.pos
		}
		if text, ok := output.Content[pos].(types.TextContent); ok {
			text.Text += delta.Value
			output.Content[pos] = text
			push(types.AssistantMessageEvent{Type: types.EventTextDelta, ContentIndex: pos, Delta: delta.Value, Partial: output})
		}
	case *brtypes.ContentBlockDeltaMemberToolUse:
		if !haveBlock {
			return
		}
		if _, ok := output.Content[block.pos].(types.ToolCall); ok {
			input := ""
			if delta.Value.Input != nil {
				input = *delta.Value.Input
			}
			block.partialJson += input
			call := output.Content[block.pos].(types.ToolCall)
			call.Arguments = utils.ParseStreamingJSON(&block.partialJson)
			output.Content[block.pos] = call
			push(types.AssistantMessageEvent{Type: types.EventToolCallDelta, ContentIndex: block.pos, Delta: input, Partial: output})
		}
	case *brtypes.ContentBlockDeltaMemberReasoningContent:
		reasoning := delta.Value
		var pos int
		if !haveBlock {
			output.Content = append(output.Content, types.NewThinkingContent(""))
			pos = len(output.Content) - 1
			block = &bedrockBlock{index: contentBlockIndex, pos: pos}
			blocks[contentBlockIndex] = block
			push(types.AssistantMessageEvent{Type: types.EventThinkingStart, ContentIndex: pos, Partial: output})
		} else {
			pos = block.pos
		}
		if thinking, ok := output.Content[pos].(types.ThinkingContent); ok {
			if textMember, ok := reasoning.(*brtypes.ReasoningContentBlockDeltaMemberText); ok {
				thinking.Thinking += textMember.Value
				output.Content[pos] = thinking
				push(types.AssistantMessageEvent{Type: types.EventThinkingDelta, ContentIndex: pos, Delta: textMember.Value, Partial: output})
			}
			// thinkingSignature holds either an Anthropic signature or an
			// opaque redacted payload, never both: mixing them would corrupt
			// whichever arrived first.
			if signatureMember, ok := reasoning.(*brtypes.ReasoningContentBlockDeltaMemberSignature); ok && !thinking.Redacted {
				thinking.ThinkingSignature += signatureMember.Value
				output.Content[pos] = thinking
			}
			if redactedMember, ok := reasoning.(*brtypes.ReasoningContentBlockDeltaMemberRedactedContent); ok && len(redactedMember.Value) > 0 {
				// Encrypted reasoning from non-Anthropic models on Bedrock
				// (e.g. OpenAI GPT-5.6). The payload is opaque, so keep it
				// verbatim in thinkingSignature the way the Anthropic path
				// stores redacted thinking, and replay it on the next turn.
				if !thinking.Redacted {
					thinking.Redacted = true
					thinking.ThinkingSignature = ""
					thinking.Thinking += bedrockRedactedThinkingPlaceholder
					output.Content[pos] = thinking
					push(types.AssistantMessageEvent{
						Type: types.EventThinkingDelta, ContentIndex: pos, Delta: bedrockRedactedThinkingPlaceholder, Partial: output,
					})
				}
				block.redactedChunks = append(block.redactedChunks, redactedMember.Value)
			}
		}
	}
}

func handleBedrockContentBlockStop(event *brtypes.ContentBlockStopEvent, output *types.AssistantMessage, blocks map[int32]*bedrockBlock, push func(types.AssistantMessageEvent)) {
	contentBlockIndex := lo.FromPtr(event.ContentBlockIndex)
	block, ok := blocks[contentBlockIndex]
	if !ok {
		return
	}
	pos := block.pos
	if pos < 0 || pos >= len(output.Content) {
		return
	}

	switch content := output.Content[pos].(type) {
	case types.TextContent:
		push(types.AssistantMessageEvent{Type: types.EventTextEnd, ContentIndex: pos, Content: content.Text, Partial: output})
	case types.ThinkingContent:
		// pi: flushRedactedContent before thinking_end.
		flushBedrockRedactedContent(output, block)
		thinking := output.Content[pos].(types.ThinkingContent)
		push(types.AssistantMessageEvent{Type: types.EventThinkingEnd, ContentIndex: pos, Content: thinking.Thinking, Partial: output})
	case types.ToolCall:
		content.Arguments, content.ArgumentsError = utils.ParseToolCallArguments(block.partialJson)
		// Finalize in-place and strip the scratch buffer so replay only
		// carries parsed arguments.
		output.Content[pos] = content
		delete(blocks, contentBlockIndex)
		push(types.AssistantMessageEvent{Type: types.EventToolCallEnd, ContentIndex: pos, ToolCall: &content, Partial: output})
	}
}

// flushBedrockRedactedContent encodes buffered encrypted reasoning into
// thinkingSignature and drops the scratch buffer, which must never reach a
// persisted message (pi: flushRedactedContent).
func flushBedrockRedactedContent(output *types.AssistantMessage, block *bedrockBlock) {
	if len(block.redactedChunks) == 0 {
		return
	}
	if block.pos >= 0 && block.pos < len(output.Content) {
		if thinking, ok := output.Content[block.pos].(types.ThinkingContent); ok {
			thinking.ThinkingSignature = bytesToBase64(block.redactedChunks)
			output.Content[block.pos] = thinking
		}
	}
	block.redactedChunks = nil
}

// finalizeAllBedrockBlocks strips every streaming scratch field. Runs from the
// terminal paths as well as contentBlockStop, because a stream can settle
// without stopping each block (pi: the finalizeStreamingBlock loops in the try
// and catch paths). pig's scratch (index / partialJson / redactedChunks) lives
// beside output.Content, so only the redacted-payload flush is observable on
// the message.
func finalizeAllBedrockBlocks(output *types.AssistantMessage, blocks map[int32]*bedrockBlock) {
	for _, block := range blocks {
		flushBedrockRedactedContent(output, block)
	}
}

func handleBedrockMetadata(event *brtypes.ConverseStreamMetadataEvent, model *types.Model, output *types.AssistantMessage) {
	if event.Usage == nil {
		return
	}
	usage := event.Usage
	output.Usage.Input = int64(lo.FromPtr(usage.InputTokens))
	output.Usage.Output = int64(lo.FromPtr(usage.OutputTokens))
	output.Usage.CacheRead = int64(lo.FromPtr(usage.CacheReadInputTokens))
	output.Usage.CacheWrite = int64(lo.FromPtr(usage.CacheWriteInputTokens))
	// pi v0.86.0 ("price Bedrock one-hour cache writes"): the 1h subset of
	// CacheWrite comes from the per-TTL cache details.
	var cacheWrite1h int64
	for _, detail := range usage.CacheDetails {
		if detail.Ttl == brtypes.CacheTTLOneHour {
			cacheWrite1h += int64(lo.FromPtr(detail.InputTokens))
		}
	}
	output.Usage.CacheWrite1h = lo.ToPtr(cacheWrite1h)
	total := int64(lo.FromPtr(usage.TotalTokens))
	if total == 0 {
		total = output.Usage.Input + output.Usage.Output
	}
	output.Usage.TotalTokens = total
	ai.CalculateCost(model, &output.Usage)
}

// --- model predicates (pi: supportsAdaptiveThinking and friends) ---

// getModelMatchCandidates checks both model ID and model name to support
// application inference profiles whose ARNs don't contain the model name.
func getModelMatchCandidates(modelID string, modelName string) []string {
	values := []string{modelID}
	if modelName != "" {
		values = append(values, modelName)
	}
	var candidates []string
	for _, value := range values {
		lower := strings.ToLower(value)
		candidates = append(candidates, lower, bedrockNormalizeModelRef(lower))
	}
	return candidates
}

// bedrockSeparatorRunRe replaces separator runs with "-" (pi:
// lower.replace(/[\s_.:]+/g, "-")).
var bedrockSeparatorRunRe = regexp.MustCompile(`[\s_.:]+`)

func bedrockNormalizeModelRef(value string) string {
	return bedrockSeparatorRunRe.ReplaceAllString(value, "-")
}

func supportsAdaptiveThinking(modelID string, modelName string) bool {
	// Opus 4.6+, Sonnet 4.6, Haiku 5.5 (checks id and name candidates).
	return anyBedrockCandidate(modelID, modelName, func(s string) bool {
		return strings.Contains(s, "opus-4-6") ||
			strings.Contains(s, "opus-4-7") ||
			strings.Contains(s, "opus-4-8") ||
			strings.Contains(s, "opus-5") ||
			strings.Contains(s, "sonnet-4-6") ||
			strings.Contains(s, "sonnet-5") ||
			strings.Contains(s, "haiku-5") ||
			strings.Contains(s, "fable-5")
	})
}

func supportsNativeXhighEffort(model *types.Model) bool {
	return anyBedrockCandidate(model.ID, model.Name, func(s string) bool {
		return strings.Contains(s, "opus-4-7") ||
			strings.Contains(s, "opus-4-8") ||
			strings.Contains(s, "opus-5") ||
			strings.Contains(s, "sonnet-5") ||
			strings.Contains(s, "haiku-5") ||
			strings.Contains(s, "fable-5")
	})
}

// supportsThinkingBlockBinding checks if the model accepts
// `thinking.block_binding`. Opus 4.6 and Sonnet 4.6 reject it with
// "thinking.adaptive.block_binding: Extra inputs are not permitted" (pi:
// supportsThinkingBlockBinding).
func supportsThinkingBlockBinding(model *types.Model) bool {
	return anyBedrockCandidate(model.ID, model.Name, func(s string) bool {
		return strings.Contains(s, "opus-4-7") ||
			strings.Contains(s, "opus-4-8") ||
			strings.Contains(s, "opus-5") ||
			strings.Contains(s, "sonnet-5") ||
			strings.Contains(s, "haiku-5") ||
			strings.Contains(s, "fable-5")
	})
}

func anyBedrockCandidate(modelID, modelName string, match func(string) bool) bool {
	for _, candidate := range getModelMatchCandidates(modelID, modelName) {
		if match(candidate) {
			return true
		}
	}
	return false
}

func bedrockMapThinkingLevelToEffort(model *types.Model, level types.ThinkingLevel) string {
	if level == types.ThinkingLevelXHigh && supportsNativeXhighEffort(model) {
		return "xhigh"
	}

	if level != "" {
		if mapped, ok := model.ThinkingLevelMap[types.ModelThinkingLevel(level)]; ok && mapped != nil {
			return *mapped
		}
	}

	switch level {
	case types.ThinkingLevelMinimal, types.ThinkingLevelLow:
		return "low"
	case types.ThinkingLevelMedium:
		return "medium"
	case types.ThinkingLevelHigh:
		return "high"
	default:
		return "high"
	}
}

// resolveCacheRetention resolves the cache retention preference. Defaults to
// "short" and uses PI_CACHE_RETENTION for backward compatibility (pi:
// resolveCacheRetention).
func bedrockResolveCacheRetention(cacheRetention types.CacheRetention, env types.ProviderEnv) types.CacheRetention {
	if cacheRetention != "" {
		return cacheRetention
	}
	if utils.GetProviderEnvValue("PI_CACHE_RETENTION", env) == "long" {
		return "long"
	}
	return "short"
}

// isAnthropicClaudeModel checks if the model is an Anthropic Claude model on
// Bedrock. Checks both model ID and model name to support application
// inference profiles whose ARNs don't contain the model name.
func isAnthropicClaudeModel(model *types.Model) bool {
	id := strings.ToLower(model.ID)
	name := strings.ToLower(model.Name)
	return strings.Contains(id, "anthropic.claude") ||
		strings.Contains(id, "anthropic/claude") ||
		strings.Contains(name, "anthropic.claude") ||
		strings.Contains(name, "anthropic/claude") ||
		strings.Contains(name, "claude")
}

// supportsPromptCaching checks if the model supports prompt caching.
// Supported: Claude 3.5 Haiku, Claude 3.7 Sonnet, Claude 4.x models, Claude 5
// models.
//
// For base models and system-defined inference profiles the model ID / ARN
// contains the model name, so we can decide locally.
//
// For application inference profiles (whose ARNs don't contain the model
// name), also checks model.name which is user-controlled via models.json or
// registerProvider. As a last resort, set AWS_BEDROCK_FORCE_CACHE=1 to enable
// cache points. Amazon Nova models have automatic caching and don't need
// explicit cache points.
func supportsPromptCaching(model *types.Model, env types.ProviderEnv) bool {
	hasClaudeRef := anyBedrockCandidate(model.ID, model.Name, func(s string) bool {
		return strings.Contains(s, "claude")
	})
	if !hasClaudeRef {
		// Application inference profiles don't contain the model name in the
		// ARN. Allow users to force cache points via environment variable.
		return utils.GetProviderEnvValue("AWS_BEDROCK_FORCE_CACHE", env) == "1"
	}
	// Claude 5 models (fable-5, opus-5, sonnet-5, haiku-5)
	if anyBedrockCandidate(model.ID, model.Name, func(s string) bool {
		return strings.Contains(s, "fable-5") || strings.Contains(s, "opus-5") || strings.Contains(s, "sonnet-5") || strings.Contains(s, "haiku-5")
	}) {
		return true
	}
	// Claude 4.x models (opus-4, sonnet-4, haiku-4)
	if anyBedrockCandidate(model.ID, model.Name, func(s string) bool {
		return strings.Contains(s, "-4-")
	}) {
		return true
	}
	// Claude 3.7 Sonnet
	if anyBedrockCandidate(model.ID, model.Name, func(s string) bool {
		return strings.Contains(s, "claude-3-7-sonnet")
	}) {
		return true
	}
	// Claude 3.5 Haiku
	if anyBedrockCandidate(model.ID, model.Name, func(s string) bool {
		return strings.Contains(s, "claude-3-5-haiku")
	}) {
		return true
	}
	return false
}

// supportsThinkingSignature checks if the model supports thinking signatures
// in reasoningContent. Only Anthropic Claude models support the signature
// field. Other models (OpenAI, Qwen, Minimax, Moonshot, etc.) reject it with:
// "This model doesn't support the reasoningContent.reasoningText.signature field".
func supportsThinkingSignature(model *types.Model) bool {
	return isAnthropicClaudeModel(model)
}

// --- request building (pi: buildSystemPrompt, convertMessages,
// convertToolConfig, buildAdditionalModelRequestFields) ---

func buildBedrockSystemPrompt(systemPrompt string, model *types.Model, cacheRetention types.CacheRetention, env types.ProviderEnv) []bedrockSystemContentBlock {
	if systemPrompt == "" {
		return nil
	}

	blocks := []bedrockSystemContentBlock{{Text: lo.ToPtr(utils.SanitizeSurrogates(systemPrompt))}}

	// Add cache point for supported Claude models when caching is enabled
	if cacheRetention != types.CacheRetentionNone && supportsPromptCaching(model, env) {
		blocks = append(blocks, bedrockSystemContentBlock{CachePoint: newBedrockCachePoint(cacheRetention)})
	}

	return blocks
}

// bedrockCachePoint is the default cache point (pi:
// { cachePoint: { type: "default", ...(long ? { ttl: "ONE_HOUR" } : {}) } }).
func newBedrockCachePoint(cacheRetention types.CacheRetention) *bedrockCachePoint {
	cachePoint := &bedrockCachePoint{Type: "default"}
	if cacheRetention == types.CacheRetentionLong {
		cachePoint.TTL = lo.ToPtr("ONE_HOUR")
	}
	return cachePoint
}

var bedrockToolCallIDSanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func normalizeBedrockToolCallID(id string) string {
	sanitized := bedrockToolCallIDSanitizeRe.ReplaceAllString(id, "_")
	if len(sanitized) > 64 {
		return sanitized[:64]
	}
	return sanitized
}

func createNonBlankBedrockTextBlock(text string) *string {
	sanitized := utils.SanitizeSurrogates(text)
	if strings.TrimSpace(sanitized) == "" {
		return nil
	}
	return &sanitized
}

func createRequiredBedrockTextBlock(text string) *string {
	if block := createNonBlankBedrockTextBlock(text); block != nil {
		return block
	}
	return lo.ToPtr(bedrockEmptyTextPlaceholder)
}

// sanitizeBedrockDocument strips empty-string keys recursively from replayed
// tool arguments (pi: sanitizeBedrockDocument — Bedrock rejects "" keys).
func sanitizeBedrockDocument(value any) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, sanitizeBedrockDocument(item))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, nested := range v {
			if len(key) == 0 {
				continue
			}
			out[key] = sanitizeBedrockDocument(nested)
		}
		return out
	default:
		return value
	}
}

func convertBedrockToolResultContent(content []types.Content) []bedrockToolResultContentBlock {
	var result []bedrockToolResultContentBlock
	for _, c := range content {
		if image, ok := c.(types.ImageContent); ok {
			result = append(result, bedrockToolResultContentBlock{Image: createBedrockImageBlock(image.MimeType, image.Data)})
		} else if text, ok := c.(types.TextContent); ok {
			if textBlock := createNonBlankBedrockTextBlock(text.Text); textBlock != nil {
				result = append(result, bedrockToolResultContentBlock{Text: textBlock})
			}
		}
	}
	if len(result) == 0 {
		result = append(result, bedrockToolResultContentBlock{Text: lo.ToPtr(bedrockEmptyTextPlaceholder)})
	}
	return result
}

func convertBedrockMessages(llmContext *types.TranscriptContext, model *types.Model, cacheRetention types.CacheRetention, env types.ProviderEnv) []bedrockMessage {
	var result []bedrockMessage
	transformedMessages := TransformMessages(utils.WithoutInitialSystemMessage(llmContext.Messages), model, func(id string, _ *types.Model, _ *types.AssistantMessage) string {
		return normalizeBedrockToolCallID(id)
	})

	for i := 0; i < len(transformedMessages); i++ {
		m := transformedMessages[i]

		switch msg := m.(type) {
		case types.UserMessage:
			var content []bedrockContentBlock
			if msg.Content.Parts != nil {
				for _, c := range msg.Content.Parts {
					switch block := c.(type) {
					case types.TextContent:
						if textBlock := createNonBlankBedrockTextBlock(block.Text); textBlock != nil {
							content = append(content, bedrockContentBlock{Text: textBlock})
						}
					case types.ImageContent:
						content = append(content, bedrockContentBlock{Image: createBedrockImageBlock(block.MimeType, block.Data)})
					default:
						// Skip unknown user content blocks instead of throwing.
						continue
					}
				}
				if len(content) == 0 {
					content = append(content, bedrockContentBlock{Text: lo.ToPtr(bedrockEmptyTextPlaceholder)})
				}
			} else {
				// A string user message is a required (non-blank) text block.
				content = append(content, bedrockContentBlock{Text: createRequiredBedrockTextBlock(msg.Content.String)})
			}
			result = append(result, bedrockMessage{Role: "user", Content: content})

		case types.AssistantMessage:
			// Skip assistant messages with empty content (e.g., from aborted
			// requests). Bedrock rejects messages with empty content arrays.
			if len(msg.Content) == 0 {
				continue
			}
			var contentBlocks []bedrockContentBlock
			for _, c := range msg.Content {
				switch block := c.(type) {
				case types.TextContent:
					// Skip empty text blocks
					if textBlock := createNonBlankBedrockTextBlock(block.Text); textBlock != nil {
						contentBlocks = append(contentBlocks, bedrockContentBlock{Text: textBlock})
					}
				case types.ToolCall:
					contentBlocks = append(contentBlocks, bedrockContentBlock{
						ToolUse: &bedrockToolUseBlock{
							ToolUseID: block.ID,
							Name:      block.Name,
							Input:     sanitizeBedrockDocument(block.Arguments),
						},
					})
				case types.ThinkingContent:
					// Encrypted reasoning is opaque: replay the stored payload
					// as the redactedContent member instead of lowering it to
					// reasoning text.
					if block.Redacted {
						redactedContent := decodeBedrockRedactedContent(block.ThinkingSignature)
						if len(redactedContent) > 0 {
							contentBlocks = append(contentBlocks, bedrockContentBlock{
								ReasoningContent: &bedrockReasoningContentBlock{RedactedContent: redactedContent},
							})
						}
						continue
					}
					// Skip empty thinking blocks
					thinking := utils.SanitizeSurrogates(block.Thinking)
					if strings.TrimSpace(thinking) == "" {
						continue
					}
					// Only Anthropic models support the signature field in
					// reasoningText. For other models, we omit the signature
					// to avoid errors like: "This model doesn't support the
					// reasoningContent.reasoningText.signature field"
					if supportsThinkingSignature(model) {
						// Signatures arrive after thinking deltas. If a
						// partial or externally persisted message lacks a
						// signature, Bedrock rejects the replayed reasoning
						// block. Fall back to plain text, matching Anthropic.
						if strings.TrimSpace(block.ThinkingSignature) == "" {
							contentBlocks = append(contentBlocks, bedrockContentBlock{Text: &thinking})
						} else {
							contentBlocks = append(contentBlocks, bedrockContentBlock{
								ReasoningContent: &bedrockReasoningContentBlock{
									ReasoningText: &bedrockReasoningTextBlock{
										Text:      thinking,
										Signature: lo.ToPtr(block.ThinkingSignature),
									},
								},
							})
						}
					} else {
						contentBlocks = append(contentBlocks, bedrockContentBlock{
							ReasoningContent: &bedrockReasoningContentBlock{
								ReasoningText: &bedrockReasoningTextBlock{Text: thinking},
							},
						})
					}
				default:
					continue
				}
			}
			// Skip if all content blocks were filtered out
			if len(contentBlocks) == 0 {
				continue
			}
			result = append(result, bedrockMessage{Role: "assistant", Content: contentBlocks})

		case types.ToolResultMessage:
			// Collect all consecutive toolResult messages into a single user
			// message; Bedrock requires all tool results to be in one message.
			var toolResults []bedrockContentBlock
			// Claude rejects error tool results that contain anything but text,
			// so for Claude their images follow all tool results of the turn
			// (#10794). Other models keep them in place.
			var errorImages []bedrockContentBlock
			j := i
			for j < len(transformedMessages) {
				next, ok := transformedMessages[j].(types.ToolResultMessage)
				if !ok {
					break
				}
				var split *SplitErrorToolResult
				if isAnthropicClaudeModel(model) {
					split = SplitErrorToolResultImages(&next)
				}
				content := next.Content
				if split != nil {
					content = split.Content
				}
				toolResults = append(toolResults, bedrockContentBlock{
					ToolResult: &bedrockToolResultBlock{
						ToolUseID: next.ToolCallID,
						Content:   convertBedrockToolResultContent(content),
						Status:    bedrockToolResultStatus(next.IsError),
					},
				})
				if split != nil {
					caption := split.Caption
					errorImages = append(errorImages, bedrockContentBlock{Text: &caption})
					for _, image := range split.Images {
						errorImages = append(errorImages, bedrockContentBlock{Image: createBedrockImageBlock(image.MimeType, image.Data)})
					}
				}
				j++
			}

			// Skip the messages we've already processed
			i = j - 1

			result = append(result, bedrockMessage{Role: "user", Content: append(toolResults, errorImages...)})
		}
	}

	// Add cache point to the last user message for supported Claude models
	// when caching is enabled
	if cacheRetention != types.CacheRetentionNone && supportsPromptCaching(model, env) && len(result) > 0 {
		last := &result[len(result)-1]
		if last.Role == "user" {
			last.Content = append(last.Content, bedrockContentBlock{CachePoint: newBedrockCachePoint(cacheRetention)})
		}
	}

	return result
}

// bedrockToolResultStatus maps the error flag (pi: ToolResultStatus).
func bedrockToolResultStatus(isError bool) string {
	if isError {
		return "error"
	}
	return "success"
}

func convertBedrockToolConfig(tools []types.Tool, toolChoice *BedrockToolChoice, supportsStrictMode bool) (*bedrockToolConfiguration, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	if toolChoice != nil && toolChoice.Type == "none" {
		return nil, nil
	}

	var bedrockTools []bedrockTool
	for i := range tools {
		tool := &tools[i]
		strict, err := ResolveJSONSchemaStrictSampling(tool, supportsStrictMode, nil)
		if err != nil {
			return nil, err
		}
		strictEnabled := strict != nil
		parameters, err := GetJSONSchemaToolParameters(tool, strictEnabled)
		if err != nil {
			return nil, err
		}
		spec := bedrockToolSpec{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: &bedrockToolInputSchema{JSON: parameters},
		}
		if strictEnabled {
			spec.Strict = lo.ToPtr(true)
		}
		bedrockTools = append(bedrockTools, bedrockTool{ToolSpec: &spec})
	}

	var bedrockChoice *bedrockToolChoice
	if toolChoice != nil {
		switch toolChoice.Type {
		case "auto":
			bedrockChoice = &bedrockToolChoice{Auto: lo.ToPtr(true)}
		case "any":
			bedrockChoice = &bedrockToolChoice{Any: lo.ToPtr(true)}
		case "tool":
			bedrockChoice = &bedrockToolChoice{Tool: &bedrockNamedToolChoice{Name: toolChoice.Name}}
		}
	}

	return &bedrockToolConfiguration{Tools: bedrockTools, ToolChoice: bedrockChoice}, nil
}

func mapBedrockStopReason(reason string) (types.StopReason, string) {
	// pi matches the SDK's StopReason enum values; the string literals are the
	// same wire values.
	switch reason {
	case "end_turn", "stop_sequence":
		return types.StopReasonStop, ""
	case "max_tokens", "model_context_window_exceeded":
		return types.StopReasonLength, ""
	case "tool_use":
		return types.StopReasonToolUse, ""
	default:
		if reason != "" {
			return types.StopReasonError, fmt.Sprintf("Provider stopped with: %s", reason)
		}
		return types.StopReasonError, ""
	}
}

func getConfiguredBedrockRegion(options *BedrockOptions) string {
	return firstNonEmpty(
		options.Region,
		utils.GetProviderEnvValue("AWS_REGION", options.Env),
		utils.GetProviderEnvValue("AWS_DEFAULT_REGION", options.Env),
	)
}

// bedrockStaticCredentials mirrors the SDK's static credential shape (pi:
// BedrockRuntimeClientConfig.credentials).
type bedrockStaticCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

func getConfiguredBedrockCredentials(env types.ProviderEnv) *bedrockStaticCredentials {
	accessKeyID := utils.GetProviderEnvValue("AWS_ACCESS_KEY_ID", env)
	secretAccessKey := utils.GetProviderEnvValue("AWS_SECRET_ACCESS_KEY", env)
	if accessKeyID == "" || secretAccessKey == "" {
		return nil
	}
	return &bedrockStaticCredentials{
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		SessionToken:    utils.GetProviderEnvValue("AWS_SESSION_TOKEN", env),
	}
}

// getStandardBedrockEndpointRegion extracts the region from a standard AWS
// Bedrock runtime endpoint (pi: getStandardBedrockEndpointRegion).
func getStandardBedrockEndpointRegion(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	match := bedrockStandardEndpointRe.FindStringSubmatch(strings.ToLower(parsed.Hostname()))
	if match == nil {
		return ""
	}
	return match[1]
}

// bedrockStandardEndpointRe matches pi's
// /^bedrock-runtime(?:-fips)?\.([a-z0-9-]+)\.amazonaws\.com(?:\.cn)?$/.
var bedrockStandardEndpointRe = regexp.MustCompile(`^bedrock-runtime(?:-fips)?\.([a-z0-9-]+)\.amazonaws\.com(?:\.cn)?$`)

func shouldUseExplicitBedrockEndpoint(baseURL string, configuredRegion string, hasAmbientConfiguredProfile bool) bool {
	endpointRegion := getStandardBedrockEndpointRegion(baseURL)
	if endpointRegion == "" {
		return true
	}
	return configuredRegion == "" && !hasAmbientConfiguredProfile
}

func isGovCloudBedrockTarget(model *types.Model, options *BedrockOptions) bool {
	region := getConfiguredBedrockRegion(options)
	if strings.HasPrefix(strings.ToLower(region), "us-gov-") {
		return true
	}
	modelID := strings.ToLower(model.ID)
	return strings.HasPrefix(modelID, "us-gov.") || strings.HasPrefix(modelID, "arn:aws-us-gov:")
}

// buildAdditionalModelRequestFields assembles the thinking configuration (pi:
// buildAdditionalModelRequestFields).
func buildAdditionalModelRequestFields(model *types.Model, options *BedrockOptions) map[string]any {
	if options.Reasoning == "" || !model.Reasoning {
		return nil
	}

	if isAnthropicClaudeModel(model) {
		// GovCloud Bedrock currently rejects the Claude thinking.display
		// field. Omit it there until the GovCloud Converse schema catches up.
		isGovCloud := isGovCloudBedrockTarget(model, options)
		var display *string
		if !isGovCloud {
			thinkingDisplay := options.ThinkingDisplay
			if thinkingDisplay == "" {
				thinkingDisplay = BedrockThinkingDisplaySummarized
			}
			display = lo.ToPtr(string(thinkingDisplay))
		}
		// Replayed signed thinking blocks are bound to the system prompt and
		// tools they were created with. Bedrock 400s on replay after either
		// changes unless stale blocks are dropped, matching the Anthropic
		// provider. Skipped on GovCloud like display.
		useBlockBinding := !isGovCloud && supportsThinkingBlockBinding(model)
		result := map[string]any{}
		if supportsAdaptiveThinking(model.ID, model.Name) {
			thinking := map[string]any{"type": "adaptive"}
			if display != nil {
				thinking["display"] = *display
			}
			if useBlockBinding {
				thinking["block_binding"] = map[string]any{"prefix_mismatch_behavior": "drop_block"}
			}
			result["thinking"] = thinking
			result["output_config"] = map[string]any{"effort": bedrockMapThinkingLevelToEffort(model, options.Reasoning)}
			if useBlockBinding {
				result["anthropic_beta"] = []string{thinkingBindingControlsBetaBedrock}
			}
			return result
		}

		// pi's default budgets; budget-based Claude clamps extended levels to
		// high.
		defaultBudgets := map[types.ThinkingLevel]int64{
			types.ThinkingLevelMinimal: 1024,
			types.ThinkingLevelLow:     2048,
			types.ThinkingLevelMedium:  8192,
			types.ThinkingLevelHigh:    16384,
			types.ThinkingLevelXHigh:   16384,
			types.ThinkingLevelMax:     16384,
		}
		// Custom budgets only cover token-based levels through high.
		level := options.Reasoning
		if options.Reasoning == types.ThinkingLevelXHigh || options.Reasoning == types.ThinkingLevelMax {
			level = types.ThinkingLevelHigh
		}
		budget := defaultBudgets[options.Reasoning]
		if options.ThinkingBudgets != nil {
			var custom *int64
			switch level {
			case types.ThinkingLevelMinimal:
				custom = options.ThinkingBudgets.Minimal
			case types.ThinkingLevelLow:
				custom = options.ThinkingBudgets.Low
			case types.ThinkingLevelMedium:
				custom = options.ThinkingBudgets.Medium
			case types.ThinkingLevelHigh:
				custom = options.ThinkingBudgets.High
			}
			if custom != nil {
				budget = *custom
			}
		}
		thinking := map[string]any{"type": "enabled", "budget_tokens": budget}
		if display != nil {
			thinking["display"] = *display
		}
		result["thinking"] = thinking

		interleaved := options.InterleavedThinking == nil || *options.InterleavedThinking
		if interleaved {
			result["anthropic_beta"] = []string{"interleaved-thinking-2025-05-14"}
		}
		return result
	}

	// OpenAI models on Bedrock never received the configured thinking level
	// before this branch (#10142 / pi #9331): they always ran at Bedrock's
	// default effort.
	candidates := getModelMatchCandidates(model.ID, model.Name)
	for _, candidate := range candidates {
		if strings.Contains(candidate, "gpt-oss") {
			return map[string]any{"reasoning_effort": openAIGptOssEffort[options.Reasoning]}
		}
	}
	for _, candidate := range candidates {
		if strings.Contains(candidate, "gpt-") {
			if mapped, ok := model.ThinkingLevelMap[types.ModelThinkingLevel(options.Reasoning)]; ok && mapped != nil {
				return map[string]any{"reasoning": map[string]any{"effort": *mapped}}
			}
			return map[string]any{"reasoning": map[string]any{"effort": openAIGptEffort[options.Reasoning]}}
		}
	}

	return nil
}

// openAIGptEffort maps thinking levels for OpenAI GPT models (GPT-5.x, GPT-6);
// they take a nested `reasoning.effort` and reject `minimal`.
var openAIGptEffort = map[types.ThinkingLevel]string{
	types.ThinkingLevelMinimal: "low",
	types.ThinkingLevelLow:     "low",
	types.ThinkingLevelMedium:  "medium",
	types.ThinkingLevelHigh:    "high",
	types.ThinkingLevelXHigh:   "xhigh",
	types.ThinkingLevelMax:     "max",
}

// openAIGptOssEffort maps thinking levels for gpt-oss; it takes a flat
// `reasoning_effort` and only accepts low, medium and high.
var openAIGptOssEffort = map[types.ThinkingLevel]string{
	types.ThinkingLevelMinimal: "low",
	types.ThinkingLevelLow:     "low",
	types.ThinkingLevelMedium:  "medium",
	types.ThinkingLevelHigh:    "high",
	types.ThinkingLevelXHigh:   "high",
	types.ThinkingLevelMax:     "high",
}

func createBedrockImageBlock(mimeType string, data string) *bedrockImageBlock {
	var format string
	switch mimeType {
	case "image/jpeg", "image/jpg":
		format = "jpeg"
	case "image/png":
		format = "png"
	case "image/gif":
		format = "gif"
	case "image/webp":
		format = "webp"
	default:
		panic(fmt.Sprintf("Unknown image type: %s", mimeType))
	}
	return &bedrockImageBlock{Format: format, Source: &bedrockImageSource{Bytes: base64ToBedrockBytes(data)}}
}

// base64ToBedrockBytes decodes a base64 image payload (pi: base64ToBytes via
// atob; a malformed payload throws in both).
func base64ToBedrockBytes(data string) []byte {
	bytes, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		panic(err)
	}
	return bytes
}

// decodeBedrockRedactedContent decodes a stored redacted payload. The AWS SDK
// hands the blob over as bytes, but a persisted session carries it as base64.
// A hand-edited or externally produced session can hold a signature that is
// not base64; drop that block instead of failing the whole request (pi:
// decodeRedactedContent).
func decodeBedrockRedactedContent(signature string) []byte {
	if signature == "" {
		return nil
	}
	bytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return nil
	}
	return bytes
}

// bytesToBase64 joins the buffered redacted chunks into one base64 payload
// (pi: bytesToBase64).
func bytesToBase64(chunks [][]byte) string {
	total := 0
	for _, chunk := range chunks {
		total += len(chunk)
	}
	joined := make([]byte, 0, total)
	for _, chunk := range chunks {
		joined = append(joined, chunk...)
	}
	return base64.StdEncoding.EncodeToString(joined)
}

// --- wire payload structs (pi: the plain object handed to
// ConverseStreamCommand; onPayload sees exactly this shape) ---

// bedrockConverseInput is the ConverseStream command input.
type bedrockConverseInput struct {
	ModelID                      string
	Messages                     []bedrockMessage
	System                       []bedrockSystemContentBlock
	InferenceConfig              *bedrockInferenceConfig
	ToolConfig                   *bedrockToolConfiguration
	AdditionalModelRequestFields map[string]any
	RequestMetadata              map[string]string
}

// bedrockMessage is one Converse message (pi: Message with ConversationRole).
type bedrockMessage struct {
	Role    string // "user" | "assistant"
	Content []bedrockContentBlock
}

// bedrockContentBlock is one content block (pi: ContentBlock union; pointer
// members mark the variant).
type bedrockContentBlock struct {
	Text             *string
	Image            *bedrockImageBlock
	ToolUse          *bedrockToolUseBlock
	ToolResult       *bedrockToolResultBlock
	ReasoningContent *bedrockReasoningContentBlock
	CachePoint       *bedrockCachePoint
}

type bedrockImageBlock struct {
	Format string // "jpeg" | "png" | "gif" | "webp"
	Source *bedrockImageSource
}

type bedrockImageSource struct {
	Bytes []byte
}

type bedrockToolUseBlock struct {
	ToolUseID string
	Name      string
	Input     any
}

type bedrockToolResultBlock struct {
	ToolUseID string
	Content   []bedrockToolResultContentBlock
	Status    string // "success" | "error"
}

// bedrockToolResultContentBlock is one tool-result block (pi:
// ToolResultContentBlock union).
type bedrockToolResultContentBlock struct {
	Text  *string
	Image *bedrockImageBlock
}

type bedrockReasoningContentBlock struct {
	// ReasoningText carries { text, signature } (pi:
	// reasoningContent.reasoningText).
	ReasoningText *bedrockReasoningTextBlock
	// RedactedContent carries the opaque encrypted payload (pi:
	// reasoningContent.redactedContent).
	RedactedContent []byte
}

type bedrockReasoningTextBlock struct {
	Text      string
	Signature *string
}

type bedrockCachePoint struct {
	Type string
	// TTL is set to "ONE_HOUR" for long cache retention (pi: CacheTTL.ONE_HOUR).
	TTL *string
}

// bedrockSystemContentBlock is one system block (pi: SystemContentBlock
// union).
type bedrockSystemContentBlock struct {
	Text       *string
	CachePoint *bedrockCachePoint
}

type bedrockInferenceConfig struct {
	MaxTokens   *int64
	Temperature *float64
}

type bedrockToolConfiguration struct {
	Tools      []bedrockTool
	ToolChoice *bedrockToolChoice
}

type bedrockTool struct {
	ToolSpec *bedrockToolSpec
}

type bedrockToolSpec struct {
	Name        string
	Description string
	InputSchema *bedrockToolInputSchema
	// Strict enables Bedrock strict tool schemas (pi: spec.strict).
	Strict *bool
}

type bedrockToolInputSchema struct {
	// JSON carries the tool parameter schema (pi: { json: <document> }).
	JSON []byte
}

// bedrockToolChoice is the wire tool choice (pi: ToolChoice union).
type bedrockToolChoice struct {
	Auto *bool
	Any  *bool
	Tool *bedrockNamedToolChoice
}

type bedrockNamedToolChoice struct {
	Name string
}

// bedrockUserAgent renders the client identity as an AWS SDK application
// identifier (pi: bedrockUserAgent). The SDK escapes characters that are not
// valid in a user-agent token, so pi's detailed form is reduced to name and
// version; pig hands the SDK the same reduced form through AppID.
func bedrockUserAgent(identity *types.ClientIdentity) string {
	mode := types.UserAgentDetailed()
	if identity != nil && identity.UserAgent != nil {
		mode = identity.UserAgent
	}
	if mode.Custom != "" {
		return mode.Custom
	}
	if mode.Short {
		return "pi"
	}
	return "pi/" + utils.GetClientVersion(identity)
}

// --- transport seam (pi: BedrockRuntimeClient + ConverseStreamCommand) ---

// bedrockClientConfig mirrors the BedrockRuntimeClientConfig fields pi sets on
// the client constructor config. Tests capture it like pi's
// bedrockMock.constructorCalls.
type bedrockClientConfig struct {
	// Profile selects a shared-config profile (pi: config.profile).
	Profile string
	// Region is the resolved region (pi: config.region).
	Region string
	// Endpoint is the resolved endpoint override (pi: config.endpoint).
	Endpoint string
	// Credentials are static credentials; nil defers to the SDK chain (pi:
	// config.credentials).
	Credentials *bedrockStaticCredentials
	// Token is the Bedrock bearer token (pi: config.token = { token }).
	Token string
	// AuthSchemePreference pins the bearer auth scheme (pi:
	// config.authSchemePreference).
	AuthSchemePreference []string
	// ProxyURL routes the request through an HTTP(S) proxy (pi: requestHandler
	// = new NodeHttpHandler({ httpAgent, httpsAgent })).
	ProxyURL string
	// ForceHTTP1 is the AWS_BEDROCK_FORCE_HTTP1=1 flag (pi: requestHandler =
	// new NodeHttpHandler()).
	ForceHTTP1 bool
	// UserAgent is the client identity as an AWS SDK application identifier
	// (pi: customUserAgent); empty leaves the SDK default.
	UserAgent string
}

// bedrockClientParams carries everything the client factory needs beyond the
// config (pi: the addCustomHeadersMiddleware / addResponseHeadersMiddleware
// registrations that follow construction).
type bedrockClientParams struct {
	config bedrockClientConfig
	// customHeaders are injected at the Smithy build step, before signing (pi:
	// addCustomHeadersMiddleware). Empty/nil = no middleware.
	customHeaders map[string]string
	// onResponse fires at the Smithy deserialize step with the raw response
	// (pi: addResponseHeadersMiddleware). nil = no middleware.
	onResponse func(types.ProviderResponse, *types.Model)
	model      *types.Model
}

// bedrockResponseMetadata is pi's response.$metadata slice.
type bedrockResponseMetadata struct {
	HTTPStatusCode int
	RequestID      string
	// Observed reports that onResponse already fired from the raw-response
	// middleware (pi: observedRawResponse flag guarding the $metadata
	// fallback).
	Observed bool
}

// bedrockConverseStreamResult carries the response metadata and the decoded
// event stream (pi: the client.send result's $metadata + response.stream).
type bedrockConverseStreamResult struct {
	Metadata bedrockResponseMetadata
	Events   bedrockEventSource
}

// bedrockEventSource iterates decoded ConverseStream events (pi: for await
// over response.stream). Next returns io.EOF when the stream ended cleanly;
// mid-stream provider exceptions surface as errors (pi: exception items
// thrown from the iterator).
type bedrockEventSource interface {
	Next(ctx context.Context) (brtypes.ConverseStreamOutput, error)
	Close()
}

// bedrockStreamClient is the transport seam (pi: BedrockRuntimeClient.send).
type bedrockStreamClient interface {
	ConverseStream(ctx context.Context, input *bedrockConverseInput) (*bedrockConverseStreamResult, error)
}

// bedrockNewClient is the client factory seam (pi: vi.mock of
// @aws-sdk/client-bedrock-runtime). Tests override it to capture config and
// emit canned events.
var bedrockNewClient = func(ctx context.Context, params bedrockClientParams) (bedrockStreamClient, error) {
	return newBedrockSDKClient(ctx, params)
}

// newBedrockSDKClient builds the real aws-sdk-go-v2 client (pi: new
// BedrockRuntimeClient(config)).
func newBedrockSDKClient(ctx context.Context, params bedrockClientParams) (bedrockStreamClient, error) {
	cfg := params.config
	var loadOpts []awsconfig.LoadOptionsFunc
	if cfg.Profile != "" {
		loadOpts = append(loadOpts, awsconfig.WithSharedConfigProfile(cfg.Profile))
	}
	if cfg.Region != "" {
		loadOpts = append(loadOpts, awsconfig.WithRegion(cfg.Region))
	}
	if cfg.Endpoint != "" {
		loadOpts = append(loadOpts, awsconfig.WithBaseEndpoint(cfg.Endpoint))
	}
	if cfg.Credentials != nil {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: awsCredentialsValue(cfg.Credentials),
		}))
	} else if cfg.Profile != "" {
		// JS SDK semantics (pi #6957): a configured profile beats ambient env
		// keys. The Go SDK's default chain prefers env keys, so force
		// shared-config resolution when the profile provides static keys.
		if provider := bedrockProfileCredentialsProvider(ctx, cfg.Profile); provider != nil {
			loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(provider))
		}
	}
	if cfg.Token != "" {
		loadOpts = append(loadOpts, awsconfig.WithBearerAuthTokenProvider(bedrockBearerTokenProvider(cfg.Token)))
		if len(cfg.AuthSchemePreference) > 0 {
			loadOpts = append(loadOpts, awsconfig.WithAuthSchemePreference(cfg.AuthSchemePreference...))
		}
	}

	loadOptsFns := make([]func(*awsconfig.LoadOptions) error, len(loadOpts))
	for i, fn := range loadOpts {
		loadOptsFns[i] = fn
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOptsFns...)
	if err != nil {
		return nil, err
	}

	// metadata carries the raw response facts the adapter reads (pi:
	// response.$metadata + the observedRawResponse flag). The client is
	// per-request, so one metadata instance is race-free.
	metadata := &bedrockResponseMetadata{}
	client := bedrockruntime.NewFromConfig(awsCfg, func(o *bedrockruntime.Options) {
		if cfg.UserAgent != "" {
			o.AppID = cfg.UserAgent
		}
		if cfg.Token != "" {
			o.BearerAuthTokenProvider = bedrockBearerTokenProvider(cfg.Token)
			o.AuthSchemePreference = cfg.AuthSchemePreference
		}
		if cfg.ProxyURL != "" {
			// pi: NodeHttpHandler with HttpProxyAgent/HttpsProxyAgent. The Go
			// transport proxies both schemes through ProxyURL (CONNECT for
			// https targets).
			o.HTTPClient = &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(bedrockMustParseURL(cfg.ProxyURL))}}
		}
		// cfg.ForceHTTP1 is a no-op: the Go SDK is HTTP/1.1 by default.
		if len(params.customHeaders) > 0 {
			o.APIOptions = append(o.APIOptions, func(stack *smithymiddleware.Stack) error {
				return stack.Build.Add(&bedrockCustomHeadersMiddleware{headers: params.customHeaders}, smithymiddleware.After)
			})
		}
		o.APIOptions = append(o.APIOptions, func(stack *smithymiddleware.Stack) error {
			return stack.Deserialize.Add(&bedrockResponseHeadersMiddleware{
				onResponse: params.onResponse,
				model:      params.model,
				metadata:   metadata,
			}, smithymiddleware.Before)
		})
	})
	return &bedrockSDKClient{client: client, metadata: metadata}, nil
}

func bedrockMustParseURL(raw string) *url.URL {
	parsed, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return parsed
}

func awsCredentialsValue(creds *bedrockStaticCredentials) aws.Credentials {
	return aws.Credentials{
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		SessionToken:    creds.SessionToken,
	}
}

// bedrockProfileCredentialsProvider resolves shared-config profile
// credentials for the JS SDK's profile-beats-env-keys semantics (pi #6957).
// Static-key profiles resolve here; role/SSO/process profiles return nil and
// fall back to the SDK default chain, where ambient env keys may win (declared
// deviation — pig does not re-implement the SDK's full shared-config
// credential resolver).
func bedrockProfileCredentialsProvider(ctx context.Context, profile string) aws.CredentialsProvider {
	shared, err := awsconfig.LoadSharedConfigProfile(ctx, profile)
	if err != nil {
		return nil
	}
	if shared.Credentials.HasKeys() {
		return credentials.StaticCredentialsProvider{Value: shared.Credentials}
	}
	return nil
}

// bedrockBearerTokenProvider adapts a static Bedrock API key to the smithy
// bearer token provider (pi: config.token = { token }).
func bedrockBearerTokenProvider(token string) smithybearer.TokenProvider {
	return smithybearer.TokenProviderFunc(func(ctx context.Context) (smithybearer.Token, error) {
		return smithybearer.Token{Value: token}, nil
	})
}

// bedrockCustomHeadersMiddleware is the Smithy build-step middleware injecting
// caller headers (pi: addCustomHeadersMiddleware, step "build", priority
// "low", name "pi-ai-custom-headers").
type bedrockCustomHeadersMiddleware struct {
	headers map[string]string
}

func (m *bedrockCustomHeadersMiddleware) ID() string { return "pi-ai-custom-headers" }

func (m *bedrockCustomHeadersMiddleware) HandleBuild(ctx context.Context, in smithymiddleware.BuildInput, next smithymiddleware.BuildHandler) (smithymiddleware.BuildOutput, smithymiddleware.Metadata, error) {
	if request, ok := in.Request.(*smithyhttp.Request); ok && request != nil && request.Header != nil {
		applyBedrockCustomHeaders(request.Header, m.headers)
	}
	return next.HandleBuild(ctx, in)
}

// bedrockResponseHeadersMiddleware captures the raw Smithy HTTP response at
// the deserialize step, after the SDK receives the response but before the
// event stream is consumed. Bedrock's modeled $metadata only preserves
// selected HTTP metadata, so custom gateway headers are otherwise lost before
// callers see onResponse (pi: addResponseHeadersMiddleware, step "deserialize",
// name "pi-ai-response-headers").
type bedrockResponseHeadersMiddleware struct {
	onResponse func(types.ProviderResponse, *types.Model)
	model      *types.Model
	metadata   *bedrockResponseMetadata
}

func (m *bedrockResponseHeadersMiddleware) ID() string { return "pi-ai-response-headers" }

func (m *bedrockResponseHeadersMiddleware) HandleDeserialize(ctx context.Context, in smithymiddleware.DeserializeInput, next smithymiddleware.DeserializeHandler) (out smithymiddleware.DeserializeOutput, metadata smithymiddleware.Metadata, err error) {
	out, metadata, err = next.HandleDeserialize(ctx, in)
	if err != nil {
		return out, metadata, err
	}
	response, ok := out.RawResponse.(*smithyhttp.Response)
	if !ok || response == nil || response.Response == nil {
		return out, metadata, err
	}
	m.metadata.HTTPStatusCode = response.StatusCode
	m.metadata.RequestID = response.Header.Get("x-amzn-requestid")
	if m.onResponse != nil {
		m.metadata.Observed = true
		m.onResponse(types.ProviderResponse{
			Status:  response.StatusCode,
			Headers: utils.HeadersToRecord(response.Header),
		}, m.model)
	}
	return out, metadata, err
}

// bedrockSDKClient adapts the aws-sdk-go-v2 client to the seam.
type bedrockSDKClient struct {
	client   *bedrockruntime.Client
	metadata *bedrockResponseMetadata
}

func (c *bedrockSDKClient) ConverseStream(ctx context.Context, input *bedrockConverseInput) (*bedrockConverseStreamResult, error) {
	sdkInput, err := toBedrockSDKInput(input)
	if err != nil {
		return nil, err
	}
	out, err := c.client.ConverseStream(ctx, sdkInput)
	if err != nil {
		return nil, err
	}
	stream := out.GetStream()
	return &bedrockConverseStreamResult{
		Metadata: *c.metadata,
		Events: &bedrockSDKEventSource{
			events: stream.Events(),
			stream: stream,
		},
	}, nil
}

// bedrockSDKEventSource wraps the SDK's event stream channel.
type bedrockSDKEventSource struct {
	events <-chan brtypes.ConverseStreamOutput
	stream *bedrockruntime.ConverseStreamEventStream
}

func (s *bedrockSDKEventSource) Next(ctx context.Context) (brtypes.ConverseStreamOutput, error) {
	select {
	case event, ok := <-s.events:
		if !ok {
			// Channel closed: a mid-stream exception (Err set) or clean EOF.
			if err := s.stream.Err(); err != nil {
				return nil, err
			}
			return nil, io.EOF
		}
		return event, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *bedrockSDKEventSource) Close() {
	_ = s.stream.Close()
}

// toBedrockSDKInput converts the neutral payload into the SDK input (pi: the
// plain object is passed straight into ConverseStreamCommand).
func toBedrockSDKInput(input *bedrockConverseInput) (*bedrockruntime.ConverseStreamInput, error) {
	if input == nil {
		return nil, nil
	}
	sdkInput := &bedrockruntime.ConverseStreamInput{
		ModelId: lo.ToPtr(input.ModelID),
	}
	for _, message := range input.Messages {
		sdkMessage := brtypes.Message{Role: brtypes.ConversationRole(message.Role)}
		for _, block := range message.Content {
			switch {
			case block.Text != nil:
				sdkMessage.Content = append(sdkMessage.Content, &brtypes.ContentBlockMemberText{Value: *block.Text})
			case block.Image != nil:
				sdkMessage.Content = append(sdkMessage.Content, &brtypes.ContentBlockMemberImage{Value: brtypes.ImageBlock{
					Format: brtypes.ImageFormat(block.Image.Format),
					Source: &brtypes.ImageSourceMemberBytes{Value: block.Image.Source.Bytes},
				}})
			case block.ToolUse != nil:
				sdkMessage.Content = append(sdkMessage.Content, &brtypes.ContentBlockMemberToolUse{Value: brtypes.ToolUseBlock{
					ToolUseId: lo.ToPtr(block.ToolUse.ToolUseID),
					Name:      lo.ToPtr(block.ToolUse.Name),
					Input:     document.NewLazyDocument(block.ToolUse.Input),
				}})
			case block.ToolResult != nil:
				toolResult := block.ToolResult
				blocks := make([]brtypes.ToolResultContentBlock, 0, len(toolResult.Content))
				for _, contentBlock := range toolResult.Content {
					if contentBlock.Text != nil {
						blocks = append(blocks, &brtypes.ToolResultContentBlockMemberText{Value: *contentBlock.Text})
					} else if contentBlock.Image != nil {
						blocks = append(blocks, &brtypes.ToolResultContentBlockMemberImage{Value: brtypes.ImageBlock{
							Format: brtypes.ImageFormat(contentBlock.Image.Format),
							Source: &brtypes.ImageSourceMemberBytes{Value: contentBlock.Image.Source.Bytes},
						}})
					}
				}
				sdkMessage.Content = append(sdkMessage.Content, &brtypes.ContentBlockMemberToolResult{Value: brtypes.ToolResultBlock{
					ToolUseId: lo.ToPtr(toolResult.ToolUseID),
					Content:   blocks,
					Status:    brtypes.ToolResultStatus(toolResult.Status),
				}})
			case block.ReasoningContent != nil:
				reasoning := block.ReasoningContent
				if reasoning.RedactedContent != nil {
					sdkMessage.Content = append(sdkMessage.Content, &brtypes.ContentBlockMemberReasoningContent{Value: &brtypes.ReasoningContentBlockMemberRedactedContent{Value: reasoning.RedactedContent}})
				} else if reasoning.ReasoningText != nil {
					sdkMessage.Content = append(sdkMessage.Content, &brtypes.ContentBlockMemberReasoningContent{Value: &brtypes.ReasoningContentBlockMemberReasoningText{Value: brtypes.ReasoningTextBlock{
						Text:      lo.ToPtr(reasoning.ReasoningText.Text),
						Signature: reasoning.ReasoningText.Signature,
					}}})
				}
			case block.CachePoint != nil:
				sdkMessage.Content = append(sdkMessage.Content, &brtypes.ContentBlockMemberCachePoint{Value: bedrockSDKCachePoint(block.CachePoint)})
			}
		}
		sdkInput.Messages = append(sdkInput.Messages, sdkMessage)
	}
	for _, block := range input.System {
		if block.Text != nil {
			sdkInput.System = append(sdkInput.System, &brtypes.SystemContentBlockMemberText{Value: *block.Text})
		} else if block.CachePoint != nil {
			sdkInput.System = append(sdkInput.System, &brtypes.SystemContentBlockMemberCachePoint{Value: bedrockSDKCachePoint(block.CachePoint)})
		}
	}
	if input.InferenceConfig != nil {
		inferenceConfig := &brtypes.InferenceConfiguration{}
		if input.InferenceConfig.MaxTokens != nil {
			inferenceConfig.MaxTokens = lo.ToPtr(int32(*input.InferenceConfig.MaxTokens))
		}
		if input.InferenceConfig.Temperature != nil {
			inferenceConfig.Temperature = lo.ToPtr(float32(*input.InferenceConfig.Temperature))
		}
		sdkInput.InferenceConfig = inferenceConfig
	}
	if input.ToolConfig != nil {
		toolConfig := &brtypes.ToolConfiguration{}
		for _, tool := range input.ToolConfig.Tools {
			spec := tool.ToolSpec
			var schema any
			if err := json.Unmarshal(spec.InputSchema.JSON, &schema); err != nil {
				return nil, err
			}
			toolSpec := brtypes.ToolSpecification{
				Name:        lo.ToPtr(spec.Name),
				Description: lo.ToPtr(spec.Description),
				InputSchema: &brtypes.ToolInputSchemaMemberJson{Value: document.NewLazyDocument(schema)},
			}
			if spec.Strict != nil {
				toolSpec.Strict = spec.Strict
			}
			toolConfig.Tools = append(toolConfig.Tools, &brtypes.ToolMemberToolSpec{Value: toolSpec})
		}
		if choice := input.ToolConfig.ToolChoice; choice != nil {
			switch {
			case choice.Auto != nil:
				toolConfig.ToolChoice = &brtypes.ToolChoiceMemberAuto{Value: brtypes.AutoToolChoice{}}
			case choice.Any != nil:
				toolConfig.ToolChoice = &brtypes.ToolChoiceMemberAny{Value: brtypes.AnyToolChoice{}}
			case choice.Tool != nil:
				toolConfig.ToolChoice = &brtypes.ToolChoiceMemberTool{Value: brtypes.SpecificToolChoice{Name: lo.ToPtr(choice.Tool.Name)}}
			}
		}
		sdkInput.ToolConfig = toolConfig
	}
	if input.AdditionalModelRequestFields != nil {
		sdkInput.AdditionalModelRequestFields = document.NewLazyDocument(input.AdditionalModelRequestFields)
	}
	if input.RequestMetadata != nil {
		sdkInput.RequestMetadata = input.RequestMetadata
	}
	return sdkInput, nil
}

// bedrockSDKCachePoint converts the neutral cache point (pi: CachePointBlock).
func bedrockSDKCachePoint(cachePoint *bedrockCachePoint) brtypes.CachePointBlock {
	block := brtypes.CachePointBlock{Type: brtypes.CachePointType(cachePoint.Type)}
	if cachePoint.TTL != nil {
		block.Ttl = brtypes.CacheTTL(*cachePoint.TTL)
	}
	return block
}
