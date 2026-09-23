// pi: test/max-thinking.test.ts — max thinking level semantics: clamping
// holes and the Codex Responses max-effort payload. First-ported with S9
// (S8-6 偏差 2 continuation). pi throws from onPayload to stop after the
// request is built; pig captures through OnPayload and serves a completed
// SSE body via the Fetch seam.
package api

import (
	gocontext "context"
	"net/http"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// maxThinkingCatalogModel fetches a catalog model (pi: getModel).
func maxThinkingCatalogModel(t *testing.T, provider types.ProviderID, id string) types.Model {
	t.Helper()
	models, ok := ai.MODELS[ai.ProviderID(provider)]
	if !ok {
		t.Fatalf("provider %q not in catalog", provider)
	}
	model, ok := models[id]
	if !ok {
		t.Fatalf("model %s/%s not in catalog", provider, id)
	}
	return model
}

// TestMaxThinkingIsOptInForOrdinaryReasoningModels ports "is opt-in for
// ordinary reasoning models": no thinkingLevelMap means max/xhigh absent and
// max clamps to high.
func TestMaxThinkingIsOptInForOrdinaryReasoningModels(t *testing.T) {
	model := &types.Model{
		ID:            "ordinary-reasoning",
		Name:          "Ordinary Reasoning",
		API:           types.ApiOpenAICompletions,
		Provider:      "test",
		BaseURL:       "https://example.com/v1",
		Reasoning:     true,
		Input:         []types.ModelInput{types.ModelInputText},
		Cost:          types.ModelCost{},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
	levels := ai.GetSupportedThinkingLevels(model)
	want := []string{"off", "minimal", "low", "medium", "high"}
	if len(levels) != len(want) {
		t.Fatalf("levels = %v, want %v", levels, want)
	}
	for i, level := range levels {
		if string(level) != want[i] {
			t.Fatalf("levels = %v, want %v", levels, want)
		}
	}
	if got := ai.ClampThinkingLevel(model, types.ModelThinkingMax); got != types.ModelThinkingHigh {
		t.Fatalf("clamp(max) = %s, want high", got)
	}
}

// TestMaxThinkingCodexExposesXhighAndMax ports "exposes xhigh and max for
// openai-codex/%s".
func TestMaxThinkingCodexExposesXhighAndMax(t *testing.T) {
	for _, modelID := range []string{"gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-6-luna", "gpt-6-sol"} {
		model := maxThinkingCatalogModel(t, "openai-codex", modelID)
		if model.ThinkingLevelMap[types.ModelThinkingXHigh] == nil || *model.ThinkingLevelMap[types.ModelThinkingXHigh] != "xhigh" {
			t.Errorf("%s xhigh mapping missing", modelID)
		}
		if model.ThinkingLevelMap[types.ModelThinkingMax] == nil || *model.ThinkingLevelMap[types.ModelThinkingMax] != "max" {
			t.Errorf("%s max mapping missing", modelID)
		}
		levels := ai.GetSupportedThinkingLevels(&model)
		want := []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
		if len(levels) != len(want) {
			t.Fatalf("%s levels = %v, want %v", modelID, levels, want)
		}
		for i, level := range levels {
			if string(level) != want[i] {
				t.Fatalf("%s levels = %v, want %v", modelID, levels, want)
			}
		}
	}
}

// TestMaxThinkingSupportsHoleBetweenHighAndMax ports "supports a hole between
// high and max": xhigh maps to null, so xhigh clamps up to max.
func TestMaxThinkingSupportsHoleBetweenHighAndMax(t *testing.T) {
	model := &types.Model{
		ID:        "high-and-max",
		Name:      "High and Max",
		API:       types.ApiOpenAICompletions,
		Provider:  "test",
		BaseURL:   "https://example.com/v1",
		Reasoning: true,
		ThinkingLevelMap: types.ThinkingLevelMap{
			types.ModelThinkingXHigh: nil,
			types.ModelThinkingMax:   responsesStrPtr("max"),
		},
		Input:         []types.ModelInput{types.ModelInputText},
		Cost:          types.ModelCost{},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
	levels := ai.GetSupportedThinkingLevels(model)
	want := []string{"off", "minimal", "low", "medium", "high", "max"}
	if len(levels) != len(want) {
		t.Fatalf("levels = %v, want %v", levels, want)
	}
	for i, level := range levels {
		if string(level) != want[i] {
			t.Fatalf("levels = %v, want %v", levels, want)
		}
	}
	if got := ai.ClampThinkingLevel(model, types.ModelThinkingXHigh); got != types.ModelThinkingMax {
		t.Fatalf("clamp(xhigh) = %s, want max", got)
	}
}

// TestMaxThinkingSendsMaxToCodexResponsesAPI ports "sends max to the Codex
// Responses API for %s".
func TestMaxThinkingSendsMaxToCodexResponsesAPI(t *testing.T) {
	for _, modelID := range []string{"gpt-5.6-sol", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna"} {
		modelID := modelID
		t.Run(modelID, func(t *testing.T) {
			model := maxThinkingCatalogModel(t, "openai-codex", modelID)
			llmContext := utils.NormalizeContext(&types.Context{
				SystemPrompt: "You are a helpful assistant.",
				Messages:     []types.Message{codexUserMessage("Hello", time.Now().UnixMilli())},
			})
			var payload map[string]any
			options := &types.SimpleStreamOptions{Reasoning: types.ThinkingLevelMax}
			options.Transport = types.TransportSSE
			options.APIKey = codexMockToken("acc_test")
			options.Fetch = func(req *http.Request) (*http.Response, error) {
				payload = codexDecodeRequestBody(t, req)
				return codexSSEResponse(codexHelloSSE()), nil
			}
			result := OpenAICodexResponses{}.StreamSimple(gocontext.Background(), &model, llmContext, options).Result()
			if result.StopReason != types.StopReasonStop {
				t.Fatalf("%s: stopReason = %v (%s)", modelID, result.StopReason, result.ErrorMessage)
			}
			reasoning, ok := payload["reasoning"].(map[string]any)
			if !ok || reasoning["effort"] != "max" || reasoning["summary"] != "auto" {
				t.Fatalf("%s: reasoning = %v, want effort max summary auto", modelID, payload["reasoning"])
			}
		})
	}
}
