// pi: test/anthropic-strict-tool-schema.test.ts (the strict-schema payload
// test moved here from anthropic-eager-tool-input-compat.test.ts in 295cc72b0,
// plus the #9953 rejected-keywords regression).
package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

// strictToolSchemaModel mirrors pi's createModel: forceAdaptiveThinking +
// supportsStrictTools.
func strictToolSchemaModel() *types.Model {
	return anthropicTestModel("claude-opus-4-8", types.ProviderID("test-anthropic"), &types.AnthropicMessagesCompat{
		ForceAdaptiveThinking: boolPtr(true),
		SupportsStrictTools:   boolPtr(true),
	})
}

func strictSchemaTestTool(parameters string) types.Tool {
	return types.Tool{
		Name:        "lookup",
		Description: "Look up a value",
		Parameters:  json.RawMessage(parameters),
	}
}

func strictSchemaPreferTool(parameters string) types.Tool {
	tool := strictSchemaTestTool(parameters)
	tool.ConstrainedSampling = map[string]any{"type": "json_schema", "strict": "prefer"}
	return tool
}

func strictSchemaTestContext(tools []types.Tool) *types.TranscriptContext {
	messages := []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Use the tool"}, Timestamp: 1}}
	if len(tools) > 0 {
		messages = append([]types.Message{types.SystemMessage{
			Role: types.RoleSystem, Content: types.SystemContent{String: ""},
			ToolsAdded: tools, Timestamp: 1,
		}}, messages...)
	}
	return &types.TranscriptContext{Messages: messages}
}

// captureStrictSchemaFirstTool captures the first tool payload through
// OnPayload (pi: captureFirstTool with the PayloadCaptured throw; pig's
// captureAnthropicPayload cannot abort, so the unreachable base URL fails the
// request after capture instead).
func captureStrictSchemaFirstTool(t *testing.T, tool types.Tool) map[string]any {
	t.Helper()
	model := strictToolSchemaModel()
	model.BaseURL = unreachableAnthropicBaseURL
	options := &AnthropicOptions{}
	captured := captureAnthropicPayload(t, model, strictSchemaTestContext([]types.Tool{tool}), options, nil)
	// Round-trip through JSON so nested typed slices ([]map[string]any)
	// become the generic []any shape the assertions walk.
	encoded, err := json.Marshal(captured)
	if err != nil {
		t.Fatal(err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		t.Fatal(err)
	}
	tools, ok := normalized["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("Expected a tool in the captured Anthropic payload, got %#v", normalized)
	}
	first, _ := tools[0].(map[string]any)
	if first == nil {
		t.Fatalf("Expected a tool object, got %#v", tools[0])
	}
	return first
}

func strictSchemaInputSchema(t *testing.T, tool map[string]any) map[string]any {
	t.Helper()
	schema, ok := tool["input_schema"].(map[string]any)
	if !ok {
		t.Fatalf("Expected first tool input schema in request body, got %#v", tool["input_schema"])
	}
	return schema
}

func TestAnthropicStrictToolSchemaSendsFullInputSchemaOnlyForStrictTools(t *testing.T) {
	legacyParameters := `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false,"title":"LookupInput"}`
	legacyTool := captureStrictSchemaFirstTool(t, strictSchemaTestTool(legacyParameters))
	if _, sent := legacyTool["strict"]; sent {
		t.Errorf("strict = %#v, want it omitted", legacyTool["strict"])
	}
	var legacyParsed map[string]any
	if err := json.Unmarshal([]byte(legacyParameters), &legacyParsed); err != nil {
		t.Fatal(err)
	}
	wantSchema := map[string]any{
		"type":       "object",
		"properties": legacyParsed["properties"],
		"required":   legacyParsed["required"],
	}
	if got := strictSchemaInputSchema(t, legacyTool); !reflect.DeepEqual(got, wantSchema) {
		t.Errorf("legacy input_schema = %#v, want %#v", got, wantSchema)
	}

	strictParameters := `{"type":"object","properties":{"value":{"type":"string"},"optional":{"type":"number"}},"required":["value"],"title":"StrictLookupInput"}`
	strictTool := captureStrictSchemaFirstTool(t, strictSchemaPreferTool(strictParameters))
	if got, _ := strictTool["strict"].(bool); !got {
		t.Errorf("strict = %#v, want true", strictTool["strict"])
	}
	schema := strictSchemaInputSchema(t, strictTool)
	if got, present := schema["additionalProperties"]; !present || got != false {
		t.Errorf("additionalProperties = %#v, want false", schema["additionalProperties"])
	}
	required := schema["required"]
	requiredList, _ := required.([]any)
	var requiredStrings []string
	for _, entry := range requiredList {
		if s, ok := entry.(string); ok {
			requiredStrings = append(requiredStrings, s)
		}
	}
	if strings.Join(requiredStrings, ",") != "value,optional" {
		t.Errorf("required = %#v, want [value optional]", required)
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

// https://github.com/earendil-works/pi/issues/9953
func TestAnthropicStrictToolSchemaSendsPreferToolsNonStrictWhenKeywordsRejected(t *testing.T) {
	unsupportedParameters := []string{
		`{"type":"object","properties":{"timeoutMs":{"type":"integer","minimum":1,"maximum":300000}},"required":["timeoutMs"]}`,
		`{"type":"object","properties":{"options":{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"},"minItems":2}},"required":["tags"]}},"required":["options"]}`,
		`{"type":"object","properties":{"expression":{"type":"string","format":"regex"}},"required":["expression"]}`,
	}
	for _, parameters := range unsupportedParameters {
		tool := captureStrictSchemaFirstTool(t, strictSchemaPreferTool(parameters))
		if _, sent := tool["strict"]; sent {
			t.Errorf("strict = %#v, want it omitted for %s", tool["strict"], parameters)
		}
	}

	supportedTool := captureStrictSchemaFirstTool(t, strictSchemaPreferTool(
		`{"type":"object","properties":{"code":{"type":"string","minLength":1,"maxLength":1000,"pattern":"^[a-z]+$"},"url":{"type":"string","format":"uri"},"tags":{"type":"array","items":{"type":"string"},"minItems":1}},"required":["code","url","tags"]}`,
	))
	if got, _ := supportedTool["strict"].(bool); !got {
		t.Errorf("strict = %#v, want true", supportedTool["strict"])
	}
}
