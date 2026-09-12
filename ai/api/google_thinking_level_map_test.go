// pi: test/google-thinking-level-map.test.ts.
//
// The two captureVertexPayload cases and their vertexModel fixtures arrive
// with the vertex adapter (C4).
package api

import (
	"context"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

func googleTLMContext() *types.Context {
	return &types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 0},
		},
	}
}

func googleTLMModel(id string, thinkingLevelMap types.ThinkingLevelMap) *types.Model {
	return &types.Model{
		ID:               id,
		Name:             id,
		API:              ai.ApiGoogleGenerativeAI,
		Provider:         "test-google",
		BaseURL:          "https://example.invalid/v1beta",
		Reasoning:        true,
		ThinkingLevelMap: thinkingLevelMap,
		Input:            []types.ModelInput{types.ModelInputText},
		Cost:             types.ModelCost{},
		ContextWindow:    128000,
		MaxTokens:        4096,
	}
}

// captureGooglePayload runs streamSimple with an onPayload hook that captures
// the request and then errors the stream (pi: the onPayload capture helper).
func captureGooglePayload(t *testing.T, model *types.Model, reasoning types.ThinkingLevel, thinkingBudgets *types.ThinkingBudgets) *googleGenerateContentParams {
	t.Helper()
	var payload *googleGenerateContentParams
	options := &types.SimpleStreamOptions{}
	options.APIKey = "test"
	options.Reasoning = reasoning
	options.ThinkingBudgets = thinkingBudgets
	options.OnPayload = func(request any, _ *types.Model) any {
		payload = request.(*googleGenerateContentParams)
		panic("payload captured")
	}

	stream := GoogleGenerativeAI{}.StreamSimple(context.Background(), model, googleTLMContext(), options)
	_, result := collectResponsesEvents(t, stream)
	if !strings.Contains(result.ErrorMessage, "payload captured") {
		t.Fatalf("errorMessage = %q, want it to contain the capture marker", result.ErrorMessage)
	}
	if payload == nil {
		t.Fatal("Google payload was not captured")
	}
	return payload
}

func TestGoogleThinkingLevelResolveExhaustive(t *testing.T) {
	// pi: off→high and identity mappings for the supported levels.
	defaults := map[types.ModelThinkingLevel]types.ModelThinkingLevel{
		types.ModelThinkingOff:     types.ModelThinkingHigh,
		types.ModelThinkingMinimal: types.ModelThinkingMinimal,
		types.ModelThinkingLow:     types.ModelThinkingLow,
		types.ModelThinkingMedium:  types.ModelThinkingMedium,
		types.ModelThinkingHigh:    types.ModelThinkingHigh,
	}
	model := googleTLMModel("gemini-3.7-flash", types.ThinkingLevelMap{})
	for level, expected := range defaults {
		resolved, err := ResolveGoogleThinkingLevel(model, level)
		if err != nil {
			t.Fatalf("resolve(%s) failed: %v", level, err)
		}
		if resolved != expected {
			t.Errorf("resolve(%s) = %s, want %s", level, resolved, expected)
		}
	}

	// pi: provider mapping values, upper and lower case, resolve through the
	// same switch.
	mapped := map[string]types.ModelThinkingLevel{
		"minimal": types.ModelThinkingMinimal,
		"low":     types.ModelThinkingLow,
		"medium":  types.ModelThinkingMedium,
		"high":    types.ModelThinkingHigh,
		"MINIMAL": types.ModelThinkingMinimal,
		"LOW":     types.ModelThinkingLow,
		"MEDIUM":  types.ModelThinkingMedium,
		"HIGH":    types.ModelThinkingHigh,
	}
	for mappedValue, expected := range mapped {
		model := googleTLMModel("gemini-3.7-flash", types.ThinkingLevelMap{
			types.ModelThinkingHigh:  lo.ToPtr(mappedValue),
			types.ModelThinkingXHigh: lo.ToPtr(mappedValue),
			types.ModelThinkingMax:   lo.ToPtr(mappedValue),
		})
		for _, level := range []types.ModelThinkingLevel{types.ModelThinkingHigh, types.ModelThinkingXHigh, types.ModelThinkingMax} {
			resolved, err := ResolveGoogleThinkingLevel(model, level)
			if err != nil {
				t.Fatalf("resolve(%s) failed: %v", level, err)
			}
			if resolved != expected {
				t.Errorf("resolve(%s) with map %q = %s, want %s", level, mappedValue, resolved, expected)
			}
		}
	}

	invalidModel := googleTLMModel("gemini-3.7-flash", types.ThinkingLevelMap{
		types.ModelThinkingXHigh: lo.ToPtr("extreme"),
	})
	if _, err := ResolveGoogleThinkingLevel(invalidModel, types.ModelThinkingXHigh); err == nil ||
		err.Error() != "Unsupported Google thinking level mapping for test-google/gemini-3.7-flash: xhigh -> extreme" {
		t.Errorf("err = %v, want the unsupported-mapping failure", err)
	}
	if _, err := ResolveGoogleThinkingLevel(googleTLMModel("gemini-3.7-flash", types.ThinkingLevelMap{}), types.ModelThinkingMax); err == nil ||
		err.Error() != "Unsupported Google thinking level mapping for test-google/gemini-3.7-flash: max -> undefined" {
		t.Errorf("err = %v, want the unsupported-mapping failure", err)
	}
}

func TestGoogleThinkingLevelMapsExtendedLevels(t *testing.T) {
	for _, reasoning := range []types.ThinkingLevel{"xhigh", "max"} {
		payload := captureGooglePayload(t, googleTLMModel("gemini-3.7-flash", types.ThinkingLevelMap{
			types.ModelThinkingXHigh: lo.ToPtr("high"),
			types.ModelThinkingMax:   lo.ToPtr("high"),
		}), reasoning, nil)

		tc := payload.Config.ThinkingConfig
		if tc == nil || !tc.IncludeThoughts || tc.ThinkingLevel != GoogleThinkingLevelHigh {
			t.Errorf("%s: thinkingConfig = %+v, want includeThoughts + HIGH", reasoning, tc)
		}
	}
}

func TestGoogleThinkingLevelHonorsUppercaseProviderValues(t *testing.T) {
	payload := captureGooglePayload(t, googleTLMModel("gemini-3.7-flash", types.ThinkingLevelMap{
		types.ModelThinkingHigh: lo.ToPtr("LOW"),
	}), "high", nil)

	if payload.Config.ThinkingConfig == nil || payload.Config.ThinkingConfig.ThinkingLevel != GoogleThinkingLevelLow {
		t.Errorf("thinkingConfig = %+v, want thinkingLevel LOW", payload.Config.ThinkingConfig)
	}
}

func TestGoogleThinkingLevelUsesMappedLevelsForBudgets(t *testing.T) {
	payload := captureGooglePayload(t, googleTLMModel("gemini-2.5-flash", types.ThinkingLevelMap{
		types.ModelThinkingXHigh: lo.ToPtr("high"),
	}), "xhigh", &types.ThinkingBudgets{High: lo.ToPtr(int64(1234))})

	if payload.Config.ThinkingConfig == nil || payload.Config.ThinkingConfig.ThinkingBudget == nil ||
		*payload.Config.ThinkingConfig.ThinkingBudget != 1234 {
		t.Errorf("thinkingConfig = %+v, want thinkingBudget 1234", payload.Config.ThinkingConfig)
	}
}

// TestGoogleThinkingLevelMapsVertexExtendedLevels and the vertex budget case
// arrive with the vertex adapter (C4).
