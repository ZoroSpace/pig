// pi: test/openrouter-user-models.test.ts.
package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
)

func openRouterChatModel(id string) ai.Model {
	return ai.Model{
		ID:            id,
		Name:          id,
		API:           ai.ApiOpenAICompletions,
		Provider:      ai.ProviderOpenRouter,
		BaseURL:       openRouterBaseURL,
		Type:          ai.ModelTypeChat,
		Input:         []ai.ModelInput{ai.ModelInputText},
		Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 1, Output: 2, CacheRead: 0.1}},
		ContextWindow: 200000,
		MaxTokens:     50000,
	}
}

func openRouterRefreshContext() *ai.RefreshModelsContext {
	return &ai.RefreshModelsContext{
		Credential:   &auth.ApiKeyCredential{Key: "sk-or-test"},
		AllowNetwork: true,
		Context:      context.Background(),
	}
}

// stubOpenRouterUserModels replaces the package transport for one test (pi:
// vi.spyOn(globalThis, "fetch")).
func stubOpenRouterUserModels(t *testing.T, status int, body string) {
	t.Helper()
	previous := openRouterFetch
	t.Cleanup(func() { openRouterFetch = previous })
	openRouterFetch = func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	}
}

func TestPersonalizeOpenRouterCatalogKeepsKeysChatModels(t *testing.T) {
	data := []map[string]any{
		{
			"id":             "listed",
			"context_length": 1000000,
			"top_provider":   map[string]any{"context_length": 262144, "max_completion_tokens": 32768},
			"pricing":        map[string]any{"prompt": "0.000003", "completion": "0.000015"},
		},
		// A missing limit keeps the catalog value, so generator fixes for
		// missing OpenRouter data survive.
		{"id": "no-limits", "context_length": 0, "top_provider": map[string]any{"context_length": nil, "max_completion_tokens": nil}},
		// Router models report -1, meaning the price depends on the model they pick.
		{"id": "router", "pricing": map[string]any{"prompt": "-1", "completion": "-1"}},
	}
	body, _ := json.Marshal(map[string]any{"data": data})
	stubOpenRouterUserModels(t, 200, string(body))

	imageModel := ai.Model{Type: ai.ModelTypeImage, ID: "image", Name: "image", API: ai.ImageApiOpenRouter, Provider: ai.ProviderOpenRouter, BaseURL: openRouterBaseURL, Input: []ai.ModelInput{ai.ModelInputText}, Output: []ai.ModelInput{ai.ModelInputImage}}
	classifierModel := ai.Model{Type: ai.ModelTypeClassifier, ID: "classifier", Name: "classifier", API: ai.ClassifierApiTypesafeSystemOne, Provider: ai.ProviderOpenRouter, BaseURL: openRouterBaseURL, Input: []ai.ModelInput{ai.ModelInputText}, ContextWindow: 64000}
	catalog := []ai.Model{
		openRouterChatModel("listed"),
		openRouterChatModel("no-limits"),
		openRouterChatModel("router"),
		openRouterChatModel("blocked"),
		imageModel,
		classifierModel,
	}

	result, err := PersonalizeOpenRouterCatalog(catalog, openRouterRefreshContext())
	if err != nil {
		t.Fatalf("PersonalizeOpenRouterCatalog: %v", err)
	}
	ids := make([]string, len(result))
	for i, model := range result {
		ids[i] = model.ID
	}
	wantIDs := []string{"listed", "no-limits", "router", "image", "classifier"}
	gotIDs, _ := json.Marshal(ids)
	wantIDsJSON, _ := json.Marshal(wantIDs)
	if string(gotIDs) != string(wantIDsJSON) {
		t.Fatalf("ids = %s, want %s", gotIDs, wantIDsJSON)
	}
	if result[0].ContextWindow != 262144 || result[0].MaxTokens != 32768 {
		t.Errorf("listed limits = (%d, %d), want (262144, 32768)", result[0].ContextWindow, result[0].MaxTokens)
	}
	wantCost := ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 3, Output: 15}}
	if result[0].Cost.ModelCostRates != wantCost.ModelCostRates {
		t.Errorf("listed cost = %+v, want %+v", result[0].Cost.ModelCostRates, wantCost.ModelCostRates)
	}
	if result[1].ContextWindow != 200000 || result[1].MaxTokens != 50000 || result[1].Cost.ModelCostRates != openRouterChatModel("x").Cost.ModelCostRates {
		t.Errorf("no-limits kept wrong catalog values: %+v", result[1])
	}
	if result[2].Cost.ModelCostRates != (ai.ModelCostRates{}) {
		t.Errorf("router cost = %+v, want zero", result[2].Cost.ModelCostRates)
	}
	// /models/user lists text models only, so image and classifier models are
	// not filtered.
	if result[3].ID != imageModel.ID || result[3].Type != ai.ModelTypeImage {
		t.Errorf("image model = %+v", result[3])
	}
	if result[4].ID != classifierModel.ID || result[4].Type != ai.ModelTypeClassifier {
		t.Errorf("classifier model = %+v", result[4])
	}
}

func TestPersonalizeOpenRouterCatalogReturnsCatalogOnRejectedKey(t *testing.T) {
	for _, status := range []int{401, 403, 404} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			stubOpenRouterUserModels(t, status, "")
			catalog := []ai.Model{openRouterChatModel("a")}
			result, err := PersonalizeOpenRouterCatalog(catalog, openRouterRefreshContext())
			if err != nil {
				t.Fatalf("PersonalizeOpenRouterCatalog: %v", err)
			}
			if len(result) != 1 || result[0].ID != "a" {
				t.Fatalf("result = %+v, want unchanged catalog", result)
			}
		})
	}
}
