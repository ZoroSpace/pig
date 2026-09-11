// pi: test/xiaomi-token-plan-ams-anthropic-empty-signature-smoke.test.ts.
// Runs against the real endpoint when a key is configured; skips otherwise.
package api

import (
	gocontext "context"
	"os"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
)

// xiaomiSmokeModel mirrors pi's xiaomi-token-plan-ams smoke model.
func xiaomiSmokeModel(t *testing.T) (*types.Model, string) {
	t.Helper()
	apiKey := xiaomiSmokeAPIKey()
	if apiKey == "" {
		t.Skip("XIAOMI_TOKEN_PLAN_AMS_API_KEY not set")
	}
	model := anthropicTestModel("mimo-v2.5-pro", types.ProviderXiaomiTokenPlanAMS, &types.AnthropicMessagesCompat{AllowEmptySignature: lo.ToPtr(true)})
	model.Name = "MiMo-V2.5-Pro Anthropic smoke"
	model.BaseURL = "https://token-plan-ams.xiaomimimo.com/anthropic"
	model.Cost = types.ModelCost{ModelCostRates: types.ModelCostRates{Input: 1, Output: 3, CacheRead: 0.2}}
	model.ContextWindow = 1048576
	model.MaxTokens = 1024
	return model, apiKey
}

func xiaomiSmokeAPIKey() string {
	for _, name := range []string{"XIAOMI_TOKEN_PLAN_AMS_API_KEY", "XIAOMI_API_KEY"} {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

func TestXiaomiTokenPlanAMSSmoke(t *testing.T) {
	model, apiKey := xiaomiSmokeModel(t)
	options := &types.SimpleStreamOptions{}
	options.APIKey = apiKey
	options.MaxTokens = lo.ToPtr(int64(96))
	stream := (&AnthropicMessages{}).StreamSimple(gocontext.Background(), model, &types.Context{
		Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Reply with exactly one word: pong"}, Timestamp: 1}},
	}, options)
	result := stream.Result()
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", result.StopReason, result.ErrorMessage)
	}
	t.Logf("usage=%+v", result.Usage)
}
