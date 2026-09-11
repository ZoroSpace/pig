// pi: test/anthropic-sse-parsing.test.ts, plus shared fake-transport helpers
// for the anthropic-messages tests (the TS tests inject a fake Anthropic SDK
// client; pig replaces the transport via StreamOptions.Fetch — the adapter's
// declared injection surface).
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
)

type anthropicTestEvent struct {
	event string
	data  string
}

// capturedAnthropicRequest records what createMessage built.
type capturedAnthropicRequest struct {
	req  *http.Request
	body map[string]any
}

// fakeAnthropicFetch returns a FetchFunction responding with an SSE body
// framed from events (pi: createSseResponse + createFakeAnthropicClient).
func fakeAnthropicFetch(events []anthropicTestEvent, captured *capturedAnthropicRequest) types.FetchFunction {
	return func(req *http.Request) (*http.Response, error) {
		if captured != nil {
			captured.req = req
			bodyBytes, _ := io.ReadAll(req.Body)
			var parsed map[string]any
			_ = json.Unmarshal(bodyBytes, &parsed)
			captured.body = parsed
		}
		var b strings.Builder
		for _, e := range events {
			fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", e.event, e.data)
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(b.String())),
			Request:    req,
		}, nil
	}
}

// anthropicTestOptions builds stream options with a fake SSE transport (pi:
// { client: createFakeAnthropicClient(response) } — pi's injected client
// bypasses the auth assert, so the fake sets an api key to match).
func anthropicTestOptions(events []anthropicTestEvent, captured *capturedAnthropicRequest) *AnthropicOptions {
	return &AnthropicOptions{StreamOptions: types.StreamOptions{
		ProviderRequestOptions: types.ProviderRequestOptions{
			Fetch:  fakeAnthropicFetch(events, captured),
			APIKey: "test-key",
		},
	}}
}

var minimalAnthropicEvents = []anthropicTestEvent{
	{
		event: "message_start",
		data: mustJSON(map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": "msg_test",
				"usage": map[string]any{
					"input_tokens":                12,
					"output_tokens":               0,
					"cache_read_input_tokens":     0,
					"cache_creation_input_tokens": 0,
				},
			},
		}),
	},
	{
		event: "content_block_start",
		data: mustJSON(map[string]any{
			"type":          "content_block_start",
			"index":         0,
			"content_block": map[string]any{"type": "text", "text": ""},
		}),
	},
	{
		event: "content_block_delta",
		data: mustJSON(map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "Hello"},
		}),
	},
	{
		event: "content_block_stop",
		data:  mustJSON(map[string]any{"type": "content_block_stop", "index": 0}),
	},
	{
		event: "message_delta",
		data: mustJSON(map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": "end_turn"},
			"usage": map[string]any{
				"input_tokens":                12,
				"output_tokens":               5,
				"cache_read_input_tokens":     0,
				"cache_creation_input_tokens": 0,
			},
		}),
	},
	{
		event: "message_stop",
		data:  mustJSON(map[string]any{"type": "message_stop"}),
	},
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// anthropicTestModel builds an inline anthropic-messages model (pi: the
// object literals in the test files). pi's tests mostly use getModel against
// the generated catalog; pig's catalog entries that predate
// supportsMidConvoEffort (claude-fable-5-1 et al.) are reconstructed here
// until the B2 catalog regeneration.
func anthropicTestModel(id string, provider types.ProviderID, compat *types.AnthropicMessagesCompat) *types.Model {
	return &types.Model{
		ID:            id,
		Name:          id,
		API:           types.ApiAnthropicMessages,
		Provider:      provider,
		BaseURL:       "https://api.anthropic.com",
		Reasoning:     true,
		Input:         []types.ModelInput{types.ModelInputText},
		Cost:          types.ModelCost{},
		ContextWindow: 200000,
		MaxTokens:     32000,
		Compat:        compat,
	}
}

// runAnthropicStream drives a stream to completion and returns the final
// message (pi: stream(...).result()).
func runAnthropicStream(t *testing.T, model *types.Model, llmContext *types.Context, options *AnthropicOptions) types.AssistantMessage {
	t.Helper()
	stream := streamAnthropicMessages(context.Background(), model, llmContext, options)
	result := stream.Result()
	if result.StopReason == types.StopReasonError && containsGoroutinePanic(result.ErrorMessage) {
		t.Fatalf("stream failed unexpectedly: %s", result.ErrorMessage)
	}
	return result
}

func containsGoroutinePanic(message string) bool {
	// Placeholder for potential future diagnostics assertions.
	return false && message != ""
}

// captureAnthropicPayload captures the request body through OnPayload; the
// request then fails against the unreachable base URL (pi: the PayloadCaptured
// throw in onPayload — pig's OnPayload cannot throw, so the unreachable
// 127.0.0.1:9 base URL fails the request after capture instead).
func captureAnthropicPayload(t *testing.T, model *types.Model, llmContext *types.Context, options *AnthropicOptions, mutate func(options *AnthropicOptions)) map[string]any {
	t.Helper()
	var captured map[string]any
	options.APIKey = "fake-key"
	options.OnPayload = func(payload any, model *types.Model) any {
		if m, ok := payload.(map[string]any); ok {
			captured = m
		}
		return nil
	}
	if mutate != nil {
		mutate(options)
	}
	stream := streamAnthropicMessages(context.Background(), model, llmContext, options)
	stream.Result()
	if captured == nil {
		t.Fatal("Expected payload to be captured before request failure")
	}
	return captured
}

// unreachableAnthropicBaseURL fails fast with connection refused; used by the
// payload-capture tests.
const unreachableAnthropicBaseURL = "http://127.0.0.1:9"

func TestAnthropicSSEParsingFallbackAfterOutput(t *testing.T) {
	model := anthropicTestModel("claude-fable-5-1", types.ProviderAnthropic, &types.AnthropicMessagesCompat{
		ForceAdaptiveThinking:  lo.ToPtr(true),
		SupportsMidConvoEffort: lo.ToPtr(true),
	})
	events := []anthropicTestEvent{
		{
			event: "message_start",
			data: mustJSON(map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id":    "msg_fallback",
					"model": "claude-opus-5",
					"usage": map[string]any{"input_tokens": 1, "output_tokens": 0},
				},
			}),
		},
		{
			event: "content_block_start",
			data: mustJSON(map[string]any{
				"type":          "content_block_start",
				"index":         0,
				"content_block": map[string]any{"type": "text", "text": "partial"},
			}),
		},
		{event: "content_block_stop", data: mustJSON(map[string]any{"type": "content_block_stop", "index": 0})},
		{
			event: "content_block_start",
			data: mustJSON(map[string]any{
				"type":  "content_block_start",
				"index": 1,
				"content_block": map[string]any{
					"type": "fallback",
					"from": map[string]any{"model": "claude-opus-5"},
					"to":   map[string]any{"model": "claude-opus-4-8"},
				},
			}),
		},
	}
	var captured capturedAnthropicRequest
	options := anthropicTestOptions(events, &captured)
	result := runAnthropicStream(t, model, &types.Context{Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}}}, options)

	if result.StopReason != types.StopReasonError {
		t.Errorf("stopReason = %q, want error", result.StopReason)
	}
	if !strings.Contains(result.ErrorMessage, "unsupported mid-output model fallback") {
		t.Errorf("errorMessage = %q, want it to contain the fallback error", result.ErrorMessage)
	}
}

func TestAnthropicSSEParsingForcesStreamingAfterOnPayloadReplacement(t *testing.T) {
	model := anthropicTestModel("claude-fable-5-1", types.ProviderAnthropic, &types.AnthropicMessagesCompat{
		ForceAdaptiveThinking:  lo.ToPtr(true),
		SupportsMidConvoEffort: lo.ToPtr(true),
	})
	var captured capturedAnthropicRequest
	options := anthropicTestOptions(minimalAnthropicEvents, &captured)
	options.OnPayload = func(payload any, model *types.Model) any {
		if params, ok := payload.(map[string]any); ok {
			next := map[string]any{}
			for key, value := range params {
				next[key] = value
			}
			next["stream"] = false
			return next
		}
		return nil
	}
	runAnthropicStream(t, model, &types.Context{Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}}}, options)

	streaming, _ := captured.body["stream"].(bool)
	if !streaming {
		t.Errorf("stream = %v, want true after onPayload replacement", captured.body["stream"])
	}
}

func TestAnthropicSSEParsingOmitsInterleavedThinkingBetaWhenDisabled(t *testing.T) {
	// pi: getModel("openrouter", "anthropic/claude-3-haiku") — the catalog
	// entry is reconstructed inline until the B2 regeneration.
	model := anthropicTestModel("anthropic/claude-3-haiku", types.ProviderOpenRouter, nil)
	var captured capturedAnthropicRequest
	options := anthropicTestOptions(minimalAnthropicEvents, &captured)
	options.ThinkingEnabled = lo.ToPtr(false)
	runAnthropicStream(t, model, &types.Context{Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}}}, options)

	betas := betasFromBody(captured.body)
	for _, beta := range betas {
		if beta == "interleaved-thinking-2025-05-14" {
			t.Errorf("betas = %v, want it to omit the interleaved-thinking beta", betas)
		}
	}
}

func TestAnthropicSSEParsingPassesManagedBetaFeatures(t *testing.T) {
	model := anthropicTestModel("claude-fable-5-1", types.ProviderAnthropic, &types.AnthropicMessagesCompat{
		ForceAdaptiveThinking:  lo.ToPtr(true),
		SupportsMidConvoEffort: lo.ToPtr(true),
	})
	var captured capturedAnthropicRequest
	options := anthropicTestOptions(minimalAnthropicEvents, &captured)
	result := runAnthropicStream(t, model, &types.Context{Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}}}, options)

	if result.StopReason != types.StopReasonStop {
		t.Errorf("stopReason = %q, want stop", result.StopReason)
	}
	betas := betasFromBody(captured.body)
	has := func(feature string) bool {
		for _, beta := range betas {
			if beta == feature {
				return true
			}
		}
		return false
	}
	if !has("mid-conversation-output-config-2026-07-01") || !has("thinking-binding-controls-2026-08-01") {
		t.Errorf("betas = %v, want the managed mid-conversation betas", betas)
	}
}

func betasFromBody(body map[string]any) []string {
	switch value := body["betas"].(type) {
	case []any:
		var out []string
		for _, entry := range value {
			if s, ok := entry.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return value
	}
	return nil
}

func TestAnthropicSSEParsingInputTransformationsFromFinalEvent(t *testing.T) {
	model := anthropicTestModel("claude-fable-5-1", types.ProviderAnthropic, &types.AnthropicMessagesCompat{
		ForceAdaptiveThinking:  lo.ToPtr(true),
		SupportsMidConvoEffort: lo.ToPtr(true),
	})
	events := append([]anthropicTestEvent{}, minimalAnthropicEvents...)
	events[0].data = mustJSON(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":    "msg_transformations",
			"model": "claude-fable-5-1",
			"usage": map[string]any{"input_tokens": 12, "output_tokens": 0},
			"input_transformations": []any{
				map[string]any{"type": "thinking_dropped", "path": "messages.1.content.0", "reason": "prefix_binding_mismatch"},
			},
		},
	})
	var delta map[string]any
	if err := json.Unmarshal([]byte(events[4].data), &delta); err != nil {
		t.Fatal(err)
	}
	delta["input_transformations"] = []any{
		map[string]any{"type": "thinking_dropped", "path": "messages.3.content.0", "reason": "model_binding_mismatch"},
	}
	events[4].data = mustJSON(delta)

	var captured capturedAnthropicRequest
	options := anthropicTestOptions(events, &captured)
	result := runAnthropicStream(t, model, &types.Context{Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}}}, options)

	if len(result.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v, want exactly one entry", result.Diagnostics)
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.Type != "anthropic_input_transformations" {
		t.Errorf("diagnostic type = %q", diagnostic.Type)
	}
	transformations, _ := diagnostic.Details["transformations"].([]map[string]any)
	if len(transformations) != 1 {
		t.Fatalf("transformations = %#v, want one entry", diagnostic.Details["transformations"])
	}
	if transformations[0]["type"] != "thinking_dropped" || transformations[0]["path"] != "messages.3.content.0" || transformations[0]["reason"] != "model_binding_mismatch" {
		t.Errorf("transformation = %#v, want the final event's entry", transformations[0])
	}
}

func TestAnthropicSSEParsingRepairsMalformedJSON(t *testing.T) {
	model := anthropicTestModel("claude-haiku-4-5", types.ProviderAnthropic, nil)
	// pi builds the malformed delta with String.raw: the partial_json value
	// carries a literal backslash before H and a literal tab.
	malformedToolJsonDelta := `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"A\H\",\"text\":\"col1	col2\"}"}}`
	events := []anthropicTestEvent{
		{
			event: "message_start",
			data: mustJSON(map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id": "msg_test",
					"usage": map[string]any{
						"input_tokens":                12,
						"output_tokens":               0,
						"cache_read_input_tokens":     0,
						"cache_creation_input_tokens": 0,
					},
				},
			}),
		},
		{
			event: "content_block_start",
			data: mustJSON(map[string]any{
				"type":          "content_block_start",
				"index":         0,
				"content_block": map[string]any{"type": "tool_use", "id": "toolu_test", "name": "edit", "input": map[string]any{}},
			}),
		},
		{event: "content_block_delta", data: malformedToolJsonDelta},
		{event: "content_block_stop", data: mustJSON(map[string]any{"type": "content_block_stop", "index": 0})},
		{
			event: "message_delta",
			data: mustJSON(map[string]any{
				"type":  "message_delta",
				"delta": map[string]any{"stop_reason": "tool_use"},
				"usage": map[string]any{
					"input_tokens":                12,
					"output_tokens":               5,
					"cache_read_input_tokens":     0,
					"cache_creation_input_tokens": 0,
				},
			}),
		},
		{event: "message_stop", data: mustJSON(map[string]any{"type": "message_stop"})},
	}

	llmContext := &types.Context{
		Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Use the edit tool."}, Timestamp: 1}},
		Tools: []types.Tool{
			{
				Name:        "edit",
				Description: "Edit a file.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"text":{"type":"string"}},"required":["path","text"]}`),
			},
		},
	}
	var captured capturedAnthropicRequest
	options := anthropicTestOptions(events, &captured)
	result := runAnthropicStream(t, model, llmContext, options)

	if result.StopReason != types.StopReasonToolUse {
		t.Errorf("stopReason = %q (%s), want toolUse", result.StopReason, result.ErrorMessage)
	}
	if result.ErrorMessage != "" {
		t.Errorf("errorMessage = %q, want empty", result.ErrorMessage)
	}
	var toolCall *types.ToolCall
	for _, block := range result.Content {
		if tc, ok := block.(types.ToolCall); ok {
			toolCall = &tc
			break
		}
	}
	if toolCall == nil {
		t.Fatal("Expected a tool call in the response content")
	}
	if got, ok := toolCall.Arguments["path"].(string); !ok || got != "A\\H" {
		t.Errorf("arguments.path = %#v, want \"A\\\\H\"", toolCall.Arguments["path"])
	}
	if got, ok := toolCall.Arguments["text"].(string); !ok || got != "col1\tcol2" {
		t.Errorf("arguments.text = %#v, want \"col1\\tcol2\"", toolCall.Arguments["text"])
	}
}

func TestAnthropicSSEParsingPreservesContentBlockStartContent(t *testing.T) {
	model := anthropicTestModel("claude-haiku-4-5", types.ProviderAnthropic, nil)
	events := []anthropicTestEvent{
		{
			event: "message_start",
			data: mustJSON(map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id": "msg_initial_content",
					"usage": map[string]any{
						"input_tokens":                12,
						"output_tokens":               0,
						"cache_read_input_tokens":     0,
						"cache_creation_input_tokens": 0,
					},
				},
			}),
		},
		{
			event: "content_block_start",
			data: mustJSON(map[string]any{
				"type":          "content_block_start",
				"index":         0,
				"content_block": map[string]any{"type": "text", "text": "Initial text"},
			}),
		},
		{
			event: "content_block_delta",
			data: mustJSON(map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{"type": "text_delta", "text": " plus delta"},
			}),
		},
		{event: "content_block_stop", data: mustJSON(map[string]any{"type": "content_block_stop", "index": 0})},
		{
			event: "content_block_start",
			data: mustJSON(map[string]any{
				"type":          "content_block_start",
				"index":         1,
				"content_block": map[string]any{"type": "thinking", "thinking": "Initial thinking", "signature": "initial signature"},
			}),
		},
		{
			event: "content_block_delta",
			data: mustJSON(map[string]any{
				"type":  "content_block_delta",
				"index": 1,
				"delta": map[string]any{"type": "thinking_delta", "thinking": " plus delta"},
			}),
		},
		{
			event: "content_block_delta",
			data: mustJSON(map[string]any{
				"type":  "content_block_delta",
				"index": 1,
				"delta": map[string]any{"type": "signature_delta", "signature": " plus delta"},
			}),
		},
		{event: "content_block_stop", data: mustJSON(map[string]any{"type": "content_block_stop", "index": 1})},
		{
			event: "message_delta",
			data: mustJSON(map[string]any{
				"type":  "message_delta",
				"delta": map[string]any{"stop_reason": "end_turn"},
				"usage": map[string]any{
					"input_tokens":                12,
					"output_tokens":               5,
					"cache_read_input_tokens":     0,
					"cache_creation_input_tokens": 0,
				},
			}),
		},
		{event: "message_stop", data: mustJSON(map[string]any{"type": "message_stop"})},
	}

	var captured capturedAnthropicRequest
	options := anthropicTestOptions(events, &captured)
	result := runAnthropicStream(t, model, &types.Context{Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Say hello."}, Timestamp: 1}}}, options)

	if len(result.Content) != 2 {
		t.Fatalf("content = %#v, want 2 blocks", result.Content)
	}
	text, ok := result.Content[0].(types.TextContent)
	if !ok || text.Text != "Initial text plus delta" {
		t.Errorf("content[0] = %#v, want text \"Initial text plus delta\"", result.Content[0])
	}
	thinking, ok := result.Content[1].(types.ThinkingContent)
	if !ok || thinking.Thinking != "Initial thinking plus delta" || thinking.ThinkingSignature != "initial signature plus delta" {
		t.Errorf("content[1] = %#v, want thinking with merged signature", result.Content[1])
	}
}

func TestAnthropicSSEParsingRefusalStopDetails(t *testing.T) {
	model := anthropicTestModel("claude-fable-5", types.ProviderAnthropic, nil)
	explanation := "This request triggered restrictions on violative cyber content and was blocked under Anthropic's Usage Policy. To learn more, provide feedback, or request an exemption based on how you use Claude, visit our help center: https://support.claude.com/en/articles/14604842-real-time-cyber-safeguards-on-claude."
	events := []anthropicTestEvent{
		{
			event: "message_start",
			data: mustJSON(map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id": "msg_01XFUDYJgAACzvnptvVoYEL",
					"usage": map[string]any{
						"input_tokens":                412,
						"output_tokens":               0,
						"cache_read_input_tokens":     0,
						"cache_creation_input_tokens": 0,
					},
				},
			}),
		},
		{
			event: "message_delta",
			data: mustJSON(map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason":  "refusal",
					"stop_details": map[string]any{"type": "refusal", "category": "cyber", "explanation": explanation},
				},
				"usage": map[string]any{
					"input_tokens":                412,
					"output_tokens":               0,
					"cache_read_input_tokens":     0,
					"cache_creation_input_tokens": 0,
				},
			}),
		},
		{event: "message_stop", data: mustJSON(map[string]any{"type": "message_stop"})},
	}

	var captured capturedAnthropicRequest
	options := anthropicTestOptions(events, &captured)
	result := runAnthropicStream(t, model, &types.Context{Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "blocked request"}, Timestamp: 1}}}, options)

	if result.StopReason != types.StopReasonError {
		t.Errorf("stopReason = %q, want error", result.StopReason)
	}
	if result.RawStopReason != "refusal" {
		t.Errorf("rawStopReason = %q, want refusal", result.RawStopReason)
	}
	if result.ErrorMessage != explanation {
		t.Errorf("errorMessage = %q, want the refusal explanation", result.ErrorMessage)
	}
}

func TestAnthropicSSEParsingSensitiveStopReason(t *testing.T) {
	model := anthropicTestModel("claude-haiku-4-5", types.ProviderAnthropic, nil)
	events := []anthropicTestEvent{
		{
			event: "message_start",
			data: mustJSON(map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id": "msg_sensitive",
					"usage": map[string]any{
						"input_tokens":                12,
						"output_tokens":               0,
						"cache_read_input_tokens":     0,
						"cache_creation_input_tokens": 0,
					},
				},
			}),
		},
		{
			event: "message_delta",
			data: mustJSON(map[string]any{
				"type":  "message_delta",
				"delta": map[string]any{"stop_reason": "sensitive"},
				"usage": map[string]any{
					"input_tokens":                12,
					"output_tokens":               0,
					"cache_read_input_tokens":     0,
					"cache_creation_input_tokens": 0,
				},
			}),
		},
		{event: "message_stop", data: mustJSON(map[string]any{"type": "message_stop"})},
	}

	var captured capturedAnthropicRequest
	options := anthropicTestOptions(events, &captured)
	result := runAnthropicStream(t, model, &types.Context{Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "blocked request"}, Timestamp: 1}}}, options)

	if result.StopReason != types.StopReasonError {
		t.Errorf("stopReason = %q, want error", result.StopReason)
	}
	if result.RawStopReason != "sensitive" {
		t.Errorf("rawStopReason = %q, want sensitive", result.RawStopReason)
	}
	if result.ErrorMessage != "Provider stopped with: sensitive" {
		t.Errorf("errorMessage = %q", result.ErrorMessage)
	}
}

func TestAnthropicSSEParsingMessageDeltaWithoutUsage(t *testing.T) {
	model := anthropicTestModel("claude-haiku-4-5", types.ProviderAnthropic, nil)
	var events []anthropicTestEvent
	for _, event := range minimalAnthropicEvents {
		if event.event == "message_delta" {
			events = append(events, anthropicTestEvent{
				event: "message_delta",
				data:  mustJSON(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}}),
			})
			continue
		}
		events = append(events, event)
	}

	var captured capturedAnthropicRequest
	options := anthropicTestOptions(events, &captured)
	result := runAnthropicStream(t, model, &types.Context{Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Say hello."}, Timestamp: 1}}}, options)

	if result.StopReason != types.StopReasonStop {
		t.Errorf("stopReason = %q, want stop", result.StopReason)
	}
	if result.ErrorMessage != "" {
		t.Errorf("errorMessage = %q, want empty", result.ErrorMessage)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content = %#v, want one block", result.Content)
	}
	if text, ok := result.Content[0].(types.TextContent); !ok || text.Text != "Hello" {
		t.Errorf("content[0] = %#v, want text \"Hello\"", result.Content[0])
	}
	if result.Usage.Input != 12 {
		t.Errorf("usage.input = %d, want 12 preserved from message_start", result.Usage.Input)
	}
	if result.Usage.TotalTokens != 12 {
		t.Errorf("usage.totalTokens = %d, want 12", result.Usage.TotalTokens)
	}
}

func TestAnthropicSSEParsingIgnoresUnknownEventsAfterMessageStop(t *testing.T) {
	model := anthropicTestModel("claude-haiku-4-5", types.ProviderAnthropic, nil)
	events := append(append([]anthropicTestEvent{}, minimalAnthropicEvents...),
		anthropicTestEvent{event: "done", data: "[DONE]"},
		anthropicTestEvent{event: "proxy.stats", data: "not json"},
	)

	var captured capturedAnthropicRequest
	options := anthropicTestOptions(events, &captured)
	result := runAnthropicStream(t, model, &types.Context{Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Say hello."}, Timestamp: 1}}}, options)

	if result.StopReason != types.StopReasonStop {
		t.Errorf("stopReason = %q, want stop", result.StopReason)
	}
	if result.ErrorMessage != "" {
		t.Errorf("errorMessage = %q, want empty", result.ErrorMessage)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content = %#v, want one block", result.Content)
	}
	if text, ok := result.Content[0].(types.TextContent); !ok || text.Text != "Hello" {
		t.Errorf("content[0] = %#v, want text \"Hello\"", result.Content[0])
	}
}
