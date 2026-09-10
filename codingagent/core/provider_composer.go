// pi: src/core/provider-composer.ts — compose built-in, models.json, and
// extension provider layers without reading credentials.
//
// Segment deviations (declared):
//   - pi's ExtensionOAuthConfig.login receives OAuthLoginCallbacks with a
//     signal member; pig's AuthInteraction carries cancellation at the
//     interaction level, so the callbacks struct has no signal (declared).
//   - OAuthSelectPrompt's Promise<string | undefined> collapses to
//     (string, error) — an empty string is "undefined" (declared).
//   - pi's async auth callbacks are synchronous here; cancellation rides the
//     context where applicable.
//   - pi's Provider literal with optional refreshModels/fetchDeferred members
//     becomes a struct implementing ai.Provider plus the optional
//     ai dynamic/deferred capability interfaces.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

// ExtensionOAuthConfig is the OAuth config of the extension registerProvider
// API (pi: ExtensionOAuthConfig).
type ExtensionOAuthConfig struct {
	Name string
	// IsSubscription reports whether access through this auth method is
	// backed by a provider subscription.
	IsSubscription bool
	// UsesCallbackServer is retained for extension source compatibility;
	// ignored by canonical auth flows (pi: @deprecated).
	UsesCallbackServer bool
	Login              func(callbacks ai.OAuthLoginCallbacks) (auth.OAuthCredentials, error)
	// RefreshToken refreshes the stored token; pi's signal rides the context.
	RefreshToken func(ctx context.Context, credentials auth.OAuthCredentials) (auth.OAuthCredentials, error)
	GetAPIKey    func(credentials auth.OAuthCredentials) string
	ModifyModels func(models []ai.Model, credentials auth.OAuthCredentials) []ai.Model
}

// ProviderConfigInputModel is one extension-registered model definition (pi:
// ProviderConfigInput["models"] item).
type ProviderConfigInputModel struct {
	ID               string
	Name             string
	API              ai.Api
	BaseURL          string
	Reasoning        bool
	ThinkingLevelMap types.ThinkingLevelMap
	Input            []ai.ModelInput
	Cost             ai.ModelCost
	ContextWindow    int64
	MaxTokens        int64
	SamplingParams   map[string]any
	Headers          map[string]string
	Compat           ai.ModelCompat
}

// ProviderConfigInput is the input type of the extension registerProvider API
// (pi: ProviderConfigInput). Empty strings = pi's undefined.
type ProviderConfigInput struct {
	Name    string
	BaseURL string
	APIKey  string
	API     ai.Api
	// StreamSimple is the custom stream implementation (pi: streamSimple).
	StreamSimple func(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream
	Headers      map[string]string
	// AuthHeader distinguishes "unset" (nil) from explicit false so the
	// models.json layer can still supply it (pi: authHeader?: boolean).
	AuthHeader *bool
	OAuth      *ExtensionOAuthConfig
	Models     []ProviderConfigInputModel
	// RefreshModels refreshes the dynamic model list (pi: refreshModels).
	RefreshModels func(ctx context.Context, refreshCtx *ai.RefreshModelsContext) ([]ProviderConfigInputModel, error)
}

// AuthStatus reports provider auth configuration (pi: AuthStatus).
type AuthStatus struct {
	Configured bool
	// Source is "stored" | "runtime" | "environment" | "fallback" |
	// "models_json_key" | "models_json_command".
	Source string
	Label  string
}

// ClearApiKeyCache clears the config-value resolution cache (pi:
// clearApiKeyCache = clearConfigValueCache).
var ClearApiKeyCache = ClearConfigValueCache

// mergeCompat merges one compat layer over another (pi: mergeCompat). Base
// may be nil; override is raw models.json compat JSON. Nested routing and
// chat-template kwarg objects merge per key.
func mergeCompat(base ai.ModelCompat, override json.RawMessage, api ai.Api) (ai.ModelCompat, error) {
	baseMap := compatToMap(base)
	var overrideMap map[string]any
	if len(override) > 0 && string(override) != "null" {
		if err := json.Unmarshal(override, &overrideMap); err != nil {
			return nil, err
		}
	}
	if overrideMap == nil && baseMap == nil {
		return nil, nil
	}
	merged := map[string]any{}
	for key, value := range baseMap {
		merged[key] = value
	}
	for key, value := range overrideMap {
		merged[key] = value
	}
	// Nested objects merge instead of replacing.
	for _, key := range []string{"openRouterRouting", "vercelGatewayRouting", "chatTemplateKwargs", "chatTemplateArgs"} {
		baseValue, hasBase := baseMap[key]
		overrideValue, hasOverride := overrideMap[key]
		baseObject, baseIsObject := baseValue.(map[string]any)
		overrideObject, overrideIsObject := overrideValue.(map[string]any)
		if (hasBase && baseIsObject) || (hasOverride && overrideIsObject) {
			nested := map[string]any{}
			for k, v := range baseObject {
				nested[k] = v
			}
			for k, v := range overrideObject {
				nested[k] = v
			}
			merged[key] = nested
		}
	}
	return decodeCompat(api, merged)
}

// compatToMap renders a typed compat into a JSON map (nil stays nil).
func compatToMap(compat ai.ModelCompat) map[string]any {
	if compat == nil {
		return nil
	}
	encoded, err := json.Marshal(compat)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(encoded, &out) != nil {
		return nil
	}
	return out
}

// decodeCompat decodes a compat map into the concrete struct for api (mirrors
// ai Model.UnmarshalJSON's dispatch).
func decodeCompat(api ai.Api, data map[string]any) (ai.ModelCompat, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	if api == ai.ApiAnthropicMessages {
		compat := new(ai.AnthropicMessagesCompat)
		if err := json.Unmarshal(encoded, compat); err != nil {
			return nil, err
		}
		return compat, nil
	}
	// openai-completions and untyped custom APIs use the openai shape.
	compat := new(ai.OpenAICompletionsCompat)
	if err := json.Unmarshal(encoded, compat); err != nil {
		return nil, err
	}
	return compat, nil
}

// applyModelOverride applies one models.json model override to a model (pi:
// applyModelOverride).
func applyModelOverride(model ai.Model, override *ModelsJsonModelOverride) ai.Model {
	if override.Name != "" {
		model.Name = override.Name
	}
	if override.Reasoning != nil {
		model.Reasoning = *override.Reasoning
	}
	if override.ThinkingLevelMap != nil {
		merged := model.ThinkingLevelMap
		if merged == nil {
			merged = types.ThinkingLevelMap{}
		}
		for key, value := range thinkingLevelMapFromJSON(override.ThinkingLevelMap) {
			merged[key] = value
		}
		model.ThinkingLevelMap = merged
	}
	if override.Input != nil {
		model.Input = inputFromJSON(override.Input)
	}
	if override.Cost != nil {
		if override.Cost.Input != nil {
			model.Cost.Input = *override.Cost.Input
		}
		if override.Cost.Output != nil {
			model.Cost.Output = *override.Cost.Output
		}
		if override.Cost.CacheRead != nil {
			model.Cost.CacheRead = *override.Cost.CacheRead
		}
		if override.Cost.CacheWrite != nil {
			model.Cost.CacheWrite = *override.Cost.CacheWrite
		}
		if override.Cost.Tiers != nil {
			model.Cost.Tiers = costTiersFromJSON(override.Cost.Tiers)
		}
	}
	if override.ContextWindow != nil {
		model.ContextWindow = int64(*override.ContextWindow)
	}
	if override.MaxTokens != nil {
		model.MaxTokens = int64(*override.MaxTokens)
	}
	if override.SamplingParams != nil {
		merged := model.SamplingParams
		if merged == nil {
			merged = map[string]any{}
		}
		for key, value := range override.SamplingParams {
			merged[key] = value
		}
		model.SamplingParams = merged
	}
	if len(override.Compat) > 0 {
		if compat, err := mergeCompat(model.Compat, override.Compat, model.API); err == nil {
			model.Compat = compat
		}
	}
	return model
}

func thinkingLevelMapFromJSON(m *ModelsJsonThinkingLevelMap) types.ThinkingLevelMap {
	out := types.ThinkingLevelMap{}
	if m == nil {
		return out
	}
	// pi's map values are string | null: only keys the JSON carried become
	// entries — an explicit null writes a nil entry (suppressing the
	// inherited level), a missing key stays absent.
	pairs := map[string]*string{
		"off": m.Off, "minimal": m.Minimal, "low": m.Low, "medium": m.Medium,
		"high": m.High, "xhigh": m.Xhigh, "max": m.Max,
	}
	for key, value := range pairs {
		if m.Present != nil && !m.Present[key] {
			continue
		}
		if value != nil {
			level := string(*value)
			out[types.ModelThinkingLevel(key)] = &level
		} else {
			out[types.ModelThinkingLevel(key)] = nil
		}
	}
	return out
}

func inputFromJSON(input []string) []ai.ModelInput {
	out := make([]ai.ModelInput, 0, len(input))
	for _, entry := range input {
		out = append(out, ai.ModelInput(entry))
	}
	return out
}

func costTiersFromJSON(tiers []ModelsJsonCostTier) []ai.ModelCostTier {
	out := make([]ai.ModelCostTier, 0, len(tiers))
	for _, tier := range tiers {
		out = append(out, ai.ModelCostTier{
			ModelCostRates: ai.ModelCostRates{
				Input:      tier.Input,
				Output:     tier.Output,
				CacheRead:  tier.CacheRead,
				CacheWrite: tier.CacheWrite,
			},
			InputTokensAbove: int64(tier.InputTokensAbove),
		})
	}
	return out
}

// modelFromJSON builds a model from a models.json definition (pi:
// modelFromJson). defaults is the same-id model or the provider's first
// model; it supplies api/baseUrl fallbacks.
func modelFromJSON(providerID string, definition *ModelsJsonModel, providerConfig *ModelsJsonProvider, defaults *ai.Model) (ai.Model, error) {
	api := definition.API
	if api == "" {
		api = providerConfig.API
	}
	if api == "" && defaults != nil {
		api = string(defaults.API)
	}
	if api == "" {
		return ai.Model{}, fmt.Errorf("Provider %s, model %s: no %q specified. Set at provider or model level.", providerID, definition.ID, "api")
	}
	baseURL := definition.BaseURL
	if baseURL == "" {
		baseURL = providerConfig.BaseURL
	}
	if baseURL == "" && defaults != nil {
		baseURL = defaults.BaseURL
	}
	if baseURL == "" {
		return ai.Model{}, fmt.Errorf("Provider %s: %q is required when defining custom models.", providerID, "baseUrl")
	}
	if definition.ContextWindow != nil && *definition.ContextWindow <= 0 {
		return ai.Model{}, fmt.Errorf("Provider %s, model %s: invalid contextWindow", providerID, definition.ID)
	}
	if definition.MaxTokens != nil && *definition.MaxTokens <= 0 {
		return ai.Model{}, fmt.Errorf("Provider %s, model %s: invalid maxTokens", providerID, definition.ID)
	}
	name := definition.Name
	if name == "" {
		name = definition.ID
	}
	reasoning := false
	if definition.Reasoning != nil {
		reasoning = *definition.Reasoning
	}
	input := []ai.ModelInput{ai.ModelInputText}
	if definition.Input != nil {
		input = inputFromJSON(definition.Input)
	}
	cost := ai.ModelCost{}
	if definition.Cost != nil {
		cost = ai.ModelCost{
			ModelCostRates: ai.ModelCostRates{
				Input:      definition.Cost.Input,
				Output:     definition.Cost.Output,
				CacheRead:  definition.Cost.CacheRead,
				CacheWrite: definition.Cost.CacheWrite,
			},
		}
		for _, tier := range definition.Cost.Tiers {
			cost.Tiers = append(cost.Tiers, ai.ModelCostTier{
				ModelCostRates: ai.ModelCostRates{
					Input:      tier.Input,
					Output:     tier.Output,
					CacheRead:  tier.CacheRead,
					CacheWrite: tier.CacheWrite,
				},
				InputTokensAbove: int64(tier.InputTokensAbove),
			})
		}
	}
	contextWindow := int64(128000)
	if definition.ContextWindow != nil {
		contextWindow = int64(*definition.ContextWindow)
	}
	maxTokens := int64(16384)
	if definition.MaxTokens != nil {
		maxTokens = int64(*definition.MaxTokens)
	}
	compat, err := mergeCompat(compatToMapToCompat(providerConfig.Compat, ai.Api(api)), definition.Compat, ai.Api(api))
	if err != nil {
		return ai.Model{}, err
	}
	return ai.Model{
		ID:               definition.ID,
		Name:             name,
		API:              ai.Api(api),
		Provider:         ai.ProviderID(providerID),
		BaseURL:          baseURL,
		Reasoning:        reasoning,
		ThinkingLevelMap: thinkingLevelMapFromJSON(definition.ThinkingLevelMap),
		Input:            input,
		Cost:             cost,
		ContextWindow:    contextWindow,
		MaxTokens:        maxTokens,
		SamplingParams:   definition.SamplingParams,
		Compat:           compat,
	}, nil
}

// compatToMapToCompat decodes raw provider compat to a typed compat (nil-safe
// helper shared by modelFromJSON).
func compatToMapToCompat(raw json.RawMessage, api ai.Api) ai.ModelCompat {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	compat, err := decodeCompat(api, m)
	if err != nil {
		return nil
	}
	return compat
}

// applyModelsJSON overlays a models.json provider config onto base models
// (pi: applyModelsJson).
func applyModelsJSON(providerID string, baseModels []ai.Model, config *ModelsJsonProvider) ([]ai.Model, error) {
	if config == nil {
		return append([]ai.Model(nil), baseModels...), nil
	}
	if config.OAuth != "" && config.BaseURL == "" {
		return nil, fmt.Errorf("Provider %s: %q is required when %q is set.", providerID, "baseUrl", "oauth")
	}
	hasOverrides := len(config.ModelOverrides) > 0
	if len(config.Models) == 0 && config.BaseURL == "" && config.Headers == nil &&
		len(config.Compat) == 0 && !hasOverrides && config.APIKey == "" && config.OAuth == "" &&
		config.AuthHeader == nil {
		return nil, fmt.Errorf(
			"Provider %s: must specify %q, %q, %q, %q, or %q.",
			providerID, "baseUrl", "headers", "compat", "modelOverrides", "models")
	}

	models := make([]ai.Model, 0, len(baseModels))
	for _, model := range baseModels {
		copied := model
		if config.OAuth != "radius" {
			// pi: config.baseUrl ?? model.baseUrl.
			if config.BaseURL != "" {
				copied.BaseURL = config.BaseURL
			}
		}
		compat, err := mergeCompat(model.Compat, config.Compat, model.API)
		if err != nil {
			return nil, err
		}
		copied.Compat = compat
		models = append(models, copied)
	}
	for _, definition := range config.Models {
		existingIndex := slices.IndexFunc(models, func(model ai.Model) bool { return model.ID == definition.ID })
		var defaults *ai.Model
		if existingIndex >= 0 {
			defaults = &models[existingIndex]
		} else if len(models) > 0 {
			defaults = &models[0]
		}
		model, err := modelFromJSON(providerID, definition, config, defaults)
		if err != nil {
			return nil, err
		}
		if existingIndex >= 0 {
			models[existingIndex] = model
		} else {
			models = append(models, model)
		}
	}
	return models, nil
}

// applyExtension overlays an extension provider config onto models (pi:
// applyExtension).
func applyExtension(providerID string, models []ai.Model, config *ProviderConfigInput) ([]ai.Model, error) {
	if config == nil {
		return append([]ai.Model(nil), models...), nil
	}
	if config.Models == nil {
		if config.BaseURL != "" {
			out := make([]ai.Model, 0, len(models))
			for _, model := range models {
				copied := model
				copied.BaseURL = config.BaseURL
				out = append(out, copied)
			}
			return out, nil
		}
		return append([]ai.Model(nil), models...), nil
	}
	out := make([]ai.Model, 0, len(config.Models))
	for _, definition := range config.Models {
		var defaults *ai.Model
		for i := range models {
			if models[i].ID == definition.ID {
				defaults = &models[i]
				break
			}
		}
		if defaults == nil && len(models) > 0 {
			defaults = &models[0]
		}
		api := string(definition.API)
		if api == "" {
			api = string(config.API)
		}
		if api == "" && defaults != nil {
			api = string(defaults.API)
		}
		if api == "" {
			return nil, fmt.Errorf("Provider %s, model %s: no %q specified. Set at provider or model level.", providerID, definition.ID, "api")
		}
		baseURL := definition.BaseURL
		if baseURL == "" {
			baseURL = config.BaseURL
		}
		if baseURL == "" && defaults != nil {
			baseURL = defaults.BaseURL
		}
		if baseURL == "" {
			return nil, fmt.Errorf("Provider %s: %q is required when defining custom models.", providerID, "baseUrl")
		}
		out = append(out, ai.Model{
			ID:               definition.ID,
			Name:             definition.Name,
			API:              ai.Api(api),
			Provider:         ai.ProviderID(providerID),
			BaseURL:          baseURL,
			Reasoning:        definition.Reasoning,
			ThinkingLevelMap: definition.ThinkingLevelMap,
			Input:            definition.Input,
			Cost:             definition.Cost,
			ContextWindow:    definition.ContextWindow,
			MaxTokens:        definition.MaxTokens,
			SamplingParams:   definition.SamplingParams,
			Compat:           definition.Compat,
		})
	}
	return out, nil
}

// adaptOAuth adapts an extension OAuth config to the canonical auth surface
// (pi: adaptOAuth).
func adaptOAuth(config *ExtensionOAuthConfig) auth.OAuthAuth {
	return &adaptedExtensionOAuth{config: config}
}

type adaptedExtensionOAuth struct {
	config *ExtensionOAuthConfig
}

func (a *adaptedExtensionOAuth) Name() string         { return a.config.Name }
func (a *adaptedExtensionOAuth) IsSubscription() bool { return a.config.IsSubscription }
func (a *adaptedExtensionOAuth) LoginLabel() string   { return "" }

func (a *adaptedExtensionOAuth) Login(ctx context.Context, interaction auth.AuthInteraction) (*auth.OAuthCredential, error) {
	credential, err := a.config.Login(ai.OAuthLoginCallbacks{
		OnAuth: func(info ai.OAuthAuthInfo) {
			interaction.Notify(auth.AuthEvent{Type: "auth_url", URL: info.URL, Instructions: info.Instructions})
		},
		OnDeviceCode: func(info ai.OAuthDeviceCodeInfo) {
			event := auth.AuthEvent{Type: "device_code", UserCode: info.UserCode, VerificationURI: info.VerificationURI}
			if info.IntervalSeconds != nil {
				event.IntervalSeconds = lo.ToPtr(*info.IntervalSeconds)
			}
			if info.ExpiresInSeconds != nil {
				event.ExpiresInSeconds = lo.ToPtr(*info.ExpiresInSeconds)
			}
			interaction.Notify(event)
		},
		OnPrompt: func(prompt ai.OAuthPrompt) (string, error) {
			return interaction.Prompt(auth.AuthPrompt{Type: "text", Message: prompt.Message, Placeholder: prompt.Placeholder})
		},
		OnProgress: func(message string) {
			interaction.Notify(auth.AuthEvent{Type: "progress", Message: message})
		},
		OnManualCodeInput: func() (string, error) {
			return interaction.Prompt(auth.AuthPrompt{Type: "manual_code", Message: "Paste the authorization code"})
		},
		OnSelect: func(prompt ai.OAuthSelectPrompt) (string, error) {
			options := make([]auth.AuthPromptOption, 0, len(prompt.Options))
			for _, option := range prompt.Options {
				options = append(options, auth.AuthPromptOption{ID: option.ID, Label: option.Label})
			}
			return interaction.Prompt(auth.AuthPrompt{Type: "select", Message: prompt.Message, Options: options})
		},
	})
	if err != nil {
		return nil, err
	}
	return &auth.OAuthCredential{OAuthCredentials: credential}, nil
}

func (a *adaptedExtensionOAuth) Refresh(ctx context.Context, credential *auth.OAuthCredential) (*auth.OAuthCredential, error) {
	refreshed, err := a.config.RefreshToken(ctx, credential.OAuthCredentials)
	if err != nil {
		return nil, err
	}
	return &auth.OAuthCredential{OAuthCredentials: refreshed}, nil
}

func (a *adaptedExtensionOAuth) ToAuth(credential *auth.OAuthCredential) (*auth.ModelAuth, error) {
	return &auth.ModelAuth{APIKey: a.config.GetAPIKey(credential.OAuthCredentials)}, nil
}

// withConfiguredAuth merges configured headers into auth and optionally sets
// the Authorization bearer header (pi: withConfiguredAuth).
func withConfiguredAuth(authResult auth.ModelAuth, headers map[string]string, authHeader bool) (auth.ModelAuth, error) {
	var mergedHeaders types.ProviderHeaders
	if authResult.Headers != nil || headers != nil {
		mergedHeaders = types.ProviderHeaders{}
		for name, value := range authResult.Headers {
			mergedHeaders[name] = value
		}
		for name, value := range headers {
			mergedHeaders[name] = lo.ToPtr(value)
		}
	}
	if authHeader {
		if authResult.APIKey == "" {
			return auth.ModelAuth{}, fmt.Errorf("authHeader requires a resolved API key")
		}
		if mergedHeaders == nil {
			mergedHeaders = types.ProviderHeaders{}
		}
		mergedHeaders["Authorization"] = lo.ToPtr("Bearer " + authResult.APIKey)
	}
	authResult.Headers = mergedHeaders
	return authResult, nil
}

// configuredApiKey returns the extension or models.json api key (pi:
// configuredApiKey).
func configuredApiKey(config *ModelsJsonProvider, extension *ProviderConfigInput) string {
	if extension != nil && extension.APIKey != "" {
		return extension.APIKey
	}
	if config != nil {
		return config.APIKey
	}
	return ""
}

// configuredHeaders merges the configured provider headers (pi:
// configuredHeaders).
func configuredHeaders(config *ModelsJsonProvider, extension *ProviderConfigInput) map[string]string {
	if (config == nil || config.Headers == nil) && (extension == nil || extension.Headers == nil) {
		return nil
	}
	merged := map[string]string{}
	if config != nil {
		for key, value := range config.Headers {
			merged[key] = value
		}
	}
	if extension != nil {
		for key, value := range extension.Headers {
			merged[key] = value
		}
	}
	return merged
}

// configContextEnv resolves the env-var names referenced by config values via
// the auth context (pi: configContextEnv).
func configContextEnv(values []string, ctx auth.AuthContext, explicit map[string]string) map[string]string {
	env := map[string]string{}
	for key, value := range explicit {
		env[key] = value
	}
	seen := map[string]bool{}
	for _, value := range values {
		for _, name := range GetConfigValueEnvVarNames(value) {
			if seen[name] {
				continue
			}
			seen[name] = true
			if _, present := env[name]; present {
				continue
			}
			if resolved, ok := ctx.Env(name); ok {
				env[name] = resolved
			}
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

// composeAPIKeyAuth composes the api-key auth across layers (pi:
// composeApiKeyAuth). Returns nil when the provider is OAuth-only.
func composeAPIKeyAuth(providerID string, base ai.Provider, config *ModelsJsonProvider, extension *ProviderConfigInput) auth.ApiKeyAuth {
	var inherited auth.ApiKeyAuth
	if base != nil {
		inherited = base.Auth().APIKey
	}
	rawKey := configuredApiKey(config, extension)
	// pi: extension?.oauth ?? base?.auth.oauth — presence check only here.
	oauthPresent := (extension != nil && extension.OAuth != nil) ||
		(base != nil && base.Auth().OAuth != nil)
	// OAuth-only providers get no fabricated API-key login method.
	if inherited == nil && rawKey == "" && oauthPresent {
		return nil
	}
	rawHeaders := configuredHeaders(config, extension)
	var authHeader bool
	if extension != nil && extension.AuthHeader != nil {
		authHeader = *extension.AuthHeader
	} else if config != nil && config.AuthHeader != nil {
		authHeader = *config.AuthHeader
	}

	name := "API key"
	if inherited != nil {
		name = inherited.Name()
	}

	return &composedAPIKeyAuth{
		providerID: providerID,
		base:       base,
		inherited:  inherited,
		rawKey:     rawKey,
		rawHeaders: rawHeaders,
		authHeader: authHeader,
		name:       name,
	}
}

type composedAPIKeyAuth struct {
	providerID string
	base       ai.Provider
	inherited  auth.ApiKeyAuth
	rawKey     string
	rawHeaders map[string]string
	authHeader bool
	name       string
}

func (a *composedAPIKeyAuth) Name() string { return a.name }

// Login is the optional capability (pi: composed login member). It prompts
// for a key when no inherited login exists.
func (a *composedAPIKeyAuth) Login(ctx context.Context, interaction auth.AuthInteraction) (*auth.ApiKeyCredential, error) {
	if a.inherited != nil {
		if login, ok := a.inherited.(auth.ApiKeyAuthLogin); ok {
			return login.Login(ctx, interaction)
		}
	}
	key, err := interaction.Prompt(auth.AuthPrompt{Type: "secret", Message: "Enter API key"})
	if err != nil {
		return nil, err
	}
	return &auth.ApiKeyCredential{Key: key}, nil
}

func (a *composedAPIKeyAuth) Check(input auth.ApiKeyAuthInput) (*auth.AuthCheck, error) {
	if input.Credential != nil {
		if a.inherited != nil {
			if checker, ok := a.inherited.(auth.ApiKeyAuthChecker); ok {
				return checker.Check(input)
			}
		}
		if input.Credential.Key != "" {
			return &auth.AuthCheck{Type: auth.AuthTypeAPIKey, Source: "stored credential"}, nil
		}
		if a.inherited != nil {
			if resolved, err := a.inherited.Resolve(input); err == nil && resolved != nil {
				return &auth.AuthCheck{Type: auth.AuthTypeAPIKey, Source: resolved.Source}, nil
			}
		}
		return nil, nil
	}
	if a.rawKey != "" {
		if IsCommandConfigValue(a.rawKey) {
			return &auth.AuthCheck{Type: auth.AuthTypeAPIKey, Source: "configured API key"}, nil
		}
		for _, name := range GetConfigValueEnvVarNames(a.rawKey) {
			if _, ok := input.Ctx.Env(name); !ok {
				return nil, nil
			}
		}
		return &auth.AuthCheck{Type: auth.AuthTypeAPIKey, Source: "configured API key"}, nil
	}
	if a.inherited != nil {
		if checker, ok := a.inherited.(auth.ApiKeyAuthChecker); ok {
			return checker.Check(input)
		}
	}
	if a.inherited != nil {
		if resolved, err := a.inherited.Resolve(input); err == nil && resolved != nil {
			return &auth.AuthCheck{Type: auth.AuthTypeAPIKey, Source: resolved.Source}, nil
		}
	}
	return nil, nil
}

func (a *composedAPIKeyAuth) Resolve(input auth.ApiKeyAuthInput) (*auth.AuthResult, error) {
	var result *auth.AuthResult
	if input.Credential != nil {
		if a.inherited != nil {
			resolved, err := a.inherited.Resolve(input)
			if err != nil {
				return nil, err
			}
			result = resolved
		} else if input.Credential.Key != "" {
			env := types.ProviderEnv{}
			for key, value := range input.Credential.Env {
				env[key] = value
			}
			result = &auth.AuthResult{
				Auth:   auth.ModelAuth{APIKey: input.Credential.Key},
				Env:    env,
				Source: "stored credential",
			}
		}
	} else if a.rawKey != "" {
		env := configContextEnv([]string{a.rawKey}, input.Ctx, nil)
		key, err := ResolveConfigValueOrThrow(a.rawKey, fmt.Sprintf("API key for provider %q", a.providerID), env)
		if err != nil {
			return nil, err
		}
		if a.inherited != nil {
			credentialInput := input
			credentialInput.Credential = &auth.ApiKeyCredential{Key: key}
			resolved, err := a.inherited.Resolve(credentialInput)
			if err != nil {
				return nil, err
			}
			result = resolved
		} else {
			result = &auth.AuthResult{Auth: auth.ModelAuth{APIKey: key}, Source: "configured API key"}
		}
	} else if a.inherited != nil {
		resolved, err := a.inherited.Resolve(input)
		if err != nil {
			return nil, err
		}
		result = resolved
	}
	if result == nil {
		return nil, nil
	}
	explicitEnv := map[string]string{}
	if input.Credential != nil {
		for key, value := range input.Credential.Env {
			explicitEnv[key] = value
		}
	}
	for key, value := range result.Env {
		explicitEnv[key] = value
	}
	headerEnv := configContextEnv(headerValues(a.rawHeaders), input.Ctx, explicitEnv)
	headers, err := ResolveHeadersOrThrow(a.rawHeaders, fmt.Sprintf("provider %q", a.providerID), headerEnv)
	if err != nil {
		return nil, err
	}
	merged, err := withConfiguredAuth(result.Auth, headers, a.authHeader)
	if err != nil {
		return nil, err
	}
	result.Auth = merged
	return result, nil
}

func headerValues(headers map[string]string) []string {
	values := make([]string, 0, len(headers))
	for _, value := range headers {
		values = append(values, value)
	}
	return values
}

// composeOAuthAuth composes the OAuth auth across layers (pi:
// composeOAuthAuth). Returns nil when no OAuth layer exists.
func composeOAuthAuth(providerID string, base ai.Provider, config *ModelsJsonProvider, extension *ProviderConfigInput) auth.OAuthAuth {
	var oauth auth.OAuthAuth
	if extension != nil && extension.OAuth != nil {
		oauth = adaptOAuth(extension.OAuth)
	} else if base != nil {
		oauth = base.Auth().OAuth
	}
	if oauth == nil {
		return nil
	}
	rawHeaders := configuredHeaders(config, extension)
	var authHeader bool
	if extension != nil && extension.AuthHeader != nil {
		authHeader = *extension.AuthHeader
	} else if config != nil && config.AuthHeader != nil {
		authHeader = *config.AuthHeader
	}
	return &composedOAuthAuth{
		providerID: providerID,
		oauth:      oauth,
		rawHeaders: rawHeaders,
		authHeader: authHeader,
	}
}

type composedOAuthAuth struct {
	providerID string
	oauth      auth.OAuthAuth
	rawHeaders map[string]string
	authHeader bool
}

func (o *composedOAuthAuth) Name() string         { return o.oauth.Name() }
func (o *composedOAuthAuth) IsSubscription() bool { return o.oauth.IsSubscription() }
func (o *composedOAuthAuth) LoginLabel() string   { return o.oauth.LoginLabel() }

func (o *composedOAuthAuth) Login(ctx context.Context, interaction auth.AuthInteraction) (*auth.OAuthCredential, error) {
	return o.oauth.Login(ctx, interaction)
}

func (o *composedOAuthAuth) Refresh(ctx context.Context, credential *auth.OAuthCredential) (*auth.OAuthCredential, error) {
	return o.oauth.Refresh(ctx, credential)
}

func (o *composedOAuthAuth) ToAuth(credential *auth.OAuthCredential) (*auth.ModelAuth, error) {
	authResult, err := o.oauth.ToAuth(credential)
	if err != nil {
		return nil, err
	}
	// pi: credential.env — an extra field on the legacy credential shape.
	var env map[string]string
	if rawEnv, ok := credential.Extra["env"].(map[string]any); ok {
		env = map[string]string{}
		for key, value := range rawEnv {
			if s, ok := value.(string); ok {
				env[key] = s
			}
		}
	}
	headers, err := ResolveHeadersOrThrow(o.rawHeaders, fmt.Sprintf("provider %q", o.providerID), env)
	if err != nil {
		return nil, err
	}
	merged, err := withConfiguredAuth(*authResult, headers, o.authHeader)
	if err != nil {
		return nil, err
	}
	return &merged, nil
}

// rawModelHeaders collects the per-model configured headers across layers
// (pi: rawModelHeaders).
func rawModelHeaders(model ai.Model, config *ModelsJsonProvider, extension *ProviderConfigInput) map[string]string {
	headers := map[string]string{}
	if config != nil {
		if override, ok := config.ModelOverrides[model.ID]; ok && override.Headers != nil {
			for key, value := range override.Headers {
				headers[key] = value
			}
		}
		if config.Models != nil {
			for _, entry := range config.Models {
				if entry.ID == model.ID && entry.Headers != nil {
					for key, value := range entry.Headers {
						headers[key] = value
					}
				}
			}
		}
	}
	if extension != nil {
		for _, entry := range extension.Models {
			if entry.ID == model.ID && entry.Headers != nil {
				for key, value := range entry.Headers {
					headers[key] = value
				}
			}
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

// ValidateExtensionProvider validates an extension registration on its own
// (pi: validateExtensionProvider).
func ValidateExtensionProvider(providerID string, base ai.Provider, modelsConfig *ModelsJsonProvider, extension *ProviderConfigInput) error {
	if extension.StreamSimple != nil && extension.API == "" {
		return fmt.Errorf("Provider %s: %q is required when registering streamSimple.", providerID, "api")
	}
	var baseModels []ai.Model
	if base != nil {
		baseModels = base.GetModels()
	}
	models, err := applyModelsJSON(providerID, baseModels, modelsConfig)
	if err != nil {
		return err
	}
	_, err = applyExtension(providerID, models, extension)
	return err
}

// ComposeModelProvider composes built-in, models.json, and extension layers
// without reading credentials (pi: composeModelProvider).
func ComposeModelProvider(providerID string, base ai.Provider, modelConfig *ModelConfig, extension *ProviderConfigInput) (ai.Provider, error) {
	var config *ModelsJsonProvider
	if modelConfig != nil {
		config = modelConfig.GetProvider(providerID)
	}

	provider := &composedProviderState{
		providerID: providerID,
		base:       base,
		config:     config,
		extension:  extension,
	}

	// Validate eagerly so registration/reload reports structural errors
	// immediately (pi: getModels() call).
	if _, err := provider.computeModels(); err != nil {
		return nil, err
	}

	apiKey := composeAPIKeyAuth(providerID, base, config, extension)
	oauth := composeOAuthAuth(providerID, base, config, extension)
	authMethods := auth.ProviderAuth{}
	if apiKey != nil {
		authMethods.APIKey = apiKey
	}
	if oauth != nil {
		authMethods.OAuth = oauth
	}
	if authMethods.APIKey == nil && authMethods.OAuth == nil {
		return nil, fmt.Errorf("Provider %s: no authentication method configured.", providerID)
	}

	name := providerID
	if extension != nil && extension.Name != "" {
		name = extension.Name
	} else if config != nil && config.Name != "" {
		name = config.Name
	} else if base != nil {
		name = base.Name()
	} else if extension != nil && extension.OAuth != nil {
		name = extension.OAuth.Name
	}
	baseURL := ""
	if extension != nil && extension.BaseURL != "" {
		baseURL = extension.BaseURL
	} else if config != nil && config.BaseURL != "" {
		baseURL = config.BaseURL
	} else if base != nil {
		baseURL = base.BaseURL()
	}

	provider.name = name
	provider.baseURL = baseURL
	provider.auth = authMethods

	var headers types.ProviderHeaders
	if base != nil {
		headers = base.Headers()
	}
	provider.headers = headers

	var refreshable bool
	if base != nil {
		if _, ok := base.(interface {
			RefreshModels(ctx context.Context, refreshCtx *ai.RefreshModelsContext) error
		}); ok {
			refreshable = true
		}
	}
	if extension != nil && extension.RefreshModels != nil {
		refreshable = true
	}
	if extension != nil && extension.OAuth != nil && extension.OAuth.ModifyModels != nil {
		refreshable = true
	}
	provider.refreshable = refreshable

	// pi attaches fetchDeferred/cancelDeferred to the composed provider only
	// when the base provider has them; Go expresses conditional members with
	// a wrapper type.
	var deferred ai.ProviderDeferredStreamer
	if base != nil {
		if d, ok := base.(ai.ProviderDeferredStreamer); ok {
			deferred = d
		}
	}
	if deferred != nil {
		return &composedProviderDeferred{composedProviderState: provider, deferred: deferred}, nil
	}
	return provider, nil
}

// composedProviderDeferred adds the deferred-response capability when the
// base provider has it (pi: conditional fetchDeferred/cancelDeferred).
type composedProviderDeferred struct {
	*composedProviderState
	deferred ai.ProviderDeferredStreamer
}

func (p *composedProviderDeferred) FetchDeferred(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, options *ai.DeferredFetchOptions) *ai.AssistantMessageEventStream {
	return ai.LazyStream(model, func() (*ai.AssistantMessageEventStream, error) {
		return p.deferred.FetchDeferred(ctx, model, handle, options), nil
	})
}

func (p *composedProviderDeferred) CancelDeferred(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, options *ai.DeferredCancelOptions) error {
	return p.deferred.CancelDeferred(ctx, model, handle, options)
}

// composedProviderState is the composed provider (pi: the provider literal
// closure in composeModelProvider).
type composedProviderState struct {
	providerID string
	base       ai.Provider
	config     *ModelsJsonProvider
	extension  *ProviderConfigInput

	mu                       sync.Mutex
	extensionOAuthCredential *auth.OAuthCredential
	refreshedExtensionModels []ProviderConfigInputModel

	name        string
	baseURL     string
	headers     types.ProviderHeaders
	auth        auth.ProviderAuth
	refreshable bool
}

var _ ai.Provider = (*composedProviderState)(nil)

var _ interface {
	RefreshModels(ctx context.Context, refreshCtx *ai.RefreshModelsContext) error
} = (*composedProviderState)(nil)

func (p *composedProviderState) ID() string                     { return p.providerID }
func (p *composedProviderState) Name() string                   { return p.name }
func (p *composedProviderState) BaseURL() string                { return p.baseURL }
func (p *composedProviderState) Headers() types.ProviderHeaders { return p.headers }
func (p *composedProviderState) Auth() auth.ProviderAuth        { return p.auth }

func (p *composedProviderState) currentExtension() *ProviderConfigInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.extension != nil && p.refreshedExtensionModels != nil {
		effective := *p.extension
		effective.Models = p.refreshedExtensionModels
		return &effective
	}
	return p.extension
}

func (p *composedProviderState) oauthCredential() *auth.OAuthCredential {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.extensionOAuthCredential
}

// computeModels builds the synchronous model list (pi: getModels closure).
func (p *composedProviderState) computeModels() ([]ai.Model, error) {
	var baseModels []ai.Model
	if p.base != nil {
		baseModels = p.base.GetModels()
	}
	models, err := applyModelsJSON(p.providerID, baseModels, p.config)
	if err != nil {
		return nil, err
	}
	models, err = applyExtension(p.providerID, models, p.currentExtension())
	if err != nil {
		return nil, err
	}
	extension := p.extension
	if credential := p.oauthCredential(); credential != nil && extension != nil && extension.OAuth != nil && extension.OAuth.ModifyModels != nil {
		models = extension.OAuth.ModifyModels(models, credential.OAuthCredentials)
	}
	// models.json modelOverrides are the topmost user-config layer: they
	// apply once, after custom-model upserts, extension model replacement,
	// and legacy OAuth projection.
	if p.config != nil && len(p.config.ModelOverrides) > 0 {
		for i := range models {
			if override, ok := p.config.ModelOverrides[models[i].ID]; ok {
				models[i] = applyModelOverride(models[i], override)
			}
		}
	}
	return models, nil
}

func (p *composedProviderState) GetModels() []ai.Model {
	models, err := p.computeModels()
	if err != nil {
		return nil
	}
	return models
}

func (p *composedProviderState) supportsBaseAPI(model ai.Model) bool {
	if p.base == nil {
		return false
	}
	for _, entry := range p.base.GetModels() {
		if entry.API == model.API {
			return true
		}
	}
	return false
}

// streamWith dispatches to the extension, the base provider, or the api
// registry (pi: streamWith). pi casts one options object between
// StreamOptions and SimpleStreamOptions; pig carries both so each callee
// receives its own shape.
func (p *composedProviderState) streamWith(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.StreamOptions, simple bool, simpleOptions *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	return ai.LazyStream(model, func() (*ai.AssistantMessageEventStream, error) {
		extension := p.currentExtension()
		if extension != nil && extension.StreamSimple != nil && model.API == extension.API {
			if simple && simpleOptions != nil {
				return extension.StreamSimple(ctx, model, llmContext, simpleOptions), nil
			}
			return extension.StreamSimple(ctx, model, llmContext, &ai.SimpleStreamOptions{StreamOptions: *options}), nil
		}
		if p.base != nil && p.supportsBaseAPI(*model) {
			if simple {
				if simpleOptions != nil {
					return p.base.StreamSimple(ctx, model, llmContext, simpleOptions), nil
				}
				return p.base.StreamSimple(ctx, model, llmContext, &ai.SimpleStreamOptions{StreamOptions: *options}), nil
			}
			return p.base.Stream(ctx, model, llmContext, options), nil
		}
		apiStreams, ok := ai.GetApiProvider(model.API)
		if !ok {
			return nil, fmt.Errorf("No API provider registered for api: %s", model.API)
		}
		if simple {
			if simpleOptions != nil {
				return apiStreams.StreamSimple(ctx, model, llmContext, simpleOptions), nil
			}
			return apiStreams.StreamSimple(ctx, model, llmContext, &ai.SimpleStreamOptions{StreamOptions: *options}), nil
		}
		return apiStreams.Stream(ctx, model, llmContext, options), nil
	})
}

func (p *composedProviderState) Stream(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	return p.streamWith(ctx, model, llmContext, options, false, nil)
}

func (p *composedProviderState) StreamSimple(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	streamOptions := &ai.StreamOptions{}
	if options != nil {
		streamOptions = &options.StreamOptions
	}
	return p.streamWith(ctx, model, llmContext, streamOptions, true, options)
}

// RefreshModels implements the dynamic-provider capability (pi:
// refreshModels member).
func (p *composedProviderState) RefreshModels(ctx context.Context, refreshCtx *ai.RefreshModelsContext) error {
	if !p.refreshable {
		return nil
	}
	if base, ok := p.base.(interface {
		RefreshModels(ctx context.Context, refreshCtx *ai.RefreshModelsContext) error
	}); ok && p.base != nil {
		if err := base.RefreshModels(ctx, refreshCtx); err != nil {
			return err
		}
	}
	extension := p.extension
	var refreshed []ProviderConfigInputModel
	if extension != nil && extension.RefreshModels != nil {
		models, err := extension.RefreshModels(ctx, refreshCtx)
		if err != nil {
			return err
		}
		refreshed = models
	}
	if refreshCtx.Context.Err() != nil {
		return nil
	}
	var oauthCredential *auth.OAuthCredential
	if credential, ok := refreshCtx.Credential.(*auth.OAuthCredential); ok {
		oauthCredential = credential
	}
	_, err := refreshCtx.Publish(ai.ModelsPublication{
		Update: func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if refreshed != nil {
				p.refreshedExtensionModels = refreshed
			}
			p.extensionOAuthCredential = oauthCredential
		},
	})
	return err
}

// ResolveConfiguredModelHeaders resolves the per-model configured headers (pi:
// resolveConfiguredModelHeaders).
func ResolveConfiguredModelHeaders(model ai.Model, config *ModelsJsonProvider, extension *ProviderConfigInput, env map[string]string) (map[string]string, error) {
	return ResolveHeadersOrThrow(
		rawModelHeaders(model, config, extension),
		fmt.Sprintf("model %q", string(model.Provider)+"/"+model.ID),
		env,
	)
}

// CompatibilityRequestConfig is the request config used when provider auth is
// unconfigured (pi: CompatibilityRequestConfig).
type CompatibilityRequestConfig struct {
	Headers    types.ProviderHeaders
	AuthHeader bool
}

// ResolveCompatibilityRequestConfig resolves the compatibility request config
// (pi: resolveCompatibilityRequestConfig).
func ResolveCompatibilityRequestConfig(model ai.Model, config *ModelsJsonProvider, extension *ProviderConfigInput) (CompatibilityRequestConfig, error) {
	configured := map[string]string{}
	for key, value := range configuredHeaders(config, extension) {
		configured[key] = value
	}
	for key, value := range rawModelHeaders(model, config, extension) {
		configured[key] = value
	}
	resolved, err := ResolveHeadersOrThrow(configured, fmt.Sprintf("model %q", string(model.Provider)+"/"+model.ID), nil)
	if err != nil {
		return CompatibilityRequestConfig{}, err
	}
	var headers types.ProviderHeaders
	if len(model.Headers) > 0 || len(resolved) > 0 {
		headers = types.ProviderHeaders{}
		for key, value := range model.Headers {
			headers[key] = lo.ToPtr(value)
		}
		for key, value := range resolved {
			headers[key] = lo.ToPtr(value)
		}
	}
	var authHeader bool
	if extension != nil && extension.AuthHeader != nil {
		authHeader = *extension.AuthHeader
	} else if config != nil && config.AuthHeader != nil {
		authHeader = *config.AuthHeader
	}
	return CompatibilityRequestConfig{Headers: headers, AuthHeader: authHeader}, nil
}

// ConfiguredRequestAuthStatus reports the configured-key auth status without
// touching credentials (pi: configuredRequestAuthStatus).
func ConfiguredRequestAuthStatus(config *ModelsJsonProvider, extension *ProviderConfigInput) *AuthStatus {
	value := configuredApiKey(config, extension)
	if value == "" {
		return nil
	}
	if IsCommandConfigValue(value) {
		return &AuthStatus{Configured: true, Source: "models_json_command"}
	}
	names := GetConfigValueEnvVarNames(value)
	if len(names) > 0 {
		if IsConfigValueConfigured(value, nil) {
			return &AuthStatus{Configured: true, Source: "environment", Label: strings.Join(names, ", ")}
		}
		return &AuthStatus{Configured: false}
	}
	source := "models_json_key"
	if extension != nil && extension.APIKey != "" {
		source = "fallback"
	}
	return &AuthStatus{Configured: true, Source: source}
}
