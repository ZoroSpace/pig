package ai

// pi: index.ts — the re-export surface. The canonical message model and
// provider types live in ai/types (pi: types.ts); these type aliases and
// var bindings keep the package-ai spelling identical, so runtime code and
// external consumers read ai.Model exactly like pi reads Model from pi-ai.

import (
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// Wire protocol and provider identity.
type (
	Api                = types.Api
	ImagesApi          = types.ImagesApi
	ProviderID         = types.ProviderID
	ImagesProviderID   = types.ImagesProviderID
	ToolChoice         = types.ToolChoice
	ThinkingLevel      = types.ThinkingLevel
	ModelThinkingLevel = types.ModelThinkingLevel
	ThinkingLevelMap   = types.ThinkingLevelMap
)

const (
	ApiOpenAICompletions    = types.ApiOpenAICompletions
	ApiAnthropicMessages    = types.ApiAnthropicMessages
	ApiOpenAIResponses      = types.ApiOpenAIResponses
	ApiAzureOpenAIResponses = types.ApiAzureOpenAIResponses
	ApiOpenAICodexResponses = types.ApiOpenAICodexResponses
	ImagesApiOpenRouter     = types.ImagesApiOpenRouter
)

const (
	ProviderOpenAI              = types.ProviderOpenAI
	ProviderOpenAICodex         = types.ProviderOpenAICodex
	ProviderAnthropic           = types.ProviderAnthropic
	ProviderOpenRouter          = types.ProviderOpenRouter
	ProviderDeepSeek            = types.ProviderDeepSeek
	ProviderNVIDIA              = types.ProviderNVIDIA
	ProviderXAI                 = types.ProviderXAI
	ProviderGroq                = types.ProviderGroq
	ProviderCerebras            = types.ProviderCerebras
	ProviderVercelAIGateway     = types.ProviderVercelAIGateway
	ProviderZai                 = types.ProviderZai
	ProviderZaiCodingCN         = types.ProviderZaiCodingCN
	ProviderMistral             = types.ProviderMistral
	ProviderMinimax             = types.ProviderMinimax
	ProviderMinimaxCN           = types.ProviderMinimaxCN
	ProviderMoonshotAI          = types.ProviderMoonshotAI
	ProviderMoonshotAICN        = types.ProviderMoonshotAICN
	ProviderHuggingFace         = types.ProviderHuggingFace
	ProviderFireworks           = types.ProviderFireworks
	ProviderTogether            = types.ProviderTogether
	ProviderBaseten             = types.ProviderBaseten
	ProviderOpenCode            = types.ProviderOpenCode
	ProviderOpenCodeGo          = types.ProviderOpenCodeGo
	ProviderKimiCoding          = types.ProviderKimiCoding
	ProviderCloudflareWorkersAI = types.ProviderCloudflareWorkersAI
	ProviderCloudflareAIGateway = types.ProviderCloudflareAIGateway
	ProviderAntLing             = types.ProviderAntLing
	ProviderRadius              = types.ProviderRadius
	ProviderQwenTokenPlan       = types.ProviderQwenTokenPlan
	ProviderQwenTokenPlanCN     = types.ProviderQwenTokenPlanCN
	ProviderQwenTokenPlanInd    = types.ProviderQwenTokenPlanInd
	ProviderXiaomi              = types.ProviderXiaomi
	ProviderXiaomiTokenPlanCN   = types.ProviderXiaomiTokenPlanCN
	ProviderXiaomiTokenPlanAMS  = types.ProviderXiaomiTokenPlanAMS
	ProviderXiaomiTokenPlanSGP  = types.ProviderXiaomiTokenPlanSGP
	ProviderVolcenginePlan      = types.ProviderVolcenginePlan
)

const (
	ImagesProviderOpenRouter = types.ImagesProviderOpenRouter
	ToolChoiceAuto           = types.ToolChoiceAuto
	ToolChoiceNone           = types.ToolChoiceNone
)

const (
	ThinkingLevelMinimal = types.ThinkingLevelMinimal
	ThinkingLevelLow     = types.ThinkingLevelLow
	ThinkingLevelMedium  = types.ThinkingLevelMedium
	ThinkingLevelHigh    = types.ThinkingLevelHigh
	ThinkingLevelXHigh   = types.ThinkingLevelXHigh
	ThinkingLevelMax     = types.ThinkingLevelMax
)

const (
	ModelThinkingOff     = types.ModelThinkingOff
	ModelThinkingMinimal = types.ModelThinkingMinimal
	ModelThinkingLow     = types.ModelThinkingLow
	ModelThinkingMedium  = types.ModelThinkingMedium
	ModelThinkingHigh    = types.ModelThinkingHigh
	ModelThinkingXHigh   = types.ModelThinkingXHigh
	ModelThinkingMax     = types.ModelThinkingMax
)

// Chat-template kwargs.
type (
	ChatTemplateVar          = types.ChatTemplateVar
	ChatTemplateKwargVar     = types.ChatTemplateKwargVar
	ChatTemplateKwargValue   = types.ChatTemplateKwargValue
	ThinkingTokenBudgetField = types.ThinkingTokenBudgetField
	ThinkingBudgets          = types.ThinkingBudgets
)

const (
	ChatTemplateVarThinkingEnabled = types.ChatTemplateVarThinkingEnabled
	ChatTemplateVarThinkingEffort  = types.ChatTemplateVarThinkingEffort
	ChatTemplateVarThinkingBudget  = types.ChatTemplateVarThinkingBudget
)

const (
	ThinkingTokenBudgetFieldVLLM     = types.ThinkingTokenBudgetFieldVLLM
	ThinkingTokenBudgetFieldQwen     = types.ThinkingTokenBudgetFieldQwen
	ThinkingTokenBudgetFieldLlamaCpp = types.ThinkingTokenBudgetFieldLlamaCpp
)

// Base options shared by all providers.
type (
	SessionAffinityFormat  = types.SessionAffinityFormat
	CacheRetention         = types.CacheRetention
	Transport              = types.Transport
	ProviderEnv            = types.ProviderEnv
	ProviderHeaders        = types.ProviderHeaders
	FetchFunction          = types.FetchFunction
	ProviderResponse       = types.ProviderResponse
	ProviderRequestOptions = types.ProviderRequestOptions
	StreamOptions          = types.StreamOptions
	ProviderStreamOptions  = types.ProviderStreamOptions
	DeferredFetchOptions   = types.DeferredFetchOptions
	DeferredCancelOptions  = types.DeferredCancelOptions
	ProviderStreams        = types.ProviderStreams
	DeferredStreamer       = types.DeferredStreamer
	ProviderImages         = types.ProviderImages
	ImagesOptions          = types.ImagesOptions
	ProviderImagesOptions  = types.ProviderImagesOptions
	DeferredWindow         = types.DeferredWindow
	SimpleStreamOptions    = types.SimpleStreamOptions
	StreamFunction         = types.StreamFunction
	ImagesFunction         = types.ImagesFunction
)

const (
	SessionAffinityOpenAI          = types.SessionAffinityOpenAI
	SessionAffinityOpenAINoSession = types.SessionAffinityOpenAINoSession
	SessionAffinityOpenRouter      = types.SessionAffinityOpenRouter
)

const (
	CacheRetentionNone  = types.CacheRetentionNone
	CacheRetentionShort = types.CacheRetentionShort
	CacheRetentionLong  = types.CacheRetentionLong
)

const (
	TransportSSE             = types.TransportSSE
	TransportWebsocket       = types.TransportWebsocket
	TransportWebsocketCached = types.TransportWebsocketCached
	TransportAuto            = types.TransportAuto
)

const (
	DeferredWindow15m = types.DeferredWindow15m
	DeferredWindow1h  = types.DeferredWindow1h
	DeferredWindow24h = types.DeferredWindow24h
)

// Content model.
type (
	ContentKind                = types.ContentKind
	Content                    = types.Content
	TextContent                = types.TextContent
	ThinkingContent            = types.ThinkingContent
	ImageContent               = types.ImageContent
	ToolCall                   = types.ToolCall
	Cost                       = types.Cost
	Usage                      = types.Usage
	StopReason                 = types.StopReason
	DeferredHandle             = types.DeferredHandle
	DiagnosticErrorInfo        = types.DiagnosticErrorInfo
	AssistantMessageDiagnostic = types.AssistantMessageDiagnostic
	Role                       = types.Role
	UserContent                = types.UserContent
	UserMessage                = types.UserMessage
	AssistantMessage           = types.AssistantMessage
	ToolResultMessage          = types.ToolResultMessage
	Message                    = types.Message
	TextSignatureV1            = types.TextSignatureV1
)

const (
	RoleUser       = types.RoleUser
	RoleAssistant  = types.RoleAssistant
	RoleToolResult = types.RoleToolResult
)

const (
	KindText     = types.KindText
	KindThinking = types.KindThinking
	KindImage    = types.KindImage
	KindToolCall = types.KindToolCall
)

const (
	StopReasonPending  = types.StopReasonPending
	StopReasonStop     = types.StopReasonStop
	StopReasonLength   = types.StopReasonLength
	StopReasonToolUse  = types.StopReasonToolUse
	StopReasonError    = types.StopReasonError
	StopReasonAborted  = types.StopReasonAborted
	StopReasonDeferred = types.StopReasonDeferred
)

// Event stream (pi: utils/event-stream.ts, re-exported by types.ts).
// The generic EventStream[E, R] cannot be aliased without instantiation
// (no generic type aliases in Go yet): consumers needing the raw generic
// (the agent loop) import github.com/ZoroSpace/pig/ai/types directly.
type (
	AssistantMessageEventStream = types.AssistantMessageEventStream
)

var (
	NewAssistantMessageEventStream = types.NewAssistantMessageEventStream
)

var (
	NewTextContent     = types.NewTextContent
	NewThinkingContent = types.NewThinkingContent
	NewImageContent    = types.NewImageContent
	NewToolCall        = types.NewToolCall
	DecodeContent      = types.DecodeContent
	DecodeContentList  = types.DecodeContentList
	DecodeMessage      = types.DecodeMessage
	DecodeMessageList  = types.DecodeMessageList
)

// Image generation.
type (
	ImagesInputContent  = types.ImagesInputContent
	ImagesOutputContent = types.ImagesOutputContent
	ImagesContext       = types.ImagesContext
	ImagesStopReason    = types.ImagesStopReason
	AssistantImages     = types.AssistantImages
	ImagesModel         = types.ImagesModel
)

const (
	ImagesStopStop    = types.ImagesStopStop
	ImagesStopError   = types.ImagesStopError
	ImagesStopAborted = types.ImagesStopAborted
)

// Tool argument validation (pi: utils/validation.ts, star-exported by
// index.ts).
var (
	ValidateToolCall      = utils.ValidateToolCall
	ValidateToolArguments = utils.ValidateToolArguments
)

// UUID generation (pi: utils/uuid.ts, star-exported by index.ts).
var UUIDv7 = utils.UUIDv7

// Text extraction (pi: utils/text.ts, star-exported by index.ts).
var ContentText = utils.ContentText

// Assistant-call retry (pi: utils/retry.ts, star-exported by index.ts).
type (
	RetryPolicy    = utils.RetryPolicy
	RetryCallbacks = utils.RetryCallbacks
)

var (
	RetryAssistantCall        = utils.RetryAssistantCall
	IsRetryableAssistantError = utils.IsRetryableAssistantError
)

// Tools, context, and stream events.
type (
	GrammarFormat                 = types.GrammarFormat
	GrammarVariants               = types.GrammarVariants
	ConstrainedSamplingConfig     = types.ConstrainedSamplingConfig
	Tool                          = types.Tool
	Context                       = types.Context
	AssistantMessageEventType     = types.AssistantMessageEventType
	AssistantMessageEvent         = types.AssistantMessageEvent
	ThinkingFormat                = types.ThinkingFormat
	MaxTokensField                = types.MaxTokensField
	CacheControlFormat            = types.CacheControlFormat
	DeferredToolsMode             = types.DeferredToolsMode
	OpenAICompletionsCompat       = types.OpenAICompletionsCompat
	OpenAIResponsesCompat         = types.OpenAIResponsesCompat
	AnthropicMessagesCompat       = types.AnthropicMessagesCompat
	AnthropicAllowedFallbackModel = types.AnthropicAllowedFallbackModel
	ModelCompat                   = types.ModelCompat
	OpenRouterRouting             = types.OpenRouterRouting
	OpenRouterMaxPrice            = types.OpenRouterMaxPrice
	VercelGatewayRouting          = types.VercelGatewayRouting
	ModelCostRates                = types.ModelCostRates
	ModelCostTier                 = types.ModelCostTier
	ModelCost                     = types.ModelCost
	ModelInput                    = types.ModelInput
	Model                         = types.Model
)

const (
	GrammarOpenAILark  = types.GrammarOpenAILark
	GrammarOpenAIRegex = types.GrammarOpenAIRegex
)

const (
	EventStart         = types.EventStart
	EventTextStart     = types.EventTextStart
	EventTextDelta     = types.EventTextDelta
	EventTextEnd       = types.EventTextEnd
	EventThinkingStart = types.EventThinkingStart
	EventThinkingDelta = types.EventThinkingDelta
	EventThinkingEnd   = types.EventThinkingEnd
	EventToolCallStart = types.EventToolCallStart
	EventToolCallDelta = types.EventToolCallDelta
	EventToolCallEnd   = types.EventToolCallEnd
	EventDone          = types.EventDone
	EventError         = types.EventError
)

const (
	ThinkingFormatOpenAI           = types.ThinkingFormatOpenAI
	ThinkingFormatOpenRouter       = types.ThinkingFormatOpenRouter
	ThinkingFormatDeepSeek         = types.ThinkingFormatDeepSeek
	ThinkingFormatTogether         = types.ThinkingFormatTogether
	ThinkingFormatBaseten          = types.ThinkingFormatBaseten
	ThinkingFormatZai              = types.ThinkingFormatZai
	ThinkingFormatQwen             = types.ThinkingFormatQwen
	ThinkingFormatChatTemplate     = types.ThinkingFormatChatTemplate
	ThinkingFormatQwenChatTemplate = types.ThinkingFormatQwenChatTemplate
	ThinkingFormatStringThinking   = types.ThinkingFormatStringThinking
	ThinkingFormatAntLing          = types.ThinkingFormatAntLing
	ThinkingFormatVolcengine       = types.ThinkingFormatVolcengine
)

const (
	MaxTokensCompletion = types.MaxTokensCompletion
	MaxTokensPlain      = types.MaxTokensPlain
)

const (
	CacheControlAnthropic = types.CacheControlAnthropic
	DeferredToolsKimi     = types.DeferredToolsKimi
)

const (
	ModelInputText  = types.ModelInputText
	ModelInputImage = types.ModelInputImage
)
