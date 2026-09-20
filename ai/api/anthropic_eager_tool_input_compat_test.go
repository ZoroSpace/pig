// pi: test/anthropic-eager-tool-input-compat.test.ts.
package api

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

// eagerTestModel mirrors pi's createModel: forceAdaptiveThinking on, optional
// compat overlay.
func eagerTestModel(compat *types.AnthropicMessagesCompat) *types.Model {
	merged := &types.AnthropicMessagesCompat{ForceAdaptiveThinking: boolPtr(true)}
	if compat != nil {
		if compat.SupportsEagerToolInputStreaming != nil {
			merged.SupportsEagerToolInputStreaming = compat.SupportsEagerToolInputStreaming
		}
		if compat.SupportsStrictTools != nil {
			merged.SupportsStrictTools = compat.SupportsStrictTools
		}
	}
	model := anthropicTestModel("claude-opus-4-8", types.ProviderID("test-anthropic"), merged)
	model.BaseURL = "http://127.0.0.1:9" // replaced per test by the fake fetch
	return model
}

func boolPtr(b bool) *bool { return &b }

var eagerTestTool = types.Tool{
	Name:        "lookup",
	Description: "Look up a value",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`),
}

var eagerSchemaCompatibilityTool = types.Tool{
	Name:        "lookup",
	Description: "Look up a value",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false,"title":"LookupInput"}`),
}

var eagerStrictTool = types.Tool{
	Name:        "lookup",
	Description: "Look up a value",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"},"optional":{"type":"number"}},"required":["value"],"title":"StrictLookupInput"}`),
	ConstrainedSampling: map[string]any{
		"type":   "json_schema",
		"strict": "prefer",
	},
}

func eagerTestContext(tools []types.Tool) *types.TranscriptContext {
	messages := []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Use the tool"}, Timestamp: 1}}
	if len(tools) > 0 {
		// v0.86.0: tools are declared by the leading system message.
		messages = append([]types.Message{types.SystemMessage{
			Role: types.RoleSystem, Content: types.SystemContent{String: ""},
			ToolsAdded: tools, Timestamp: 1,
		}}, messages...)
	}
	return &types.TranscriptContext{Messages: messages}
}

// captureEagerRequest drives one request through the fake fetch and returns
// the captured request (pi: captureAnthropicRequest against a local HTTP
// server; the fake transport captures the same wire data in-process).
func captureEagerRequest(t *testing.T, compat *types.AnthropicMessagesCompat, llmContext *types.TranscriptContext) *capturedAnthropicRequest {
	t.Helper()
	model := eagerTestModel(compat)
	model.BaseURL = "http://127.0.0.1:9"
	var captured capturedAnthropicRequest
	options := anthropicTestOptions(nil, &captured)
	options.CacheRetention = types.CacheRetentionNone
	options.APIKey = "test-key"
	stream := streamAnthropicMessages(context.Background(), model, llmContext, options)
	result := stream.Result()
	if result.StopReason != types.StopReasonError && result.StopReason != types.StopReasonStop {
		t.Fatalf("unexpected stop reason %q (%s)", result.StopReason, result.ErrorMessage)
	}
	if captured.body == nil {
		t.Fatal("Anthropic request was not captured")
	}
	return &captured
}

func firstEagerTool(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("Expected first tool in request body, got %#v", body["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	return tool
}

func firstEagerToolInputSchema(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	schema, ok := firstEagerTool(t, body)["input_schema"].(map[string]any)
	if !ok {
		t.Fatalf("Expected first tool input schema in request body, got %#v", firstEagerTool(t, body)["input_schema"])
	}
	return schema
}

func TestAnthropicEagerToolInputSendsPerToolByDefault(t *testing.T) {
	captured := captureEagerRequest(t, nil, eagerTestContext([]types.Tool{eagerTestTool}))
	if got, _ := firstEagerTool(t, captured.body)["eager_input_streaming"].(bool); !got {
		t.Errorf("eager_input_streaming = %#v, want true", firstEagerTool(t, captured.body)["eager_input_streaming"])
	}
	if captured.req.Header.Get("anthropic-beta") != "" {
		t.Errorf("anthropic-beta = %q, want no header", captured.req.Header.Get("anthropic-beta"))
	}
}

func TestAnthropicEagerToolInputLegacyBetaWhenDisabled(t *testing.T) {
	captured := captureEagerRequest(t, &types.AnthropicMessagesCompat{SupportsEagerToolInputStreaming: boolPtr(false)}, eagerTestContext([]types.Tool{eagerTestTool}))
	if _, sent := firstEagerTool(t, captured.body)["eager_input_streaming"]; sent {
		t.Errorf("eager_input_streaming = %#v, want it omitted", firstEagerTool(t, captured.body)["eager_input_streaming"])
	}
	if got := captured.req.Header.Get("anthropic-beta"); got != "fine-grained-tool-streaming-2025-05-14" {
		t.Errorf("anthropic-beta = %q, want the legacy fine-grained beta", got)
	}
}

func TestAnthropicEagerToolInputNoBetaWithoutTools(t *testing.T) {
	captured := captureEagerRequest(t, &types.AnthropicMessagesCompat{SupportsEagerToolInputStreaming: boolPtr(false)}, eagerTestContext(nil))
	if _, present := captured.body["tools"]; present {
		t.Errorf("tools = %#v, want it omitted", captured.body["tools"])
	}
	if got := captured.req.Header.Get("anthropic-beta"); got != "" {
		t.Errorf("anthropic-beta = %q, want no header", got)
	}
}

func TestAnthropicEagerToolInputStrictSchemas(t *testing.T) {
	legacyCaptured := captureEagerRequest(t, &types.AnthropicMessagesCompat{SupportsStrictTools: boolPtr(true)}, eagerTestContext([]types.Tool{eagerSchemaCompatibilityTool}))
	wantSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"value": map[string]any{"type": "string"},
		},
		"required": []any{"value"},
	}
	if got := firstEagerToolInputSchema(t, legacyCaptured.body); !reflect.DeepEqual(got, wantSchema) {
		t.Errorf("legacy input_schema = %#v, want %#v", got, wantSchema)
	}

	strictCaptured := captureEagerRequest(t, &types.AnthropicMessagesCompat{SupportsStrictTools: boolPtr(true)}, eagerTestContext([]types.Tool{eagerStrictTool}))
	strictTool := firstEagerTool(t, strictCaptured.body)
	if got, _ := strictTool["strict"].(bool); !got {
		t.Errorf("strict = %#v, want true", strictTool["strict"])
	}
	schema := firstEagerToolInputSchema(t, strictCaptured.body)
	if got, present := schema["additionalProperties"]; !present || got != false {
		t.Errorf("additionalProperties = %#v, want false", schema["additionalProperties"])
	}
	required := strings.Join(stringifySchemaStrings(schema["required"]), ",")
	if required != "value,optional" {
		t.Errorf("required = %#v, want [value optional]", schema["required"])
	}
	properties, _ := schema["properties"].(map[string]any)
	optional, _ := properties["optional"].(map[string]any)
	anyOf, _ := optional["anyOf"].([]any)
	if len(anyOf) != 2 {
		t.Errorf("optional property = %#v, want anyOf [number, null]", optional)
	}
	if schema["title"] != "StrictLookupInput" {
		t.Errorf("title = %#v, want StrictLookupInput", schema["title"])
	}
}

func stringifySchemaStrings(value any) []string {
	arr, _ := value.([]any)
	var out []string
	for _, entry := range arr {
		if s, ok := entry.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
