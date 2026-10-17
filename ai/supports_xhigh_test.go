// pi: test/supports-xhigh.test.ts — getSupportedThinkingLevels over the
// generated catalog. First-ported with S9 (S8-6 偏差 2 continuation); the
// catalog assertions run against ai.MODELS exactly like pi's getModel.
package ai

import (
	"testing"
)

// supportsXhighModel fetches one catalog model (pi: getModel).
func supportsXhighModel(t *testing.T, provider ProviderID, id string) Model {
	t.Helper()
	models, ok := MODELS[provider]
	if !ok {
		t.Fatalf("provider %q not in catalog", provider)
	}
	model, ok := models[id]
	if !ok {
		t.Fatalf("model %s/%s not in catalog", provider, id)
	}
	return model
}

// supportsXhighLevels returns the supported thinking levels as strings.
func supportsXhighLevels(t *testing.T, model Model) []string {
	t.Helper()
	levels := GetSupportedThinkingLevels(&model)
	out := make([]string, 0, len(levels))
	for _, level := range levels {
		out = append(out, string(level))
	}
	return out
}

func supportsXhighContains(t *testing.T, levels []string, want string) bool {
	t.Helper()
	for _, level := range levels {
		if level == want {
			return true
		}
	}
	return false
}

// supportsXhighAssertLevels asserts an exact level list.
func supportsXhighAssertLevels(t *testing.T, model Model, want ...string) {
	t.Helper()
	levels := supportsXhighLevels(t, model)
	if len(levels) != len(want) {
		t.Fatalf("%s levels = %v, want %v", model.ID, levels, want)
	}
	for i, level := range levels {
		if level != want[i] {
			t.Fatalf("%s levels = %v, want %v", model.ID, levels, want)
		}
	}
}

func TestGetSupportedThinkingLevelsCatalog(t *testing.T) {
	t.Run("includes max but not xhigh for Anthropic Opus 4.6 on anthropic-messages API", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderAnthropic, "claude-opus-4-6")
		if !supportsXhighContains(t, supportsXhighLevels(t, model), "max") {
			t.Error("opus-4-6 must support max")
		}
		if supportsXhighContains(t, supportsXhighLevels(t, model), "xhigh") {
			t.Error("opus-4-6 must not support xhigh")
		}
	})

	t.Run("includes xhigh and max for Anthropic Opus 4.8 on anthropic-messages API", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderAnthropic, "claude-opus-4-8")
		levels := supportsXhighLevels(t, model)
		if !supportsXhighContains(t, levels, "xhigh") || !supportsXhighContains(t, levels, "max") {
			t.Errorf("opus-4-8 levels = %v, want xhigh and max", levels)
		}
	})

	t.Run("includes xhigh and max for Anthropic Opus 5 on anthropic-messages API", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderAnthropic, "claude-opus-5")
		levels := supportsXhighLevels(t, model)
		if !supportsXhighContains(t, levels, "xhigh") || !supportsXhighContains(t, levels, "max") {
			t.Errorf("opus-5 levels = %v, want xhigh and max", levels)
		}
	})

	t.Run("includes Claude Opus 5.5 with its always-on effort levels and official pricing", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderAnthropic, "claude-opus-5-5")
		if model.Cost.Input != 4 || model.Cost.Output != 20 || model.Cost.CacheRead != 0.2 || model.Cost.CacheWrite != 5 {
			t.Errorf("opus-5-5 cost = %+v", model.Cost.ModelCostRates)
		}
		if model.ContextWindow != 1_000_000 || model.MaxTokens != 128_000 {
			t.Errorf("opus-5-5 context = %d/%d", model.ContextWindow, model.MaxTokens)
		}
		compat, ok := model.Compat.(*AnthropicMessagesCompat)
		if !ok || compat == nil {
			t.Fatalf("opus-5-5 compat = %T, want AnthropicMessagesCompat", model.Compat)
		}
		if compat.ForceAdaptiveThinking == nil || !*compat.ForceAdaptiveThinking {
			t.Error("opus-5-5 forceAdaptiveThinking = false, want true")
		}
		if compat.SupportsMidConvoEffort == nil || !*compat.SupportsMidConvoEffort {
			t.Error("opus-5-5 supportsMidConvoEffort = false, want true")
		}
		if compat.SupportsMidConvoSystemMessages == nil || !*compat.SupportsMidConvoSystemMessages {
			t.Error("opus-5-5 supportsMidConvoSystemMessages = false, want true")
		}
		if compat.SupportsMidConvoToolChanges == nil || !*compat.SupportsMidConvoToolChanges {
			t.Error("opus-5-5 supportsMidConvoToolChanges = false, want true")
		}
		supportsXhighAssertLevels(t, model, "low", "medium", "high", "xhigh", "max")
	})

	t.Run("includes max but not xhigh for Anthropic Sonnet 4.6 on anthropic-messages API", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderAnthropic, "claude-sonnet-4-6")
		if !supportsXhighContains(t, supportsXhighLevels(t, model), "max") {
			t.Error("sonnet-4-6 must support max")
		}
		if supportsXhighContains(t, supportsXhighLevels(t, model), "xhigh") {
			t.Error("sonnet-4-6 must not support xhigh")
		}
	})

	t.Run("includes xhigh and max for Anthropic Sonnet 5 on anthropic-messages API", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderAnthropic, "claude-sonnet-5")
		levels := supportsXhighLevels(t, model)
		if !supportsXhighContains(t, levels, "xhigh") || !supportsXhighContains(t, levels, "max") {
			t.Errorf("sonnet-5 levels = %v, want xhigh and max", levels)
		}
	})

	t.Run("includes xhigh and max but not off for Anthropic Claude Fable 5 on anthropic-messages API", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderAnthropic, "claude-fable-5")
		levels := supportsXhighLevels(t, model)
		if !supportsXhighContains(t, levels, "xhigh") || !supportsXhighContains(t, levels, "max") {
			t.Errorf("fable-5 levels = %v, want xhigh and max", levels)
		}
		if supportsXhighContains(t, levels, "off") {
			t.Error("fable-5 must not support off")
		}
	})

	t.Run("does not include xhigh or max for Claude Sonnet 4.5", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderAnthropic, "claude-sonnet-4-5")
		levels := supportsXhighLevels(t, model)
		if supportsXhighContains(t, levels, "xhigh") || supportsXhighContains(t, levels, "max") {
			t.Errorf("sonnet-4-5 levels = %v, want no xhigh/max", levels)
		}
	})

	t.Run("includes Claude Sonnet 5.5 with managed effort levels and official pricing", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderAnthropic, "claude-sonnet-5-5")
		if model.Cost.Input != 2 || model.Cost.Output != 10 || model.Cost.CacheRead != 0.1 || model.Cost.CacheWrite != 2.5 {
			t.Fatalf("sonnet-5-5 cost = %+v", model.Cost.ModelCostRates)
		}
		if model.ContextWindow != 1000000 || model.MaxTokens != 128000 {
			t.Fatalf("sonnet-5-5 context = %d/%d", model.ContextWindow, model.MaxTokens)
		}
		compat, ok := model.Compat.(*AnthropicMessagesCompat)
		if !ok || compat == nil {
			t.Fatalf("sonnet-5-5 compat = %T", model.Compat)
		}
		if compat.ForceAdaptiveThinking == nil || !*compat.ForceAdaptiveThinking {
			t.Error("sonnet-5-5 must force adaptive thinking")
		}
		if compat.SupportsMidConvoEffort == nil || !*compat.SupportsMidConvoEffort {
			t.Error("sonnet-5-5 must support mid-convo effort")
		}
		if compat.SupportsMidConvoSystemMessages == nil || !*compat.SupportsMidConvoSystemMessages {
			t.Error("sonnet-5-5 must support mid-convo system messages")
		}
		if compat.SupportsMidConvoToolChanges == nil || !*compat.SupportsMidConvoToolChanges {
			t.Error("sonnet-5-5 must support mid-convo tool changes")
		}
		if compat.SupportsTemperature == nil || *compat.SupportsTemperature {
			t.Error("sonnet-5-5 must not support temperature")
		}
		supportsXhighAssertLevels(t, model, "low", "medium", "high", "xhigh", "max")
	})

	// S17 (pi f76c1db66 + a2eef9eb6): Claude Haiku 5.5 joins the catalog with
	// its prompt-length pricing tier and adaptive thinking metadata.
	t.Run("includes Claude Haiku 5.5 with tiered pricing and adaptive thinking", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderAnthropic, "claude-haiku-5-5")
		if model.Cost.Input != 0.1 || model.Cost.Output != 0.5 || model.Cost.CacheRead != 0.01 || model.Cost.CacheWrite != 0.125 {
			t.Fatalf("haiku-5-5 cost = %+v", model.Cost.ModelCostRates)
		}
		if len(model.Cost.Tiers) != 1 {
			t.Fatalf("haiku-5-5 tiers = %+v, want exactly one", model.Cost.Tiers)
		}
		tier := model.Cost.Tiers[0]
		if tier.InputTokensAbove != 100000 || tier.Input != 0.5 || tier.Output != 2.5 || tier.CacheRead != 0.05 || tier.CacheWrite != 0.625 {
			t.Fatalf("haiku-5-5 tier = %+v", tier)
		}
		if model.ContextWindow != 1000000 || model.MaxTokens != 128000 {
			t.Fatalf("haiku-5-5 context = %d/%d", model.ContextWindow, model.MaxTokens)
		}
		compat, ok := model.Compat.(*AnthropicMessagesCompat)
		if !ok || compat == nil {
			t.Fatalf("haiku-5-5 compat = %T", model.Compat)
		}
		if compat.ForceAdaptiveThinking == nil || !*compat.ForceAdaptiveThinking {
			t.Error("haiku-5-5 must force adaptive thinking")
		}
		if compat.SupportsMidConvoEffort == nil || !*compat.SupportsMidConvoEffort {
			t.Error("haiku-5-5 must support mid-convo effort")
		}
		if compat.SupportsMidConvoSystemMessages == nil || !*compat.SupportsMidConvoSystemMessages {
			t.Error("haiku-5-5 must support mid-convo system messages")
		}
		if compat.SupportsTemperature == nil || *compat.SupportsTemperature {
			t.Error("haiku-5-5 must not support temperature")
		}
		// dc939913f: Haiku 5.5 accepts thinking: {type: "disabled"}, so off is a
		// valid level (unlike Opus 5.5 / Sonnet 5.5 / Fable 5.1).
		supportsXhighAssertLevels(t, model, "off", "low", "medium", "high", "xhigh", "max")
	})

	t.Run("does not support off for GPT-6.1 Sol", func(t *testing.T) {
		// OpenAI and Codex reject reasoning.effort "none" for GPT-6.1 Sol.
		expected := map[ProviderID][]string{
			ProviderOpenAI:      {"low", "medium", "high", "xhigh", "max"},
			ProviderOpenAICodex: {"minimal", "low", "medium", "high", "xhigh", "max"},
		}
		for _, provider := range []ProviderID{ProviderOpenAI, ProviderOpenAICodex} {
			model := supportsXhighModel(t, provider, "gpt-6.1-sol")
			supportsXhighAssertLevels(t, model, expected[provider]...)
			off, hasOff := model.ThinkingLevelMap[ModelThinkingOff]
			if !hasOff || off != nil {
				t.Errorf("gpt-6.1-sol/%s thinkingLevelMap.off = %#v, want null", provider, off)
			}
		}
	})

	for _, modelID := range []string{"gpt-5.5", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-6.1-sol"} {
		modelID := modelID
		t.Run("includes xhigh for openai-codex "+modelID, func(t *testing.T) {
			model := supportsXhighModel(t, ProviderOpenAICodex, modelID)
			if !supportsXhighContains(t, supportsXhighLevels(t, model), "xhigh") {
				t.Errorf("openai-codex %s levels = %v, want xhigh", modelID, supportsXhighLevels(t, model))
			}
		})
	}

	for _, modelID := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-sol", "gpt-6-luna"} {
		modelID := modelID
		t.Run("includes xhigh and max for OpenAI "+modelID, func(t *testing.T) {
			model := supportsXhighModel(t, ProviderOpenAI, modelID)
			supportsXhighAssertLevels(t, model, "off", "low", "medium", "high", "xhigh", "max")
		})
	}

	for _, tc := range []struct {
		id                 string
		cost               ModelCostRates
		codexContextWindow int64
	}{
		{"gpt-6-sol", ModelCostRates{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5}, 272000},
		// Codex serves GPT-6.1 Sol with a 500K default context window (#10840)
		{"gpt-6.1-sol", ModelCostRates{Input: 2, Output: 10, CacheRead: 0.1, CacheWrite: 2.5}, 500000},
		{"gpt-6-luna", ModelCostRates{Input: 0.1, Output: 0.5, CacheRead: 0.01, CacheWrite: 0.125}, 272000},
	} {
		tc := tc
		t.Run("includes official metadata for OpenAI and Codex "+tc.id, func(t *testing.T) {
			for _, provider := range []ProviderID{ProviderOpenAI, ProviderOpenAICodex} {
				model := supportsXhighModel(t, provider, tc.id)
				if want := []ModelInput{ModelInputText, ModelInputImage}; len(model.Input) != len(want) {
					t.Fatalf("%s/%s input = %v", provider, tc.id, model.Input)
				}
				if model.Cost.Input != tc.cost.Input || model.Cost.Output != tc.cost.Output ||
					model.Cost.CacheRead != tc.cost.CacheRead || model.Cost.CacheWrite != tc.cost.CacheWrite {
					t.Fatalf("%s/%s cost = %+v", provider, tc.id, model.Cost.ModelCostRates)
				}
				if len(model.Cost.Tiers) != 1 {
					t.Fatalf("%s/%s tiers = %d, want 1", provider, tc.id, len(model.Cost.Tiers))
				}
				tier := model.Cost.Tiers[0]
				if tier.InputTokensAbove != 272000 ||
					tier.Input != tc.cost.Input*2 || tier.Output != tc.cost.Output*1.5 ||
					tier.CacheRead != tc.cost.CacheRead*2 || tier.CacheWrite != tc.cost.CacheWrite*2 {
					t.Fatalf("%s/%s long-context tier = %+v", provider, tc.id, tier)
				}
				contextWindow := int64(272000)
				if provider == ProviderOpenAICodex {
					contextWindow = tc.codexContextWindow
				}
				if model.ContextWindow != contextWindow || model.MaxTokens != 128000 {
					t.Fatalf("%s/%s context = %d/%d", provider, tc.id, model.ContextWindow, model.MaxTokens)
				}
				compat, ok := model.Compat.(*OpenAIResponsesCompat)
				if !ok || compat == nil {
					t.Fatalf("%s/%s compat = %T", provider, tc.id, model.Compat)
				}
				if compat.SupportsAdditionalTools == nil || !*compat.SupportsAdditionalTools {
					t.Errorf("%s/%s supportsAdditionalTools = false", provider, tc.id)
				}
				if compat.SupportsMidConvoSystemMessages == nil || !*compat.SupportsMidConvoSystemMessages {
					t.Errorf("%s/%s supportsMidConvoSystemMessages = false", provider, tc.id)
				}
				if compat.SupportsOpenAIGrammarTools == nil || !*compat.SupportsOpenAIGrammarTools {
					t.Errorf("%s/%s supportsOpenAIGrammarTools = false", provider, tc.id)
				}
				if compat.SupportsToolSearch == nil || !*compat.SupportsToolSearch {
					t.Errorf("%s/%s supportsToolSearch = false", provider, tc.id)
				}
			}
		})
	}

	t.Run("includes only medium/high/xhigh for OpenAI GPT-5.5 Pro", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderOpenAI, "gpt-5.5-pro")
		supportsXhighAssertLevels(t, model, "medium", "high", "xhigh")
	})

	t.Run("includes only medium/high/xhigh for OpenRouter GPT-5.5 Pro", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderOpenRouter, "openai/gpt-5.5-pro")
		supportsXhighAssertLevels(t, model, "medium", "high", "xhigh")
	})

	t.Run("includes low/high/max plus off for DeepSeek V4.1 Flash on the DeepSeek provider", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderDeepSeek, "deepseek-flash")
		supportsXhighAssertLevels(t, model, "off", "low", "high", "max")
	})

	t.Run("preserves low/high/max metadata for DeepSeek V4.1 Flash on OpenRouter", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderOpenRouter, "deepseek/deepseek-v4.1-flash")
		supportsXhighAssertLevels(t, model, "off", "low", "high", "max")
	})

	t.Run("preserves low/high/max metadata for DeepSeek V4.1 Flash on opencode-go", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderOpenCodeGo, "deepseek-v4.1-flash")
		supportsXhighAssertLevels(t, model, "low", "high", "max")
	})

	t.Run("excludes thinking off for Moonshot Kimi K2.7 Code models", func(t *testing.T) {
		for _, provider := range []ProviderID{ProviderMoonshotAI, ProviderMoonshotAICN} {
			model := supportsXhighModel(t, provider, "kimi-k2.7-code")
			supportsXhighAssertLevels(t, model, "minimal", "low", "medium", "high")
		}
	})

	for _, provider := range []ProviderID{ProviderMoonshotAI, ProviderMoonshotAICN} {
		provider := provider
		t.Run("uses the verified effort options for "+string(provider)+" Kimi K3", func(t *testing.T) {
			model := supportsXhighModel(t, provider, "kimi-k3")
			supportsXhighAssertLevels(t, model, "low", "high", "max")
		})
	}

	t.Run("includes only low, high, max for Kimi Coding K3", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderKimiCoding, "k3")
		supportsXhighAssertLevels(t, model, "low", "high", "max")
	})

	t.Run("excludes off for always-thinking Kimi Coding Highspeed", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderKimiCoding, "kimi-for-coding-highspeed")
		levels := supportsXhighLevels(t, model)
		if supportsXhighContains(t, levels, "off") {
			t.Fatalf("levels = %v, want no off", levels)
		}
	})

	t.Run("includes only high for OpenCode Grok Build", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderOpenCode, "grok-build-0.1")
		supportsXhighAssertLevels(t, model, "high")
	})

	t.Run("includes only high/xhigh plus off for DeepSeek V4 Flash on OpenRouter", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderOpenRouter, "deepseek/deepseek-v4-flash")
		supportsXhighAssertLevels(t, model, "off", "high", "xhigh")
	})

	t.Run("includes max but not xhigh for OpenRouter Opus 4.6 (openai-completions API)", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderOpenRouter, "anthropic/claude-opus-4.6")
		levels := supportsXhighLevels(t, model)
		if !supportsXhighContains(t, levels, "max") {
			t.Error("openrouter opus-4.6 must support max")
		}
		if supportsXhighContains(t, levels, "xhigh") {
			t.Error("openrouter opus-4.6 must not support xhigh")
		}
	})

	t.Run("includes xhigh and max for Bedrock Claude Opus 5", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderAmazonBedrock, "global.anthropic.claude-opus-5")
		levels := supportsXhighLevels(t, model)
		if !supportsXhighContains(t, levels, "xhigh") || !supportsXhighContains(t, levels, "max") {
			t.Errorf("bedrock opus-5 levels = %v, want xhigh and max", levels)
		}
	})

	t.Run("includes xhigh but not off or max for xAI Grok 4.6", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderXAI, "grok-4.6")
		supportsXhighAssertLevels(t, model, "low", "medium", "high", "xhigh")
	})

	t.Run("includes xhigh and max but not off for Bedrock Claude Fable 5", func(t *testing.T) {
		model := supportsXhighModel(t, ProviderAmazonBedrock, "global.anthropic.claude-fable-5")
		levels := supportsXhighLevels(t, model)
		if !supportsXhighContains(t, levels, "xhigh") || !supportsXhighContains(t, levels, "max") {
			t.Errorf("bedrock fable-5 levels = %v, want xhigh and max", levels)
		}
		if supportsXhighContains(t, levels, "off") {
			t.Error("bedrock fable-5 must not support off")
		}
	})
}
