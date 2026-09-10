package types

import (
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
)

// ImagesApi identifies an image-generation wire protocol.
type ImagesApi string

const ImagesApiOpenRouter ImagesApi = "openrouter-images"

// ProviderID identifies a provider (openai, openrouter, a local vLLM, ...).
// The type is open on purpose: custom gateways and local endpoints use
// arbitrary ids. The constants below cover pi's KnownProvider set reachable
// today; ids belonging to unported wire protocols (google, amazon-bedrock,
// azure-openai-responses, openai-codex) arrive with those protocols.
type ProviderID string

const (
	ProviderOpenAI              ProviderID = "openai"
	ProviderAnthropic           ProviderID = "anthropic"
	ProviderOpenRouter          ProviderID = "openrouter"
	ProviderDeepSeek            ProviderID = "deepseek"
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
)

// ImagesProviderID identifies an image-generation provider.
type ImagesProviderID string

const ImagesProviderOpenRouter ImagesProviderID = "openrouter"

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
	Stream(ctx context.Context, model *Model, llmContext *Context, options *StreamOptions) *AssistantMessageEventStream
	StreamSimple(ctx context.Context, model *Model, llmContext *Context, options *SimpleStreamOptions) *AssistantMessageEventStream
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
type ProviderImages interface {
	GenerateImages(model *ImagesModel, images *ImagesContext, options *ImagesOptions) (*AssistantImages, error)
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
type ImagesFunction func(model *ImagesModel, images *ImagesContext, options *ImagesOptions) (*AssistantImages, error)

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
	Usage *Usage `json:"usage,omitempty"`
	// AddedToolNames lists names from Context.Tools that became available
	// after this result. Providers with native deferred tool loading use
	// this as the load point; other providers ignore it and use Context.Tools
	// normally.
	AddedToolNames []string `json:"addedToolNames,omitempty"`
	IsError        bool     `json:"isError"`
	// Timestamp is a Unix timestamp in milliseconds.
	Timestamp int64 `json:"timestamp"`
}

// Message is a session-history entry. Concrete types: UserMessage,
// AssistantMessage, ToolResultMessage.
type Message interface {
	MessageRole() Role
}

func (m UserMessage) MessageRole() Role       { return m.Role }
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
	API          ImagesApi        `json:"api"`
	Provider     ImagesProviderID `json:"provider"`
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

// Context is one LLM request body: system prompt, history, and tools (pi:
// Context). Parameter names spell it llmContext, never ctx, so it is not
// confused with Go's context.Context.
type Context struct {
	SystemPrompt string    `json:"systemPrompt,omitempty"`
	Messages     []Message `json:"messages"`
	Tools        []Tool    `json:"tools,omitempty"`
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
)

type MaxTokensField string

const (
	MaxTokensCompletion MaxTokensField = "max_completion_tokens"
	MaxTokensPlain      MaxTokensField = "max_tokens"
)

type CacheControlFormat string

const CacheControlAnthropic CacheControlFormat = "anthropic"

type DeferredToolsMode string

const DeferredToolsKimi DeferredToolsMode = "kimi"

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
	// SupportsStrictMode: whether the provider supports the strict field in
	// tool definitions. Default: true.
	SupportsStrictMode *bool `json:"supportsStrictMode,omitempty"`
	// CacheControlFormat selects the prompt-caching convention. anthropic
	// applies Anthropic-style cache_control markers to the system prompt,
	// the last tool definition, and the last user, assistant, or
	// tool-result text content.
	CacheControlFormat *CacheControlFormat `json:"cacheControlFormat,omitempty"`
	// SendSessionAffinityHeaders: whether to send session-affinity data from
	// options SessionID. Default: false.
	SendSessionAffinityHeaders *bool `json:"sendSessionAffinityHeaders,omitempty"`
	// DeferredToolsMode selects the provider-specific deferred tool
	// serialization mode.
	DeferredToolsMode *DeferredToolsMode `json:"deferredToolsMode,omitempty"`
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

// OpenAIResponsesCompat is missing here by design (pi: compatibility settings
// for OpenAI Responses, Azure OpenAI Responses, and OpenAI Codex Responses
// APIs); it arrives with those adapters.

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
	// SendSessionAffinityHeaders: send x-session-affinity from options
	// SessionID when caching is enabled; required by providers that route
	// prompt-cache hits by session affinity (e.g. Fireworks). Default: false.
	SendSessionAffinityHeaders *bool `json:"sendSessionAffinityHeaders,omitempty"`
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
	// AllowedFallbackModels: models Anthropic may fall back to on
	// server-side refusal; empty means the fallbacks field must be omitted.
	AllowedFallbackModels []AnthropicAllowedFallbackModel `json:"allowedFallbackModels,omitempty"`
	// SupportsToolReferences: deferred tools loaded via tool_reference blocks
	// in tool results. Default: true for first-party Anthropic models except
	// Haiku and pre-4.5; false elsewhere.
	SupportsToolReferences *bool `json:"supportsToolReferences,omitempty"`
}

// BedrockCompat is missing here by design (pi: supportsStrictMode for Amazon
// Bedrock); it arrives with the bedrock adapter.

// ModelCompat is the per-API compatibility settings carried by Model.Compat
// (pi: the conditional Model.compat type). Concrete types:
// OpenAICompletionsCompat for "openai-completions" (and untyped custom APIs)
// and AnthropicMessagesCompat for "anthropic-messages"; decoding dispatches on
// Model.API.
type ModelCompat interface {
	modelCompat()
}

func (c *OpenAICompletionsCompat) modelCompat() {}
func (c *AnthropicMessagesCompat) modelCompat() {}

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

// Model describes one callable model. API selects the wire protocol; Compat
// holds that API's compatibility settings (decoded by Model.UnmarshalJSON
// according to API).
type Model struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	API       Api        `json:"api"`
	Provider  ProviderID `json:"provider"`
	BaseURL   string     `json:"baseUrl"`
	Reasoning bool       `json:"reasoning"`
	// ThinkingLevelMap maps pi thinking levels to provider/model-specific
	// values; missing keys use provider defaults, nil marks a level
	// unsupported.
	ThinkingLevelMap ThinkingLevelMap `json:"thinkingLevelMap,omitempty"`
	Input            []ModelInput     `json:"input"`
	Cost             ModelCost        `json:"cost"`
	ContextWindow    int64            `json:"contextWindow"`
	MaxTokens        int64            `json:"maxTokens"`
	// SamplingParams are the model's default sampling parameters; per-request
	// keys in StreamOptions.SamplingParams override them.
	SamplingParams map[string]any    `json:"samplingParams,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	// Compat holds per-API compatibility overrides; when unset, URL-based
	// auto-detection applies.
	Compat ModelCompat `json:"compat,omitempty"`
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

// ImagesModel describes one image-generation model (pi: ImagesModel): the
// Model fields that apply to image generation, plus Output. pi's Omit also
// retains thinkingLevelMap; pig drops it because thinking levels do not
// apply to image generation.
type ImagesModel struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	API            ImagesApi         `json:"api"`
	Provider       ImagesProviderID  `json:"provider"`
	BaseURL        string            `json:"baseUrl"`
	Input          []ModelInput      `json:"input"`
	Output         []ModelInput      `json:"output"`
	Cost           ModelCost         `json:"cost"`
	SamplingParams map[string]any    `json:"samplingParams,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
}
