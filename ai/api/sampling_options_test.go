// pi: test/sampling-options.test.ts (#9506 additions included: model-level
// samplingParams must apply to direct stream()/complete() calls, with
// per-request keys taking precedence).
package api

import (
	gocontext "context"
	"fmt"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
	"github.com/samber/lo"
)

type samplingPayload = map[string]any

func samplingTestModel(api types.Api, samplingParams map[string]any) *types.Model {
	return &types.Model{
		ID: "custom-model", Name: "Custom Model",
		API: api, Provider: "custom-provider", BaseURL: "http://127.0.0.1:9/v1",
		Reasoning: false, Input: []types.ModelInput{types.ModelInputText},
		Cost:          types.ModelCost{},
		ContextWindow: 128000, MaxTokens: 16384,
		SamplingParams: samplingParams,
	}
}

func samplingTestContext() *types.TranscriptContext {
	return utils.NormalizeContext(&types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1},
		},
	})
}

// captureSamplingPayload drives the stream entry point matching the model's
// API with OnPayload capture; capture aborts before the request goes out and
// the unreachable port keeps retries at zero.
func captureSamplingPayload(t *testing.T, model *types.Model, options *types.StreamOptions) samplingPayload {
	t.Helper()
	var captured samplingPayload
	if options == nil {
		options = &types.StreamOptions{}
	}
	options.APIKey = "fake-key"
	options.MaxRetries = lo.ToPtr(0)
	options.OnPayload = func(payload any, model *types.Model) any {
		if m, ok := payload.(map[string]any); ok {
			captured = m
		}
		return nil
	}
	var stream *types.AssistantMessageEventStream
	switch model.API {
	case types.ApiOpenAICompletions:
		stream = (&OpenAICompletions{}).Stream(gocontext.Background(), model, samplingTestContext(), options)
	case types.ApiOpenAIResponses:
		stream = (&OpenAIResponses{}).Stream(gocontext.Background(), model, samplingTestContext(), options)
	case types.ApiAzureOpenAIResponses:
		stream = (&AzureOpenAIResponses{}).Stream(gocontext.Background(), model, samplingTestContext(), options)
	default:
		t.Fatalf("unsupported api %q", model.API)
	}
	stream.Result()
	if captured == nil {
		t.Fatal("Expected payload capture before request")
	}
	return captured
}

func captureSamplingSimple(t *testing.T, model *types.Model, options *types.SimpleStreamOptions) samplingPayload {
	t.Helper()
	var captured samplingPayload
	options.APIKey = "fake-key"
	options.MaxRetries = lo.ToPtr(0)
	options.OnPayload = func(payload any, model *types.Model) any {
		if m, ok := payload.(map[string]any); ok {
			captured = m
		}
		return nil
	}
	(&OpenAICompletions{}).StreamSimple(gocontext.Background(), model, samplingTestContext(), options).Result()
	if captured == nil {
		t.Fatal("Expected payload capture before request")
	}
	return captured
}

func TestSamplingParams(t *testing.T) {
	t.Run("merges request sampling params into the request body", func(t *testing.T) {
		payload := captureSamplingPayload(t, samplingTestModel(types.ApiOpenAICompletions, nil),
			&types.StreamOptions{SamplingParams: map[string]any{"top_p": 0.95, "top_k": 0, "min_p": 0}})
		if payload["top_p"] != 0.95 {
			t.Fatalf("payload = %#v", payload)
		}
		// JSON numbers decode as float64; the passthrough keys keep their Go
		// types, so compare numerically.
		if topK := payload["top_k"]; topK == nil || fmt.Sprint(topK) != "0" {
			t.Fatalf("top_k = %#v, want 0", payload["top_k"])
		}
		if minP := payload["min_p"]; minP == nil || fmt.Sprint(minP) != "0" {
			t.Fatalf("min_p = %#v, want 0", payload["min_p"])
		}
	})

	t.Run("omits sampling params when neither options nor model set them", func(t *testing.T) {
		payload := captureSamplingPayload(t, samplingTestModel(types.ApiOpenAICompletions, nil), nil)
		if _, present := payload["temperature"]; present {
			t.Fatalf("temperature = %#v, want absent", payload["temperature"])
		}
		if _, present := payload["top_p"]; present {
			t.Fatalf("top_p = %#v, want absent", payload["top_p"])
		}
	})

	// Model defaults must apply to direct stream()/complete() calls, not only
	// streamSimple() (#9506).
	for _, api := range []types.Api{types.ApiOpenAICompletions, types.ApiOpenAIResponses, types.ApiAzureOpenAIResponses} {
		t.Run("applies model-level sampling params with request keys taking precedence for "+string(api), func(t *testing.T) {
			payload := captureSamplingPayload(t,
				samplingTestModel(api, map[string]any{"top_p": 0.95, "min_p": 0.05}),
				&types.StreamOptions{SamplingParams: map[string]any{"top_p": 0.5}})
			if payload["top_p"] != 0.5 {
				t.Fatalf("top_p = %#v, want 0.5 (request keys override model defaults)", payload["top_p"])
			}
			if payload["min_p"] != 0.05 {
				t.Fatalf("min_p = %#v, want 0.05 (model defaults apply)", payload["min_p"])
			}
		})
	}

	t.Run("passes request sampling params through streamSimple", func(t *testing.T) {
		payload := captureSamplingSimple(t, samplingTestModel(types.ApiOpenAICompletions, nil),
			&types.SimpleStreamOptions{StreamOptions: types.StreamOptions{SamplingParams: map[string]any{"top_p": 0.5}}})
		if payload["top_p"] != 0.5 {
			t.Fatalf("top_p = %#v, want 0.5", payload["top_p"])
		}
	})

	// v1.0.2 #9776: per-thinking-level sampling params.
	t.Run("applies sampling params for the effective thinking level over model defaults", func(t *testing.T) {
		model := samplingTestModel(types.ApiOpenAICompletions, map[string]any{"temperature": 1, "top_p": 0.95})
		model.Reasoning = true
		model.ThinkingLevelMap = types.ThinkingLevelMap{
			types.ModelThinkingLow:    nil,
			types.ModelThinkingMedium: nil,
		}
		model.SamplingParamsByThinkingLevel = types.SamplingParamsByThinkingLevel{
			types.ModelThinkingHigh: {"temperature": 0.8, "top_k": 64},
		}
		payload := captureSamplingSimple(t, model, &types.SimpleStreamOptions{Reasoning: types.ThinkingLevelLow})
		if payload["temperature"] != 0.8 {
			t.Fatalf("temperature = %#v, want 0.8 (low clamps up to high)", payload["temperature"])
		}
		if payload["top_p"] != 0.95 {
			t.Fatalf("top_p = %#v, want 0.95 (model defaults apply)", payload["top_p"])
		}
		if payload["top_k"] != 64 {
			t.Fatalf("top_k = %#v, want 64", payload["top_k"])
		}
	})

	t.Run("applies off sampling params when reasoning is disabled", func(t *testing.T) {
		model := samplingTestModel(types.ApiOpenAICompletions, nil)
		model.SamplingParamsByThinkingLevel = types.SamplingParamsByThinkingLevel{
			types.ModelThinkingOff: {"temperature": 0.7},
		}
		payload := captureSamplingSimple(t, model, &types.SimpleStreamOptions{})
		if payload["temperature"] != 0.7 {
			t.Fatalf("temperature = %#v, want 0.7", payload["temperature"])
		}
	})

	t.Run("merges stream-option keys over thinking-level keys", func(t *testing.T) {
		model := samplingTestModel(types.ApiOpenAICompletions, nil)
		model.Reasoning = true
		model.SamplingParamsByThinkingLevel = types.SamplingParamsByThinkingLevel{
			types.ModelThinkingLow: {"temperature": 0.6, "top_p": 0.95},
		}
		payload := captureSamplingSimple(t, model, &types.SimpleStreamOptions{
			Reasoning:     types.ThinkingLevelLow,
			StreamOptions: types.StreamOptions{SamplingParams: map[string]any{"top_p": 0.5}},
		})
		if payload["temperature"] != 0.6 {
			t.Fatalf("temperature = %#v, want 0.6", payload["temperature"])
		}
		if payload["top_p"] != 0.5 {
			t.Fatalf("top_p = %#v, want 0.5 (request keys override level keys)", payload["top_p"])
		}
	})

	for _, api := range []types.Api{types.ApiOpenAICompletions, types.ApiOpenAIResponses, types.ApiAzureOpenAIResponses} {
		t.Run("applies thinking-level params between model and request params for "+string(api), func(t *testing.T) {
			model := samplingTestModel(api, map[string]any{"temperature": 1, "top_p": 0.95})
			model.Reasoning = true
			model.SamplingParamsByThinkingLevel = types.SamplingParamsByThinkingLevel{
				types.ModelThinkingLow: {"temperature": 0.6, "top_k": 64},
			}
			// pi drives stream() with reasoningEffort on StreamOptions; pig
			// keeps ReasoningEffort on the adapter options, so capture there.
			var captured samplingPayload
			setCapture := func(o *types.StreamOptions) {
				o.APIKey = "fake-key"
				o.MaxRetries = lo.ToPtr(0)
				o.OnPayload = func(payload any, model *types.Model) any {
					if m, ok := payload.(map[string]any); ok {
						captured = m
					}
					return nil
				}
			}
			switch api {
			case types.ApiOpenAICompletions:
				opts := &OpenAICompletionsOptions{ReasoningEffort: types.ModelThinkingLow}
				opts.SamplingParams = map[string]any{"top_p": 0.5}
				setCapture(&opts.StreamOptions)
				streamOpenAICompletions(gocontext.Background(), model, samplingTestContext(), opts).Result()
			case types.ApiOpenAIResponses:
				opts := &OpenAIResponsesOptions{ReasoningEffort: types.ModelThinkingLow}
				opts.SamplingParams = map[string]any{"top_p": 0.5}
				setCapture(&opts.StreamOptions)
				streamOpenAIResponses(gocontext.Background(), model, samplingTestContext(), opts).Result()
			case types.ApiAzureOpenAIResponses:
				opts := &AzureOpenAIResponsesOptions{ReasoningEffort: types.ModelThinkingLow}
				opts.SamplingParams = map[string]any{"top_p": 0.5}
				setCapture(&opts.StreamOptions)
				streamAzureOpenAIResponses(gocontext.Background(), model, samplingTestContext(), opts).Result()
			}
			if captured == nil {
				t.Fatal("Expected payload capture before request")
			}
			if captured["temperature"] != 0.6 {
				t.Fatalf("temperature = %#v, want 0.6", captured["temperature"])
			}
			if captured["top_p"] != 0.5 {
				t.Fatalf("top_p = %#v, want 0.5", captured["top_p"])
			}
			if captured["top_k"] != 64 {
				t.Fatalf("top_k = %#v, want 64", captured["top_k"])
			}
		})
	}

	for _, api := range []types.Api{types.ApiOpenAIResponses, types.ApiAzureOpenAIResponses} {
		t.Run("uses medium sampling params for summary-only "+string(api)+" requests", func(t *testing.T) {
			model := samplingTestModel(api, nil)
			model.Reasoning = true
			model.SamplingParamsByThinkingLevel = types.SamplingParamsByThinkingLevel{
				types.ModelThinkingOff:    {"temperature": 0.7},
				types.ModelThinkingMedium: {"temperature": 0.8},
			}
			// pi drives the full stream() with reasoningSummary; pig keeps
			// ReasoningSummary on the adapter options, so capture there.
			var captured samplingPayload
			setCapture := func(o *types.StreamOptions) {
				o.APIKey = "fake-key"
				o.MaxRetries = lo.ToPtr(0)
				o.OnPayload = func(payload any, model *types.Model) any {
					if m, ok := payload.(map[string]any); ok {
						captured = m
					}
					return nil
				}
			}
			switch api {
			case types.ApiOpenAIResponses:
				opts := &OpenAIResponsesOptions{ReasoningSummary: "auto"}
				setCapture(&opts.StreamOptions)
				streamOpenAIResponses(gocontext.Background(), model, samplingTestContext(), opts).Result()
			case types.ApiAzureOpenAIResponses:
				opts := &AzureOpenAIResponsesOptions{ReasoningSummary: "auto"}
				setCapture(&opts.StreamOptions)
				streamAzureOpenAIResponses(gocontext.Background(), model, samplingTestContext(), opts).Result()
			}
			if captured == nil {
				t.Fatal("Expected payload capture before request")
			}
			reasoning, _ := captured["reasoning"].(map[string]any)
			if reasoning == nil || reasoning["effort"] != "medium" {
				t.Fatalf("reasoning = %#v, want effort medium", captured["reasoning"])
			}
			if captured["temperature"] != 0.8 {
				t.Fatalf("temperature = %#v, want 0.8 (summary-only defaults to medium level)", captured["temperature"])
			}
		})
	}

	t.Run("overrides named request fields", func(t *testing.T) {
		payload := captureSamplingPayload(t, samplingTestModel(types.ApiOpenAICompletions, nil),
			&types.StreamOptions{Temperature: lo.ToPtr(0.0), SamplingParams: map[string]any{"temperature": 1.0}})
		if payload["temperature"] != float64(1) {
			t.Fatalf("temperature = %#v, want 1", payload["temperature"])
		}
	})

	t.Run("is ignored by non-OpenAI-compatible APIs", func(t *testing.T) {
		var captured samplingPayload
		model := samplingTestModel(types.ApiAnthropicMessages, nil)
		options := &types.StreamOptions{}
		options.APIKey = "fake-key"
		options.MaxRetries = lo.ToPtr(0)
		options.OnPayload = func(payload any, model *types.Model) any {
			if m, ok := payload.(map[string]any); ok {
				captured = m
			}
			return nil
		}
		options.SamplingParams = map[string]any{"top_p": 0.9, "top_k": 40}
		(&AnthropicMessages{}).Stream(gocontext.Background(), model, samplingTestContext(), options).Result()
		if captured == nil {
			t.Fatal("Expected payload capture")
		}
		if _, present := captured["top_p"]; present {
			t.Fatalf("top_p = %#v, want absent", captured["top_p"])
		}
		if _, present := captured["top_k"]; present {
			t.Fatalf("top_k = %#v, want absent", captured["top_k"])
		}
	})
}
