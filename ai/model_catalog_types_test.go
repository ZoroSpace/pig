// pi: test/model-catalog-types.test.ts — the runtime half of the Copilot GPT
// routing test (S9 first-port). The expectTypeOf literal-type assertions are
// TypeScript compile-time checks with no Go runtime equivalent (declared
// deviation); the catalog-shape assertions run against ai.MODELS.
package ai

import (
	"strings"
	"testing"
)

func TestGitHubCopilotGPTModelsRouteThroughResponses(t *testing.T) {
	catalog, ok := MODELS[ProviderGithubCopilot]
	if !ok {
		t.Fatal("github-copilot not in catalog")
	}
	count := 0
	for _, model := range catalog {
		if strings.HasPrefix(model.ID, "gpt-") {
			count++
			if model.API != ApiOpenAIResponses {
				t.Errorf("copilot %s api = %s, want openai-responses", model.ID, model.API)
			}
		}
	}
	if count == 0 {
		t.Fatal("no copilot gpt models in catalog")
	}
	for _, modelID := range []string{"gpt-6-sol", "gpt-6-luna"} {
		model, ok := catalog[modelID]
		if !ok {
			t.Fatalf("%s not in copilot catalog", modelID)
		}
		if model.API != ApiOpenAIResponses {
			t.Errorf("%s api = %s, want openai-responses", modelID, model.API)
		}
		if model.ContextWindow != 1000000 {
			t.Errorf("%s contextWindow = %d, want 1000000", modelID, model.ContextWindow)
		}
		if model.MaxTokens != 128000 {
			t.Errorf("%s maxTokens = %d, want 128000", modelID, model.MaxTokens)
		}
		if got := model.ThinkingLevelMap[ModelThinkingOff]; got == nil || *got != "none" {
			t.Errorf("%s off mapping = %v, want none", modelID, got)
		}
		if got := model.ThinkingLevelMap[ModelThinkingMax]; got == nil || *got != "max" {
			t.Errorf("%s max mapping = %v, want max", modelID, got)
		}
	}
}
