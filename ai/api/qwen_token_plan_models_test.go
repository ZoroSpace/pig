// pi: test/qwen-token-plan-models.test.ts — catalog shape plus the Qwen
// thinking-field wire format (enable_thinking / reasoning_effort, never
// thinking). pi mocks the OpenAI SDK; pig serves a mock SSE endpoint and
// points the catalog model's baseUrl at it (established pig equivalent).
package api

import (
	"context"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

const qwenTestCatalogModel = "qwen-token-plan"

func qwenCatalogModel(t *testing.T, provider types.ProviderID, modelID string) *types.Model {
	t.Helper()
	models, ok := ai.MODELS[provider]
	if !ok {
		t.Fatalf("provider %s missing from the generated catalog", provider)
	}
	model, ok := models[modelID]
	if !ok {
		t.Fatalf("model %s/%s missing from the generated catalog", provider, modelID)
	}
	return &model
}

func qwenCatalogModelIDs(t *testing.T, provider types.ProviderID) []string {
	t.Helper()
	models, ok := ai.MODELS[provider]
	if !ok {
		t.Fatalf("provider %s missing from the generated catalog", provider)
	}
	ids := make([]string, 0, len(models))
	for id := range models {
		ids = append(ids, id)
	}
	return ids
}

func TestQwenTokenPlanIndividualExposesExactlyDocumentedTextModels(t *testing.T) {
	// pi #9021: the Individual view is a strict allowlist.
	want := []string{
		"deepseek-v4-flash-0731",
		"deepseek-v4-pro",
		"deepseek-v4-pro-0813",
		"glm-5.2",
		"qwen3.6-flash",
		"qwen3.7-max",
		"qwen3.7-plus",
		"qwen3.8-flash",
		"qwen3.8-max",
	}
	got := qwenCatalogModelIDs(t, types.ProviderQwenTokenPlanInd)
	if len(got) != len(want) {
		t.Fatalf("individual catalog = %v, want %v", got, want)
	}
	for _, id := range want {
		if !containsString(got, id) {
			t.Errorf("individual catalog missing %s (got %v)", id, got)
		}
	}
}

func TestQwenTokenPlanIndividualReusesInternationalEnvVar(t *testing.T) {
	keys := ai.FindEnvKeys(types.ProviderQwenTokenPlanInd, types.ProviderEnv{"QWEN_TOKEN_PLAN_API_KEY": "test"})
	if len(keys) != 1 || keys[0] != "QWEN_TOKEN_PLAN_API_KEY" {
		t.Errorf("FindEnvKeys = %v, want [QWEN_TOKEN_PLAN_API_KEY]", keys)
	}
}

func TestQwenTokenPlanExposesTextModels(t *testing.T) {
	textModels := []string{
		"MiniMax-M2.5",
		"deepseek-v3.2",
		"deepseek-v4-flash",
		"deepseek-v4-pro",
		"glm-5",
		"glm-5.1",
		"glm-5.2",
		"kimi-k2.5",
		"kimi-k2.6",
		"kimi-k2.7-code",
		"qwen3.6-flash",
		"qwen3.6-plus",
		"qwen3.7-max",
		"qwen3.7-plus",
		"qwen3.8-flash",
		"qwen3.8-max",
	}
	for _, provider := range []types.ProviderID{types.ProviderQwenTokenPlan, types.ProviderQwenTokenPlanCN} {
		ids := qwenCatalogModelIDs(t, provider)
		for _, expected := range textModels {
			if !containsString(ids, expected) {
				t.Errorf("%s should include %s (got %v)", provider, expected, ids)
			}
		}
	}
}

func TestQwenTokenPlanOmitsImageModels(t *testing.T) {
	imageModels := []string{"qwen-image-2.0", "qwen-image-2.0-pro", "wan2.7-image", "wan2.7-image-pro"}
	for _, provider := range []types.ProviderID{types.ProviderQwenTokenPlan, types.ProviderQwenTokenPlanCN} {
		ids := qwenCatalogModelIDs(t, provider)
		for _, excluded := range imageModels {
			if containsString(ids, excluded) {
				t.Errorf("%s should not include %s", provider, excluded)
			}
		}
	}
}

func TestQwenTokenPlanOmitsRetiredPreview(t *testing.T) {
	for _, provider := range []types.ProviderID{types.ProviderQwenTokenPlan, types.ProviderQwenTokenPlanCN, types.ProviderQwenTokenPlanInd} {
		if containsString(qwenCatalogModelIDs(t, provider), "qwen3.8-max-preview") {
			t.Errorf("%s should not include the retired qwen3.8-max-preview", provider)
		}
	}
}

// qwenExpectThinkingPayload asserts enable_thinking at the top level and no
// thinking field (pi: "sends Qwen thinking fields").
func qwenExpectThinkingPayload(t *testing.T, model *types.Model, reasoning types.ThinkingLevel, expectEffort string) {
	t.Helper()
	sse := "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"" + model.ID + "\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	srv, captured := newMockSSEServer(t, sse)
	model.BaseURL = srv.URL

	var payload map[string]any
	options := &types.SimpleStreamOptions{}
	options.APIKey = "test"
	options.Reasoning = reasoning
	options.OnPayload = func(received any, _ *types.Model) any {
		if typed, ok := received.(map[string]any); ok {
			payload = typed
		}
		return received
	}
	stream := ai.StreamSimple(context.Background(), model, &types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hi"}, Timestamp: 1},
		},
	}, options)
	result := stream.Result()
	if result.StopReason == types.StopReasonError {
		t.Fatalf("streamSimple failed: %s", result.ErrorMessage)
	}
	if len(*captured) == 0 {
		t.Fatal("no request captured")
	}
	if payload == nil {
		t.Fatalf("OnPayload did not receive a map: %v", (*captured)[0])
	}
	if payload["enable_thinking"] != true {
		t.Errorf("payload enable_thinking = %v, want true", payload["enable_thinking"])
	}
	if _, has := payload["thinking"]; has {
		t.Errorf("payload must not carry a thinking field: %v", payload["thinking"])
	}
	if expectEffort != "" && payload["reasoning_effort"] != expectEffort {
		t.Errorf("payload reasoning_effort = %v, want %q", payload["reasoning_effort"], expectEffort)
	}
}

func TestQwenTokenPlanSendsQwenThinkingFields(t *testing.T) {
	providers := []types.ProviderID{types.ProviderQwenTokenPlan, types.ProviderQwenTokenPlanCN}
	thinkingModels := []string{
		"deepseek-v3.2",
		"deepseek-v4-flash",
		"deepseek-v4-pro",
		"glm-5",
		"glm-5.1",
		"glm-5.2",
		"kimi-k2.5",
		"kimi-k2.6",
		"kimi-k2.7-code",
		"qwen3.6-flash",
		"qwen3.6-plus",
		"qwen3.7-max",
		"qwen3.7-plus",
		"qwen3.8-flash",
		"qwen3.8-max",
	}
	for _, provider := range providers {
		for _, modelID := range thinkingModels {
			model := qwenCatalogModel(t, provider, modelID)
			qwenExpectThinkingPayload(t, model, types.ThinkingLevelHigh, "")
		}
	}
	for _, modelID := range []string{"deepseek-v4-flash-0731", "deepseek-v4-pro", "deepseek-v4-pro-0813", "glm-5.2", "qwen3.6-flash", "qwen3.7-max", "qwen3.7-plus", "qwen3.8-flash", "qwen3.8-max"} {
		model := qwenCatalogModel(t, types.ProviderQwenTokenPlanInd, modelID)
		qwenExpectThinkingPayload(t, model, types.ThinkingLevelHigh, "")
	}
}

// qwenExpectThinkingLevelMap asserts the listed entries of the model's
// thinkingLevelMap (pi: toMatchObject — present keys, null stays null).
func qwenExpectThinkingLevelMap(t *testing.T, model *types.Model, want types.ThinkingLevelMap) {
	t.Helper()
	for level, expected := range want {
		got, present := model.ThinkingLevelMap[level]
		if !present {
			t.Errorf("%s thinkingLevelMap missing %q", model.ID, level)
			continue
		}
		if expected == nil {
			if got != nil {
				t.Errorf("%s thinkingLevelMap[%s] = %v, want null", model.ID, level, *got)
			}
		} else if got == nil || *got != *expected {
			t.Errorf("%s thinkingLevelMap[%s] = %v, want %q", model.ID, level, got, *expected)
		}
	}
}

func TestQwenTokenPlanReasoningEffortLevels(t *testing.T) {
	effortWant := types.ThinkingLevelMap{
		types.ModelThinkingMinimal: nil,
		types.ModelThinkingLow:     nil,
		types.ModelThinkingMedium:  nil,
		types.ModelThinkingHigh:    lo.ToPtr("high"),
		types.ModelThinkingXHigh:   nil,
		types.ModelThinkingMax:     lo.ToPtr("max"),
	}
	check := func(provider types.ProviderID, modelIDs []string) {
		for _, modelID := range modelIDs {
			model := qwenCatalogModel(t, provider, modelID)
			if model.ThinkingLevelMap == nil {
				t.Errorf("%s/%s has no thinkingLevelMap", provider, modelID)
				continue
			}
			qwenExpectThinkingLevelMap(t, model, effortWant)
		}
	}
	check(types.ProviderQwenTokenPlan, []string{"deepseek-v4-flash", "deepseek-v4-pro", "glm-5", "glm-5.1", "glm-5.2"})
	check(types.ProviderQwenTokenPlanCN, []string{"deepseek-v4-flash", "deepseek-v4-pro", "glm-5", "glm-5.1", "glm-5.2"})
	check(types.ProviderQwenTokenPlanInd, []string{"deepseek-v4-flash-0731", "deepseek-v4-pro", "deepseek-v4-pro-0813", "glm-5.2"})
}

func TestQwen38ReasoningEffortLevels(t *testing.T) {
	qwen38Want := types.ThinkingLevelMap{
		types.ModelThinkingMinimal: nil,
		types.ModelThinkingLow:     lo.ToPtr("low"),
		types.ModelThinkingMedium:  lo.ToPtr("medium"),
		types.ModelThinkingHigh:    nil,
		types.ModelThinkingXHigh:   lo.ToPtr("xhigh"),
		types.ModelThinkingMax:     nil,
	}
	for _, provider := range []types.ProviderID{types.ProviderQwenTokenPlan, types.ProviderQwenTokenPlanCN, types.ProviderQwenTokenPlanInd} {
		for _, modelID := range []string{"qwen3.8-flash", "qwen3.8-max"} {
			model := qwenCatalogModel(t, provider, modelID)
			if model.ThinkingLevelMap == nil {
				t.Errorf("%s/%s has no thinkingLevelMap", provider, modelID)
				continue
			}
			qwenExpectThinkingLevelMap(t, model, qwen38Want)
		}
	}
}

func TestQwenTokenPlanSendsReasoningEffort(t *testing.T) {
	check := func(provider types.ProviderID, modelIDs []string) {
		for _, modelID := range modelIDs {
			model := qwenCatalogModel(t, provider, modelID)
			qwenExpectThinkingPayload(t, model, types.ThinkingLevelHigh, "high")
		}
	}
	check(types.ProviderQwenTokenPlan, []string{"deepseek-v4-flash", "deepseek-v4-pro", "glm-5", "glm-5.1", "glm-5.2"})
	check(types.ProviderQwenTokenPlanCN, []string{"deepseek-v4-flash", "deepseek-v4-pro", "glm-5", "glm-5.1", "glm-5.2"})
	check(types.ProviderQwenTokenPlanInd, []string{"deepseek-v4-flash-0731", "deepseek-v4-pro", "deepseek-v4-pro-0813", "glm-5.2"})
}

func TestQwen38SendsXhighReasoningEffort(t *testing.T) {
	for _, provider := range []types.ProviderID{types.ProviderQwenTokenPlan, types.ProviderQwenTokenPlanCN, types.ProviderQwenTokenPlanInd} {
		for _, modelID := range []string{"qwen3.8-flash", "qwen3.8-max"} {
			model := qwenCatalogModel(t, provider, modelID)
			qwenExpectThinkingPayload(t, model, types.ThinkingLevelXHigh, "xhigh")
		}
	}
}
