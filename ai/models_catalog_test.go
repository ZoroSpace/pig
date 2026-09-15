package ai

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	} {
		t.Run(string(tt.provider), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", tt.dataFile))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			// pi data file shape: {<topKey>: {<modelId>: {...fields...}}}.
			var doc map[string]map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("decode golden: %v", err)
			}
			want := doc[tt.topKey]
			if want == nil {
				t.Fatalf("golden %s has no top-level key %q", tt.dataFile, tt.topKey)
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
