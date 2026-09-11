// Shared test fixtures for the openai-responses adapter tests (pi: the
// getModel(...) catalog lookups in test/openai-responses-*.test.ts). pig's
// generated catalog has no openai-provider models until B2 regenerates it, so
// the fixtures mirror pi's post-metadata catalog values verbatim (extracted
// from packages/ai/src/providers/data/*.json at v0.85.1); B2's catalog regen
// is the authoritative source afterwards.
//
// The TS tests mock globalThis.fetch; pig injects FetchFunction (the
// adapter's declared transport surface) and captures the request there.
package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

// responsesModelSpec is one fixture model (pi's Model<"openai-responses">).
type responsesModelSpec struct {
	provider      string
	id            string
	name          string
	baseURL       string
	reasoning     bool
	input         []types.ModelInput
	cost          types.ModelCost
	contextWindow int64
	maxTokens     int64
	headers       map[string]string
	tlm           types.ThinkingLevelMap
	compat        types.ModelCompat
}

// openAIResponsesBaseURL is pi's openai provider baseUrl.
const openAIResponsesBaseURL = "https://api.openai.com/v1"

// responsesModelCost builds ModelCost from rates plus optional tiers.
func responsesModelCost(input, output, cacheRead, cacheWrite float64, tiers ...types.ModelCostTier) types.ModelCost {
	return types.ModelCost{
		ModelCostRates: types.ModelCostRates{
			Input: input, Output: output, CacheRead: cacheRead, CacheWrite: cacheWrite,
		},
		Tiers: tiers,
	}
}

// responsesTier builds one pricing tier.
func responsesTier(above int64, input, output, cacheRead, cacheWrite float64) types.ModelCostTier {
	return types.ModelCostTier{
		InputTokensAbove: above,
		ModelCostRates:   types.ModelCostRates{Input: input, Output: output, CacheRead: cacheRead, CacheWrite: cacheWrite},
	}
}

// responsesTLM builds a ThinkingLevelMap from level values; a nil *string
// entry marks the level unsupported (pi: null).
func responsesTLM(entries map[types.ModelThinkingLevel]*string) types.ThinkingLevelMap {
	return entries
}

// strPtr is lo.ToPtr for string map literals.
func responsesStrPtr(s string) *string { return &s }

// responsesNullPtr marks an unsupported level (pi: null).
func responsesNullPtr() *string { return nil }

// responsesCompat is the OpenAIResponsesCompat fixture builder.
func responsesCompat(entries *types.OpenAIResponsesCompat) types.ModelCompat {
	if entries == nil {
		return nil
	}
	return entries
}

// openAIResponsesModelSpecs mirrors the catalog entries the tests reference.
var openAIResponsesModelSpecs = map[string]responsesModelSpec{
	// openai provider (pi: data/openai.json, api openai-responses).
	"openai|gpt-5.4": {
		provider: "openai", id: "gpt-5.4", name: "GPT-5.4", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(2.5, 15, 0.25, 0, responsesTier(272000, 5, 22.5, 0.5, 0)),
		contextWindow: 272000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesStrPtr("none"), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesStrPtr("xhigh"),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
			SupportsAdditionalTools: loBoolPtr(true), SupportsToolSearch: loBoolPtr(true),
		}),
	},
	"openai|gpt-5.5": {
		provider: "openai", id: "gpt-5.5", name: "GPT-5.5", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(5, 30, 0.5, 0, responsesTier(272000, 10, 45, 1, 0)),
		contextWindow: 272000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesStrPtr("none"), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesStrPtr("xhigh"),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
			SupportsAdditionalTools: loBoolPtr(true), SupportsToolSearch: loBoolPtr(true),
		}),
	},
	"openai|gpt-5-mini": {
		provider: "openai", id: "gpt-5-mini", name: "GPT-5 Mini", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(0.25, 2, 0.025, 0),
		contextWindow: 400000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesNullPtr(), types.ModelThinkingMinimal: responsesStrPtr("minimal"),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesNullPtr(),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
		}),
	},
	"openai|gpt-5.1": {
		provider: "openai", id: "gpt-5.1", name: "GPT-5.1", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(1.25, 10, 0.125, 0),
		contextWindow: 400000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesStrPtr("none"), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesNullPtr(),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
		}),
	},
	"openai|gpt-5.2": {
		provider: "openai", id: "gpt-5.2", name: "GPT-5.2", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(1.75, 14, 0.175, 0),
		contextWindow: 400000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesStrPtr("none"), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesStrPtr("xhigh"),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
		}),
	},
	"openai|gpt-5.3-codex": {
		provider: "openai", id: "gpt-5.3-codex", name: "GPT-5.3 Codex", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(1.75, 14, 0.175, 0),
		contextWindow: 400000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesStrPtr("none"), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesStrPtr("xhigh"),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
		}),
	},
	"openai|gpt-5.4-mini": {
		provider: "openai", id: "gpt-5.4-mini", name: "GPT-5.4 Mini", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(0.75, 4.5, 0.075, 0),
		contextWindow: 400000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesStrPtr("none"), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesStrPtr("xhigh"),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
			SupportsAdditionalTools: loBoolPtr(true), SupportsToolSearch: loBoolPtr(true),
		}),
	},
	"openai|gpt-5.4-nano": {
		provider: "openai", id: "gpt-5.4-nano", name: "GPT-5.4 Nano", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(0.2, 1.25, 0.02, 0),
		contextWindow: 400000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesStrPtr("none"), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesStrPtr("xhigh"),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
		}),
	},
	"openai|gpt-5.6-sol": {
		provider: "openai", id: "gpt-5.6-sol", name: "GPT-5.6 Sol", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(4, 20, 0.4, 5, responsesTier(272000, 8, 30, 0.8, 10)),
		contextWindow: 272000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesStrPtr("none"), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesStrPtr("xhigh"),
			types.ModelThinkingMax: responsesStrPtr("max"),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
			SupportsAdditionalTools: loBoolPtr(true), SupportsToolSearch: loBoolPtr(true),
			SupportsExplicitPromptCacheMode: loBoolPtr(true),
		}),
	},
	"openai|gpt-5.6-terra": {
		provider: "openai", id: "gpt-5.6-terra", name: "GPT-5.6 Terra", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(2, 12, 0.2, 2.5, responsesTier(272000, 4, 18, 0.4, 5)),
		contextWindow: 272000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesStrPtr("none"), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesStrPtr("xhigh"),
			types.ModelThinkingMax: responsesStrPtr("max"),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
			SupportsAdditionalTools: loBoolPtr(true), SupportsToolSearch: loBoolPtr(true),
			SupportsExplicitPromptCacheMode: loBoolPtr(true),
		}),
	},
	"openai|gpt-5.6-luna": {
		provider: "openai", id: "gpt-5.6-luna", name: "GPT-5.6 Luna", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(0.2, 1.2, 0.02, 0.25, responsesTier(272000, 0.4, 1.8, 0.04, 0.5)),
		contextWindow: 272000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesStrPtr("none"), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesStrPtr("xhigh"),
			types.ModelThinkingMax: responsesStrPtr("max"),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
			SupportsAdditionalTools: loBoolPtr(true), SupportsToolSearch: loBoolPtr(true),
			SupportsExplicitPromptCacheMode: loBoolPtr(true),
		}),
	},
	"openai|gpt-5": {
		provider: "openai", id: "gpt-5", name: "GPT-5", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(1.25, 10, 0.125, 0),
		contextWindow: 400000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesNullPtr(), types.ModelThinkingMinimal: responsesStrPtr("minimal"),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesNullPtr(),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
		}),
	},
	"openai|gpt-5-nano": {
		provider: "openai", id: "gpt-5-nano", name: "GPT-5 Nano", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(0.05, 0.4, 0.005, 0),
		contextWindow: 400000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesNullPtr(), types.ModelThinkingMinimal: responsesStrPtr("minimal"),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesNullPtr(),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
		}),
	},
	"openai|gpt-5-pro": {
		provider: "openai", id: "gpt-5-pro", name: "GPT-5 Pro", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(15, 120, 0, 0),
		contextWindow: 400000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesNullPtr(), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesNullPtr(), types.ModelThinkingMedium: responsesNullPtr(),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesNullPtr(),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
		}),
	},
	"openai|gpt-5.2-pro": {
		provider: "openai", id: "gpt-5.2-pro", name: "GPT-5.2 Pro", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(21, 168, 0, 0),
		contextWindow: 400000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesNullPtr(), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesNullPtr(), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesStrPtr("xhigh"),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
		}),
	},
	"openai|gpt-5.4-pro": {
		provider: "openai", id: "gpt-5.4-pro", name: "GPT-5.4 Pro", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(30, 180, 0, 0, responsesTier(272000, 60, 270, 0, 0)),
		contextWindow: 1050000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesNullPtr(), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesNullPtr(), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesStrPtr("xhigh"),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
			SupportsAdditionalTools: loBoolPtr(true), SupportsToolSearch: loBoolPtr(true),
		}),
	},
	"openai|gpt-5.5-pro": {
		provider: "openai", id: "gpt-5.5-pro", name: "GPT-5.5 Pro", baseURL: openAIResponsesBaseURL,
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(30, 180, 0, 0, responsesTier(272000, 60, 270, 0, 0)),
		contextWindow: 1050000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesNullPtr(), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesNullPtr(), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesStrPtr("xhigh"),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
		}),
	},
	"openai|gpt-4o-mini": {
		provider: "openai", id: "gpt-4o-mini", name: "GPT-4o mini", baseURL: openAIResponsesBaseURL,
		reasoning: false, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(0.15, 0.6, 0.075, 0),
		contextWindow: 128000, maxTokens: 16384,
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true),
		}),
	},
	// github-copilot provider (pi: data/github-copilot.json).
	"github-copilot|gpt-5-mini": {
		provider: "github-copilot", id: "gpt-5-mini", name: "GPT-5 Mini",
		baseURL: "https://api.individual.githubcopilot.com",
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(0.25, 2, 0.025, 0),
		contextWindow: 264000, maxTokens: 64000,
		headers: map[string]string{
			"User-Agent":             "GitHubCopilotChat/0.35.0",
			"Editor-Version":         "vscode/1.107.0",
			"Editor-Plugin-Version":  "copilot-chat/0.35.0",
			"Copilot-Integration-Id": "vscode-chat",
		},
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesNullPtr(), types.ModelThinkingMinimal: responsesStrPtr("low"),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesNullPtr(),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsOpenAIGrammarTools: loBoolPtr(true),
		}),
	},
	// cloudflare-ai-gateway provider (pi: data/cloudflare-ai-gateway.json).
	"cloudflare-ai-gateway|gpt-5.6-sol": {
		provider: "cloudflare-ai-gateway", id: "gpt-5.6-sol", name: "GPT-5.6 Sol",
		baseURL: "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/openai",
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(2, 10, 0.25, 3.125),
		contextWindow: 1050000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesNullPtr(), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesStrPtr("xhigh"),
			types.ModelThinkingMax: responsesStrPtr("max"),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SupportsStrictMode: loBoolPtr(true), SupportsOpenAIGrammarTools: loBoolPtr(true),
		}),
	},
	// opencode provider (pi: data/opencode.json).
	"opencode|gpt-5.4": {
		provider: "opencode", id: "gpt-5.4", name: "GPT-5.4", baseURL: "https://opencode.ai/zen/v1",
		reasoning: true, input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		cost:          responsesModelCost(2.5, 15, 0.25, 0),
		contextWindow: 272000, maxTokens: 128000,
		tlm: responsesTLM(map[types.ModelThinkingLevel]*string{
			types.ModelThinkingOff: responsesNullPtr(), types.ModelThinkingMinimal: responsesNullPtr(),
			types.ModelThinkingLow: responsesStrPtr("low"), types.ModelThinkingMedium: responsesStrPtr("medium"),
			types.ModelThinkingHigh: responsesStrPtr("high"), types.ModelThinkingXHigh: responsesStrPtr("xhigh"),
			types.ModelThinkingMax: responsesNullPtr(),
		}),
		compat: responsesCompat(&types.OpenAIResponsesCompat{
			SessionAffinityFormat:     loFormatPtr("openai-nosession"),
			SupportsOpenAIGrammarTools: loBoolPtr(true),
		}),
	},
}

// loBoolPtr builds a *bool compat entry.
func loBoolPtr(v bool) *bool { return &v }

// loFormatPtr builds a *SessionAffinityFormat compat entry.
func loFormatPtr(v string) *types.SessionAffinityFormat {
	format := types.SessionAffinityFormat(v)
	return &format
}

// responsesTestModel returns the fixture model for provider|id.
func responsesTestModel(t *testing.T, provider, id string) *types.Model {
	t.Helper()
	spec, ok := openAIResponsesModelSpecs[provider+"|"+id]
	if !ok {
		t.Fatalf("no fixture model for %s/%s", provider, id)
	}
	return responsesModelFromSpec(spec)
}

// responsesModelFromSpec materializes a spec into a Model.
func responsesModelFromSpec(spec responsesModelSpec) *types.Model {
	return &types.Model{
		ID:               spec.id,
		Name:             spec.name,
		API:              types.ApiOpenAIResponses,
		Provider:         types.ProviderID(spec.provider),
		BaseURL:          spec.baseURL,
		Reasoning:        spec.reasoning,
		ThinkingLevelMap: spec.tlm,
		Input:            spec.input,
		Cost:             spec.cost,
		ContextWindow:    spec.contextWindow,
		MaxTokens:        spec.maxTokens,
		Headers:          spec.headers,
		Compat:           spec.compat,
	}
}

// responsesUserMessage builds a plain text user message (pi: { role: "user",
// content: "...", timestamp }).
func responsesUserMessage(text string) types.UserMessage {
	return types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: text}, Timestamp: 1}
}

// responsesHeaderValue reads a request header case-insensitively (pi:
// getHeader over the mocked fetch init headers).
func responsesHeaderValue(req *http.Request, name string) string {
	if req == nil {
		return ""
	}
	for key, values := range req.Header {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

// capturedResponsesRequest records what the adapter sent (pi's fetch mock
// captures; pig captures inside FetchFunction).
type capturedResponsesRequest struct {
	req  *http.Request
	body map[string]any
}

// newResponsesSSEResponse builds an SSE http.Response from data payloads.
func newResponsesSSEResponse(payloads ...string) *http.Response {
	var b strings.Builder
	for _, p := range payloads {
		b.WriteString("data: " + p + "\n\n")
	}
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(b.String())),
	}
}

// fakeResponsesFetch returns a FetchFunction that captures the request and
// responds with the given SSE payloads (pi: vi.spyOn(globalThis, "fetch")).
func fakeResponsesFetch(captured *capturedResponsesRequest, payloads ...string) types.FetchFunction {
	return func(req *http.Request) (*http.Response, error) {
		if captured != nil {
			captured.req = req
			bodyBytes, _ := io.ReadAll(req.Body)
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			var parsed map[string]any
			_ = json.Unmarshal(bodyBytes, &parsed)
			captured.body = parsed
		}
		return newResponsesSSEResponse(payloads...), nil
	}
}

// collectResponsesEvents drains the stream, returning all events and the
// final result (pi: `for await ... break` plus stream.result()).
func collectResponsesEvents(t *testing.T, s *types.AssistantMessageEventStream) ([]types.AssistantMessageEvent, types.AssistantMessage) {
	t.Helper()
	var events []types.AssistantMessageEvent
	for event := range s.Events() {
		events = append(events, event)
		if event.Type == types.EventDone || event.Type == types.EventError {
			break
		}
	}
	result := s.Result()
	return events, result
}
