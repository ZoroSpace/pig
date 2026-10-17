// pi: test/xiaomi-models.test.ts — deprecated MiMo models are omitted and the
// replacement v2.5 models are present across the four xiaomi providers.
package providers

import (
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

func xiaomiProviders() []types.ProviderID {
	return []types.ProviderID{
		types.ProviderXiaomi,
		types.ProviderXiaomiTokenPlanCN,
		types.ProviderXiaomiTokenPlanAMS,
		types.ProviderXiaomiTokenPlanSGP,
	}
}

func TestXiaomiMiMoCatalogReplacement(t *testing.T) {
	deprecatedModelIDs := []string{"mimo-v2-flash", "mimo-v2-omni", "mimo-v2-pro"}
	replacementModelIDs := []string{"mimo-v2.5", "mimo-v2.5-pro"}

	for _, provider := range xiaomiProviders() {
		models := GetBuiltinModels(provider)
		if len(models) == 0 {
			t.Fatalf("%s has no catalog models", provider)
		}
		ids := make(map[string]bool, len(models))
		for _, model := range models {
			ids[model.ID] = true
		}
		for _, modelID := range deprecatedModelIDs {
			if ids[modelID] {
				t.Errorf("%s omits deprecated model %s", provider, modelID)
			}
		}
		for _, modelID := range replacementModelIDs {
			if !ids[modelID] {
				t.Errorf("%s keeps replacement model %s", provider, modelID)
			}
		}
	}
}

// TestXiaomiMimoThinkingToggleLevels is pi #10818: MiMo Chat Completions only
// supports a thinking on/off toggle.
func TestXiaomiMimoThinkingToggleLevels(t *testing.T) {
	for _, provider := range xiaomiProviders() {
		models := GetBuiltinModels(provider)
		reasoningCount := 0
		for _, model := range models {
			if !model.Reasoning {
				continue
			}
			reasoningCount++
			model := model
			var levels []string
			for _, level := range ai.GetSupportedThinkingLevels(&model) {
				levels = append(levels, string(level))
			}
			if len(levels) != 2 || levels[0] != "off" || levels[1] != "high" {
				t.Errorf("%s/%s supported levels = %v, want [off high]", provider, model.ID, levels)
			}
		}
		if reasoningCount == 0 {
			t.Errorf("%s has no reasoning models", provider)
		}
	}
}
