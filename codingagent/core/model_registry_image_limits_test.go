package core

// model_registry_image_limits_test.go — pi: test/model-registry.test.ts
// v0.87.0 addition (f5c946480, #9631 regression): model overrides
// deep-merge the image resize limits from the model definition.

import (
	"reflect"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai"
)

func TestModelRegistryModelOverrideDeepMergesImageResizeLimits(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"test": map[string]any{
			"baseUrl": "https://example.com",
			"apiKey":  "test-key",
			"api":     "openai-completions",
			"models": []any{
				map[string]any{
					"id":    "vision-model",
					"input": []any{"text", "image"},
					"inputLimits": map[string]any{
						"maxRequestBytes": 32 * 1024 * 1024,
						"images": map[string]any{
							"maxPerRequest": 100,
							"resize": map[string]any{
								"maxWidth":    2000,
								"maxHeight":   2000,
								"maxBytes":    4.5 * 1024 * 1024,
								"jpegQuality": 80,
							},
						},
					},
				},
			},
			"modelOverrides": map[string]any{
				"vision-model": map[string]any{
					"inputLimits": map[string]any{
						"images": map[string]any{
							"resize": map[string]any{
								"maxWidth":    1568,
								"maxBytes":    524288,
								"jpegQuality": 75,
							},
						},
					},
				},
			},
		},
	})

	registry := h.createRegistry(t)
	if registry.GetError() != "" {
		t.Fatalf("registry error = %q", registry.GetError())
	}
	model := lo.FindOrElse(getModelsForProvider(registry, "test"), ai.Model{}, func(m ai.Model) bool { return m.ID == "vision-model" })

	want := &ai.ModelInputLimits{
		MaxRequestBytes: 32 * 1024 * 1024,
		Images: &ai.ModelImageInputLimits{
			MaxPerRequest: 100,
			Resize: &ai.ModelImageResizeOptions{
				MaxWidth:    1568,
				MaxHeight:   2000,
				MaxBytes:    524288,
				JpegQuality: 75,
			},
		},
	}
	if !reflect.DeepEqual(model.InputLimits, want) {
		t.Fatalf("inputLimits = %+v, want %+v", model.InputLimits, want)
	}
}
