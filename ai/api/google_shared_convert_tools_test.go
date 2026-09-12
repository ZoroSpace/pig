// pi: test/google-shared-convert-tools.test.ts.
package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

func googleTestTool(parameters any) types.Tool {
	tool := types.Tool{
		Name:        "test_tool",
		Description: "A test tool",
	}
	if raw, err := json.Marshal(parameters); err == nil {
		tool.Parameters = raw
	}
	return tool
}

func googleTestDeclaration(t *testing.T, tools []types.Tool, useParameters bool) map[string]any {
	t.Helper()
	result, err := ConvertGoogleTools(tools, useParameters, true)
	if err != nil {
		t.Fatalf("ConvertGoogleTools failed: %v", err)
	}
	if len(result) != 1 || len(result[0].FunctionDeclarations) != 1 {
		t.Fatalf("ConvertGoogleTools = %+v, want one declaration", result)
	}
	return result[0].FunctionDeclarations[0]
}

func TestGoogleConvertToolsStripsMetaKeysWhenUseParameters(t *testing.T) {
	tools := []types.Tool{googleTestTool(map[string]any{
		"$schema":  "http://json-schema.org/draft-07/schema#",
		"$id":      "urn:bash-tool",
		"$comment": "A bash tool for demonstration",
		"$defs": map[string]any{
			"commandDef": map[string]any{"type": "string"},
		},
		"definitions": map[string]any{
			"legacyDef": map[string]any{"type": "number"},
		},
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
		},
		"required": []any{"command"},
	})}

	decl := googleTestDeclaration(t, tools, true)
	parameters := decl["parameters"]
	want := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
		},
		"required": []any{"command"},
	}
	if !reflect.DeepEqual(parameters, want) {
		t.Errorf("parameters = %v, want %v", parameters, want)
	}
	for _, key := range []string{"$schema", "$id", "$comment", "$defs", "definitions"} {
		if _, ok := parameters.(map[string]any)[key]; ok {
			t.Errorf("parameters retains meta key %q", key)
		}
	}
}

func TestGoogleConvertToolsStripsNestedMetaKeys(t *testing.T) {
	tools := []types.Tool{googleTestTool(map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type":    "object",
		"properties": map[string]any{
			"deep": map[string]any{
				"$schema": "http://json-schema.org/draft-07/schema#",
				"$id":     "urn:nested",
				"type":    "string",
			},
		},
	})}

	decl := googleTestDeclaration(t, tools, true)
	want := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"deep": map[string]any{"type": "string"},
		},
	}
	if !reflect.DeepEqual(decl["parameters"], want) {
		t.Errorf("parameters = %v, want %v", decl["parameters"], want)
	}
}

func TestGoogleConvertToolsPreservesRef(t *testing.T) {
	tools := []types.Tool{googleTestTool(map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type":    "object",
		"properties": map[string]any{
			"refProp": map[string]any{
				"$ref": "#/$defs/someDef",
				"type": "string",
			},
		},
	})}

	decl := googleTestDeclaration(t, tools, true)
	want := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"refProp": map[string]any{
				"$ref": "#/$defs/someDef",
				"type": "string",
			},
		},
	}
	if !reflect.DeepEqual(decl["parameters"], want) {
		t.Errorf("parameters = %v, want %v", decl["parameters"], want)
	}
}

func TestGoogleConvertToolsDoesNotMutateOriginalParameters(t *testing.T) {
	originalParameters := map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type":    "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
		},
		"required": []any{"command"},
	}
	tools := []types.Tool{googleTestTool(originalParameters)}

	if _, err := ConvertGoogleTools(tools, true, true); err != nil {
		t.Fatalf("ConvertGoogleTools failed: %v", err)
	}

	if !reflect.DeepEqual(originalParameters["properties"], map[string]any{
		"command": map[string]any{"type": "string"},
	}) {
		t.Errorf("original parameters mutated: %v", originalParameters)
	}
	if originalParameters["$schema"] != "http://json-schema.org/draft-07/schema#" {
		t.Errorf("original $schema mutated: %v", originalParameters["$schema"])
	}
}

func TestGoogleConvertToolsPreservesSchemaInParametersJsonSchema(t *testing.T) {
	tools := []types.Tool{googleTestTool(map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type":    "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
		},
		"required": []any{"command"},
	})}

	decl := googleTestDeclaration(t, tools, false)
	var rawSchema any
	if err := json.Unmarshal(decl["parametersJsonSchema"].(json.RawMessage), &rawSchema); err != nil {
		t.Fatalf("parametersJsonSchema is not JSON: %v", err)
	}
	want := map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type":    "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
		},
		"required": []any{"command"},
	}
	if !reflect.DeepEqual(rawSchema, want) {
		t.Errorf("parametersJsonSchema = %v, want %v", rawSchema, want)
	}
}

func TestGoogleConvertToolsHandlesToolsWithoutSchema(t *testing.T) {
	tools := []types.Tool{googleTestTool(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []any{"path"},
	})}

	decl := googleTestDeclaration(t, tools, true)
	want := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []any{"path"},
	}
	if !reflect.DeepEqual(decl["parameters"], want) {
		t.Errorf("parameters = %v, want %v", decl["parameters"], want)
	}
}

func TestGoogleConvertToolsValidatedCallingForStrictToolsOnGemini3(t *testing.T) {
	tool := googleTestTool(map[string]any{"type": "object", "properties": map[string]any{}})
	tool.ConstrainedSampling = map[string]any{"type": "json_schema", "strict": "require"}

	if !SupportsGoogleStrictToolSampling("gemini-3.1-pro-preview") {
		t.Error("gemini-3.1-pro-preview should support strict tool sampling")
	}
	if SupportsGoogleStrictToolSampling("gemini-2.5-pro") {
		t.Error("gemini-2.5-pro should not support strict tool sampling")
	}
	mode, err := ResolveGoogleFunctionCallingMode([]types.Tool{tool}, "", true)
	if err != nil {
		t.Fatalf("ResolveGoogleFunctionCallingMode failed: %v", err)
	}
	if mode != "VALIDATED" {
		t.Errorf("mode = %q, want VALIDATED", mode)
	}
	if _, err := ResolveGoogleFunctionCallingMode([]types.Tool{tool}, "", false); err == nil ||
		err.Error() != `Tool "test_tool" requires JSON-schema constrained sampling, but strict tools are unsupported.` {
		t.Errorf("err = %v, want the constrained-sampling failure", err)
	}
}

func TestGoogleConvertToolsReturnsNilForEmptyToolList(t *testing.T) {
	empty := []types.Tool{}
	if result, err := ConvertGoogleTools(empty, false, true); err != nil || result != nil {
		t.Errorf("ConvertGoogleTools(empty) = %v, %v; want nil, nil", result, err)
	}
	if result, err := ConvertGoogleTools(empty, true, true); err != nil || result != nil {
		t.Errorf("ConvertGoogleTools(empty, true) = %v, %v; want nil, nil", result, err)
	}
}
