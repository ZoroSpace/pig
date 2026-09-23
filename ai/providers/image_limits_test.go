// image_limits_test.go — pi: test/providers.test.ts v0.87.0 additions
// (f5c946480, #9631): every vision model carries the conservative default
// resize profile, known direct providers record their request-size and
// image-count limits, and gateway APIs do not inherit limits from their
// upstream compatibility.
package providers

import (
	"reflect"
	"testing"

	"github.com/ZoroSpace/pig/ai"
)

var defaultImageResizeProfile = ai.ModelImageResizeOptions{
	MaxWidth:    2000,
	MaxHeight:   2000,
	MaxBytes:    4.5 * 1024 * 1024,
	JpegQuality: 80,
}

func TestKeepsConservativeResizeProfileOnEveryVisionModel(t *testing.T) {
	count := 0
	for _, providerID := range GetBuiltinProviders() {
		for _, model := range GetBuiltinModels(providerID) {
			hasImage := false
			for _, input := range model.Input {
				if input == ai.ModelInputImage {
					hasImage = true
					break
				}
			}
			if !hasImage {
				continue
			}
			count++
			if model.InputLimits == nil || model.InputLimits.Images == nil || model.InputLimits.Images.Resize == nil {
				t.Errorf("vision model %s/%s has no resize profile", model.Provider, model.ID)
				continue
			}
			if *model.InputLimits.Images.Resize != defaultImageResizeProfile {
				t.Errorf("vision model %s/%s resize = %+v, want %+v", model.Provider, model.ID, *model.InputLimits.Images.Resize, defaultImageResizeProfile)
			}
		}
	}
	if count == 0 {
		t.Fatal("no vision models in the builtin catalog")
	}
}

func TestRecordsKnownDirectProviderImageRequestLimits(t *testing.T) {
	anthropic, ok := GetBuiltinModel("anthropic", "claude-haiku-4-5")
	if !ok {
		t.Fatal("anthropic/claude-haiku-4-5 missing")
	}
	if anthropic.InputLimits == nil || anthropic.InputLimits.MaxRequestBytes != 32*1024*1024 ||
		anthropic.InputLimits.Images == nil || anthropic.InputLimits.Images.MaxPerRequest != 100 {
		t.Fatalf("anthropic claude-haiku-4-5 inputLimits = %+v", anthropic.InputLimits)
	}
	opus, ok := GetBuiltinModel("anthropic", "claude-opus-5")
	if !ok {
		t.Fatal("anthropic/claude-opus-5 missing")
	}
	if opus.InputLimits == nil || opus.InputLimits.Images == nil || opus.InputLimits.Images.MaxPerRequest != 600 {
		t.Fatalf("anthropic claude-opus-5 inputLimits = %+v", opus.InputLimits)
	}
	bedrock, ok := GetBuiltinModel("amazon-bedrock", "anthropic.claude-haiku-4-5-20251001-v1:0")
	if !ok {
		t.Fatal("amazon-bedrock haiku missing")
	}
	if bedrock.InputLimits == nil || bedrock.InputLimits.Images == nil || bedrock.InputLimits.Images.MaxPerMessage != 20 {
		t.Fatalf("bedrock haiku inputLimits = %+v", bedrock.InputLimits)
	}
	openai, ok := GetBuiltinModel("openai", "gpt-4o")
	if !ok {
		t.Fatal("openai/gpt-4o missing")
	}
	if openai.InputLimits == nil || openai.InputLimits.MaxRequestBytes != 512*1024*1024 ||
		openai.InputLimits.Images == nil || openai.InputLimits.Images.MaxPerRequest != 1500 {
		t.Fatalf("openai gpt-4o inputLimits = %+v", openai.InputLimits)
	}
	google, ok := GetBuiltinModel("google", "gemini-2.5-flash")
	if !ok {
		t.Fatal("google/gemini-2.5-flash missing")
	}
	if google.InputLimits == nil || google.InputLimits.MaxRequestBytes != 20*1024*1024 ||
		google.InputLimits.Images == nil || google.InputLimits.Images.MaxPerRequest != 3600 {
		t.Fatalf("google gemini-2.5-flash inputLimits = %+v", google.InputLimits)
	}
}

func TestDoesNotInferImageLimitsFromGatewayAPICompatibility(t *testing.T) {
	var openRouterModel *ai.Model
	for _, model := range GetBuiltinModels("openrouter") {
		for _, input := range model.Input {
			if input == ai.ModelInputImage {
				m := model
				openRouterModel = &m
				break
			}
		}
		if openRouterModel != nil {
			break
		}
	}
	if openRouterModel == nil {
		t.Fatal("no openrouter vision model")
	}
	want := &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &defaultImageResizeProfile}}
	if !reflect.DeepEqual(openRouterModel.InputLimits, want) {
		t.Fatalf("openrouter vision model inputLimits = %+v, want %+v", openRouterModel.InputLimits, want)
	}
}
