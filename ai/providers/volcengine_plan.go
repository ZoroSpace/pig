// Volcengine Coding Plan provider — not a pi builtin. Ported from the
// pi-provider-volcengine-codingplan extension
// (github.com/buwalle/pi-provider-volcengine-codingplan, extensions/index.ts
// + extensions/fallback-models.ts) into a pig built-in (user decision,
// 2026-09-10). Two deliberate deltas vs the extension:
//   - The extension's startup registry fetch (jsDelivr/GitHub model list,
//     VOLCENGINE_PLAN_REGISTRY_URL override) is not ported: builtin models
//     ship with the binary; new Ark models go through the user's models.json
//     (pig's standard custom-model escape hatch).
//   - The extension's message_end hook normalizing ark overflow errors is
//     replaced by two patterns in ai/utils/overflow.go, which is where
//     IsContextOverflow consumers (auto-compaction retry) read them.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

// VolcenginePlanProvider is the built-in Volcengine Coding Plan provider
// (extension: registerProvider("volcengine-plan", ...)). The Coding Plan
// endpoint (…/api/coding/v3) is intentional — it differs from the plain Ark
// base-model endpoint in call path and billing; do not "normalize" it.
func VolcenginePlanProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "volcengine-plan",
		Name:    "Volcengine Coding Plan",
		BaseURL: "https://ark.cn-beijing.volces.com/api/coding/v3",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Volcengine API key", []string{"VOLCENGINE_API_KEY"}),
		},
		Models: volcenginePlanModels(),
		// api: openAICompletionsApi() (pi: openAICompletionsApi(), lazy-loaded;
		// Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}

// volcenginePlanModels is the hand-written model table (extension:
// FALLBACK_MODELS, generated from registry/models.json by `arkcli plans
// model-list --plan coding-plan` + `arkcli models get`; last sync
// 2026-09-01, registry version "2026-09-01"). Model order follows the
// registry. All rates are 0: Coding Plan usage is subscription quota.
// This is pig's first built-in provider with hand-written models outside
// models_generated.go — the Coding Plan set is not in the models.dev catalog.
func volcenginePlanModels() []ai.Model {
	compat := &types.OpenAICompletionsCompat{
		SupportsDeveloperRole: lo.ToPtr(false),
		MaxTokensField:        lo.ToPtr(types.MaxTokensPlain),
		// Ark ignores reasoning_effort (and it suppresses GLM thinking), so
		// thinking is controlled via the volcengine format's thinking param.
		SupportsReasoningEffort: lo.ToPtr(false),
		ThinkingFormat:          lo.ToPtr(types.ThinkingFormatVolcengine),
	}
	// Models that reject thinking.type "disabled" with HTTP 400 (probed live
	// 2026-09-10): glm-5-3-flash, glm-5.3, kimi-k2.7-code. off is marked nil
	// so the volcengine format omits the thinking param instead (the hybrid
	// model then applies its own default).
	offUnsupported := types.ThinkingLevelMap{
		types.ModelThinkingOff: nil,
	}
	baseURL := "https://ark.cn-beijing.volces.com/api/coding/v3"
	newModel := func(id string, name string, input []types.ModelInput, contextWindow int64, maxTokens int64, thinkingLevelMap types.ThinkingLevelMap) ai.Model {
		return ai.Model{
			ID:               id,
			Name:             name,
			API:              types.ApiOpenAICompletions,
			Provider:         types.ProviderVolcenginePlan,
			BaseURL:          baseURL,
			Reasoning:        true,
			ThinkingLevelMap: thinkingLevelMap,
			Input:            input,
			Cost:             types.ModelCost{},
			ContextWindow:    contextWindow,
			MaxTokens:        maxTokens,
			Compat:           compat,
		}
	}
	textImage := []types.ModelInput{types.ModelInputText, types.ModelInputImage}
	textOnly := []types.ModelInput{types.ModelInputText}
	return []ai.Model{
		newModel("doubao-seed-evolving", "doubao-seed-evolving", textImage, 1024000, 256000, nil),
		newModel("doubao-seed-2-1-turbo", "doubao-seed-2-1-turbo", textImage, 256000, 256000, nil),
		newModel("doubao-seed-2.0-lite", "doubao-seed-2.0-lite", textImage, 256000, 128000, nil),
		// Registry quirk kept faithfully: id glm-5-3-flash, display name
		// glm-5.3-flash (id/name differ in registry/models.json too).
		newModel("glm-5-3-flash", "glm-5.3-flash", textImage, 1048576, 128000, offUnsupported),
		newModel("glm-5.3", "glm-5.3", textOnly, 1048576, 128000, offUnsupported),
		newModel("deepseek-v4-pro", "deepseek-v4-pro", textOnly, 1024000, 384000, nil),
		newModel("deepseek-v4-flash", "deepseek-v4-flash", textOnly, 1024000, 384000, nil),
		newModel("kimi-k2.7-code", "kimi-k2.7-code", textImage, 256000, 32000, offUnsupported),
		newModel("minimax-m3", "minimax-m3", textImage, 512000, 128000, nil),
	}
}
