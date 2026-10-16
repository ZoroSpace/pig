// pi: test/validation.test.ts. The "still validates when Function constructor
// is unavailable" case is missing: it exercises TypeBox's interpreted fallback
// when eval is CSP-blocked, and pig has no code-generated validator to fall
// back from.
package utils

import (
	"encoding/json"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

func rawSchema(t *testing.T, schema any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	return encoded
}

// createToolCallWithPlainSchema wraps a parameter schema in
// { type: "object", properties: { value: schema }, required: ["value"] }.
func createToolCallWithPlainSchema(t *testing.T, parameterSchema any, value any) (*types.Tool, *types.ToolCall) {
	t.Helper()
	tool := &types.Tool{
		Name:        "echo",
		Description: "Echo tool",
		Parameters: rawSchema(t, map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": parameterSchema,
			},
			"required": []string{"value"},
		}),
	}
	toolCall := &types.ToolCall{
		Kind:      types.KindToolCall,
		ID:        "tool-1",
		Name:      "echo",
		Arguments: map[string]any{"value": value},
	}
	return tool, toolCall
}

func TestValidateToolArgumentsCoercesPlainJSONSchemas(t *testing.T) {
	passingCases := []struct {
		schema   map[string]any
		input    any
		expected any
	}{
		{schema: map[string]any{"type": "number"}, input: "42", expected: float64(42)},
		{schema: map[string]any{"type": "number"}, input: true, expected: float64(1)},
		{schema: map[string]any{"type": "number"}, input: nil, expected: float64(0)},
		{schema: map[string]any{"type": "integer"}, input: "42", expected: float64(42)},
		{schema: map[string]any{"type": "boolean"}, input: "true", expected: true},
		{schema: map[string]any{"type": "boolean"}, input: "false", expected: false},
		{schema: map[string]any{"type": "boolean"}, input: float64(1), expected: true},
		{schema: map[string]any{"type": "boolean"}, input: float64(0), expected: false},
		{schema: map[string]any{"type": "string"}, input: nil, expected: ""},
		{schema: map[string]any{"type": "string"}, input: true, expected: "true"},
		{schema: map[string]any{"type": "null"}, input: "", expected: nil},
		{schema: map[string]any{"type": "null"}, input: float64(0), expected: nil},
		{schema: map[string]any{"type": "null"}, input: false, expected: nil},
		{schema: map[string]any{"type": []string{"number", "string"}}, input: "1", expected: "1"},
		{schema: map[string]any{"type": []string{"boolean", "number"}}, input: "1", expected: float64(1)},
	}

	for _, testCase := range passingCases {
		tool, toolCall := createToolCallWithPlainSchema(t, testCase.schema, testCase.input)
		validated, err := ValidateToolArguments(tool, toolCall)
		if err != nil {
			t.Fatalf("schema %v input %v: unexpected error: %v", testCase.schema, testCase.input, err)
		}
		args := validated.(map[string]any)
		if value, ok := args["value"]; !ok || value != testCase.expected {
			t.Errorf("schema %v input %v: got %#v, want %#v", testCase.schema, testCase.input, args["value"], testCase.expected)
		}
	}
}

func TestValidateToolArgumentsRejectsInvalidCoercions(t *testing.T) {
	failingCases := []struct {
		schema map[string]any
		input  any
	}{
		{schema: map[string]any{"type": "boolean"}, input: "1"},
		{schema: map[string]any{"type": "boolean"}, input: "0"},
		{schema: map[string]any{"type": "null"}, input: "null"},
		{schema: map[string]any{"type": "integer"}, input: "42.1"},
	}

	for _, testCase := range failingCases {
		tool, toolCall := createToolCallWithPlainSchema(t, testCase.schema, testCase.input)
		_, err := ValidateToolArguments(tool, toolCall)
		if err == nil {
			t.Errorf("schema %v input %v: expected validation failure", testCase.schema, testCase.input)
		} else if want := "Validation failed"; len(err.Error()) < len(want) || err.Error()[:len(want)] != want {
			t.Errorf("schema %v input %v: error %q should start with %q", testCase.schema, testCase.input, err.Error(), want)
		}
	}
}

// toolCallWithSchema builds a tool/argument pair with a top-level schema.
func toolCallWithSchema(t *testing.T, schema json.RawMessage, arguments map[string]any) (*types.Tool, *types.ToolCall) {
	t.Helper()
	return &types.Tool{Name: "echo", Description: "Echo tool", Parameters: schema},
		&types.ToolCall{Kind: types.KindToolCall, ID: "tool-1", Name: "echo", Arguments: arguments}
}

func TestValidateToolArgumentsTreatsNullAsOmissionForOptionalNonNullableProperties(t *testing.T) {
	tool, toolCall := toolCallWithSchema(t, rawSchema(t, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":     map[string]any{"type": "string"},
			"offset":   map[string]any{"type": "number"},
			"nullable": map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "null"}}},
			"metadata": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"enabled": map[string]any{"type": "boolean"},
				},
			},
		},
		"required": []string{"path"},
	}), map[string]any{
		"path":     "file.txt",
		"offset":   nil,
		"nullable": nil,
		"metadata": map[string]any{"enabled": nil},
	})

	validated, err := ValidateToolArguments(tool, toolCall)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := validated.(map[string]any)
	if args["path"] != "file.txt" {
		t.Errorf("path: got %#v", args["path"])
	}
	if _, present := args["offset"]; present {
		t.Errorf("offset should be dropped, got %#v", args["offset"])
	}
	if value, present := args["nullable"]; !present || value != nil {
		t.Errorf("nullable: got %#v present=%v", args["nullable"], present)
	}
	metadata := args["metadata"].(map[string]any)
	if _, present := metadata["enabled"]; present {
		t.Errorf("metadata.enabled should be dropped, got %#v", metadata)
	}
}

func TestValidateToolArgumentsPreservesOptionalNullsWithNullableReferencedSchema(t *testing.T) {
	tool, toolCall := toolCallWithSchema(t, rawSchema(t, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"value": map[string]any{"$ref": "#/$defs/value"},
		},
		"$defs": map[string]any{
			"value": map[string]any{"anyOf": []any{map[string]any{"type": "number"}, map[string]any{"type": "null"}}},
		},
	}), map[string]any{"value": nil})

	validated, err := ValidateToolArguments(tool, toolCall)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := validated.(map[string]any)
	if value, present := args["value"]; !present || value != nil {
		t.Errorf("value: got %#v present=%v", args["value"], present)
	}
}

func TestValidateToolArgumentsPreservesNullableUnionArms(t *testing.T) {
	t.Run("anyOf via type list", func(t *testing.T) {
		tool, toolCall := toolCallWithSchema(t, rawSchema(t, map[string]any{
			"type":       "object",
			"properties": map[string]any{"value": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "string"}}},
		}), map[string]any{"value": nil})
		validated, err := ValidateToolArguments(tool, toolCall)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if value := validated.(map[string]any)["value"]; value != nil {
			t.Errorf("value: got %#v, want nil", value)
		}
	})

	t.Run("oneOf", func(t *testing.T) {
		tool, toolCall := createToolCallWithPlainSchema(t, map[string]any{
			"oneOf": []any{map[string]any{"type": "number"}, map[string]any{"type": "null"}},
		}, nil)
		validated, err := ValidateToolArguments(tool, toolCall)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if value := validated.(map[string]any)["value"]; value != nil {
			t.Errorf("value: got %#v, want nil", value)
		}
	})

	t.Run("union with null arm", func(t *testing.T) {
		tool, toolCall := toolCallWithSchema(t, rawSchema(t, map[string]any{
			"type":       "object",
			"properties": map[string]any{"value": map[string]any{"anyOf": []any{map[string]any{"type": "number"}, map[string]any{"type": "null"}}}},
		}), map[string]any{"value": nil})
		validated, err := ValidateToolArguments(tool, toolCall)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if value := validated.(map[string]any)["value"]; value != nil {
			t.Errorf("value: got %#v, want nil", value)
		}
	})

	t.Run("still coerces nullable unions when the original value does not match any arm", func(t *testing.T) {
		tool, toolCall := createToolCallWithPlainSchema(t, map[string]any{
			"anyOf": []any{map[string]any{"type": "number"}, map[string]any{"type": "null"}},
		}, "42")
		validated, err := ValidateToolArguments(tool, toolCall)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if value := validated.(map[string]any)["value"]; value != float64(42) {
			t.Errorf("value: got %#v, want 42", value)
		}
	})
}

func TestValidateToolArgumentsReportsMalformedArgumentsError(t *testing.T) {
	// #10897: a finished tool call whose arguments were malformed carries
	// argumentsError; validation fails with it instead of validating {}.
	tool, toolCall := createToolCallWithPlainSchema(t, map[string]any{"type": "string"}, "x")
	tool.Name = "echo"
	toolCall.Arguments = map[string]any{}
	toolCall.ArgumentsError = "Unexpected token"
	_, err := ValidateToolArguments(tool, toolCall)
	if err == nil || err.Error() != `Invalid arguments for tool "echo": Unexpected token` {
		t.Fatalf("got %v", err)
	}
}

func TestValidateToolCallNotFound(t *testing.T) {
	tool, toolCall := createToolCallWithPlainSchema(t, map[string]any{"type": "string"}, "x")
	tool.Name = "echo"
	_, err := ValidateToolCall([]types.Tool{*tool}, toolCall)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolCall.Name = "missing"
	if _, err := ValidateToolCall([]types.Tool{*tool}, toolCall); err == nil || err.Error() != `tool "missing" not found` {
		t.Errorf("got %v", err)
	}
}
