// pi: test/anthropic-eager-tool-input-e2e.test.ts. Real-API probe covering the
// generated anthropic-messages models; every live case skips without a
// provider API key (pig's catalog currently carries the anthropic provider's
// anthropic-messages models; kimi-coding and friends arrive with A2).
package api

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

type anthropicE2ECase struct {
	name   string
	model  types.Model
	apiKey string
}

func getE2EAPIKey(provider types.ProviderID) string {
	switch string(provider) {
	case "anthropic":
		return anthropicSmokeAPIKey()
	case "kimi-coding":
		return os.Getenv("KIMI_API_KEY")
	case "github-copilot":
		return os.Getenv("GITHUB_COPILOT_TOKEN")
	default:
		return ""
	}
}

// anthropicMessagesE2ECases builds one case per generated anthropic-messages
// model (pi: getProviders() x getAnthropicMessagesModels()).
func anthropicMessagesE2ECases(t *testing.T) []anthropicE2ECase {
	t.Helper()
	var cases []anthropicE2ECase
	providers := make([]types.ProviderID, 0, len(ai.MODELS))
	for provider := range ai.MODELS {
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i] < providers[j] })
	for _, provider := range providers {
		models, ok := ai.MODELS[provider]
		if !ok {
			continue
		}
		for _, model := range models {
			if model.API != types.ApiAnthropicMessages {
				continue
			}
			cases = append(cases, anthropicE2ECase{
				name:   string(provider) + "/" + model.ID,
				model:  model,
				apiKey: getE2EAPIKey(provider),
			})
		}
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].name < cases[j].name })
	return cases
}

func TestAnthropicEagerToolInputE2ECoverage(t *testing.T) {
	cases := anthropicMessagesE2ECases(t)
	if len(cases) == 0 {
		t.Fatal("expected generated anthropic-messages models")
	}
	var names []string
	for _, c := range cases {
		names = append(names, c.name)
	}
	t.Logf("covered anthropic-messages models: %s", strings.Join(names, ", "))
}

func TestAnthropicEagerToolInputE2EProbe(t *testing.T) {
	for _, c := range anthropicMessagesE2ECases(t) {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if c.apiKey == "" {
				t.Skip("no provider API key")
			}
			model := c.model
			model.Compat = mergeAnthropicCompatField(model.Compat, func(compat *types.AnthropicMessagesCompat) {
				compat.SupportsEagerToolInputStreaming = boolPtr(true)
			})
			expectAnthropicToolRequestAccepted(t, &model, c.apiKey, types.CacheRetentionShort)
		})
	}
}

// mergeAnthropicCompatField applies mutate to the model's anthropic compat
// (pi: withEagerToolInputStreaming / withLongCacheRetention spreads).
func mergeAnthropicCompatField(compat types.ModelCompat, mutate func(*types.AnthropicMessagesCompat)) types.ModelCompat {
	var merged types.AnthropicMessagesCompat
	if existing, ok := compat.(*types.AnthropicMessagesCompat); ok && existing != nil {
		merged = *existing
	}
	mutate(&merged)
	return &merged
}

// expectAnthropicToolRequestAccepted sends one tiny tool-enabled request (pi:
// expectToolEnabledRequestAccepted / expectLongCacheRetentionAccepted).
func expectAnthropicToolRequestAccepted(t *testing.T, model *types.Model, apiKey string, cacheRetention types.CacheRetention) {
	t.Helper()
	options := &types.StreamOptions{}
	options.APIKey = apiKey
	options.CacheRetention = cacheRetention
	options.MaxTokens = loToPtrInt64(128)
	llmContext := utils.NormalizeContext(&types.Context{
		SystemPrompt: "You are a concise assistant. Use tools when useful.",
		Messages: []types.Message{types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{String: "Call echo_value with value set to eager-input-streaming-compat."},
			Timestamp: 1,
		}},
		Tools: []types.Tool{{
			Name:        "echo_value",
			Description: "Echo a string value",
			Parameters:  jsonRawMessage(`{"type":"object","properties":{"value":{"type":"string","description":"The value to echo"}},"required":["value"]}`),
		}},
	})
	stream := (&AnthropicMessages{}).Stream(context.Background(), model, llmContext, options)
	result := stream.Result()
	if result.ErrorMessage != "" {
		t.Errorf("errorMessage = %q, want none", result.ErrorMessage)
	}
	if result.StopReason == types.StopReasonError {
		t.Errorf("stopReason = error")
	}
}

func loToPtrInt64(v int64) *int64 { return &v }

func jsonRawMessage(s string) json.RawMessage { return json.RawMessage(s) }
