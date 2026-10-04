package types

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ZoroSpace/pig/telemetry"
)

// Api identifies a provider wire protocol. openai-completions (which covers
// openai, openrouter, deepseek, and any OpenAI-shaped endpoint) and
// anthropic-messages are implemented for now; the string type leaves room for
// new wire protocols without schema changes. pi's KnownApi also enumerates
// seven further strings, each arriving with its adapter:
// bedrock-converse-stream, google-generative-ai, google-vertex,
// mistral-conversations, openai-responses, azure-openai-responses,
// openai-codex-responses, pi-messages.
type Api string

const (
	ApiOpenAICompletions Api = "openai-completions"
	ApiAnthropicMessages Api = "anthropic-messages"
	// ApiBedrockConverseStream is Amazon Bedrock's Converse Stream surface (pi:
	// "bedrock-converse-stream"); the Converse API event protocol over the
	// Bedrock Runtime endpoint with SigV4 / bearer-token auth.
	ApiBedrockConverseStream Api = "bedrock-converse-stream"
	ApiOpenAIResponses       Api = "openai-responses"
	// ApiAzureOpenAIResponses is Azure's Responses surface (pi:
	// "azure-openai-responses"); same event protocol as openai-responses
	// with Azure deployment/api-version URL conventions.
	ApiAzureOpenAIResponses Api = "azure-openai-responses"
	// ApiOpenAICodexResponses is the ChatGPT Codex Responses surface (pi:
	// "openai-codex-responses"); the openai-responses event protocol over
	// chatgpt.com/backend-api with SSE + WebSocket transports and ChatGPT
	// OAuth tokens.
	ApiOpenAICodexResponses Api = "openai-codex-responses"
	// ApiGoogleGenerativeAI is Google's Gemini API surface (pi:
	// "google-generative-ai"); generateContent over
	// generativelanguage.googleapis.com with SSE.
	ApiGoogleGenerativeAI Api = "google-generative-ai"
	// ApiGoogleVertex is Google's Vertex AI Gemini surface (pi:
	// "google-vertex"); the same generateContent protocol over
	// aiplatform.googleapis.com with ADC credentials or Vertex express API
	// keys.
	ApiGoogleVertex Api = "google-vertex"
	// ApiMistralConversations is Mistral's chat API surface (pi:
	// "mistral-conversations"). pig has no adapter for it (Phase 2
	// declared: deferred on demand); the constant exists so pi custom
	// models with that API decode instead of falling into the
	// openai-completions compat shape.
	ApiMistralConversations Api = "mistral-conversations"
)

// Image generation and classification reuse Api: image/classifier models live
// in the unified Model catalog (pi v0.99.1 #9948) and Model.API carries their
// wire protocol like any other entry. pi keeps separate ImageApi/ClassifierApi
// string unions; Go has no union, so the Known* sets below are Api constants
// (same pattern as ApiMistralConversations).
const (
	// ImageApiOpenRouter is the OpenRouter image-generation wire protocol
	// (pi: KnownImageApi "openrouter-images").
	ImageApiOpenRouter Api = "openrouter-images"

	// ClassifierApiTypesafeSystemOne is the System One classification
	// protocol used by the TypeSafe Jev models (pi: KnownClassifierApi
	// "typesafe-system-one"; used by catalog entries only — pig does not
	// port the classify() runtime).
	ClassifierApiTypesafeSystemOne Api = "typesafe-system-one"
	// ClassifierApiCloudflareWorkersAI is Cloudflare Workers AI's System One
	// envelope (pi: "cloudflare-workers-ai-system-one").
	ClassifierApiCloudflareWorkersAI Api = "cloudflare-workers-ai-system-one"
	// ClassifierApiLlamaCpp is llama.cpp next-token classification (pi:
	// "llama-cpp-classify"; pig has no llama.cpp provider).
	ClassifierApiLlamaCpp Api = "llama-cpp-classify"
)

// ProviderID identifies a provider (openai, openrouter, a local vLLM, ...).
// The type is open on purpose: custom gateways and local endpoints use
// arbitrary ids. The constants below cover pi's KnownProvider set reachable
// today; ids belonging to unported wire protocols (mistral) arrive with those
// protocols.
type ProviderID string

const (
	ProviderAmazonBedrock       ProviderID = "amazon-bedrock"
	ProviderOpenAI              ProviderID = "openai"
	ProviderGoogle              ProviderID = "google"
	ProviderGoogleVertex        ProviderID = "google-vertex"
	ProviderOpenAICodex         ProviderID = "openai-codex"
	ProviderAnthropic           ProviderID = "anthropic"
	ProviderOpenRouter          ProviderID = "openrouter"
	ProviderDeepSeek            ProviderID = "deepseek"
	ProviderGithubCopilot       ProviderID = "github-copilot"
	ProviderNVIDIA              ProviderID = "nvidia"
	ProviderXAI                 ProviderID = "xai"
	ProviderGroq                ProviderID = "groq"
	ProviderCerebras            ProviderID = "cerebras"
	ProviderVercelAIGateway     ProviderID = "vercel-ai-gateway"
	ProviderZai                 ProviderID = "zai"
	ProviderZaiCodingCN         ProviderID = "zai-coding-cn"
	ProviderMistral             ProviderID = "mistral"
	ProviderMinimax             ProviderID = "minimax"
	ProviderMinimaxCN           ProviderID = "minimax-cn"
	ProviderMoonshotAI          ProviderID = "moonshotai"
	ProviderMoonshotAICN        ProviderID = "moonshotai-cn"
	ProviderHuggingFace         ProviderID = "huggingface"
	ProviderFireworks           ProviderID = "fireworks"
	ProviderTogether            ProviderID = "together"
	ProviderBaseten             ProviderID = "baseten"
	ProviderOpenCode            ProviderID = "opencode"
	ProviderOpenCodeGo          ProviderID = "opencode-go"
	ProviderKimiCoding          ProviderID = "kimi-coding"
	ProviderMeta                ProviderID = "meta"
	ProviderCloudflareWorkersAI ProviderID = "cloudflare-workers-ai"
	ProviderCloudflareAIGateway ProviderID = "cloudflare-ai-gateway"
	ProviderAntLing             ProviderID = "ant-ling"
	ProviderRadius              ProviderID = "radius"
	ProviderQwenTokenPlan       ProviderID = "qwen-token-plan"
	ProviderQwenTokenPlanCN     ProviderID = "qwen-token-plan-cn"
	ProviderQwenTokenPlanInd    ProviderID = "qwen-token-plan-individual"
	ProviderXiaomi              ProviderID = "xiaomi"
	ProviderXiaomiTokenPlanCN   ProviderID = "xiaomi-token-plan-cn"
	ProviderXiaomiTokenPlanAMS  ProviderID = "xiaomi-token-plan-ams"
	ProviderXiaomiTokenPlanSGP  ProviderID = "xiaomi-token-plan-sgp"
	// ProviderVolcenginePlan is pig-only (not in pi's KnownProvider): the
	// Volcengine Coding Plan provider, made built-in from the
	// pi-provider-volcengine-codingplan extension (see
	// ai/providers/volcengine_plan.go). Named "volcengine-plan" — not
	// "volcengine" — to leave room for a future models.dev "volcengine"
	// (plain Ark base-model endpoint, pay-per-token) and to keep the
	// Coding Plan billing path explicit.
	ProviderVolcenginePlan ProviderID = "volcengine-plan"
)

// Image-provider ids died with pi v0.99.1 #9948: image models live under the
// regular ProviderID (openrouter for "openrouter-images" entries).

type ToolChoice string

const (
	ToolChoiceAuto ToolChoice = "auto"
	ToolChoiceNone ToolChoice = "none"
)

type ThinkingLevel string

const (
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	ThinkingLevelLow     ThinkingLevel = "low"
	ThinkingLevelMedium  ThinkingLevel = "medium"
	ThinkingLevelHigh    ThinkingLevel = "high"
	ThinkingLevelXHigh   ThinkingLevel = "xhigh"
	ThinkingLevelMax     ThinkingLevel = "max"
)

// ModelThinkingLevel is a ThinkingLevel plus "off".
type ModelThinkingLevel string

const (
	ModelThinkingOff     ModelThinkingLevel = "off"
	ModelThinkingMinimal ModelThinkingLevel = "minimal"
	ModelThinkingLow     ModelThinkingLevel = "low"
	ModelThinkingMedium  ModelThinkingLevel = "medium"
	ModelThinkingHigh    ModelThinkingLevel = "high"
	ModelThinkingXHigh   ModelThinkingLevel = "xhigh"
	ModelThinkingMax     ModelThinkingLevel = "max"
)

// ThinkingLevelMap maps pi thinking levels to provider/model-specific values;
// missing keys use provider defaults, and a nil value marks a level as
// unsupported.
type ThinkingLevelMap map[ModelThinkingLevel]*string

// SamplingParamsByThinkingLevel maps pi thinking levels to per-level sampling
// parameter overrides (pi: SamplingParamsByThinkingLevel, v1.0.2 #9776).
type SamplingParamsByThinkingLevel map[ModelThinkingLevel]map[string]any

// ChatTemplateVar names the thinking value a chat-template kwarg placeholder
// resolves to.
type ChatTemplateVar string

const (
	ChatTemplateVarThinkingEnabled ChatTemplateVar = "thinking.enabled"
	ChatTemplateVarThinkingEffort  ChatTemplateVar = "thinking.effort"
	ChatTemplateVarThinkingBudget  ChatTemplateVar = "thinking.budget"
)

// ChatTemplateKwargVar is the {"$var": ...} placeholder variant of
// ChatTemplateKwargValue; the provider adapter resolves Var from the request's
// thinking settings.
type ChatTemplateKwargVar struct {
	Var         ChatTemplateVar `json:"$var"`
	OmitWhenOff bool            `json:"omitWhenOff,omitempty"`
}

// ChatTemplateKwargValue is one chat-template kwarg value: a plain JSON value
// (string, number, boolean, or null) or a ChatTemplateKwargVar placeholder.
type ChatTemplateKwargValue struct {
	Value any // string | float64 | bool | nil | ChatTemplateKwargVar
}

func (v ChatTemplateKwargValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.Value)
}

func (v *ChatTemplateKwargValue) UnmarshalJSON(data []byte) error {
	var probe any
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if m, ok := probe.(map[string]any); ok {
		if s, ok := m["$var"].(string); ok {
			v.Value = ChatTemplateKwargVar{Var: ChatTemplateVar(s)}
			if b, ok := m["omitWhenOff"].(bool); ok {
				v.Value = ChatTemplateKwargVar{Var: ChatTemplateVar(s), OmitWhenOff: b}
			}
			return nil
		}
	}
	v.Value = probe
	return nil
}

// ThinkingTokenBudgetField names the request field used to cap reasoning tokens.
type ThinkingTokenBudgetField string

const (
	ThinkingTokenBudgetFieldVLLM     ThinkingTokenBudgetField = "thinking_token_budget"
	ThinkingTokenBudgetFieldQwen     ThinkingTokenBudgetField = "thinking_budget"
	ThinkingTokenBudgetFieldLlamaCpp ThinkingTokenBudgetField = "thinking_budget_tokens"
)

// ThinkingBudgets carries token budgets for token-based thinking providers.
type ThinkingBudgets struct {
	Minimal *int64 `json:"minimal,omitempty"`
	Low     *int64 `json:"low,omitempty"`
	Medium  *int64 `json:"medium,omitempty"`
	High    *int64 `json:"high,omitempty"`
}

// CacheRetention expresses prompt cache retention preference.
type CacheRetention string

const (
	CacheRetentionNone  CacheRetention = "none"
	CacheRetentionShort CacheRetention = "short"
	CacheRetentionLong  CacheRetention = "long"
)

// ModelPromptCache is a best-effort prompt cache lifetime in seconds for each
// retention tier a request can ask for. A missing tier means the lifetime is
// unknown; pig does not warm such caches (pi: ModelPromptCache).
type ModelPromptCache map[CacheRetention]int64

// Transport selects how a provider request is carried where multiple
// transports exist.
type Transport string

const (
	TransportSSE             Transport = "sse"
	TransportWebsocket       Transport = "websocket"
	TransportWebsocketCached Transport = "websocket-cached"
	TransportAuto            Transport = "auto"
)

// ProviderEnv carries provider-scoped environment overrides; values take
// precedence over process env.
type ProviderEnv map[string]string

// ProviderHeaders carries custom HTTP headers for provider requests; a nil
// value suppresses a provider default header with the same name.
type ProviderHeaders map[string]*string

// FetchFunction replaces the transport used for provider HTTP requests.
// Provider adapters that cannot inject a custom implementation reject it.
type FetchFunction func(*http.Request) (*http.Response, error)

// SessionAffinityFormat selects the session-affinity header set: openai sends
// session_id, x-client-request-id, and x-session-affinity; openai-nosession
// sends x-client-request-id and x-session-affinity; openrouter sends
// x-session-id. It does not affect the prompt_cache_key body parameter, which
// is governed by cache retention.
type SessionAffinityFormat string

const (
	SessionAffinityOpenAI          SessionAffinityFormat = "openai"
	SessionAffinityOpenAINoSession SessionAffinityFormat = "openai-nosession"
	SessionAffinityOpenRouter      SessionAffinityFormat = "openrouter"
)

// ProviderResponse is the raw HTTP response surface exposed to callers.
type ProviderResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
}

// ProviderRequestOptions carries authentication, transport, and lifecycle
// callbacks shared by provider requests (pi: ProviderRequestOptions, field
// order preserved). Callbacks are synchronous (pi allows Promise returns).
type ProviderRequestOptions struct {
	// Signal is intentionally absent (pi: signal?: AbortSignal).
	// Cancellation travels as the Go context passed to provider calls
	// alongside these options.
	TelemetryContext telemetry.TelemetryContext
	APIKey           string
	// Fetch replaces the transport for provider HTTP requests. Adapters that
	// cannot inject a custom implementation reject it; WebSocket transports
	// are unaffected.
	Fetch FetchFunction
	// Env takes precedence over process env for provider configuration such
	// as regional settings, endpoint placeholders, and proxy variables.
	Env ProviderEnv
	// OnPayload inspects or replaces a provider payload before sending;
	// return nil to keep the payload unchanged.
	OnPayload func(payload any, model *Model) any
	// OnResponse runs after an HTTP response is received.
	OnResponse func(response ProviderResponse, model *Model)
	// Headers merge over provider defaults; caller values win. A nil entry
	// suppresses a provider/API default header with the same name.
	Headers ProviderHeaders
	// TimeoutMs is the HTTP request timeout in milliseconds for providers
	// that support it (OpenAI and Anthropic SDK clients default to 10
	// minutes); 0 means that default.
	TimeoutMs int64
	// MaxRetries caps client-side retries (OpenAI and Anthropic SDK clients
	// default to 2). nil means that default; 0 disables retries.
	MaxRetries *int
	// MaxRetryDelayMs caps how long a server-requested retry wait may be; a
	// longer request fails immediately so higher-level retry logic can
	// surface it. nil means the 60s default; 0 disables the cap.
	MaxRetryDelayMs *int64
}

// StreamOptions adds per-request sampling and transport controls.
type StreamOptions struct {
	// pi re-declares onResponse at the top of StreamOptions only to refine
	// its doc: the callback runs after the HTTP response is received and
	// before its body stream is consumed. Same field as the embedded
	// OnResponse below — promoted, not duplicated.
	ProviderRequestOptions

	Temperature *float64
	// SamplingParams merge into the request body as-is, after the named
	// request fields, so keys here override them. They let custom
	// OpenAI-compatible servers (llama.cpp, vLLM, SGLang, ...) receive
	// parameters pi does not model, e.g. top_p, top_k, min_p,
	// repetition_penalty. Merged over Model.SamplingParams per key; applied
	// only by OpenAI-compatible adapters, other APIs ignore it.
	SamplingParams map[string]any
	MaxTokens      *int64
	// Transport is the preferred transport for providers that support
	// multiple; unsupported providers ignore it.
	Transport Transport
	// CacheRetention is the prompt cache retention preference; providers map
	// it to their supported values. Default: "short".
	CacheRetention CacheRetention
	// SessionID is an optional session identifier for providers that support
	// session-based caching, request routing, or other session-aware
	// features; unsupported providers ignore it.
	SessionID string
	// WebsocketConnectTimeoutMs bounds the WebSocket connection/open
	// handshake only; stream idleness after connection uses TimeoutMs.
	WebsocketConnectTimeoutMs *int64
	// Metadata goes into API requests; providers extract the fields they
	// understand and ignore the rest (e.g. Anthropic uses user_id for abuse
	// tracking and rate limiting).
	Metadata map[string]any
}

// ProviderStreamOptions allows provider-specific extra option keys on top of
// StreamOptions.
type ProviderStreamOptions struct {
	StreamOptions
	Extra map[string]any
}

// DeferredFetchOptions bounds provider long-polling.
type DeferredFetchOptions struct {
	ProviderRequestOptions
	// Wait is the maximum provider long-poll duration in milliseconds; 0
	// performs one status check.
	Wait int64
}

// DeferredCancelOptions are the options for best-effort deferred cancellation.
type DeferredCancelOptions = ProviderRequestOptions

// pi's ApiOptionsMap and ApiStreamOptions are missing here by design: they
// are TS conditional types mapping each known Api to its concrete option
// type, with no runtime counterpart. Each adapter under ai/api declares its
// own options type; dispatch type-asserts on it.

// ProviderStreams is the uniform stream contract of an API implementation
// module: every module under ai/api exports Stream and StreamSimple. This is
// the untyped dispatch shape; per-API option typing lives on the
// implementation modules.
type ProviderStreams interface {
	// ctx carries the caller's cancellation (pi: options.signal, dropped from
	// StreamOptions per the AbortSignal → context rule).
	Stream(ctx context.Context, model *Model, llmContext *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream
	StreamSimple(ctx context.Context, model *Model, llmContext *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream
	// pi's optional fetchDeferred/cancelDeferred live on DeferredStreamer:
	// Go interfaces cannot express optional methods, so deferred-capable
	// modules implement that extra interface and callers type-assert.
}

// DeferredStreamer is the optional deferred-response capability of an API
// module (pi: optional fetchDeferred/cancelDeferred members on
// ProviderStreams; Go splits optional members into a capability interface,
// checked by type assertion).
type DeferredStreamer interface {
	FetchDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *DeferredFetchOptions) *AssistantMessageEventStream
	CancelDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *DeferredCancelOptions) error
}

// ProviderImages is the uniform contract of an image-generation API module.
// pig defers the generateImages runtime (no consumers yet, S10-1 declared);
// the contract targets image-typed Model entries (pi: generateImages(model:
// ImageModel, ...)).
type ProviderImages interface {
	GenerateImages(model *Model, images *ImagesContext, options *ImagesOptions) (*AssistantImages, error)
}

// ImagesOptions are the request options for image generation.
type ImagesOptions struct {
	ProviderRequestOptions
	Metadata map[string]any
}

// ProviderImagesOptions allows provider-specific extra option keys on top of
// ImagesOptions.
type ProviderImagesOptions struct {
	ImagesOptions
	Extra map[string]any
}

// AnthropicAllowedFallbackModel lists a model Anthropic may fall back to on
// server-side refusal, with local pricing metadata for the returned fallback
// response. When absent or empty, callers must omit the fallbacks field.
type AnthropicAllowedFallbackModel struct {
	Provider ProviderID `json:"provider"`
	Model    string     `json:"model"`
	Cost     ModelCost  `json:"cost"`
}

// DeferredWindow is the async-continuation window a deferred request asks for.
type DeferredWindow string

const (
	DeferredWindow15m DeferredWindow = "15m"
	DeferredWindow1h  DeferredWindow = "1h"
	DeferredWindow24h DeferredWindow = "24h"
)

// SimpleStreamOptions adds provider-neutral tool selection and reasoning for
// simple requests. pi models Deferred as boolean|object; pig splits it into
// Deferred and DeferredWindow (empty = provider default).
type SimpleStreamOptions struct {
	StreamOptions
	// ToolChoice is the provider-neutral tool selection for simple
	// requests. When omitted, adapters use provider-specific behavior.
	ToolChoice      ToolChoice
	Reasoning       ThinkingLevel
	Deferred        bool
	DeferredWindow  DeferredWindow
	ThinkingBudgets *ThinkingBudgets
}

// StreamFunction is the contract for a provider stream function (pi:
// StreamFunction).
//
// Contract:
//   - Must return an AssistantMessageEventStream.
//   - Direct StreamSimple() calls may throw synchronously when request auth
//     is missing (pig: the Models entry points return an error; adapter
//     StreamSimple returns the failure in the stream). Once a stream is
//     returned, request/model/runtime failures are encoded in that stream,
//     never returned as an error.
//   - Error termination must produce an AssistantMessage with StopReason
//     "error" or "aborted" and errorMessage, emitted via the stream protocol.
//
// ctx carries the caller's cancellation (pi: options.signal).
type StreamFunction func(ctx context.Context, model *Model, llmContext *Context, options *StreamOptions) *AssistantMessageEventStream

// ImagesFunction generates images for one request.
type ImagesFunction func(model *Model, images *ImagesContext, options *ImagesOptions) (*AssistantImages, error)

// ContentKind is the JSON "type" discriminator of content blocks.
type ContentKind string

const (
	KindText     ContentKind = "text"
	KindThinking ContentKind = "thinking"
	KindImage    ContentKind = "image"
	KindToolCall ContentKind = "toolCall"
)

// Content is one block of message content. Concrete types: TextContent,
// ThinkingContent, ImageContent, ToolCall.
type Content interface {
	ContentKind() ContentKind
}

// TextSignatureV1 is the structured payload carried in
// TextContent.TextSignature; Phase is "commentary" or "final_answer".
type TextSignatureV1 struct {
	V     int    `json:"v"`
	ID    string `json:"id"`
	Phase string `json:"phase,omitempty"`
}

type TextContent struct {
	Kind ContentKind `json:"type"`
	Text string      `json:"text"`
	// TextSignature carries e.g. OpenAI Responses message metadata (legacy
	// id string or TextSignatureV1 JSON).
	TextSignature string `json:"textSignature,omitempty"`
}

type ThinkingContent struct {
	Kind     ContentKind `json:"type"`
	Thinking string      `json:"thinking"`
	// ThinkingSignature holds provider-specific opaque or serialized
	// reasoning replay data.
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
	// Redacted is true when the thinking content was redacted by safety
	// filters; the opaque encrypted payload stays in ThinkingSignature so it
	// can be passed back to the API for multi-turn continuity.
	Redacted bool `json:"redacted,omitempty"`
}

type ImageContent struct {
	Kind ContentKind `json:"type"`
	// Data is base64 encoded image data.
	Data string `json:"data"`
	// MimeType, e.g. "image/jpeg" or "image/png".
	MimeType string `json:"mimeType"`
}

type ToolCall struct {
	Kind      ContentKind    `json:"type"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	// ThoughtSignature is Google-specific: an opaque signature for reusing
	// thought context.
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
	// Namespace is the OpenAI Responses namespace for calls to dynamically
	// loaded or namespaced tools.
	Namespace string `json:"namespace,omitempty"`
}

func (c TextContent) ContentKind() ContentKind     { return c.Kind }
func (c ThinkingContent) ContentKind() ContentKind { return c.Kind }
func (c ImageContent) ContentKind() ContentKind    { return c.Kind }
func (c ToolCall) ContentKind() ContentKind        { return c.Kind }

func NewTextContent(text string) TextContent {
	return TextContent{Kind: KindText, Text: text}
}

func NewThinkingContent(thinking string) ThinkingContent {
	return ThinkingContent{Kind: KindThinking, Thinking: thinking}
}

func NewImageContent(data, mimeType string) ImageContent {
	return ImageContent{Kind: KindImage, Data: data, MimeType: mimeType}
}

func NewToolCall(id, name string, arguments map[string]any) ToolCall {
	if arguments == nil {
		arguments = map[string]any{}
	}
	return ToolCall{Kind: KindToolCall, ID: id, Name: name, Arguments: arguments}
}

// DecodeContent decodes one content block, dispatching on its "type" field.
func DecodeContent(data json.RawMessage) (Content, error) {
	var probe struct {
		Type ContentKind `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	switch probe.Type {
	case KindText:
		var c TextContent
		return c, json.Unmarshal(data, &c)
	case KindThinking:
		var c ThinkingContent
		return c, json.Unmarshal(data, &c)
	case KindImage:
		var c ImageContent
		return c, json.Unmarshal(data, &c)
	case KindToolCall:
		var c ToolCall
		return c, json.Unmarshal(data, &c)
	default:
		return nil, fmt.Errorf("ai: unknown content type %q", probe.Type)
	}
}

// DecodeContentList decodes a heterogeneous content array.
func DecodeContentList(data json.RawMessage) ([]Content, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]Content, 0, len(raw))
	for _, r := range raw {
		c, err := DecodeContent(r)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// Cost holds USD amounts for one request.
type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

// Usage is the token accounting of one assistant response.
type Usage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	// CacheWrite1h is the subset of CacheWrite written with 1h retention;
	// only Anthropic reports this split.
	CacheWrite1h *int64 `json:"cacheWrite1h,omitempty"`
	// Reasoning holds reasoning/thinking tokens when the provider reports
	// them: a subset of Output, which already includes these tokens. Set
	// (possibly to 0) by providers with a reasoning breakdown; nil from
	// providers without one.
	Reasoning   *int64 `json:"reasoning,omitempty"`
	TotalTokens int64  `json:"totalTokens"`
	Cost        Cost   `json:"cost"`
}

// StopReason tells why an assistant response ended.
type StopReason string

const (
	StopReasonPending  StopReason = "pending"
	StopReasonStop     StopReason = "stop"
	StopReasonLength   StopReason = "length"
	StopReasonToolUse  StopReason = "toolUse"
	StopReasonError    StopReason = "error"
	StopReasonAborted  StopReason = "aborted"
	StopReasonDeferred StopReason = "deferred"
)

// JsonValue is missing here by design (pi: JsonValue, referenced only by
// DeferredHandle.Data); Go any covers it.

// DeferredHandle carries the provider token needed to resume a response that
// was continued asynchronously. Referenced by AssistantMessage only; no
// openai-completions path produces it today.
type DeferredHandle struct {
	Provider ProviderID `json:"provider"`
	ModelID  string     `json:"modelId"`
	API      Api        `json:"api"`
	// ID is the provider token, such as a response id or batch id plus row
	// id.
	ID          string `json:"id"`
	ExpiresAt   *int64 `json:"expiresAt,omitempty"`
	PollAfterMs *int64 `json:"pollAfterMs,omitempty"`
	// Data holds provider conversion data required to reconstruct the final
	// assistant message.
	Data any `json:"data,omitempty"`
}

// Role discriminates message kinds on the wire.
type Role string

const (
	RoleSystem     Role = "system"
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleToolResult Role = "toolResult"
)

// UserContent is a user message body: pi allows either a plain string or an
// array of text/image parts. The original form is preserved on marshal.
type UserContent struct {
	String string
	Parts  []Content
}

func (c UserContent) MarshalJSON() ([]byte, error) {
	if c.Parts == nil {
		return json.Marshal(c.String)
	}
	return json.Marshal(c.Parts)
}

func (c *UserContent) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		c.String = s
		c.Parts = nil
		return nil
	}
	parts, err := DecodeContentList(data)
	if err != nil {
		return fmt.Errorf("ai: user content: %w", err)
	}
	if parts == nil {
		parts = []Content{}
	}
	c.String = ""
	c.Parts = parts
	return nil
}

// SystemMessage is system instructions and tool declarations at one point in
// the transcript.
//
// The leading system message is the system prompt. Later system messages
// change it: Content adds instructions from that point on, Sections replace
// or remove named prompt sections, and ToolsAdded/ToolsRemoved change the
// tool set. Replaying every system message in order yields the current prompt
// and tools. Providers that accept system messages mid-conversation send each
// one in place; other providers rebuild the leading system message from the
// replayed state (pi: SystemMessage, #9548).
type SystemMessage struct {
	Role Role `json:"role"`
	// Content is instruction text. On the leading message this is the base
	// prompt; later, additional instructions.
	Content SystemContent `json:"content"`
	// Sections are named, ordered prompt sections rendered verbatim after
	// Content. The leading message declares them; later messages replace
	// sections by name, and nil removes one. Keep each section
	// self-delimiting (a tag, a heading) so the model can relate an update
	// to the original. Avoid integer-like names; JSON objects reorder those.
	Sections *SystemSections `json:"sections,omitempty"`
	// ToolsAdded are complete definitions of tools that become available at
	// this point.
	ToolsAdded []Tool `json:"toolsAdded,omitempty"`
	// ToolsRemoved are tools that stop being available at this point.
	ToolsRemoved []ToolReference `json:"toolsRemoved,omitempty"`
	// Timestamp is a Unix timestamp in milliseconds.
	Timestamp int64 `json:"timestamp"`
}

// SystemContent is a system message body: pi allows either a plain string or
// an array of text parts. The original form is preserved on marshal.
type SystemContent struct {
	String string
	Parts  []Content
}

// SystemSections is a name-keyed section map that preserves JSON key order
// (Go maps do not; the prompt replay renders sections in declaration order,
// so losing order would make requests irreproducible and bust prompt
// caches).
type SystemSections struct {
	// Keys are section names in JSON declaration order.
	Keys []string
	// Values maps section names to their text; a nil value removes the
	// section.
	Values map[string]*string
}

func (s SystemSections) MarshalJSON() ([]byte, error) {
	if s.Values == nil {
		return []byte("{}"), nil
	}
	buf := []byte("{")
	for i, k := range s.Keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf = append(buf, key...)
		buf = append(buf, ':')
		value, err := json.Marshal(s.Values[k])
		if err != nil {
			return nil, err
		}
		buf = append(buf, value...)
	}
	return append(buf, '}'), nil
}

func (s *SystemSections) UnmarshalJSON(data []byte) error {
	// Two-pass decode: capture key order with a token walk, then values via
	// a plain map unmarshal.
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("ai: system sections: expected object")
	}
	var keys []string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		keys = append(keys, keyTok.(string))
		// Skip the value.
		if err := skipJSONValue(dec); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // closing '}'
		return err
	}
	var values map[string]*string
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	s.Keys = keys
	s.Values = values
	return nil
}

func skipJSONValue(dec *json.Decoder) error {
	var raw json.RawMessage
	return dec.Decode(&raw)
}

func (c SystemContent) MarshalJSON() ([]byte, error) {
	if c.Parts == nil {
		return json.Marshal(c.String)
	}
	return json.Marshal(c.Parts)
}

func (c *SystemContent) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		c.String = s
		c.Parts = nil
		return nil
	}
	parts, err := DecodeContentList(data)
	if err != nil {
		return fmt.Errorf("ai: system content: %w", err)
	}
	if parts == nil {
		parts = []Content{}
	}
	c.String = ""
	c.Parts = parts
	return nil
}

// UserMessage is a prompt from the user.
type UserMessage struct {
	Role    Role        `json:"role"`
	Content UserContent `json:"content"`
	// Timestamp is a Unix timestamp in milliseconds.
	Timestamp int64 `json:"timestamp"`
}

// DiagnosticErrorInfo describes an error captured into a diagnostic.
type DiagnosticErrorInfo struct {
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
	Stack   string `json:"stack,omitempty"`
	Code    any    `json:"code,omitempty"`
}

// AssistantMessageDiagnostic records provider/runtime failures and recoveries.
type AssistantMessageDiagnostic struct {
	Type      string               `json:"type"`
	Timestamp int64                `json:"timestamp"`
	Error     *DiagnosticErrorInfo `json:"error,omitempty"`
	Details   map[string]any       `json:"details,omitempty"`
}

// AssistantMessage is one model response with its normalized content, usage,
// and stop reason.
type AssistantMessage struct {
	Role     Role       `json:"role"`
	Content  []Content  `json:"content"`
	API      Api        `json:"api"`
	Provider ProviderID `json:"provider"`
	Model    string     `json:"model"`
	// ResponseModel is the concrete chunk model when it differs from the
	// requested model (e.g. OpenRouter "auto" -> "anthropic/...").
	ResponseModel string `json:"responseModel,omitempty"`
	// ResponseID is the provider-specific response/message identifier when
	// the upstream API exposes one.
	ResponseID string `json:"responseId,omitempty"`
	// ProviderThinkingLevel is the exact provider-native effort level used
	// for this response. Absent for legacy or unmanaged responses.
	ProviderThinkingLevel string `json:"providerThinkingLevel,omitempty"`
	// Diagnostics hold redacted provider/runtime diagnostics for failures
	// and recoveries.
	Diagnostics []AssistantMessageDiagnostic `json:"diagnostics,omitempty"`
	Usage       Usage                        `json:"usage"`
	StopReason  StopReason                   `json:"stopReason"`
	Deferred    *DeferredHandle              `json:"deferred,omitempty"`
	// ErrorMessage is set when StopReason is error or aborted.
	ErrorMessage  string `json:"errorMessage,omitempty"`
	RawStopReason string `json:"rawStopReason,omitempty"`
	// EndTurn is the provider's indication of whether the model explicitly
	// ended its turn; preserved for debugging and does not currently affect
	// agent control flow.
	EndTurn *bool `json:"endTurn,omitempty"`
	// Timestamp is a Unix timestamp in milliseconds.
	Timestamp int64 `json:"timestamp"`
}

// ToolResultMessage reports the outcome of one tool call back to the model.
type ToolResultMessage struct {
	Role       Role   `json:"role"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	// Content supports text and images.
	Content []Content `json:"content"`
	Details any       `json:"details,omitempty"`
	// Usage, when available, comes from the tool execution itself; it is not
	// part of main LLM context accounting.
	Usage   *Usage `json:"usage,omitempty"`
	IsError bool   `json:"isError"`
	// Timestamp is a Unix timestamp in milliseconds.
	Timestamp int64 `json:"timestamp"`
}

// Message is a session-history entry. Concrete types: UserMessage,
// AssistantMessage, ToolResultMessage.
type Message interface {
	MessageRole() Role
}

func (m UserMessage) MessageRole() Role       { return m.Role }
func (m SystemMessage) MessageRole() Role     { return m.Role }
func (m AssistantMessage) MessageRole() Role  { return m.Role }
func (m ToolResultMessage) MessageRole() Role { return m.Role }

func (m *UserMessage) UnmarshalJSON(data []byte) error {
	type userMessageAlias UserMessage
	aux := struct {
		Content json.RawMessage `json:"content"`
		*userMessageAlias
	}{userMessageAlias: (*userMessageAlias)(m)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	var uc UserContent
	if err := uc.UnmarshalJSON(aux.Content); err != nil {
		return err
	}
	m.Content = uc
	return nil
}

func (m *AssistantMessage) UnmarshalJSON(data []byte) error {
	type assistantMessageAlias AssistantMessage
	aux := struct {
		Content json.RawMessage `json:"content"`
		*assistantMessageAlias
	}{assistantMessageAlias: (*assistantMessageAlias)(m)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	content, err := DecodeContentList(aux.Content)
	if err != nil {
		return err
	}
	m.Content = content
	return nil
}

func (m *ToolResultMessage) UnmarshalJSON(data []byte) error {
	type toolResultMessageAlias ToolResultMessage
	aux := struct {
		Content json.RawMessage `json:"content"`
		*toolResultMessageAlias
	}{toolResultMessageAlias: (*toolResultMessageAlias)(m)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	content, err := DecodeContentList(aux.Content)
	if err != nil {
		return err
	}
	m.Content = content
	return nil
}

// DecodeMessage decodes one message, dispatching on its "role" field.
func DecodeMessage(data json.RawMessage) (Message, error) {
	var probe struct {
		Role Role `json:"role"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	switch probe.Role {
	case RoleSystem:
		var m SystemMessage
		return m, json.Unmarshal(data, &m)
	case RoleUser:
		var m UserMessage
		return m, json.Unmarshal(data, &m)
	case RoleAssistant:
		var m AssistantMessage
		return m, json.Unmarshal(data, &m)
	case RoleToolResult:
		var m ToolResultMessage
		return m, json.Unmarshal(data, &m)
	default:
		return nil, fmt.Errorf("ai: unknown message role %q", probe.Role)
	}
}

// DecodeMessageList decodes a heterogeneous message array.
func DecodeMessageList(data json.RawMessage) ([]Message, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(raw))
	for _, r := range raw {
		m, err := DecodeMessage(r)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// ToolCalls returns the tool calls in the response content.
func (m *AssistantMessage) ToolCalls() []ToolCall {
	var out []ToolCall
	for _, c := range m.Content {
		if tc, ok := c.(ToolCall); ok {
			out = append(out, tc)
		}
	}
	return out
}

// Text returns the concatenated text parts of the response.
func (m *AssistantMessage) Text() string {
	var out string
	for _, c := range m.Content {
		if tc, ok := c.(TextContent); ok {
			out += tc.Text
		}
	}
	return out
}

// AppendDiagnostic records one diagnostic on the message.
func (m *AssistantMessage) AppendDiagnostic(d AssistantMessageDiagnostic) {
	m.Diagnostics = append(m.Diagnostics, d)
}

// ImagesInputContent and ImagesOutputContent are image-request/response
// content blocks; only text and image kinds occur (pi: TextContent |
// ImageContent unions).
type ImagesInputContent = Content
type ImagesOutputContent = Content

// ImagesContext is one image-generation request body.
type ImagesContext struct {
	Input []ImagesInputContent `json:"input"`
}

type ImagesStopReason string

const (
	ImagesStopStop    ImagesStopReason = "stop"
	ImagesStopError   ImagesStopReason = "error"
	ImagesStopAborted ImagesStopReason = "aborted"
)

// AssistantImages is the result of one image-generation request.
type AssistantImages struct {
	API          Api              `json:"api"`
	Provider     ProviderID       `json:"provider"`
	Model        string           `json:"model"`
	Output       []Content        `json:"output"`
	ResponseID   string           `json:"responseId,omitempty"`
	Usage        *Usage           `json:"usage,omitempty"`
	StopReason   ImagesStopReason `json:"stopReason"`
	ErrorMessage string           `json:"errorMessage,omitempty"`
	Timestamp    int64            `json:"timestamp"`
}

func (a *AssistantImages) UnmarshalJSON(data []byte) error {
	type assistantImagesAlias AssistantImages
	aux := struct {
		Output json.RawMessage `json:"output"`
		*assistantImagesAlias
	}{assistantImagesAlias: (*assistantImagesAlias)(a)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	output, err := DecodeContentList(aux.Output)
	if err != nil {
		return err
	}
	a.Output = output
	return nil
}

type GrammarFormat string

const (
	GrammarOpenAILark  GrammarFormat = "openai_lark"
	GrammarOpenAIRegex GrammarFormat = "openai_regex"
)

// GrammarVariants maps grammar formats to their language encodings.
type GrammarVariants map[GrammarFormat]string

// ConstrainedSamplingConfig requests provider-side constrained sampling. Type
// is "json_schema" (with Strict "prefer"|"require") or "grammar" (with
// Variants).
type ConstrainedSamplingConfig struct {
	Type     string          `json:"type"`
	Strict   string          `json:"strict,omitempty"`
	Variants GrammarVariants `json:"variants,omitempty"`
}

// Tool is a callable tool exposed to the model. Parameters is a JSON Schema
// object (pi carries a TypeBox schema, which is JSON Schema at runtime).
type Tool struct {
	Name                string          `json:"name"`
	Description         string          `json:"description"`
	Parameters          json.RawMessage `json:"parameters"`
	ConstrainedSampling any             `json:"constrainedSampling,omitempty"`
}

// ToolReference names a tool previously declared in the transcript (pi:
// ToolReference; used by SystemMessage.ToolsRemoved).
type ToolReference struct {
	Name string `json:"name"`
}

// Context is one LLM request body: system prompt, history, and tools (pi:
// Context). Parameter names spell it llmContext, never ctx, so it is not
// confused with Go's context.Context.
//
// SystemPrompt and Tools are shorthand for a leading system message;
// NormalizeContext (ai/utils/transcript.go) folds them into one before the
// request reaches a provider.
type Context struct {
	SystemPrompt string    `json:"systemPrompt,omitempty"`
	Messages     []Message `json:"messages"`
	Tools        []Tool    `json:"tools,omitempty"`
}

// TranscriptContext is the normalized request context passed to providers and
// API implementations. The prompt and tool declarations are carried by the
// transcript's system messages. Only NormalizeContext produces this type, so
// a raw Context cannot reach provider code by accident (pi: TranscriptContext
// with a unique-symbol brand; Go has no brand types, the convention stands).
type TranscriptContext struct {
	Messages []Message `json:"messages"`
}

// AssistantMessageEventType enumerates the normalized stream events. The
// strings match pi exactly (wire/session compatibility).
type AssistantMessageEventType string

const (
	EventStart         AssistantMessageEventType = "start"
	EventTextStart     AssistantMessageEventType = "text_start"
	EventTextDelta     AssistantMessageEventType = "text_delta"
	EventTextEnd       AssistantMessageEventType = "text_end"
	EventThinkingStart AssistantMessageEventType = "thinking_start"
	EventThinkingDelta AssistantMessageEventType = "thinking_delta"
	EventThinkingEnd   AssistantMessageEventType = "thinking_end"
	EventToolCallStart AssistantMessageEventType = "toolcall_start"
	EventToolCallDelta AssistantMessageEventType = "toolcall_delta"
	EventToolCallEnd   AssistantMessageEventType = "toolcall_end"
	EventDone          AssistantMessageEventType = "done"
	EventError         AssistantMessageEventType = "error"
)

// AssistantMessageEvent is one step of a streaming assistant response. pi
// models it as a discriminated union; pig uses a single struct where exactly
// the fields named by Type are meaningful:
//
//	start            Partial
//	text_start       ContentIndex, Partial
//	text_delta       ContentIndex, Delta, Partial
//	text_end         ContentIndex, Content, Partial
//	thinking_*       same shape as text_*
//	toolcall_start   ContentIndex, Partial
//	toolcall_delta   ContentIndex, Delta, Partial
//	toolcall_end     ContentIndex, ToolCall, Partial
//	done             Reason (stop|length|toolUse|deferred), Message
//	error            Reason (aborted|error), Message
//
// Event protocol for AssistantMessageEventStream (pi: AssistantMessageEvent
// docs):
//
// Successful streams emit start before partial updates and terminate with
// done. A stream may terminate directly with error when request setup fails
// before generation starts; after start, failures also terminate with error.
// Direct StreamSimple() calls throw synchronously when request auth is
// missing (pig: the Models entry points return an error instead). Updates
// and done must never appear before start.
//
// Partial is the shared live response-so-far helper, not an event-time
// snapshot (pig deviation: each event carries a snapshot — see
// ai/api/openai_completions_stream.go syncContent). Text and thinking blocks
// are empty when their *_start event is emitted and grow only through their
// corresponding *_delta events until the authoritative *_end. Redacted
// thinking may be complete at start and emit no deltas. Tool-call arguments
// at toolcall_start are provider-specific; toolcall_delta carries subsequent
// JSON updates.
type AssistantMessageEvent struct {
	Type         AssistantMessageEventType `json:"type"`
	ContentIndex int                       `json:"contentIndex,omitempty"`
	Delta        string                    `json:"delta,omitempty"`
	Content      string                    `json:"content,omitempty"`
	ToolCall     *ToolCall                 `json:"toolCall,omitempty"`
	Partial      *AssistantMessage         `json:"partial,omitempty"`
	Message      *AssistantMessage         `json:"message,omitempty"`
	Reason       StopReason                `json:"reason,omitempty"`
}

// ThinkingFormat selects how the reasoning/thinking parameter is encoded on
// openai-compatible endpoints.
type ThinkingFormat string

const (
	// ThinkingFormatOpenAI sends reasoning_effort.
	ThinkingFormatOpenAI ThinkingFormat = "openai"
	// ThinkingFormatOpenRouter sends reasoning: { effort }.
	ThinkingFormatOpenRouter ThinkingFormat = "openrouter"
	// ThinkingFormatDeepSeek sends thinking: { type }, plus reasoning_effort
	// when supported.
	ThinkingFormatDeepSeek ThinkingFormat = "deepseek"
	// ThinkingFormatTogether sends reasoning: { enabled }, plus
	// reasoning_effort when supported.
	ThinkingFormatTogether ThinkingFormat = "together"
	// ThinkingFormatBaseten sends configurable chat_template_args, plus
	// reasoning_effort when supported.
	ThinkingFormatBaseten ThinkingFormat = "baseten"
	// ThinkingFormatZai sends thinking: { type }.
	ThinkingFormatZai ThinkingFormat = "zai"
	// ThinkingFormatQwen sends a top-level enable_thinking boolean.
	ThinkingFormatQwen ThinkingFormat = "qwen"
	// ThinkingFormatChatTemplate sends configurable chat_template_kwargs.
	ThinkingFormatChatTemplate ThinkingFormat = "chat-template"
	// ThinkingFormatQwenChatTemplate sends chat_template_kwargs
	// .enable_thinking and .preserve_thinking.
	ThinkingFormatQwenChatTemplate ThinkingFormat = "qwen-chat-template"
	// ThinkingFormatStringThinking sends a top-level thinking string.
	ThinkingFormatStringThinking ThinkingFormat = "string-thinking"
	// ThinkingFormatAntLing sends reasoning: { effort } only when the mapped
	// effort is non-null.
	ThinkingFormatAntLing ThinkingFormat = "ant-ling"
	// ThinkingFormatVolcengine is pig-only (no pi counterpart): Volcengine
	// ark Coding Plan encoding, probed live against all plan models
	// (2026-09-10). Like zai (thinking: { type: enabled, clear_thinking:
	// false }), with two ark-specific differences: reasoning_effort is never
	// sent (the endpoint silently ignores it and it suppresses thinking on
	// GLM models), and "off" sends the thinking param only when the model
	// allows disabling (glm-5-3-flash, glm-5.3 and kimi-k2.7-code return
	// HTTP 400 on thinking.type "disabled"; their ThinkingLevelMap marks off
	// as nil, so those requests omit the param and the hybrid model falls
	// back to its own default behavior).
	ThinkingFormatVolcengine ThinkingFormat = "volcengine"
)

type MaxTokensField string

const (
	MaxTokensCompletion MaxTokensField = "max_completion_tokens"
	MaxTokensPlain      MaxTokensField = "max_tokens"
)

type CacheControlFormat string

const CacheControlAnthropic CacheControlFormat = "anthropic"

// pi removed DeferredToolsMode ("kimi") with #9548 mid-conversation system
// messages; deferred tool placement moved to transcript system messages.

// OpenAICompletionsCompat overrides URL-based auto-detection of endpoint
// quirks (pi: OpenAICompletionsCompat). Pointer fields distinguish "unset"
// (auto-detect or provider default) from an explicit false/zero.
type OpenAICompletionsCompat struct {
	// SupportsStore: whether the provider supports the store field. Default:
	// auto-detected from URL.
	SupportsStore *bool `json:"supportsStore,omitempty"`
	// SupportsDeveloperRole: whether the provider supports the developer
	// role (vs system). Default: auto-detected from URL.
	SupportsDeveloperRole *bool `json:"supportsDeveloperRole,omitempty"`
	// SupportsReasoningEffort: whether the provider supports
	// reasoning_effort. Default: auto-detected from URL.
	SupportsReasoningEffort *bool `json:"supportsReasoningEffort,omitempty"`
	// SupportsUsageInStreaming: whether stream_options
	// { include_usage: true } is accepted for token usage in streaming
	// responses. Default: true.
	SupportsUsageInStreaming *bool `json:"supportsUsageInStreaming,omitempty"`
	// SupportsFinishReason: whether streamed responses include
	// finish_reason. When false, pig infers stop or toolUse when the stream
	// ends. Default: true.
	SupportsFinishReason *bool `json:"supportsFinishReason,omitempty"`
	// MaxTokensField: which field to use for max tokens. Default:
	// auto-detected from URL.
	MaxTokensField *MaxTokensField `json:"maxTokensField,omitempty"`
	// RequiresToolResultName: whether tool results require the name field.
	// Default: auto-detected from URL.
	RequiresToolResultName *bool `json:"requiresToolResultName,omitempty"`
	// RequiresAssistantAfterToolResult: whether a user message after tool
	// results requires an assistant message in between. Default:
	// auto-detected from URL.
	RequiresAssistantAfterToolResult *bool `json:"requiresAssistantAfterToolResult,omitempty"`
	// RequiresThinkingAsText: whether thinking blocks must be converted to
	// text blocks with <thinking> delimiters. Default: auto-detected from
	// URL.
	RequiresThinkingAsText *bool `json:"requiresThinkingAsText,omitempty"`
	// RequiresReasoningContentOnAssistantMessages: whether all replayed
	// assistant messages must include an empty reasoning_content field when
	// reasoning is enabled. Default: auto-detected from URL.
	RequiresReasoningContentOnAssistantMessages *bool `json:"requiresReasoningContentOnAssistantMessages,omitempty"`
	// ThinkingFormat: how the reasoning/thinking parameter is encoded.
	// Default: openai.
	ThinkingFormat *ThinkingFormat `json:"thinkingFormat,omitempty"`
	// ChatTemplateKwargs go out as chat_template_kwargs when ThinkingFormat
	// is chat-template; ChatTemplateVar placeholders resolve pi-controlled
	// thinking values.
	ChatTemplateKwargs map[string]ChatTemplateKwargValue `json:"chatTemplateKwargs,omitempty"`
	// ChatTemplateArgs go out as chat_template_args when ThinkingFormat is
	// baseten.
	ChatTemplateArgs map[string]ChatTemplateKwargValue `json:"chatTemplateArgs,omitempty"`
	// OpenRouterRouting carries OpenRouter-compatible routing preferences,
	// sent as the provider request field.
	OpenRouterRouting *OpenRouterRouting `json:"openRouterRouting,omitempty"`
	// VercelGatewayRouting carries Vercel AI Gateway routing preferences;
	// only used when baseUrl points at Vercel AI Gateway.
	VercelGatewayRouting *VercelGatewayRouting `json:"vercelGatewayRouting,omitempty"`
	// ZaiToolStream: whether z.ai supports top-level tool_stream true for
	// streaming tool call deltas. Default: false.
	ZaiToolStream *bool `json:"zaiToolStream,omitempty"`
	// ThinkingTokenBudgetField is the top-level request field used to cap
	// reasoning tokens from ThinkingBudgets. Reasoning and the answer share
	// max_tokens on these endpoints, so without a budget a reasoning-heavy
	// turn can consume the whole response and emit no answer.
	// thinking_token_budget is vLLM; thinking_budget is
	// Qwen/DashScope/SGLang; thinking_budget_tokens is llama.cpp. Off by
	// default.
	ThinkingTokenBudgetField *ThinkingTokenBudgetField `json:"thinkingTokenBudgetField,omitempty"`
	// SupportsThinkingTokenBudget is an alias for ThinkingTokenBudgetField
	// thinking_token_budget (vLLM); prefer ThinkingTokenBudgetField.
	SupportsThinkingTokenBudget *bool `json:"supportsThinkingTokenBudget,omitempty"`
	// SupportsOpenAIGrammarTools: whether the provider supports OpenAI
	// custom tools with Lark/regex grammar formats. When false,
	// grammar-constrained tools fall back to normal function tools.
	// Default: false.
	SupportsOpenAIGrammarTools *bool `json:"supportsOpenAIGrammarTools,omitempty"`
	// SupportsMidConvoSystemMessages: whether the exact model accepts system
	// or developer messages after the conversation has started. When false,
	// later system messages are folded into the leading system message.
	// Default: false; the generated model catalog enables it for verified
	// models.
	SupportsMidConvoSystemMessages *bool `json:"supportsMidConvoSystemMessages,omitempty"`
	// SupportsMidConvoToolAdditions: whether system messages can introduce
	// additional tools mid-conversation. Requires
	// SupportsMidConvoSystemMessages. Default: false; the generated model
	// catalog enables it for capable models.
	SupportsMidConvoToolAdditions *bool `json:"supportsMidConvoToolAdditions,omitempty"`
	// SupportsStrictMode: whether the provider supports the strict field in
	// tool definitions. Default: false; generated capable models enable it
	// explicitly (pi #9816).
	SupportsStrictMode *bool `json:"supportsStrictMode,omitempty"`
	// CacheControlFormat selects the prompt-caching convention. anthropic
	// applies Anthropic-style cache_control markers to the system prompt,
	// the last tool definition, and the last user, assistant, or
	// tool-result text content.
	CacheControlFormat *CacheControlFormat `json:"cacheControlFormat,omitempty"`
	// SendSessionAffinityHeaders: whether to send session-affinity data from
	// options SessionID. Default: true for OpenRouter endpoints, false
	// otherwise.
	SendSessionAffinityHeaders *bool `json:"sendSessionAffinityHeaders,omitempty"`
	// SessionAffinityFormat overrides URL-based auto-detection of the
	// session-affinity header set.
	SessionAffinityFormat *SessionAffinityFormat `json:"sessionAffinityFormat,omitempty"`
	// SupportsLongCacheRetention: whether the provider supports long prompt
	// cache retention (prompt_cache_retention "24h" or Anthropic-style
	// cache_control.ttl "1h", depending on format). Default: true.
	SupportsLongCacheRetention *bool `json:"supportsLongCacheRetention,omitempty"`
	// VllmPriority is the vLLM scheduler priority sent as the top-level
	// `priority` request field (lower values are handled earlier; server
	// default 0). Only meaningful when vLLM runs with
	// `--scheduling-policy priority`; useful for keeping background/batch
	// work from stalling interactive sessions. Off by default; not set on
	// the generated catalog.
	VllmPriority *float64 `json:"vllmPriority,omitempty"`
}

// OpenAIResponsesCompat carries compatibility settings for OpenAI Responses
// APIs (pi: OpenAIResponsesCompat). Pointer fields distinguish "unset"
// (documented default applies) from an explicit value.
type OpenAIResponsesCompat struct {
	// SupportsDeveloperRole: whether the provider supports the "developer"
	// role (vs "system"). Default: true.
	SupportsDeveloperRole *bool `json:"supportsDeveloperRole,omitempty"`
	// SupportsMidConvoSystemMessages: whether the exact model accepts
	// developer or system messages after the conversation has started. When
	// false, later system messages are folded into the leading system
	// message. Default: false; the generated model catalog enables it for
	// verified models.
	SupportsMidConvoSystemMessages *bool `json:"supportsMidConvoSystemMessages,omitempty"`
	// SessionAffinityFormat: "openai" sends session_id and
	// x-client-request-id; "openai-nosession" sends x-client-request-id;
	// "openrouter" sends x-session-id. Does not affect the prompt_cache_key
	// body param, which is governed by cache retention. Default:
	// auto-detected from provider/baseUrl.
	SessionAffinityFormat *SessionAffinityFormat `json:"sessionAffinityFormat,omitempty"`
	// SupportsLongCacheRetention: whether the provider supports long prompt
	// cache retention. This uses prompt_cache_options.ttl "30m" on GPT-5.6+
	// and prompt_cache_retention "24h" on earlier models. Default: true.
	SupportsLongCacheRetention *bool `json:"supportsLongCacheRetention,omitempty"`
	// SupportsStrictMode: whether the provider supports strict JSON-schema
	// function tools. Defaults are API-specific; generated OpenAI models
	// enable it explicitly.
	SupportsStrictMode *bool `json:"supportsStrictMode,omitempty"`
	// SupportsOpenAIGrammarTools: whether to emit OpenAI custom tools with
	// Lark/regex grammar formats. When false, grammar-constrained tools fall
	// back to normal function tools. Default: false; the generated model
	// catalog enables it for capable models.
	SupportsOpenAIGrammarTools *bool `json:"supportsOpenAIGrammarTools,omitempty"`
	// SupportsAdditionalTools: whether the model supports message-anchored
	// additional_tools input items. Default: false.
	SupportsAdditionalTools *bool `json:"supportsAdditionalTools,omitempty"`
	// SupportsToolSearch: whether the model supports client-executed tool
	// search for transcript-anchored additions. Default: false.
	SupportsToolSearch *bool `json:"supportsToolSearch,omitempty"`
	// SupportsExplicitPromptCacheMode: whether the model accepts
	// prompt_cache_options (OpenAI GPT-5.6+ prompt caching). Older OpenAI
	// models reject the parameter. Default: false.
	SupportsExplicitPromptCacheMode *bool `json:"supportsExplicitPromptCacheMode,omitempty"`
	// SupportsMaxOutputTokens: whether the provider accepts the
	// max_output_tokens parameter. Some Codex-protocol gateways reject it.
	// Default: true.
	SupportsMaxOutputTokens *bool `json:"supportsMaxOutputTokens,omitempty"`
}

// AnthropicMessagesCompat carries compatibility settings for
// Anthropic-Messages-compatible APIs. Pointer fields distinguish "unset"
// (documented default applies) from an explicit value.
type AnthropicMessagesCompat struct {
	// SupportsEagerToolInputStreaming: whether the provider accepts per-tool
	// eager_input_streaming. When false the adapter omits the field and sends
	// the fine-grained-tool-streaming beta header instead. Default: true.
	SupportsEagerToolInputStreaming *bool `json:"supportsEagerToolInputStreaming,omitempty"`
	// SupportsLongCacheRetention: whether cache_control.ttl "1h" is accepted.
	// Default: true.
	SupportsLongCacheRetention *bool `json:"supportsLongCacheRetention,omitempty"`
	// SendSessionAffinityHeaders: send session-affinity headers from options
	// SessionID when caching is enabled; required by providers that route
	// prompt-cache hits by session affinity (e.g. Fireworks). Default: true
	// for OpenRouter endpoints, false otherwise.
	SendSessionAffinityHeaders *bool `json:"sendSessionAffinityHeaders,omitempty"`
	// SessionAffinityFormat: "openrouter" sends x-session-id; when unset,
	// sends x-session-affinity.
	SessionAffinityFormat *string `json:"sessionAffinityFormat,omitempty"`
	// SupportsCacheControlOnTools: whether cache_control markers are accepted
	// on tool definitions; some Anthropic-compatible providers reject them.
	// Default: true.
	SupportsCacheControlOnTools *bool `json:"supportsCacheControlOnTools,omitempty"`
	// SupportsTemperature: whether the model accepts the temperature field
	// (Claude Opus 4.7+ rejects non-default values). Default: true.
	SupportsTemperature *bool `json:"supportsTemperature,omitempty"`
	// ForceAdaptiveThinking forces thinking.type "adaptive" plus
	// output_config.effort regardless of model id. Default: false.
	ForceAdaptiveThinking *bool `json:"forceAdaptiveThinking,omitempty"`
	// AllowEmptySignature replays empty thinking signatures as signature ""
	// instead of converting thinking to text. Default: false.
	AllowEmptySignature *bool `json:"allowEmptySignature,omitempty"`
	// SupportsStrictTools: Anthropic strict tool schemas. Default: false.
	SupportsStrictTools *bool `json:"supportsStrictTools,omitempty"`
	// SupportsMidConvoEffort: whether the exact model transport supports
	// effort-only system messages and thinking binding controls. Default:
	// false.
	SupportsMidConvoEffort *bool `json:"supportsMidConvoEffort,omitempty"`
	// SupportsMidConvoSystemMessages: whether the exact model accepts
	// system-role messages inside the conversation. When false, later
	// system messages are folded into the top-level system prompt. Default:
	// false.
	SupportsMidConvoSystemMessages *bool `json:"supportsMidConvoSystemMessages,omitempty"`
	// SupportsMidConvoToolChanges: whether the exact model accepts
	// mid-conversation tool_addition blocks with inline tool definitions
	// (inline-tools-2026-09-15) and tool_removal blocks. Requires
	// SupportsMidConvoSystemMessages. Default: false.
	SupportsMidConvoToolChanges *bool `json:"supportsMidConvoToolChanges,omitempty"`
	// AllowedFallbackModels: models Anthropic may fall back to on
	// server-side refusal; empty means the fallbacks field must be omitted.
	AllowedFallbackModels []AnthropicAllowedFallbackModel `json:"allowedFallbackModels,omitempty"`
}

// BedrockCompat carries compatibility settings for Amazon Bedrock models (pi:
// BedrockCompat).
type BedrockCompat struct {
	// SupportsStrictMode: whether the model supports Bedrock strict tool
	// schemas. Default: false.
	SupportsStrictMode *bool `json:"supportsStrictMode,omitempty"`
}

// ModelCompat is the per-API compatibility settings carried by Model.Compat
// (pi: the conditional Model.compat type). Concrete types:
// OpenAICompletionsCompat for "openai-completions" (and untyped custom APIs),
// AnthropicMessagesCompat for "anthropic-messages", BedrockCompat for
// "bedrock-converse-stream"; decoding dispatches on Model.API.
type ModelCompat interface {
	modelCompat()
}

func (c *OpenAICompletionsCompat) modelCompat() {}
func (c *OpenAIResponsesCompat) modelCompat()   {}
func (c *AnthropicMessagesCompat) modelCompat() {}
func (c *BedrockCompat) modelCompat()           {}

// MistralConversationsCompat is the compatibility settings for the Mistral
// chat API (pi: MistralConversationsCompat). pig has no mistral-conversations
// adapter/provider yet (Phase 2 declared: deferred on demand); the type is
// declared so pi models.json compat payloads survive decoding round-trips.
func (c *MistralConversationsCompat) modelCompat() {}

// MistralConversationsCompat: see modelCompat above.
type MistralConversationsCompat struct {
	// SupportsMidConvoSystemMessages: whether the exact model accepts system
	// messages after the conversation has started. When false, later system
	// messages are folded into the leading system message. Default: false.
	SupportsMidConvoSystemMessages *bool `json:"supportsMidConvoSystemMessages,omitempty"`
}

// OpenRouterRouting carries OpenRouter provider routing preferences (sent as
// the "provider" request field). Sort, PreferredMinThroughput, and
// PreferredMaxLatency are number|string|object unions and pass through as-is.
type OpenRouterRouting struct {
	AllowFallbacks         *bool               `json:"allow_fallbacks,omitempty"`
	RequireParameters      *bool               `json:"require_parameters,omitempty"`
	DataCollection         string              `json:"data_collection,omitempty"`
	ZDR                    *bool               `json:"zdr,omitempty"`
	EnforceDistillableText *bool               `json:"enforce_distillable_text,omitempty"`
	Order                  []string            `json:"order,omitempty"`
	Only                   []string            `json:"only,omitempty"`
	Ignore                 []string            `json:"ignore,omitempty"`
	Quantizations          []string            `json:"quantizations,omitempty"`
	Sort                   any                 `json:"sort,omitempty"`
	MaxPrice               *OpenRouterMaxPrice `json:"max_price,omitempty"`
	PreferredMinThroughput any                 `json:"preferred_min_throughput,omitempty"`
	PreferredMaxLatency    any                 `json:"preferred_max_latency,omitempty"`
}

// OpenRouterMaxPrice caps per-million-token and per-unit prices.
type OpenRouterMaxPrice struct {
	Prompt     any `json:"prompt,omitempty"`
	Completion any `json:"completion,omitempty"`
	Image      any `json:"image,omitempty"`
	Audio      any `json:"audio,omitempty"`
	Request    any `json:"request,omitempty"`
}

// VercelGatewayRouting carries Vercel AI Gateway routing preferences.
type VercelGatewayRouting struct {
	Only  []string `json:"only,omitempty"`
	Order []string `json:"order,omitempty"`
}

// ModelInput enumerates model input/output modalities.
type ModelInput string

const (
	ModelInputText  ModelInput = "text"
	ModelInputImage ModelInput = "image"
)

// ModelCostRates are USD per million tokens.
type ModelCostRates struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// ModelCostTier applies to requests whose total input exceeds InputTokensAbove.
type ModelCostTier struct {
	ModelCostRates
	// InputTokensAbove: use this tier for requests whose total input usage
	// exceeds this token count.
	InputTokensAbove int64 `json:"inputTokensAbove"`
}

// ModelCost prices a model.
type ModelCost struct {
	ModelCostRates
	// Tiers are request-wide pricing tiers; the highest matching input
	// threshold applies to the full request.
	Tiers []ModelCostTier `json:"tiers,omitempty"`
}

// ModelImageResizeOptions is a per-model image resize profile (pi:
// ModelImageResizeOptions, v0.87.0 image input limits #9631). All fields are
// optional integers with minimum 1 (jpegQuality maximum 100) per the
// models.json schema, so the zero value safely means "unset" and plain
// int64 carries the optionality without pointers.
type ModelImageResizeOptions struct {
	MaxWidth  int64 `json:"maxWidth,omitempty"`
	MaxHeight int64 `json:"maxHeight,omitempty"`
	// MaxBytes is the maximum base64-encoded payload size in bytes.
	MaxBytes    int64 `json:"maxBytes,omitempty"`
	JpegQuality int64 `json:"jpegQuality,omitempty"`
}

// ModelImageInputLimits scopes the input limits to images (pi:
// ModelImageInputLimits).
type ModelImageInputLimits struct {
	// Resize is the cache-safe resize profile applied before a new image
	// enters conversation history.
	Resize *ModelImageResizeOptions `json:"resize,omitempty"`
	// MaxPerMessage is the maximum images accepted in one provider message.
	MaxPerMessage int64 `json:"maxPerMessage,omitempty"`
	// MaxPerRequest is the maximum images accepted across one provider
	// request.
	MaxPerRequest int64 `json:"maxPerRequest,omitempty"`
}

// ModelInputLimits carries the provider request-level input limits (pi:
// ModelInputLimits).
type ModelInputLimits struct {
	// MaxRequestBytes is the maximum serialized provider request size in
	// bytes.
	MaxRequestBytes int64                  `json:"maxRequestBytes,omitempty"`
	Images          *ModelImageInputLimits `json:"images,omitempty"`
}

// ModelType says what a catalog entry is for and which Models operation
// accepts it (pi: ModelType = "chat" | "image" | "classifier").
type ModelType string

const (
	ModelTypeChat       ModelType = "chat"
	ModelTypeImage      ModelType = "image"
	ModelTypeClassifier ModelType = "classifier"
)

// Model describes one catalog entry. Chat entries are callable with stream()
// and friends; image entries (API ImageApiOpenRouter) with generateImages()
// only; classifier entries (System One APIs) with classify() only — pig's
// classify runtime is not ported, classifier entries exist for catalog parity
// with pi data (S10-1 declared deviation).
//
// Go has no discriminated union, so pi's Model/ImageModel/ClassifierModel
// collapse into one struct: Type discriminates, chat-specific fields
// (Reasoning, ThinkingLevelMap, PromptCache, MaxTokens, Compat) are
// image/classifier-relevant-free and zero on those entries, Output is
// image-only, ContextWindow is shared by chat and classifier.
//
// API selects the wire protocol; Compat holds that API's compatibility
// settings (decoded by Model.UnmarshalJSON according to API).
type Model struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	API      Api        `json:"api"`
	Provider ProviderID `json:"provider"`
	BaseURL  string     `json:"baseUrl"`
	// Type discriminates the entry. Chat is the default: zero value ("")
	// decodes as chat so legacy models.json / stored catalogs without a type
	// keep working (pi: `type?: "chat"`). Narrow mixed lists with
	// IsModelType, never by comparing Type directly.
	Type      ModelType `json:"type,omitempty"`
	Reasoning bool      `json:"reasoning"`
	// ThinkingLevelMap maps pi thinking levels to provider/model-specific
	// values; missing keys use provider defaults, nil marks a level
	// unsupported.
	ThinkingLevelMap ThinkingLevelMap `json:"thinkingLevelMap,omitempty"`
	Input            []ModelInput     `json:"input"`
	// Output holds the output modalities; only image models set it and it
	// always contains "image" (pi: ImageModel.output).
	Output []ModelInput `json:"output,omitempty"`
	// InputLimits holds the provider input limits and cache-safe image
	// preprocessing metadata (pi: Model.inputLimits, v0.87.0 #9631).
	InputLimits *ModelInputLimits `json:"inputLimits,omitempty"`
	Cost        ModelCost         `json:"cost"`
	// PromptCache holds prompt cache lifetimes per retention tier. Unset
	// when the provider's cache behavior is unknown (pi: Model.promptCache;
	// consumed by coding-agent prompt cache warming).
	PromptCache   ModelPromptCache `json:"promptCache,omitempty"`
	ContextWindow int64            `json:"contextWindow"`
	MaxTokens     int64            `json:"maxTokens"`
	// SamplingParams are the model's default sampling parameters; per-request
	// keys in StreamOptions.SamplingParams override them.
	SamplingParams map[string]any `json:"samplingParams,omitempty"`
	// SamplingParamsByThinkingLevel holds sampling parameter overrides
	// selected by the effective pi thinking level (pi:
	// Model.samplingParamsByThinkingLevel, v1.0.2 #9776). Keys are pi
	// thinking levels (off/minimal/low/medium/high/xhigh/max), not provider
	// values from ThinkingLevelMap; only the OpenAI-compatible adapters
	// consume it.
	SamplingParamsByThinkingLevel SamplingParamsByThinkingLevel `json:"samplingParamsByThinkingLevel,omitempty"`
	Headers                       map[string]string             `json:"headers,omitempty"`
	// Compat holds per-API compatibility overrides; when unset, URL-based
	// auto-detection applies.
	Compat ModelCompat `json:"compat,omitempty"`
}

// GetModelType returns the entry's type; entries without a type are chat
// models (pi: getModelType, `model.type ?? "chat"`).
func (m *Model) GetModelType() ModelType {
	if m.Type == "" {
		return ModelTypeChat
	}
	return m.Type
}

// IsModelType reports whether the entry is of the given type, including
// legacy chat entries without a Type field (pi: isModelType guard).
func (m *Model) IsModelType(t ModelType) bool {
	return m.GetModelType() == t
}

func (m *Model) UnmarshalJSON(data []byte) error {
	type modelAlias Model
	aux := struct {
		Compat json.RawMessage `json:"compat"`
		*modelAlias
	}{modelAlias: (*modelAlias)(m)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.Compat) == 0 || string(aux.Compat) == "null" {
		return nil
	}
	var compat ModelCompat
	switch m.API {
	case ApiAnthropicMessages:
		compat = new(AnthropicMessagesCompat)
	case ApiBedrockConverseStream:
		compat = new(BedrockCompat)
	case ApiOpenAIResponses, ApiAzureOpenAIResponses, ApiOpenAICodexResponses:
		compat = new(OpenAIResponsesCompat)
	case ApiMistralConversations:
		// pig has no mistral-conversations adapter (Phase 2 declared); the
		// compat shape exists so pi custom models round-trip.
		compat = new(MistralConversationsCompat)
	default:
		// openai-completions and untyped custom APIs use the openai shape.
		compat = new(OpenAICompletionsCompat)
	}
	if err := json.Unmarshal(aux.Compat, compat); err != nil {
		return err
	}
	m.Compat = compat
	return nil
}

// ImagesModel died with pi v0.99.1 #9948: image-generation models are Model
// entries with Type "image" (api "openrouter-images") in the unified catalog.
