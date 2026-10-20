// pi: test/anthropic-adaptive-thinking-models.test.ts.
package api

import (
	"sort"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

// catalogModelsSorted snapshots the generated catalog as sorted model lists
// per provider.
func catalogModelsSorted(t *testing.T) map[types.ProviderID][]types.Model {
	t.Helper()
	out := map[types.ProviderID][]types.Model{}
	for providerID, models := range ai.MODELS {
		list := make([]types.Model, 0, len(models))
		for _, model := range models {
			list = append(list, model)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
		out[providerID] = list
	}
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestAnthropicAdaptiveThinkingModelMetadata(t *testing.T) {
	// pi expects the flagged set to contain the opencode, vercel-ai-gateway,
	// and cloudflare entries too; pig asserts the catalog slice it ships
	// (anthropic + kimi-coding), the rest arrive with their providers.
	expected := []string{
		"anthropic/claude-fable-5",
		"anthropic/claude-opus-4-8",
		"anthropic/claude-opus-5",
		"anthropic/claude-sonnet-5",
		// #11121: MiniMax models with catalog effort levels require adaptive thinking.
		"minimax-cn/MiniMax-M3.1-Flash",
		"minimax/MiniMax-M3.1-Flash",
	}
	var flagged []string
	for providerID, models := range catalogModelsSorted(t) {
		for _, model := range models {
			if model.API != types.ApiAnthropicMessages {
				continue
			}
			if compat, ok := model.Compat.(*types.AnthropicMessagesCompat); ok && compat != nil &&
				compat.ForceAdaptiveThinking != nil && *compat.ForceAdaptiveThinking {
				flagged = append(flagged, string(providerID)+"/"+model.ID)
			}
		}
	}
	sort.Strings(flagged)
	for _, want := range expected {
		if !containsString(flagged, want) {
			t.Errorf("flagged models missing %q (got %v)", want, flagged)
		}
	}
}
