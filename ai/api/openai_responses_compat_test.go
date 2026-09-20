// pi: test/openai-responses-compat.test.ts.
package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// capturedResponsesHeaders extracts the captured request's affinity headers
// (pi: getHeader over the mocked fetch init).
type capturedResponsesHeaders struct {
	SessionID       *string
	ClientRequestID *string
	XSessionID      *string
}

func responsesReqHeader(req *capturedResponsesRequest, name string) *string {
	value := responsesHeaderValue(req.req, name)
	if value == "" {
		return nil
	}
	return &value
}

// captureOpenAIResponsesHeaders streams one request and returns the captured
// affinity headers (pi: captureOpenAIResponseHeaders). The SSE body ends
// without a terminal event, so the stream terminates with an error event —
// matching pi, where the loop breaks on done or error.
func captureOpenAIResponsesHeaders(
	t *testing.T,
	model *types.Model,
	options *types.StreamOptions,
) capturedResponsesHeaders {
	t.Helper()
	captured := &capturedResponsesRequest{}
	if options == nil {
		options = &types.StreamOptions{}
	}
	options.APIKey = "test-key"
	options.Fetch = fakeResponsesFetch(captured, "[DONE]")

	llmContext := utils.NormalizeContext(&types.Context{
		SystemPrompt: "sys",
		Messages:     []types.Message{responsesUserMessage("hi")},
	})
	stream := OpenAIResponses{}.Stream(context.Background(), model, llmContext, options)
	collectResponsesEvents(t, stream)

	return capturedResponsesHeaders{
		SessionID:       responsesReqHeader(captured, "session_id"),
		ClientRequestID: responsesReqHeader(captured, "x-client-request-id"),
		XSessionID:      responsesReqHeader(captured, "x-session-id"),
	}
}

// expectPayload runs one capture and returns the request body (pi: onPayload
// capture + break on done/error). options and llmContext may be nil for the
// plain defaults.
func expectPayload(t *testing.T, model *types.Model, options *types.StreamOptions, llmContext *types.TranscriptContext) map[string]any {
	t.Helper()
	captured := &capturedResponsesRequest{}
	if options == nil {
		options = &types.StreamOptions{}
	}
	options.APIKey = "test-key"
	options.Fetch = fakeResponsesFetch(captured, "[DONE]")
	if llmContext == nil {
		llmContext = utils.NormalizeContext(&types.Context{
			SystemPrompt: "sys",
			Messages:     []types.Message{responsesUserMessage("hi")},
		})
	}
	stream := streamOpenAIResponses(context.Background(), model, llmContext, &OpenAIResponsesOptions{StreamOptions: *options})
	collectResponsesEvents(t, stream)
	if captured.body == nil {
		t.Fatal("no request body captured")
	}
	return captured.body
}

func TestOpenAIResponsesProviderDefaults(t *testing.T) {
	t.Run("omits reasoning when no reasoning is requested", func(t *testing.T) {
		model := responsesTestModel(t, "github-copilot", "gpt-5-mini")
		payload := expectPayload(t, model, nil, nil)
		if _, present := payload["reasoning"]; present {
			t.Fatalf("expected no reasoning field, got %v", payload["reasoning"])
		}
	})

	t.Run("forwards required tool choice", func(t *testing.T) {
		model := responsesTestModel(t, "openai", "gpt-5.4")
		var payload map[string]any
		captured := &capturedResponsesRequest{}
		opts := &OpenAIResponsesOptions{}
		opts.APIKey = "test-key"
		opts.ToolChoice = "required"
		opts.Fetch = fakeResponsesFetch(captured, "[DONE]")
		llmContext := utils.NormalizeContext(&types.Context{
			Messages: []types.Message{responsesUserMessage("Do not call ping. Respond with text instead.")},
			Tools: []types.Tool{
				{
					Name:        "ping",
					Description: "Ping",
					Parameters:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`),
				},
			},
		})
		stream := streamOpenAIResponses(context.Background(), model, llmContext, opts)
		collectResponsesEvents(t, stream)
		payload = captured.body

		if payload["tool_choice"] != "required" {
			t.Fatalf("tool_choice = %v, want required", payload["tool_choice"])
		}
		tools, ok := payload["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Fatalf("tools = %v, want one entry", payload["tools"])
		}
		tool := tools[0].(map[string]any)
		if tool["name"] != "ping" {
			t.Fatalf("tool name = %v, want ping", tool["name"])
		}
	})

	t.Run("sets strict mode explicitly for Cloudflare OpenAI Responses tools", func(t *testing.T) {
		model := responsesTestModel(t, "cloudflare-ai-gateway", "gpt-5.6-sol")
		toolsContext := utils.NormalizeContext(&types.Context{
			Messages: []types.Message{responsesUserMessage("Use a tool.")},
			Tools: []types.Tool{
				{
					Name:        "ordinary",
					Description: "An ordinary tool",
					Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"number"}},"required":["path"]}`),
				},
				{
					Name:        "constrained",
					Description: "A constrained tool",
					Parameters:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`),
					ConstrainedSampling: map[string]any{
						"type": "json_schema", "strict": "prefer",
					},
				},
			},
		})
		payload := expectPayload(t, model, nil, toolsContext)

		if compat, ok := model.Compat.(*types.OpenAIResponsesCompat); !ok || compat.SupportsStrictMode == nil || !*compat.SupportsStrictMode {
			t.Fatalf("expected fixture supportsStrictMode true")
		}
		tools, ok := payload["tools"].([]any)
		if !ok || len(tools) != 2 {
			t.Fatalf("tools = %v, want two entries", payload["tools"])
		}
		ordinary := tools[0].(map[string]any)
		constrained := tools[1].(map[string]any)
		if ordinary["name"] != "ordinary" || ordinary["strict"] != false {
			t.Fatalf("ordinary = %v, want name ordinary strict false", ordinary)
		}
		if constrained["name"] != "constrained" || constrained["strict"] != true {
			t.Fatalf("constrained = %v, want name constrained strict true", constrained)
		}
	})

	// pi: it.each none-reasoning models.
	for _, modelID := range []string{
		"gpt-5.1", "gpt-5.2", "gpt-5.3-codex", "gpt-5.4", "gpt-5.4-mini",
		"gpt-5.4-nano", "gpt-5.5", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
	} {
		modelID := modelID
		t.Run("sends none reasoning effort for OpenAI "+modelID, func(t *testing.T) {
			model := responsesTestModel(t, "openai", modelID)
			payload := expectPayload(t, model, nil, nil)
			reasoning, ok := payload["reasoning"].(map[string]any)
			if !ok || reasoning["effort"] != "none" {
				t.Fatalf("reasoning = %v, want effort none", payload["reasoning"])
			}
		})
	}

	// pi: it.each off-unsupported models.
	for _, modelID := range []string{
		"gpt-5", "gpt-5-mini", "gpt-5-nano", "gpt-5-pro", "gpt-5.2-pro", "gpt-5.4-pro", "gpt-5.5-pro",
	} {
		modelID := modelID
		t.Run("omits reasoning effort for OpenAI "+modelID, func(t *testing.T) {
			model := responsesTestModel(t, "openai", modelID)
			payload := expectPayload(t, model, nil, nil)
			if _, present := payload["reasoning"]; present {
				t.Fatalf("expected no reasoning field, got %v", payload["reasoning"])
			}
		})
	}

	t.Run("sets cache-affinity headers for official OpenAI Responses requests with a sessionId", func(t *testing.T) {
		model := responsesTestModel(t, "openai", "gpt-5.4")
		opts := &types.StreamOptions{SessionID: "session-123"}
		captured := captureOpenAIResponsesHeaders(t, model, opts)

		if captured.SessionID == nil || *captured.SessionID != "session-123" {
			t.Fatalf("session_id header = %v", captured.SessionID)
		}
		if captured.ClientRequestID == nil || *captured.ClientRequestID != "session-123" {
			t.Fatalf("x-client-request-id header = %v", captured.ClientRequestID)
		}
	})

	t.Run("clamps prompt_cache_key to OpenAI's 64-character limit", func(t *testing.T) {
		sessionID := strings.Repeat("x", 67)
		model := responsesTestModel(t, "openai", "gpt-5.4")
		captured := &capturedResponsesRequest{}
		opts := &types.StreamOptions{}
		opts.APIKey = "test-key"
		opts.SessionID = sessionID
		opts.Fetch = fakeResponsesFetch(captured, "[DONE]")
		llmContext := utils.NormalizeContext(&types.Context{SystemPrompt: "sys", Messages: []types.Message{responsesUserMessage("hi")}})
		stream := OpenAIResponses{}.Stream(context.Background(), model, llmContext, opts)
		collectResponsesEvents(t, stream)

		if captured.body["prompt_cache_key"] != strings.Repeat("x", 64) {
			t.Fatalf("prompt_cache_key = %v", captured.body["prompt_cache_key"])
		}
	})

	t.Run("sets cache-affinity headers for proxy OpenAI Responses requests with a sessionId", func(t *testing.T) {
		model := responsesTestModel(t, "openai", "gpt-5.4")
		model.Provider = "opencode"
		model.BaseURL = "https://proxy.example.com/v1"
		opts := &types.StreamOptions{SessionID: "session-123"}
		captured := captureOpenAIResponsesHeaders(t, model, opts)

		if captured.SessionID == nil || *captured.SessionID != "session-123" {
			t.Fatalf("session_id header = %v", captured.SessionID)
		}
		if captured.ClientRequestID == nil || *captured.ClientRequestID != "session-123" {
			t.Fatalf("x-client-request-id header = %v", captured.ClientRequestID)
		}
	})

	t.Run("uses OpenRouter session-affinity header when configured", func(t *testing.T) {
		model := responsesTestModel(t, "openai", "gpt-5.4")
		model.Provider = "proxy"
		model.BaseURL = "https://proxy.example.com/v1"
		model.Compat = &types.OpenAIResponsesCompat{SessionAffinityFormat: loFormatPtr("openrouter")}
		opts := &types.StreamOptions{SessionID: "session-proxy"}
		captured := captureOpenAIResponsesHeaders(t, model, opts)

		if captured.SessionID != nil {
			t.Fatalf("session_id header = %v, want absent", *captured.SessionID)
		}
		if captured.ClientRequestID != nil {
			t.Fatalf("x-client-request-id header = %v, want absent", *captured.ClientRequestID)
		}
		if captured.XSessionID == nil || *captured.XSessionID != "session-proxy" {
			t.Fatalf("x-session-id header = %v", captured.XSessionID)
		}
		// The header capture path shares the fetch mock; assert the body
		// through a separate payload capture.
		payload := expectPayload(t, model, &types.StreamOptions{SessionID: "session-proxy"}, nil)
		if _, present := payload["session_id"]; present {
			t.Fatalf("session_id body field = %v, want absent", payload["session_id"])
		}
		if payload["prompt_cache_key"] != "session-proxy" {
			t.Fatalf("prompt_cache_key = %v", payload["prompt_cache_key"])
		}
	})

	t.Run("auto-detects OpenRouter session-affinity header for OpenRouter Responses endpoints", func(t *testing.T) {
		model := responsesTestModel(t, "openai", "gpt-5.4")
		model.Provider = "openrouter"
		model.BaseURL = "https://openrouter.ai/api/v1"
		opts := &types.StreamOptions{SessionID: "session-openrouter"}
		captured := captureOpenAIResponsesHeaders(t, model, opts)

		if captured.SessionID != nil {
			t.Fatalf("session_id header = %v, want absent", *captured.SessionID)
		}
		if captured.ClientRequestID != nil {
			t.Fatalf("x-client-request-id header = %v, want absent", *captured.ClientRequestID)
		}
		if captured.XSessionID == nil || *captured.XSessionID != "session-openrouter" {
			t.Fatalf("x-session-id header = %v", captured.XSessionID)
		}
		payload := expectPayload(t, model, &types.StreamOptions{SessionID: "session-openrouter"}, nil)
		if _, present := payload["session_id"]; present {
			t.Fatalf("session_id body field = %v, want absent", payload["session_id"])
		}
		if payload["prompt_cache_key"] != "session-openrouter" {
			t.Fatalf("prompt_cache_key = %v", payload["prompt_cache_key"])
		}
	})

	t.Run("uses OpenAI no-session format when configured", func(t *testing.T) {
		model := responsesTestModel(t, "openai", "gpt-5.4")
		model.Provider = "proxy"
		model.BaseURL = "https://proxy.example.com/v1"
		model.Compat = &types.OpenAIResponsesCompat{SessionAffinityFormat: loFormatPtr("openai-nosession")}
		opts := &types.StreamOptions{SessionID: "session-proxy"}
		captured := captureOpenAIResponsesHeaders(t, model, opts)

		if captured.SessionID != nil {
			t.Fatalf("session_id header = %v, want absent", *captured.SessionID)
		}
		if captured.ClientRequestID == nil || *captured.ClientRequestID != "session-proxy" {
			t.Fatalf("x-client-request-id header = %v", captured.ClientRequestID)
		}
		if captured.XSessionID != nil {
			t.Fatalf("x-session-id header = %v, want absent", *captured.XSessionID)
		}
		payload := expectPayload(t, model, &types.StreamOptions{SessionID: "session-proxy"}, nil)
		if _, present := payload["session_id"]; present {
			t.Fatalf("session_id body field = %v, want absent", payload["session_id"])
		}
		if payload["prompt_cache_key"] != "session-proxy" {
			t.Fatalf("prompt_cache_key = %v", payload["prompt_cache_key"])
		}
	})

	t.Run("uses OpenAI no-session format for OpenCode Responses models", func(t *testing.T) {
		model := responsesTestModel(t, "opencode", "gpt-5.4")
		opts := &types.StreamOptions{SessionID: "session-opencode"}
		captured := captureOpenAIResponsesHeaders(t, model, opts)

		compat, ok := model.Compat.(*types.OpenAIResponsesCompat)
		if !ok || compat.SessionAffinityFormat == nil || *compat.SessionAffinityFormat != types.SessionAffinityOpenAINoSession {
			t.Fatalf("expected fixture sessionAffinityFormat openai-nosession")
		}
		if captured.SessionID != nil {
			t.Fatalf("session_id header = %v, want absent", *captured.SessionID)
		}
		if captured.ClientRequestID == nil || *captured.ClientRequestID != "session-opencode" {
			t.Fatalf("x-client-request-id header = %v", captured.ClientRequestID)
		}
		if captured.XSessionID != nil {
			t.Fatalf("x-session-id header = %v, want absent", *captured.XSessionID)
		}
		payload := expectPayload(t, model, &types.StreamOptions{SessionID: "session-opencode"}, nil)
		if payload["prompt_cache_key"] != "session-opencode" {
			t.Fatalf("prompt_cache_key = %v", payload["prompt_cache_key"])
		}
	})

	t.Run("can omit OpenAI session_id header while preserving other affinity data", func(t *testing.T) {
		model := responsesTestModel(t, "openai", "gpt-5.4")
		model.Provider = "opencode"
		model.BaseURL = "https://proxy.example.com/v1"
		model.Compat = &types.OpenAIResponsesCompat{SessionAffinityFormat: loFormatPtr("openai-nosession")}
		opts := &types.StreamOptions{SessionID: "session-123"}
		captured := captureOpenAIResponsesHeaders(t, model, opts)

		if captured.SessionID != nil {
			t.Fatalf("session_id header = %v, want absent", *captured.SessionID)
		}
		if captured.ClientRequestID == nil || *captured.ClientRequestID != "session-123" {
			t.Fatalf("x-client-request-id header = %v", captured.ClientRequestID)
		}
		payload := expectPayload(t, model, &types.StreamOptions{SessionID: "session-123"}, nil)
		if payload["prompt_cache_key"] != "session-123" {
			t.Fatalf("prompt_cache_key = %v", payload["prompt_cache_key"])
		}
	})

	t.Run("lets explicit headers override the default OpenAI cache-affinity headers", func(t *testing.T) {
		model := responsesTestModel(t, "openai", "gpt-5.4")
		opts := &types.StreamOptions{SessionID: "session-123"}
		overrideSession := "override-session"
		overrideRequest := "override-request"
		opts.Headers = types.ProviderHeaders{
			"session_id":          &overrideSession,
			"x-client-request-id": &overrideRequest,
		}
		captured := captureOpenAIResponsesHeaders(t, model, opts)

		if captured.SessionID == nil || *captured.SessionID != "override-session" {
			t.Fatalf("session_id header = %v", captured.SessionID)
		}
		if captured.ClientRequestID == nil || *captured.ClientRequestID != "override-request" {
			t.Fatalf("x-client-request-id header = %v", captured.ClientRequestID)
		}
	})

	t.Run("omits OpenAI cache-affinity headers when cacheRetention is none", func(t *testing.T) {
		model := responsesTestModel(t, "openai", "gpt-5.4")
		opts := &types.StreamOptions{SessionID: "session-123", CacheRetention: types.CacheRetentionNone}
		captured := captureOpenAIResponsesHeaders(t, model, opts)

		if captured.SessionID != nil {
			t.Fatalf("session_id header = %v, want absent", *captured.SessionID)
		}
		if captured.ClientRequestID != nil {
			t.Fatalf("x-client-request-id header = %v, want absent", *captured.ClientRequestID)
		}
	})

	for _, tc := range []struct {
		modelID    string
		tier       string
		multiplier float64
	}{{"gpt-5.4", "priority", 2}, {"gpt-5.5", "priority", 2.5}, {"gpt-5.5", "flex", 0.5}} {
		tc := tc
		t.Run("applies "+tc.modelID+" "+tc.tier+" service-tier cost multiplier", func(t *testing.T) {
			model := responsesTestModel(t, "openai", tc.modelID)
			tokenCount := int64(100000)
			tokenScale := float64(tokenCount) / 1000000.0

			completed := `{"type":"response.completed","response":{"status":"completed","service_tier":"` + tc.tier + `","usage":{"input_tokens":100000,"output_tokens":100000,"total_tokens":200000,"input_tokens_details":{"cached_tokens":0}}}}`
			captured := &capturedResponsesRequest{}
			opts := &OpenAIResponsesOptions{}
			opts.APIKey = "test-key"
			opts.ServiceTier = tc.tier
			opts.Fetch = fakeResponsesFetch(captured, completed)
			llmContext := utils.NormalizeContext(&types.Context{SystemPrompt: "sys", Messages: []types.Message{responsesUserMessage("hi")}})
			stream := streamOpenAIResponses(context.Background(), model, llmContext, opts)
			events, result := collectResponsesEvents(t, stream)
			if result.StopReason != types.StopReasonStop {
				t.Fatalf("stopReason = %v (%s), want stop", result.StopReason, result.ErrorMessage)
			}

			wantInput := model.Cost.Input * tc.multiplier * tokenScale
			wantOutput := model.Cost.Output * tc.multiplier * tokenScale
			wantTotal := (model.Cost.Input + model.Cost.Output) * tc.multiplier * tokenScale
			if result.Usage.Cost.Input != wantInput {
				t.Fatalf("cost.input = %v, want %v (events: %d)", result.Usage.Cost.Input, wantInput, len(events))
			}
			if result.Usage.Cost.Output != wantOutput {
				t.Fatalf("cost.output = %v, want %v", result.Usage.Cost.Output, wantOutput)
			}
			if result.Usage.Cost.Total != wantTotal {
				t.Fatalf("cost.total = %v, want %v", result.Usage.Cost.Total, wantTotal)
			}
		})
	}
}

func TestOpenAIResponsesMaxOutputTokensCompat(t *testing.T) {
	t.Run("sends max_output_tokens by default", func(t *testing.T) {
		model := responsesTestModel(t, "openai", "gpt-5.4")
		captured := &capturedResponsesRequest{}
		opts := &types.StreamOptions{}
		opts.APIKey = "test-key"
		maxTokens := int64(1024)
		opts.MaxTokens = &maxTokens
		opts.Fetch = fakeResponsesFetch(captured, "[DONE]")
		llmContext := utils.NormalizeContext(&types.Context{SystemPrompt: "sys", Messages: []types.Message{responsesUserMessage("hi")}})
		stream := OpenAIResponses{}.Stream(context.Background(), model, llmContext, opts)
		collectResponsesEvents(t, stream)

		if captured.body["max_output_tokens"] != float64(1024) {
			t.Fatalf("max_output_tokens = %v, want 1024", captured.body["max_output_tokens"])
		}
	})

	t.Run("omits max_output_tokens when supportsMaxOutputTokens is false", func(t *testing.T) {
		model := responsesTestModel(t, "openai", "gpt-5.4")
		compat := &types.OpenAIResponsesCompat{SupportsMaxOutputTokens: loBoolPtr(false)}
		model.Compat = compat
		captured := &capturedResponsesRequest{}
		opts := &types.StreamOptions{}
		opts.APIKey = "test-key"
		maxTokens := int64(1024)
		opts.MaxTokens = &maxTokens
		opts.Fetch = fakeResponsesFetch(captured, "[DONE]")
		llmContext := utils.NormalizeContext(&types.Context{SystemPrompt: "sys", Messages: []types.Message{responsesUserMessage("hi")}})
		stream := OpenAIResponses{}.Stream(context.Background(), model, llmContext, opts)
		collectResponsesEvents(t, stream)

		if _, present := captured.body["max_output_tokens"]; present {
			t.Fatalf("max_output_tokens = %v, want absent", captured.body["max_output_tokens"])
		}
	})
}
