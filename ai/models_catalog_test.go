package ai

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestBuiltinCatalogMatchesPiData locks the builtin model catalog against
// pi's provider data files — the JSON snapshot of the models.dev-derived
// data the catalog generator (ai/scripts/generatemodels) mirrors. Converted
// from the C4 cmd_dumpcatalog probe into a resident regression (2026-09-12):
// each Model is marshaled through its json tags and deep-compared field by
// field against the golden, so both generator output and wire-shape drift
// fail loudly.
//
// Golden files are verbatim copies of pi's
// packages/ai/src/providers/data/<provider>.json at the time of last sync.
// To extend: copy the pi data file for another builtin provider into
// testdata/ and add a table row. When upstream (models.dev) drifts, the
// golden must be re-synced as an explicit recorded decision (see C2's
// accepted codex drift) — do not loosen the comparison.
// TestBuiltinTypedCatalogsMatchPiData locks the merged image and classifier
// catalogs (v0.99.1 #9948) against the same pi data snapshots: the composite
// "{type}:{id}" keys carry the non-chat entries. Zero-value struct fields
// absent from pi's entries (reasoning/maxTokens on image and classifier
// rows) are ignored.
func TestBuiltinTypedCatalogsMatchPiData(t *testing.T) {
	for _, tt := range []struct {
		provider ProviderID
		dataFile string
		prefix   string
		got      func(ProviderID) map[string]Model
	}{
		{ProviderOpenRouter, "openrouter.json", "image:", func(p ProviderID) map[string]Model { return IMAGE_MODELS[p] }},
		{ProviderOpenRouter, "openrouter.json", "classifier:", func(p ProviderID) map[string]Model { return CLASSIFIER_MODELS[p] }},
	} {
		t.Run(string(tt.provider)+"/"+strings.TrimSuffix(tt.prefix, ":"), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", tt.dataFile))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			var doc map[string]map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("decode golden: %v", err)
			}
			want := map[string]any{}
			for _, typed := range doc {
				for composite, model := range typed {
					if id, ok := strings.CutPrefix(composite, tt.prefix); ok {
						want[id] = model
					}
				}
			}
			got := map[string]any{}
			for id, model := range tt.got(tt.provider) {
				b, err := json.Marshal(model)
				if err != nil {
					t.Fatalf("marshal model %s: %v", id, err)
				}
				var v any
				if err := json.Unmarshal(b, &v); err != nil {
					t.Fatalf("re-decode model %s: %v", id, err)
				}
				if m, ok := v.(map[string]any); ok {
					for k, val := range m {
						switch val {
						case false, float64(0):
							// pi entries simply lack these keys when the
							// zero value is a pig struct artifact.
							delete(m, k)
						}
					}
				}
				got[id] = v
			}
			var onlyPi, onlyPig []string
			for id := range want {
				if _, ok := got[id]; !ok {
					onlyPi = append(onlyPi, id)
				}
			}
			for id := range got {
				if _, ok := want[id]; !ok {
					onlyPig = append(onlyPig, id)
				}
			}
			if len(onlyPi) > 0 || len(onlyPig) > 0 {
				t.Fatalf("typed catalog id set drifted from pi\ngolden-only: %v\nbuiltin-only: %v", onlyPi, onlyPig)
			}
			for _, id := range slices.Sorted(maps.Keys(want)) {
				if !reflect.DeepEqual(want[id], got[id]) {
					wj, _ := json.Marshal(want[id])
					gj, _ := json.Marshal(got[id])
					t.Errorf("model %s drifted from pi\ngolden: %s\nbuiltin: %s", id, wj, gj)
				}
			}
		})
	}
}

func TestBuiltinCatalogMatchesPiData(t *testing.T) {
	for _, tt := range []struct {
		provider ProviderID
		// dataFile is the committed pi snapshot; topKey is the provider key
		// pi uses inside it (pi keys by api name, not always by provider id).
		dataFile string
		topKey   string
	}{
		{ProviderGoogle, "google.json", "google-generative-ai"},
		{ProviderGoogleVertex, "google-vertex.json", "google-vertex"},
		{ProviderAmazonBedrock, "amazon-bedrock.json", "bedrock-converse-stream"},
		{ProviderMeta, "meta.json", "openai-responses"},
	} {
		t.Run(string(tt.provider), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", tt.dataFile))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			// pi data file shape (v0.99.1 #9948): {<topKey>:
			// {"<type>:<modelId>": {...fields...}}} — the composite key
			// disambiguates chat/image/classifier entries sharing an id.
			var doc map[string]map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("decode golden: %v", err)
			}
			wantTyped := doc[tt.topKey]
			if wantTyped == nil {
				t.Fatalf("golden %s has no top-level key %q", tt.dataFile, tt.topKey)
			}
			want := map[string]any{}
			for composite, model := range wantTyped {
				id, ok := strings.CutPrefix(composite, "chat:")
				if !ok {
					continue
				}
				want[id] = model
			}

			// Marshal each builtin Model through its json tags, then decode
			// back to generic values so both sides are directly comparable.
			got := map[string]any{}
			for id, model := range MODELS[tt.provider] {
				b, err := json.Marshal(model)
				if err != nil {
					t.Fatalf("marshal model %s: %v", id, err)
				}
				var v any
				if err := json.Unmarshal(b, &v); err != nil {
					t.Fatalf("re-decode model %s: %v", id, err)
				}
				got[id] = v
			}

			var onlyPi, onlyPig []string
			for id := range want {
				if _, ok := got[id]; !ok {
					onlyPi = append(onlyPi, id)
				}
			}
			for id := range got {
				if _, ok := want[id]; !ok {
					onlyPig = append(onlyPig, id)
				}
			}
			if len(onlyPi) > 0 || len(onlyPig) > 0 {
				t.Fatalf("model id set drifted from pi\ngolden-only: %v\nbuiltin-only: %v", onlyPi, onlyPig)
			}

			for _, id := range slices.Sorted(maps.Keys(want)) {
				if !reflect.DeepEqual(want[id], got[id]) {
					wj, _ := json.Marshal(want[id])
					gj, _ := json.Marshal(got[id])
					t.Errorf("model %s drifted from pi\ngolden: %s\nbuiltin: %s", id, wj, gj)
				}
			}
		})
	}
}

// Regression test for pi #9209 (v0.86.0): all GitHub Copilot GPT models
// route through the Responses API (pi: model-catalog-types.test.ts "routes
// all GitHub Copilot GPT models through the Responses API").
func TestBuiltinCatalogCopilotGPTModelsUseResponsesAPI(t *testing.T) {
	models, ok := MODELS[ProviderID("github-copilot")]
	if !ok {
		t.Fatal("github-copilot missing from the builtin catalog")
	}
	count := 0
	for id, model := range models {
		if len(id) >= 4 && id[:4] == "gpt-" {
			count++
			if model.API != ApiOpenAIResponses {
				t.Errorf("copilot model %s api = %s, want %s", id, model.API, ApiOpenAIResponses)
			}
		}
	}
	if count == 0 {
		t.Fatal("no gpt-* models in the github-copilot catalog")
	}
}

func TestBuiltinCatalogDropsUpstreamRetiredModels(t *testing.T) {
	retired := map[ProviderID][]string{
		ProviderGithubCopilot: {"claude-opus-4.7", "gpt-5.4", "gpt-5.5"},
		ProviderOpenAICodex:   {"gpt-5.3-codex-spark", "gpt-5.5"},
		ProviderBaseten:       {"moonshotai/Kimi-K2.6"},
		ProviderTogether:      {"MiniMaxAI/MiniMax-M2.7"},
	}
	for provider, ids := range retired {
		models := MODELS[provider]
		for _, id := range ids {
			if _, ok := models[id]; ok {
				t.Errorf("%s/%s should have been dropped from the catalog", provider, id)
			}
		}
	}
}
