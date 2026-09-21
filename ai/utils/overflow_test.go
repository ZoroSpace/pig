// pi: packages/ai/test/overflow.test.ts — isContextOverflow/isRecoverableLength
// unit coverage. The provider e2e suite (test/context-overflow.test.ts) needs
// live provider credentials and adapters that are not ported yet; it stays
// deferred with its source.
package utils

import (
	"github.com/samber/lo"

	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

func createErrorMessage(errorMessage string) types.AssistantMessage {
	return createErrorMessageWithProvider(errorMessage, "ollama")
}

// v0.86.0: the provider is parameterized for the Cerebras bodyless-error
// scoping regression (pi #9482).
func createErrorMessageWithProvider(errorMessage string, provider string) types.AssistantMessage {
	return types.AssistantMessage{
		Role:     types.RoleAssistant,
		Content:  []types.Content{},
		API:      types.ApiOpenAICompletions,
		Provider: types.ProviderID(provider),
		Model:    "qwen3.5:35b",
		Usage: types.Usage{
			Cost: types.Cost{},
		},
		StopReason:   types.StopReasonError,
		ErrorMessage: errorMessage,
		Timestamp:    0,
	}
}

func TestIsContextOverflowDetectsExplicitOllamaPromptTooLongErrors(t *testing.T) {
	message := createErrorMessage("400 `prompt too long; exceeded max context length by 100918 tokens`")
	if !IsContextOverflow(&message, 32768) {
		t.Fatal("want overflow")
	}
}

// pi v0.86.1: regression for #9805.
func TestIsContextOverflowDetectsZaiPromptTooLongErrors(t *testing.T) {
	message := createErrorMessageWithProvider(`400 {"code":"1261","message":"Prompt too long"}`, "zai")
	if !IsContextOverflow(&message, 1048576) {
		t.Fatal("want overflow")
	}
}

func TestIsContextOverflowDetectsTogetherAIContextLengthErrors(t *testing.T) {
	message := createErrorMessage(
		"400 The input (516368 tokens) is longer than the model's context length (262144 tokens).",
	)
	if !IsContextOverflow(&message, 262144) {
		t.Fatal("want overflow")
	}
}

func TestIsContextOverflowDetectsLiteLLMWrappedOpenAIMaximumContextLengthErrors(t *testing.T) {
	message := createErrorMessage(
		"Error: 503 litellm.ServiceUnavailableError: litellm.MidStreamFallbackError: litellm.APIConnectionError: APIConnectionError: OpenAIException - Requested token count exceeds the model's maximum context length of 131072 tokens.",
	)
	if !IsContextOverflow(&message, 131072) {
		t.Fatal("want overflow")
	}
}

func TestIsContextOverflowDetectsOpenAICompatibleParenthesizedMaximumContextLengthErrors(t *testing.T) {
	message := createErrorMessage(
		"Error: 400 Input length (265330) exceeds model's maximum context length (262144).",
	)
	if !IsContextOverflow(&message, 262144) {
		t.Fatal("want overflow")
	}
}

func TestIsContextOverflowDetectsOpenRouterPoolsideMaximumAllowedInputLengthErrors(t *testing.T) {
	message := createErrorMessage(
		"Provider returned error: Input length 131393 exceeds the maximum allowed input length of 131040 tokens.",
	)
	if !IsContextOverflow(&message, 131072) {
		t.Fatal("want overflow")
	}
}

func TestIsContextOverflowDetectsDS4ConfiguredContextSizeErrors(t *testing.T) {
	message := createErrorMessage(
		"400 Prompt has 256468 tokens, but the configured context size is 256000 tokens",
	)
	if !IsContextOverflow(&message, 256000) {
		t.Fatal("want overflow")
	}

	commaMessage := createErrorMessage(
		"Prompt has 5,958,968 tokens, but the configured context size is 256,000 tokens",
	)
	if !IsContextOverflow(&commaMessage, 256000) {
		t.Fatal("want overflow")
	}
}

func TestIsContextOverflowDoesNotTreatGenericNonOverflowOllamaErrorsAsOverflow(t *testing.T) {
	message := createErrorMessage("500 `model runner crashed unexpectedly`")
	if IsContextOverflow(&message, 32768) {
		t.Fatal("want no overflow")
	}
}

func TestIsContextOverflowDoesNotTreatBedrockThrottlingTooManyTokensAsOverflow(t *testing.T) {
	// Bedrock returns this for HTTP 429 rate limiting, NOT context overflow.
	// formatBedrockError uses a human-readable prefix for ThrottlingException.
	message := createErrorMessage("Throttling error: Too many tokens, please wait before trying again.")
	if IsContextOverflow(&message, 200000) {
		t.Fatal("want no overflow")
	}
}

func TestIsContextOverflowDoesNotTreatBedrockServiceUnavailableAsOverflow(t *testing.T) {
	message := createErrorMessage("Service unavailable: The service is temporarily unavailable.")
	if IsContextOverflow(&message, 200000) {
		t.Fatal("want no overflow")
	}
}

func TestIsContextOverflowDoesNotTreatGenericRateLimitErrorsAsOverflow(t *testing.T) {
	message := createErrorMessage("Rate limit exceeded, please retry after 30 seconds.")
	if IsContextOverflow(&message, 200000) {
		t.Fatal("want no overflow")
	}
}

func TestIsContextOverflowDoesNotTreatHTTP429StyleErrorsAsOverflow(t *testing.T) {
	message := createErrorMessage("Too many requests. Please slow down.")
	if IsContextOverflow(&message, 200000) {
		t.Fatal("want no overflow")
	}
}

type lengthStopOptions struct {
	input      int64
	cacheRead  int64
	output     int64
	cacheWrite int64
	api        types.Api
	provider   string
	model      string
}

func createLengthStopMessage(options lengthStopOptions) types.AssistantMessage {
	api := options.api
	if api == "" {
		api = types.ApiOpenAICompletions
	}
	provider := options.provider
	if provider == "" {
		provider = "test-provider"
	}
	model := options.model
	if model == "" {
		model = "test-model"
	}
	cacheWrite := options.cacheWrite
	return types.AssistantMessage{
		Role:     types.RoleAssistant,
		Content:  []types.Content{},
		API:      api,
		Provider: types.ProviderID(provider),
		Model:    model,
		Usage: types.Usage{
			Input:       options.input,
			Output:      options.output,
			CacheRead:   options.cacheRead,
			CacheWrite:  cacheWrite,
			TotalTokens: options.input + options.cacheRead + cacheWrite + options.output,
		},
		StopReason: types.StopReasonLength,
		Timestamp:  0,
	}
}

func TestIsContextOverflowDetectsXiaomiStyleOverflow(t *testing.T) {
	message := createLengthStopMessage(lengthStopOptions{
		input:     58,
		cacheRead: 1048512,
		output:    0,
		provider:  "xiaomi",
		model:     "mimo-v2.5-pro",
	})
	if !IsContextOverflow(&message, 1048576) {
		t.Fatal("want overflow")
	}
}

func TestIsRecoverableLengthTreatsLengthStopBelowDesiredOutputLimitAsRecoverable(t *testing.T) {
	message := createLengthStopMessage(lengthStopOptions{
		input:      3,
		cacheRead:  253584,
		cacheWrite: 25554,
		output:     16,
		api:        types.Api("openai-responses"),
		provider:   "openai",
		model:      "gpt-5.6-sol",
	})
	if !IsRecoverableLength(&message, 128000) {
		t.Fatal("want recoverable")
	}
}

func TestIsRecoverableLengthDoesNotRecoverLengthStopThatReachedDesiredOutputLimit(t *testing.T) {
	message := createLengthStopMessage(lengthStopOptions{input: 4062, cacheRead: 0, output: 1024})
	if IsRecoverableLength(&message, 1024) {
		t.Fatal("want not recoverable")
	}
}

func TestIsRecoverableLengthTreatsZeroOutputLengthStopsAsRecoverableWithoutContextMetadata(t *testing.T) {
	message := createLengthStopMessage(lengthStopOptions{input: 100, cacheRead: 0, output: 0})
	if !IsRecoverableLength(&message, 128000) {
		t.Fatal("want recoverable")
	}
}

func TestIsContextOverflowDoesNotTreatNormalLengthStopsWithOutputAsContextOverflow(t *testing.T) {
	message := createLengthStopMessage(lengthStopOptions{input: 1000, cacheRead: 0, output: 4096})
	if IsContextOverflow(&message, 200000) {
		t.Fatal("want no overflow")
	}
}

func TestIsContextOverflowDoesNotTreatZeroOutputLengthStopsFarBelowContextAsContextOverflow(t *testing.T) {
	message := createLengthStopMessage(lengthStopOptions{input: 100, cacheRead: 0, output: 0})
	if IsContextOverflow(&message, 200000) {
		t.Fatal("want no overflow")
	}
}

// pig-only additions (Volcengine ark / Coding Plan; the
// pi-provider-volcengine-codingplan extension normalized these via a
// message_end hook, pig detects them directly).

func TestIsContextOverflowDetectsVolcengineArkOutOfContextError(t *testing.T) {
	message := createErrorMessage(
		"Error: request failed: [200] OutofContextError: The context length of the model exceeds the limit.",
	)
	if !IsContextOverflow(&message, 262144) {
		t.Fatal("want overflow")
	}
}

func TestIsContextOverflowDetectsVolcengineArkExceedMaxMessageTokens(t *testing.T) {
	message := createErrorMessage(
		"Error: request failed: exceed max message tokens limit",
	)
	if !IsContextOverflow(&message, 262144) {
		t.Fatal("want overflow")
	}
}

func TestIsContextOverflowScopesBodylessErrorsToCerebras(t *testing.T) {
	// Regression for pi #9482: bodyless 400/413 are overflow errors only for
	// Cerebras; other providers report a bodyless request failure that must
	// not be treated as overflow.
	for _, errorMessage := range []string{"400 status code (no body)", "413 status code (no body)"} {
		if !IsContextOverflow(lo.ToPtr(createErrorMessageWithProvider(errorMessage, "cerebras")), 131072) {
			t.Errorf("%s (cerebras): want overflow", errorMessage)
		}
		if IsContextOverflow(lo.ToPtr(createErrorMessageWithProvider(errorMessage, "opencode-go")), 1000000) {
			t.Errorf("%s (opencode-go): want no overflow", errorMessage)
		}
	}
}
