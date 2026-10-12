// pig-side coverage for model-config.ts port (pi exercises this surface
// through model-registry tests that arrive with the ModelRuntime batch; the
// behaviors locked here are the load error wrappers and typed decode).
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai"
)

func TestModelConfigEmptyPathYieldsEmptyConfig(t *testing.T) {
	config := LoadModelConfig("")
	if len(config.GetProviderIds()) != 0 {
		t.Fatalf("providers = %v, want empty", config.GetProviderIds())
	}
	if config.GetError() != "" {
		t.Fatalf("error = %q", config.GetError())
	}
}

func TestModelConfigMissingFileYieldsEmptyConfig(t *testing.T) {
	config := LoadModelConfig(filepath.Join(t.TempDir(), "models.json"))
	if len(config.GetProviderIds()) != 0 {
		t.Fatalf("providers = %v, want empty", config.GetProviderIds())
	}
	if config.GetError() != "" {
		t.Fatalf("missing file must not produce an error, got %q", config.GetError())
	}
}

func TestModelConfigParseErrorCarriesFileWrapper(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(path, []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := LoadModelConfig(path)
	if !strings.Contains(config.GetError(), "Failed to parse models.json") {
		t.Fatalf("error = %q", config.GetError())
	}
	if !strings.Contains(config.GetError(), "File: "+path) {
		t.Fatalf("error = %q, want File wrapper", config.GetError())
	}
}

func TestModelConfigSchemaErrorListsPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	content := `{
		// pig's models.json supports comments
		"providers": {
			"custom": {
				"models": [{ "name": "no id" }]
			}
		},
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	config := LoadModelConfig(path)
	if !strings.Contains(config.GetError(), "Invalid models.json schema:") {
		t.Fatalf("error = %q", config.GetError())
	}
	if !strings.Contains(config.GetError(), "providers.custom.models.0.id") {
		t.Fatalf("error = %q, want the missing id path", config.GetError())
	}
}

func TestModelConfigDecodesTypedProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	content := `{
		"providers": {
			"custom": {
				"name": "Custom",
				"baseUrl": "https://custom.example/v1",
				"apiKey": "key-from-env",
				"authHeader": true,
				"headers": { "x-custom": "yes" },
				"models": [
					{
						"id": "custom-model",
						"name": "Custom Model",
						"reasoning": true,
						"cost": { "input": 1, "output": 2, "cacheRead": 0.1, "cacheWrite": 0.2 },
						"maxTokens": 8192,
						"compat": { "thinkingFormat": "openai" }
					}
				],
				"modelOverrides": {
					"custom-model": { "name": "Renamed", "reasoning": false }
				}
			}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	config := LoadModelConfig(path)
	if config.GetError() != "" {
		t.Fatalf("error = %q", config.GetError())
	}
	if got := config.GetProviderIds(); len(got) != 1 || got[0] != "custom" {
		t.Fatalf("provider ids = %v", got)
	}
	provider := config.GetProvider("custom")
	if provider == nil {
		t.Fatal("provider missing")
	}
	if provider.Name != "Custom" || provider.BaseURL != "https://custom.example/v1" || provider.APIKey != "key-from-env" {
		t.Fatalf("provider = %+v", provider)
	}
	if provider.AuthHeader == nil || !*provider.AuthHeader {
		t.Fatalf("authHeader = %+v", provider.AuthHeader)
	}
	if len(provider.Models) != 1 {
		t.Fatalf("models = %+v", provider.Models)
	}
	model := provider.Models[0]
	if model.ID != "custom-model" || model.Name != "Custom Model" || model.Reasoning == nil || !*model.Reasoning {
		t.Fatalf("model = %+v", model)
	}
	if model.Cost == nil || model.Cost.Input != 1 || model.Cost.Output != 2 {
		t.Fatalf("cost = %+v", model.Cost)
	}
	if model.MaxTokens == nil || *model.MaxTokens != 8192 {
		t.Fatalf("maxTokens = %+v", model.MaxTokens)
	}
	if !strings.Contains(string(model.Compat), `"thinkingFormat":"openai"`) {
		t.Fatalf("compat = %s", model.Compat)
	}
	override := provider.ModelOverrides["custom-model"]
	if override == nil || override.Name != "Renamed" || override.Reasoning == nil || *override.Reasoning {
		t.Fatalf("override = %+v", override)
	}
}

func TestModelConfigAcceptsCommentsAndTrailingCommas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	content := `{
		// provider comment
		"providers": {
			"custom": { "name": "Custom", },
		},
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	config := LoadModelConfig(path)
	if config.GetError() != "" {
		t.Fatalf("error = %q", config.GetError())
	}
	if provider := config.GetProvider("custom"); provider == nil || provider.Name != "Custom" {
		t.Fatalf("provider = %+v", provider)
	}
}

func TestModelConfigAcceptsVolcengineThinkingFormat(t *testing.T) {
	// pig-only: ThinkingFormatVolcengine exists in ai/types (the built-in
	// volcengine-plan provider's thinking format) and must be configurable
	// through models.json as well; the validator enum missing it rejected the
	// whole file (found 2026-10-12 during the volcengine burn experiment).
	path := filepath.Join(t.TempDir(), "models.json")
	content := `{
		"providers": {
			"arkproxy": {
				"baseUrl": "http://127.0.0.1:9/api/coding/v3",
				"apiKey": "sk-test",
				"api": "openai-completions",
				"models": [{
					"id": "deepseek-v4.1-flash",
					"reasoning": true,
					"input": ["text"],
					"contextWindow": 1024000,
					"maxTokens": 384000,
					"compat": { "thinkingFormat": "volcengine", "supportsReasoningEffort": false }
				}]
			}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	config := LoadModelConfig(path)
	if config.GetError() != "" {
		t.Fatalf("error = %q", config.GetError())
	}
	provider := config.GetProvider("arkproxy")
	if provider == nil || len(provider.Models) != 1 {
		t.Fatalf("provider = %+v", provider)
	}
	var compatMap map[string]any
	if err := json.Unmarshal(provider.Models[0].Compat, &compatMap); err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCompat(ai.ApiOpenAICompletions, compatMap)
	if err != nil {
		t.Fatal(err)
	}
	oc, ok := decoded.(*ai.OpenAICompletionsCompat)
	if !ok {
		t.Fatalf("decoded type %T", decoded)
	}
	if oc.ThinkingFormat == nil || *oc.ThinkingFormat != ai.ThinkingFormatVolcengine {
		t.Fatalf("thinkingFormat = %+v", oc.ThinkingFormat)
	}
}

func TestModelConfigValidatesCompatUnion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	// TypeBox object schemas allow unknown keys, so a field absent from the
	// openai-completions schema still validates through the union; a literal
	// enum violation is rejected by every alternative.
	content := `{
		"providers": {
			"anthropic": {
				"compat": { "supportsTemperature": true },
				"models": [{ "id": "m", "compat": { "thinkingFormat": "bogus" } }]
			}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	config := LoadModelConfig(path)
	if !strings.Contains(config.GetError(), "providers.anthropic.models.0.compat.thinkingFormat") {
		t.Fatalf("error = %q", config.GetError())
	}
}
