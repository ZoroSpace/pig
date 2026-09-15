// pi: test/xiaomi-models.test.ts — deprecated MiMo models are omitted and the
// replacement v2.5 models are present across the four xiaomi providers.
package providers

import (
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

func TestXiaomiMiMoCatalogReplacement(t *testing.T) {
	xiaomiProviders := []types.ProviderID{
		types.ProviderXiaomi,
		types.ProviderXiaomiTokenPlanCN,
		types.ProviderXiaomiTokenPlanAMS,
		types.ProviderXiaomiTokenPlanSGP,
	}
	deprecatedModelIDs := []string{"mimo-v2-flash", "mimo-v2-omni", "mimo-v2-pro"}
	replacementModelIDs := []string{"mimo-v2.5", "mimo-v2.5-pro"}

	for _, provider := range xiaomiProviders {
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
