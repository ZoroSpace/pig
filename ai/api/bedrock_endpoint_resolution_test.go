// pi: test/bedrock-endpoint-resolution.test.ts and
// test/bedrock-credentials.test.ts. The credential-priority cases ride here
// because they share the config-capture helper.
package api

import (
	"context"
	"os"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// resetBedrockEnv clears the AWS env vars pi's beforeEach deletes, restoring
// them afterwards.
func resetBedrockEnv(t *testing.T) {
	t.Helper()
	keys := []string{"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}
	original := map[string]string{}
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			original[key] = value
		}
		os.Unsetenv(key)
	}
	t.Cleanup(func() {
		for _, key := range keys {
			os.Unsetenv(key)
		}
		for key, value := range original {
			os.Setenv(key, value)
		}
	})
}

// captureBedrockClientConfig drives a stream to completion against the mock
// seam and returns the config the client was constructed with (pi:
// captureClientConfig).
func captureBedrockClientConfig(t *testing.T, model *types.Model, options *BedrockOptions) bedrockClientConfig {
	t.Helper()
	mock := &bedrockMockClient{}
	mock.install(t)
	streamOptions := types.StreamOptions{CacheRetention: types.CacheRetentionNone}
	if options != nil {
		streamOptions = options.StreamOptions
	}
	opts := &BedrockOptions{StreamOptions: streamOptions}
	if options != nil {
		*opts = *options
		opts.StreamOptions = streamOptions
	}
	s := streamBedrockConverse(context.Background(), model, bedrockTestContext(), opts)
	s.Result()
	if len(mock.configs) != 1 {
		t.Fatalf("expected 1 client construction, got %d", len(mock.configs))
	}
	return mock.lastConfig()
}

func bedrockTestContext() *types.TranscriptContext {
	return utils.NormalizeContext(&types.Context{Messages: []types.Message{types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{String: "hello"},
		Timestamp: 1,
	}}})
}

func TestBedrockEndpointResolution(t *testing.T) {
	resetBedrockEnv(t)

	t.Run("assigns eu-central-1 runtime URLs to built-in EU inference profiles", func(t *testing.T) {
		model, ok := ai.MODELS["amazon-bedrock"]["eu.anthropic.claude-sonnet-4-5-20250929-v1:0"]
		if !ok {
			t.Fatal("expected the EU Claude Sonnet 4.5 model in the builtin catalog")
		}
		if model.BaseURL != "https://bedrock-runtime.eu-central-1.amazonaws.com" {
			t.Fatalf("baseUrl = %q", model.BaseURL)
		}
	})

	t.Run("does not pin standard AWS endpoints when AWS_REGION is configured", func(t *testing.T) {
		t.Setenv("AWS_REGION", "us-east-2")
		model := ai.MODELS["amazon-bedrock"]["us.anthropic.claude-opus-4-8"]

		config := captureBedrockClientConfig(t, &model, nil)

		if config.Region != "us-east-2" {
			t.Fatalf("region = %q, want us-east-2", config.Region)
		}
		if config.Endpoint != "" {
			t.Fatalf("endpoint = %q, want unset", config.Endpoint)
		}
	})

	t.Run("derives region from a built-in EU endpoint when no region or profile is configured", func(t *testing.T) {
		model := ai.MODELS["amazon-bedrock"]["eu.anthropic.claude-sonnet-4-5-20250929-v1:0"]

		config := captureBedrockClientConfig(t, &model, nil)

		if config.Endpoint != "https://bedrock-runtime.eu-central-1.amazonaws.com" {
			t.Fatalf("endpoint = %q", config.Endpoint)
		}
		if config.Region != "eu-central-1" {
			t.Fatalf("region = %q, want eu-central-1", config.Region)
		}
	})

	t.Run("handles missing regions for explicit, scoped, and ambient profiles", func(t *testing.T) {
		model := ai.MODELS["amazon-bedrock"]["eu.anthropic.claude-sonnet-4-5-20250929-v1:0"]

		config := captureBedrockClientConfig(t, &model, &BedrockOptions{Profile: "bedrock-profile"})
		if config.Profile != "bedrock-profile" {
			t.Fatalf("profile = %q", config.Profile)
		}
		if config.Endpoint != "https://bedrock-runtime.eu-central-1.amazonaws.com" {
			t.Fatalf("endpoint = %q", config.Endpoint)
		}
		if config.Region != "eu-central-1" {
			t.Fatalf("region = %q", config.Region)
		}

		config = captureBedrockClientConfig(t, &model, &BedrockOptions{
			StreamOptions: types.StreamOptions{
				ProviderRequestOptions: types.ProviderRequestOptions{Env: types.ProviderEnv{"AWS_PROFILE": "scoped-bedrock-profile"}},
			},
		})
		if config.Profile != "scoped-bedrock-profile" {
			t.Fatalf("profile = %q", config.Profile)
		}
		if config.Endpoint != "https://bedrock-runtime.eu-central-1.amazonaws.com" {
			t.Fatalf("endpoint = %q", config.Endpoint)
		}
		if config.Region != "eu-central-1" {
			t.Fatalf("region = %q", config.Region)
		}

		t.Setenv("AWS_PROFILE", "ambient-bedrock-profile")
		config = captureBedrockClientConfig(t, &model, nil)
		if config.Profile != "ambient-bedrock-profile" {
			t.Fatalf("profile = %q", config.Profile)
		}
		if config.Endpoint != "" {
			t.Fatalf("endpoint = %q, want unset", config.Endpoint)
		}
		if config.Region != "" {
			t.Fatalf("region = %q, want unset (SDK default chain)", config.Region)
		}
	})

	t.Run("still passes custom Bedrock endpoints through to the SDK client", func(t *testing.T) {
		t.Setenv("AWS_REGION", "us-west-2")
		model := ai.MODELS["amazon-bedrock"]["us.anthropic.claude-opus-4-8"]
		model.BaseURL = "https://bedrock-vpc.example.com"

		config := captureBedrockClientConfig(t, &model, nil)

		if config.Endpoint != "https://bedrock-vpc.example.com" {
			t.Fatalf("endpoint = %q", config.Endpoint)
		}
		if config.Region != "us-west-2" {
			t.Fatalf("region = %q", config.Region)
		}
	})

	t.Run("extracts region from inference profile ARN regardless of AWS_REGION", func(t *testing.T) {
		t.Setenv("AWS_REGION", "us-east-1")
		model := ai.MODELS["amazon-bedrock"]["us.anthropic.claude-opus-4-8"]
		model.ID = "arn:aws:bedrock:us-west-2:123456789012:application-inference-profile/abc123"

		config := captureBedrockClientConfig(t, &model, nil)

		if config.Region != "us-west-2" {
			t.Fatalf("region = %q, want us-west-2 (ARN wins)", config.Region)
		}
	})

	t.Run("extracts region from GovCloud inference profile ARN", func(t *testing.T) {
		t.Setenv("AWS_REGION", "us-east-1")
		model := ai.MODELS["amazon-bedrock"]["us.anthropic.claude-opus-4-8"]
		model.ID = "arn:aws-us-gov:bedrock:us-gov-west-1:123456789012:application-inference-profile/abc123"

		config := captureBedrockClientConfig(t, &model, nil)

		if config.Region != "us-gov-west-1" {
			t.Fatalf("region = %q", config.Region)
		}
	})

	t.Run("preserves ambient AWS auth for custom model IDs through compat dispatch", func(t *testing.T) {
		t.Setenv("AWS_PROFILE", "bedrock-profile")
		model := ai.MODELS["amazon-bedrock"]["us.anthropic.claude-opus-4-8"]
		model.ID = "arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/example"

		config := captureBedrockClientConfig(t, &model, nil)

		if config.Profile != "bedrock-profile" {
			t.Fatalf("profile = %q", config.Profile)
		}
		if config.Token != "" {
			t.Fatalf("token = %q, want unset", config.Token)
		}
		if len(config.AuthSchemePreference) != 0 {
			t.Fatalf("authSchemePreference = %v, want unset", config.AuthSchemePreference)
		}
	})

	t.Run("uses the generic API key option as a Bedrock bearer token", func(t *testing.T) {
		model := ai.MODELS["amazon-bedrock"]["us.anthropic.claude-opus-4-8"]

		streamOptions := types.StreamOptions{
			ProviderRequestOptions: types.ProviderRequestOptions{APIKey: "bedrock-api-key"},
			CacheRetention:         types.CacheRetentionNone,
		}
		config := captureBedrockClientConfig(t, &model, &BedrockOptions{StreamOptions: streamOptions})

		if config.Token != "bedrock-api-key" {
			t.Fatalf("token = %q", config.Token)
		}
		if len(config.AuthSchemePreference) != 1 || config.AuthSchemePreference[0] != "httpBearerAuth" {
			t.Fatalf("authSchemePreference = %v", config.AuthSchemePreference)
		}
	})
}

func TestBedrockCredentialPriority(t *testing.T) {
	resetBedrockEnv(t)

	t.Run("prefers explicit and scoped profiles over ambient AWS access keys", func(t *testing.T) {
		t.Setenv("AWS_ACCESS_KEY_ID", "AKIAEXAMPLE")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "secretexample")
		model := ai.MODELS["amazon-bedrock"]["us.anthropic.claude-opus-4-8"]

		config := captureBedrockClientConfig(t, &model, &BedrockOptions{Profile: "explicit-profile"})
		if config.Profile != "explicit-profile" {
			t.Fatalf("profile = %q", config.Profile)
		}
		if config.Credentials != nil {
			t.Fatalf("credentials = %+v, want unset (profile wins)", config.Credentials)
		}

		config = captureBedrockClientConfig(t, &model, &BedrockOptions{
			StreamOptions: types.StreamOptions{
				ProviderRequestOptions: types.ProviderRequestOptions{Env: types.ProviderEnv{"AWS_PROFILE": "scoped-profile"}},
			},
		})
		if config.Profile != "scoped-profile" {
			t.Fatalf("profile = %q", config.Profile)
		}
		if config.Credentials != nil {
			t.Fatalf("credentials = %+v, want unset (profile wins)", config.Credentials)
		}
	})

	t.Run("uses ambient AWS access keys when no profile is configured", func(t *testing.T) {
		t.Setenv("AWS_ACCESS_KEY_ID", "AKIAEXAMPLE")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "secretexample")
		model := ai.MODELS["amazon-bedrock"]["us.anthropic.claude-opus-4-8"]

		config := captureBedrockClientConfig(t, &model, nil)
		if config.Profile != "" {
			t.Fatalf("profile = %q, want unset", config.Profile)
		}
		if config.Credentials == nil || config.Credentials.AccessKeyID != "AKIAEXAMPLE" || config.Credentials.SecretAccessKey != "secretexample" {
			t.Fatalf("credentials = %+v", config.Credentials)
		}
	})

	t.Run("uses ambient AWS access keys when only an ambient profile is set", func(t *testing.T) {
		t.Setenv("AWS_ACCESS_KEY_ID", "AKIAEXAMPLE")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "secretexample")
		t.Setenv("AWS_PROFILE", "ambient-profile")
		model := ai.MODELS["amazon-bedrock"]["us.anthropic.claude-opus-4-8"]

		config := captureBedrockClientConfig(t, &model, nil)
		if config.Profile != "ambient-profile" {
			t.Fatalf("profile = %q", config.Profile)
		}
		if config.Credentials == nil || config.Credentials.AccessKeyID != "AKIAEXAMPLE" || config.Credentials.SecretAccessKey != "secretexample" {
			t.Fatalf("credentials = %+v", config.Credentials)
		}
	})
}
