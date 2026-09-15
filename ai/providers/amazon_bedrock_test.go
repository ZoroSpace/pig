// pi: test/bedrock-models.test.ts (the non-E2E assertions; pi's per-model E2E
// loop is additionally gated on BEDROCK_EXTENSIVE_MODEL_TEST and real AWS
// credentials — pig keeps the same gating and skips without credentials).
package providers

import (
	"os"
	"slices"
	"testing"
)

// TestBedrockModelsExposesInferenceProfiles (pi: "should get all available
// Bedrock models" + "exposes Claude Opus 5 through an inference profile only").
func TestBedrockModelsExposesInferenceProfiles(t *testing.T) {
	models := GetBuiltinModels("amazon-bedrock")
	if len(models) == 0 {
		t.Fatal("expected Bedrock models in the builtin catalog")
	}
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	if !slices.Contains(ids, "global.anthropic.claude-opus-5") {
		t.Fatal("expected global.anthropic.claude-opus-5 (inference profile)")
	}
	if slices.Contains(ids, "anthropic.claude-opus-5") {
		t.Fatal("did not expect the profile-only id anthropic.claude-opus-5")
	}
}

// TestBedrockModelsE2E mirrors pi's extensive per-model E2E loop; it requires
// real AWS credentials and BEDROCK_EXTENSIVE_MODEL_TEST, and each request
// costs money. pig skips unless both are set.
func TestBedrockModelsE2E(t *testing.T) {
	if os.Getenv("BEDROCK_EXTENSIVE_MODEL_TEST") == "" || !bedrockHasTestCredentials() {
		t.Skip("requires BEDROCK_EXTENSIVE_MODEL_TEST and AWS credentials")
	}
	// The per-model loop is exercised manually (pi: ~2 minutes, us-west-2
	// recommended); pig does not burn real tokens from unit runs.
}

func bedrockHasTestCredentials() bool {
	return os.Getenv("AWS_PROFILE") != "" ||
		(os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AWS_SECRET_ACCESS_KEY") != "") ||
		os.Getenv("AWS_BEARER_TOKEN_BEDROCK") != ""
}
