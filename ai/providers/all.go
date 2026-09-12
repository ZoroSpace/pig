// pi: providers/all.ts.
package providers

import (
	"sort"

	"github.com/ZoroSpace/pig/ai"
)

// BuiltinProvider is a provider id present in the generated catalog (pi:
// keyof typeof MODELS; pi's comment carries over: KnownProvider additionally
// includes purely dynamic providers, e.g. "radius", that have no static
// catalog entry). Go has no keyof: the closed TS union collapses into the
// open ProviderID string type.
type BuiltinProvider = ai.ProviderID

// GetBuiltinModel reads one model from the generated built-in catalog (pi:
// getBuiltinModel). pi's generic BuiltinModelApi<TProvider, TModelId>
// static-precision typing has no Go counterpart — every API shares one
// Model shape (pi: models.ts).
func GetBuiltinModel(provider BuiltinProvider, modelID string) (ai.Model, bool) {
	models, ok := ai.MODELS[provider]
	if !ok {
		return ai.Model{}, false
	}
	model, ok := models[modelID]
	return model, ok
}

// GetBuiltinProviders lists the ids present in the generated catalog (pi:
// getBuiltinProviders, Object.keys(MODELS)). Map iteration order is random
// in Go while pi's Object.keys follows generated insertion order; the
// generator emits ids alphabetically, so pig sorts to keep the list
// deterministic.
func GetBuiltinProviders() []BuiltinProvider {
	ids := make([]BuiltinProvider, 0, len(ai.MODELS))
	for id := range ai.MODELS {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// GetBuiltinModelDataGeneratedAt returns the generation timestamp shared by
// all built-in provider catalogs (pi: getBuiltinModelDataGeneratedAt); 0
// when unknown (pi: undefined).
func GetBuiltinModelDataGeneratedAt() int64 {
	return ai.MODELSGeneratedAt
}

// GetBuiltinModels lists one provider's catalog models (pi:
// getBuiltinModels, Object.values(MODELS[provider])). Sorted by model id for
// the same determinism reason as GetBuiltinProviders.
func GetBuiltinModels(provider BuiltinProvider) []ai.Model {
	models, ok := ai.MODELS[provider]
	if !ok {
		return nil
	}
	out := make([]ai.Model, 0, len(models))
	for _, model := range models {
		out = append(out, model)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// BuiltinProviders returns all built-in providers, freshly constructed (pi:
// builtinProviders), in pi's alphabetical position order. The remaining
// factories arrive with their adapters (README Phase 2):
// amazonBedrockProvider, azureOpenAIResponsesProvider,
// cloudflareAIGatewayProvider, cloudflareWorkersAIProvider,
// githubCopilotProvider, googleProvider, googleVertexProvider,
// mistralProvider, opencodeProvider,
// opencodeGoProvider, qwenTokenPlanProvider, qwenTokenPlanCnProvider,
// qwenTokenPlanIndividualProvider, radiusProvider,
// xiaomiProvider, xiaomiTokenPlanAmsProvider, xiaomiTokenPlanCnProvider,
// xiaomiTokenPlanSgpProvider.
func BuiltinProviders() []ai.Provider {
	return []ai.Provider{
		AntLingProvider(),
		AnthropicProvider(),
		BasetenProvider(),
		CerebrasProvider(),
		DeepSeekProvider(),
		FireworksProvider(),
		GroqProvider(),
		HuggingFaceProvider(),
		KimiCodingProvider(),
		MinimaxProvider(),
		MinimaxCNProvider(),
		MoonshotAIProvider(),
		MoonshotAICNProvider(),
		NVIDIAProvider(),
		OpenAIProvider(),
		OpenAICodexProvider(),
		OpenRouterProvider(),
		TogetherProvider(),
		VercelAIGatewayProvider(),
		VolcenginePlanProvider(),
		XaiProvider(),
		ZaiProvider(),
		ZaiCodingCNProvider(),
	}
}

// BuiltinModels returns a Models collection with every built-in provider
// registered (pi: builtinModels).
func BuiltinModels(options *ai.CreateModelsOptions) ai.MutableModels {
	models := ai.NewModels(options)
	for _, provider := range BuiltinProviders() {
		models.SetProvider(provider)
	}
	return models
}

// builtinImagesProviders / builtinImagesModels (pi: all.ts tail) are missing
// here by design: they need ImagesProvider/createImagesModels (pi:
// images-models.ts), the openrouter-images adapter, and the generated image
// model catalog (pi: image-models.generated.ts); they arrive with the images
// chain.
