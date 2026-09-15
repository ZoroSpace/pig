// pi: test/bedrock-thinking-payload.test.ts.
package api

import (
	"os"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

type bedrockThinkingPayload struct {
	AdditionalModelRequestFields map[string]any `json:"additionalModelRequestFields,omitempty"`
}

// captureBedrockThinkingPayload drives a stream whose onPayload captures the
// command input, with the request pre-aborted so nothing reaches the wire
// (pi: capturePayload + PayloadCaptured throw).
func captureBedrockThinkingPayload(t *testing.T, model *types.Model, options *BedrockOptions) *bedrockConverseInput {
	t.Helper()
	var captured *bedrockConverseInput
	streamOptions := types.StreamOptions{CacheRetention: types.CacheRetentionNone}
	streamOptions.OnPayload = func(payload any, _ *types.Model) any {
		captured = payload.(*bedrockConverseInput)
		return payload
	}
	opts := &BedrockOptions{StreamOptions: streamOptions}
	if options != nil {
		*opts = *options
		opts.StreamOptions = streamOptions
	}
	if opts.Reasoning == "" {
		opts.Reasoning = "high"
	}
	s := streamBedrockConverse(alreadyCancelledContext(), model, bedrockTestContext(), opts)
	for event := range s.Events() {
		if event.Type == types.EventError {
			break
		}
	}
	if captured == nil {
		t.Fatal("Expected Bedrock payload to be captured before request abort")
	}
	return captured
}

// bedrockOpusModel re-reads the catalog Opus 4.6 profile and retargets it to
// Opus 4.8 (pi: getModel + spread).
func bedrockOpusModel(t *testing.T, id string, name string) *types.Model {
	t.Helper()
	base, ok := ai.MODELS["amazon-bedrock"]["global.anthropic.claude-opus-4-6-v1"]
	if !ok {
		t.Fatal("expected global.anthropic.claude-opus-4-6-v1 in the builtin catalog")
	}
	base.ID = id
	base.Name = name
	return &base
}

func TestBedrockThinkingPayload(t *testing.T) {
	resetBedrockEnv(t)

	adaptiveFields := func(t *testing.T, payload *bedrockConverseInput) (thinking map[string]any, outputConfig map[string]any, anthropicBeta []string) {
		t.Helper()
		if payload.AdditionalModelRequestFields == nil {
			t.Fatal("expected additionalModelRequestFields")
		}
		thinking, _ = payload.AdditionalModelRequestFields["thinking"].(map[string]any)
		outputConfig, _ = payload.AdditionalModelRequestFields["output_config"].(map[string]any)
		if raw, ok := payload.AdditionalModelRequestFields["anthropic_beta"].([]string); ok {
			anthropicBeta = raw
		}
		return thinking, outputConfig, anthropicBeta
	}

	t.Run("uses adaptive thinking for Claude Opus 4.8 when reasoning is enabled", func(t *testing.T) {
		model := bedrockOpusModel(t, "global.anthropic.claude-opus-4-8-v1", "Claude Opus 4.8 (Global)")
		payload := captureBedrockThinkingPayload(t, model, nil)

		thinking, outputConfig, beta := adaptiveFields(t, payload)
		if len(thinking) != 2 || thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
			t.Fatalf("thinking = %v, want {adaptive, summarized}", thinking)
		}
		if len(outputConfig) != 1 || outputConfig["effort"] != "high" {
			t.Fatalf("output_config = %v, want {effort: high}", outputConfig)
		}
		if beta != nil {
			t.Fatalf("anthropic_beta = %v, want unset", beta)
		}
	})

	t.Run("maps xhigh reasoning to effort=xhigh for Claude Opus 4.8", func(t *testing.T) {
		model := bedrockOpusModel(t, "global.anthropic.claude-opus-4-8-v1", "Claude Opus 4.8 (Global)")
		payload := captureBedrockThinkingPayload(t, model, &BedrockOptions{Reasoning: "xhigh"})

		thinking, outputConfig, beta := adaptiveFields(t, payload)
		if len(thinking) != 2 || thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
			t.Fatalf("thinking = %v", thinking)
		}
		if outputConfig["effort"] != "xhigh" {
			t.Fatalf("output_config = %v, want {effort: xhigh}", outputConfig)
		}
		if beta != nil {
			t.Fatalf("anthropic_beta = %v, want unset", beta)
		}
	})

	t.Run("uses adaptive thinking for Claude Fable 5 when reasoning is enabled", func(t *testing.T) {
		model, ok := ai.MODELS["amazon-bedrock"]["global.anthropic.claude-fable-5"]
		if !ok {
			t.Fatal("expected global.anthropic.claude-fable-5 in the builtin catalog")
		}
		payload := captureBedrockThinkingPayload(t, &model, nil)

		thinking, outputConfig, beta := adaptiveFields(t, payload)
		if len(thinking) != 2 || thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
			t.Fatalf("thinking = %v", thinking)
		}
		if len(outputConfig) != 1 || outputConfig["effort"] != "high" {
			t.Fatalf("output_config = %v", outputConfig)
		}
		if beta != nil {
			t.Fatalf("anthropic_beta = %v, want unset", beta)
		}
	})

	t.Run("uses adaptive thinking for Claude Sonnet 5 when reasoning is enabled", func(t *testing.T) {
		model, ok := ai.MODELS["amazon-bedrock"]["global.anthropic.claude-sonnet-5"]
		if !ok {
			t.Fatal("expected global.anthropic.claude-sonnet-5 in the builtin catalog")
		}
		payload := captureBedrockThinkingPayload(t, &model, nil)

		thinking, outputConfig, beta := adaptiveFields(t, payload)
		if len(thinking) != 2 || thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
			t.Fatalf("thinking = %v", thinking)
		}
		if len(outputConfig) != 1 || outputConfig["effort"] != "high" {
			t.Fatalf("output_config = %v", outputConfig)
		}
		if beta != nil {
			t.Fatalf("anthropic_beta = %v, want unset", beta)
		}
	})

	t.Run("uses adaptive thinking for Claude Opus 5 when reasoning is enabled", func(t *testing.T) {
		model, ok := ai.MODELS["amazon-bedrock"]["global.anthropic.claude-opus-5"]
		if !ok {
			t.Fatal("expected global.anthropic.claude-opus-5 in the builtin catalog")
		}
		payload := captureBedrockThinkingPayload(t, &model, nil)

		thinking, outputConfig, beta := adaptiveFields(t, payload)
		if len(thinking) != 2 || thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
			t.Fatalf("thinking = %v", thinking)
		}
		if len(outputConfig) != 1 || outputConfig["effort"] != "high" {
			t.Fatalf("output_config = %v", outputConfig)
		}
		if beta != nil {
			t.Fatalf("anthropic_beta = %v, want unset", beta)
		}
	})

	t.Run("maps xhigh reasoning to effort=xhigh for Claude Opus 5", func(t *testing.T) {
		model, ok := ai.MODELS["amazon-bedrock"]["global.anthropic.claude-opus-5"]
		if !ok {
			t.Fatal("expected global.anthropic.claude-opus-5 in the builtin catalog")
		}
		payload := captureBedrockThinkingPayload(t, &model, &BedrockOptions{Reasoning: "xhigh"})

		thinking, outputConfig, _ := adaptiveFields(t, payload)
		if len(thinking) != 2 || thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
			t.Fatalf("thinking = %v", thinking)
		}
		if outputConfig["effort"] != "xhigh" {
			t.Fatalf("output_config = %v, want {effort: xhigh}", outputConfig)
		}
	})

	t.Run("maps xhigh reasoning to effort=xhigh for Claude Fable 5", func(t *testing.T) {
		model, ok := ai.MODELS["amazon-bedrock"]["global.anthropic.claude-fable-5"]
		if !ok {
			t.Fatal("expected global.anthropic.claude-fable-5 in the builtin catalog")
		}
		payload := captureBedrockThinkingPayload(t, &model, &BedrockOptions{Reasoning: "xhigh"})

		thinking, outputConfig, _ := adaptiveFields(t, payload)
		if len(thinking) != 2 || thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
			t.Fatalf("thinking = %v", thinking)
		}
		if outputConfig["effort"] != "xhigh" {
			t.Fatalf("output_config = %v, want {effort: xhigh}", outputConfig)
		}
	})

	t.Run("omits display for GovCloud model ids on non-adaptive Claude thinking", func(t *testing.T) {
		base, ok := ai.MODELS["amazon-bedrock"]["us.anthropic.claude-sonnet-4-5-20250929-v1:0"]
		if !ok {
			t.Fatal("expected the US Claude Sonnet 4.5 model in the builtin catalog")
		}
		base.ID = "us-gov.anthropic.claude-sonnet-4-5-20250929-v1:0"
		base.Name = "Claude Sonnet 4.5 (GovCloud)"

		payload := captureBedrockThinkingPayload(t, &base, nil)

		if payload.AdditionalModelRequestFields == nil {
			t.Fatal("expected additionalModelRequestFields")
		}
		thinking, _ := payload.AdditionalModelRequestFields["thinking"].(map[string]any)
		if len(thinking) != 2 || thinking["type"] != "enabled" || thinking["budget_tokens"] != int64(16384) {
			t.Fatalf("thinking = %v, want {enabled, 16384}", thinking)
		}
		beta, _ := payload.AdditionalModelRequestFields["anthropic_beta"].([]string)
		if len(beta) != 1 || beta[0] != "interleaved-thinking-2025-05-14" {
			t.Fatalf("anthropic_beta = %v", beta)
		}
	})

	t.Run("omits display for GovCloud regions on adaptive Claude thinking", func(t *testing.T) {
		model := bedrockOpusModel(t, "global.anthropic.claude-opus-4-8-v1", "Claude Opus 4.8 (Global)")

		payload := captureBedrockThinkingPayload(t, model, &BedrockOptions{Region: "us-gov-west-1"})

		thinking, outputConfig, beta := adaptiveFields(t, payload)
		if len(thinking) != 1 || thinking["type"] != "adaptive" {
			t.Fatalf("thinking = %v, want {adaptive} only (display omitted)", thinking)
		}
		if len(outputConfig) != 1 || outputConfig["effort"] != "high" {
			t.Fatalf("output_config = %v", outputConfig)
		}
		if beta != nil {
			t.Fatalf("anthropic_beta = %v, want unset", beta)
		}
	})

	t.Run("uses adaptive thinking when model.name contains the model name but ARN does not", func(t *testing.T) {
		model := bedrockOpusModel(t, "arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/my-profile", "Claude Opus 4.6")

		payload := captureBedrockThinkingPayload(t, model, nil)

		thinking, outputConfig, _ := adaptiveFields(t, payload)
		if len(thinking) != 2 || thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
			t.Fatalf("thinking = %v", thinking)
		}
		if len(outputConfig) != 1 || outputConfig["effort"] != "high" {
			t.Fatalf("output_config = %v", outputConfig)
		}
	})

	t.Run("injects cache points when model.name identifies a supported Claude model", func(t *testing.T) {
		model := bedrockOpusModel(t, "arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/my-profile", "Claude Sonnet 4.6")

		payload := captureBedrockThinkingPayloadNoReasoning(t, model)

		// System prompt should have a cache point
		if payload.System == nil || len(payload.System) != 2 {
			t.Fatalf("system = %+v, want 2 blocks", payload.System)
		}
		if payload.System[1].CachePoint == nil {
			t.Fatalf("system[1] = %+v, want a cachePoint", payload.System[1])
		}

		// Last user message should have a cache point
		last := payload.Messages[len(payload.Messages)-1]
		lastContent := last.Content[len(last.Content)-1]
		if lastContent.CachePoint == nil {
			t.Fatalf("last content = %+v, want a cachePoint", lastContent)
		}
	})

	t.Run("falls back to fixed-budget thinking for non-adaptive Claude via model.name", func(t *testing.T) {
		base, ok := ai.MODELS["amazon-bedrock"]["us.anthropic.claude-sonnet-4-5-20250929-v1:0"]
		if !ok {
			t.Fatal("expected the US Claude Sonnet 4.5 model in the builtin catalog")
		}
		base.ID = "arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/my-profile"
		base.Name = "Claude Sonnet 4.5"

		payload := captureBedrockThinkingPayload(t, &base, nil)

		if payload.AdditionalModelRequestFields == nil {
			t.Fatal("expected additionalModelRequestFields")
		}
		thinking, _ := payload.AdditionalModelRequestFields["thinking"].(map[string]any)
		if thinking["type"] != "enabled" {
			t.Fatalf("thinking = %v, want enabled", thinking)
		}
		if _, ok := thinking["budget_tokens"]; !ok {
			t.Fatalf("thinking = %v, want budget_tokens", thinking)
		}
		beta, _ := payload.AdditionalModelRequestFields["anthropic_beta"].([]string)
		if len(beta) != 1 || beta[0] != "interleaved-thinking-2025-05-14" {
			t.Fatalf("anthropic_beta = %v", beta)
		}
	})
}

// captureBedrockThinkingPayloadNoReasoning captures the payload without a
// reasoning level (pi: the cache-point test sends no reasoning).
func captureBedrockThinkingPayloadNoReasoning(t *testing.T, model *types.Model) *bedrockConverseInput {
	t.Helper()
	var captured *bedrockConverseInput
	streamOptions := types.StreamOptions{}
	streamOptions.OnPayload = func(payload any, _ *types.Model) any {
		captured = payload.(*bedrockConverseInput)
		return payload
	}
	s := streamBedrockConverse(alreadyCancelledContext(), model, &types.Context{
		SystemPrompt: "You are helpful.",
		Messages: []types.Message{types.UserMessage{
			Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1,
		}},
	}, &BedrockOptions{StreamOptions: streamOptions})
	for event := range s.Events() {
		if event.Type == types.EventError {
			break
		}
	}
	if captured == nil {
		t.Fatal("Expected Bedrock payload to be captured before request abort")
	}
	return captured
}

// TestBedrockClaudeMaxTokensE2E is pi's credential-gated E2E ("Bedrock Claude
// max tokens E2E"); it needs real AWS credentials and is skipped without them.
func TestBedrockClaudeMaxTokensE2E(t *testing.T) {
	if !bedrockHasCredentials() {
		t.Skip("requires AWS credentials (AWS_PROFILE / AWS_ACCESS_KEY_ID / AWS_BEARER_TOKEN_BEDROCK)")
	}
	t.Fatal("unreachable without credentials; run manually with real AWS credentials")
}

func bedrockHasCredentials() bool {
	return os.Getenv("AWS_PROFILE") != "" ||
		(os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AWS_SECRET_ACCESS_KEY") != "") ||
		os.Getenv("AWS_BEARER_TOKEN_BEDROCK") != ""
}
