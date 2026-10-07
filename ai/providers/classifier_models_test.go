// Catalog-data assertions from pi's test/classifier-models.test.ts (v0.99.1
// #9948). The classify() runtime cases (endpoint routing, entry-point type
// rejection) are not ported: pig defers the classify() runtime (S10-1
// declared deviation, arrives with codemode if ever).
package providers

import (
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
)

func TestBuiltinClassifierCatalog(t *testing.T) {
	t.Run("exposes Jev only through classifier catalog accessors", func(t *testing.T) {
		jev, ok := GetBuiltinClassifierModel("typesafe", "jev-latest")
		if !ok {
			t.Fatal("GetBuiltinClassifierModel(typesafe, jev-latest) = not found")
		}
		if jev.Type != "classifier" || string(jev.API) != "typesafe-system-one" ||
			string(jev.Provider) != "typesafe" || jev.ContextWindow != 64000 {
			t.Fatalf("jev = %+v", jev)
		}
		list := GetBuiltinClassifierModels("typesafe")
		if len(list) != 1 || list[0].ID != "jev-latest" {
			t.Fatalf("GetBuiltinClassifierModels(typesafe) = %+v", list)
		}
		all := GetAllBuiltinModels("typesafe")
		if len(all) != 1 || all[0].ID != "jev-latest" {
			t.Fatalf("GetAllBuiltinModels(typesafe) = %+v", all)
		}

		models := BuiltinModels(nil)
		if _, ok := models.GetModel("typesafe", "jev-latest"); ok {
			t.Fatal("chat GetModel must not see classifier entries")
		}
		if got, ok := models.GetModelOfType("classifier", "typesafe", "jev-latest"); !ok || got.ID != "jev-latest" {
			t.Fatalf("GetModelOfType(classifier) = %+v, %v", got, ok)
		}
	})

	t.Run("exposes the OpenRouter Jev and image catalogs separately", func(t *testing.T) {
		classifiers := GetBuiltinClassifierModels("openrouter")
		if len(classifiers) == 0 {
			t.Fatal("openrouter classifier catalog is empty")
		}
		for _, model := range classifiers {
			if string(model.API) != "typesafe-system-one" {
				t.Fatalf("openrouter classifier %s api = %s", model.ID, model.API)
			}
			if model.BaseURL != "https://openrouter.ai/api/v1" {
				t.Fatalf("openrouter classifier %s baseUrl = %s", model.ID, model.BaseURL)
			}
		}
		images := GetBuiltinImageModels("openrouter")
		if len(images) == 0 {
			t.Fatal("openrouter image catalog is empty")
		}
		for _, model := range images {
			if model.API != ai.ImageApiOpenRouter || model.Type != "image" {
				t.Fatalf("openrouter image %s = %s/%s", model.ID, model.Type, model.API)
			}
		}
	})

	t.Run("exposes Jev through the TypeSafe-compatible gateway endpoints", func(t *testing.T) {
		cases := []struct{ provider, id string }{
			{"vercel-ai-gateway", "typesafe-ai/jev"},
			{"opencode", "jev-1.13"},
			{"opencode", "jev-1.13-free"},
			{"cloudflare-workers-ai", "typesafe/jev"},
		}
		for _, tc := range cases {
			model, ok := GetBuiltinClassifierModel(ai.ProviderID(tc.provider), tc.id)
			if !ok {
				t.Fatalf("missing %s classifier %s", tc.provider, tc.id)
			}
			if string(model.API) != "typesafe-system-one" && string(model.API) != "cloudflare-workers-ai-system-one" {
				t.Fatalf("%s/%s api = %s", tc.provider, tc.id, model.API)
			}
			if model.ContextWindow != 32000 {
				t.Fatalf("%s/%s contextWindow = %d, want 32000", tc.provider, tc.id, model.ContextWindow)
			}
			// The chat catalog must not carry classifier entries.
			if _, ok := GetBuiltinModel(ai.ProviderID(tc.provider), tc.id); ok {
				t.Fatalf("%s classifier %s leaked into the chat catalog", tc.provider, tc.id)
			}
		}
	})

	t.Run("exposes GPT-6 Luna through the Decisions classifier catalog", func(t *testing.T) {
		// Catalog assertions from pi ce8972a0e; the Decisions runtime cases
		// stay unported with the classify() runtime.
		luna, ok := GetBuiltinClassifierModel("openai", "gpt-6-luna")
		if !ok {
			t.Fatal("GetBuiltinClassifierModel(openai, gpt-6-luna) = not found")
		}
		if luna.Type != "classifier" || luna.API != ai.ClassifierApiOpenAIDecisions ||
			string(luna.Provider) != "openai" || luna.ContextWindow != 922000 {
			t.Fatalf("luna = %+v", luna)
		}
		acceptsImages := false
		for _, input := range luna.Input {
			if input == ai.ModelInputImage {
				acceptsImages = true
			}
		}
		if !acceptsImages {
			t.Fatalf("luna input = %v, want image", luna.Input)
		}
		// The chat entry with the same id stays separate (pi: the chat
		// catalog's gpt-6-luna is openai-responses).
		chat, ok := GetBuiltinModel("openai", "gpt-6-luna")
		if !ok || chat.API != ai.ApiOpenAIResponses {
			t.Fatalf("openai chat gpt-6-luna = %+v, %v; want the openai-responses chat entry", chat, ok)
		}
	})

	t.Run("hides OpenAI Decisions models from OAuth availability", func(t *testing.T) {
		// pi: "lists OpenAI Decisions models only for API key credentials" —
		// Sign in with ChatGPT tokens only reach the Responses API.
		models := []ai.Model{}
		for _, model := range GetAllBuiltinModels("openai") {
			models = append(models, model)
		}
		if len(models) == 0 {
			t.Fatal("openai mixed catalog is empty")
		}
		apiKeyFiltered := OpenAIProvider().(ai.ProviderAllModelsFilter).FilterAllModels(models, &auth.ApiKeyCredential{})
		oauthFiltered := OpenAIProvider().(ai.ProviderAllModelsFilter).FilterAllModels(models, &auth.OAuthCredential{})
		countClassifiers := func(list []ai.Model) int {
			n := 0
			for _, model := range list {
				if model.Type == "classifier" {
					n++
				}
			}
			return n
		}
		if countClassifiers(apiKeyFiltered) != 1 {
			t.Fatalf("api-key availability = %d classifier entries, want 1 (gpt-6-luna)", countClassifiers(apiKeyFiltered))
		}
		if countClassifiers(oauthFiltered) != 0 {
			t.Fatalf("oauth availability = %d classifier entries, want 0", countClassifiers(oauthFiltered))
		}
		// Chat models stay available with ChatGPT OAuth.
		if len(oauthFiltered) != len(models)-1 {
			t.Fatalf("oauth availability = %d models, want %d (all but the classifier entry)", len(oauthFiltered), len(models)-1)
		}
	})
}
