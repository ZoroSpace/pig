// pi: utils/validation.ts (exported through pi-ai's index; pig re-exports it
// from ai/index.go).
//
// Validates tool call arguments against a tool's JSON Schema, coercing
// near-miss values (string "42" into a number parameter, null into "" and so
// on) the way providers' own SDKs do before failing the call.
//
// Language-convergence deviations from the TS source, all declared here:
//
//   - TS carries two paths: a TypeBox validator (Compile/Value.Convert) for
//     TypeBox schemas and a hand-rolled JSON Schema coercion path for plain
//     JSON schemas. pig has no TypeBox: tool schemas are JSON Schema
//     documents by definition (types.Tool.Parameters is json.RawMessage), so
//     the two paths merge into one — the hand-rolled coercion rules are the
//     single coercion pass, and validation uses a checker over the same
//     schema subset. Value.Convert's coercion space is covered by
//     coerceWithJSONSchema; the merged pass is at least as coercive.
//   - The TYPEBOX_KIND symbol escape hatch (schemas with custom TypeBox
//     kinds skip the JSON Schema coercion pass) has no Go counterpart; every
//     schema goes through coercion.
//   - The schema subset handled is the one TS's JsonSchemaObject already
//     commits to (type/properties/required/items/additionalProperties/
//     allOf/anyOf/oneOf) plus enum/const, which TypeBox tool schemas use
//     heavily. Missing, to add when a real tool schema needs them: numeric
//     and string bound keywords (minimum/maximum/minLength/maxLength/pattern
//     /format) and $ref resolution inside validation (normalizeOptionalNulls
//     already treats $ref properties as opaque, matching TS).
//   - Validator error message strings differ from TypeBox's (no Go TypeBox
//     to mirror); the message *shape* — "Validation failed for tool "x":"
//     header, "  - <path>: <message>" lines, received-arguments dump — is
//     preserved because callers surface it verbatim in tool results.
package utils

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/ZoroSpace/pig/ai/types"
)

// jsonSchema is the schema subset pig validates and coerces against (see the
// package comment for the missing-keyword list).
type jsonSchema struct {
	Type                 json.RawMessage       `json:"type"`
	Properties           map[string]jsonSchema `json:"properties"`
	Required             []string              `json:"required"`
	Items                json.RawMessage       `json:"items"`
	AdditionalProperties json.RawMessage       `json:"additionalProperties"`
	AllOf                []jsonSchema          `json:"allOf"`
	AnyOf                []jsonSchema          `json:"anyOf"`
	OneOf                []jsonSchema          `json:"oneOf"`
	Enum                 []any                 `json:"enum"`
	Const                any                   `json:"const"`
	Ref                  string                `json:"$ref"`
}

// schemaError is one validation failure: a dotted instance path and a human
// readable message (pi: TLocalizedValidationError, reformatted by
// formatValidationPath).
type schemaError struct {
	path    string
	message string
}

// schemaTypeError reports a type mismatch; matchesJsonType feeds it.
func schemaTypeError(schema *jsonSchema, value any) schemaError {
	expected := schemaTypeNames(schema)
	if len(expected) == 0 {
		return schemaError{message: "unexpected value"}
	}
	return schemaError{message: fmt.Sprintf("expected %s, got %s", strings.Join(expected, " or "), jsonTypeName(value))}
}

func schemaTypeNames(schema *jsonSchema) []string {
	if len(schema.Type) == 0 {
		return nil
	}
	var single string
	if err := json.Unmarshal(schema.Type, &single); err == nil {
		return []string{single}
	}
	var list []string
	if err := json.Unmarshal(schema.Type, &list); err == nil {
		return list
	}
	return nil
}

func jsonTypeName(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64, int, int64, json.Number:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func matchesJSONType(value any, typeName string) bool {
	switch typeName {
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && n == float64(int64(n))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "null":
		return value == nil
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	default:
		return false
	}
}

func matchesAnySchemaType(value any, schema *jsonSchema) bool {
	for _, typeName := range schemaTypeNames(schema) {
		if matchesJSONType(value, typeName) {
			return true
		}
	}
	return false
}

// getSubSchemaCheck validates a value against a schema, reporting whether it
// passes (pi: getSubSchemaValidator(...)?.Check(value)); compile errors —
// here unparseable JSON — mean "no validator", i.e. pass.
func subSchemaCheck(value any, schema *jsonSchema) bool {
	return len(checkValue(value, schema, "")) == 0
}

func coercePrimitiveByType(value any, typeName string) any {
	switch typeName {
	case "number":
		switch v := value.(type) {
		case nil:
			return float64(0)
		case string:
			if v == "" {
				return value
			}
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				return parsed
			}
		case bool:
			if v {
				return float64(1)
			}
			return float64(0)
		}
		return value
	case "integer":
		switch v := value.(type) {
		case nil:
			return float64(0)
		case string:
			if v == "" {
				return value
			}
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && parsed == float64(int64(parsed)) {
				return parsed
			}
		case bool:
			if v {
				return float64(1)
			}
			return float64(0)
		}
		return value
	case "boolean":
		switch v := value.(type) {
		case nil:
			return false
		case string:
			if v == "true" {
				return true
			}
			if v == "false" {
				return false
			}
		case float64:
			if v == 1 {
				return true
			}
			if v == 0 {
				return false
			}
		}
		return value
	case "string":
		switch v := value.(type) {
		case nil:
			return ""
		case float64:
			return trimFloat(v)
		case bool:
			if v {
				return "true"
			}
			return "false"
		}
		return value
	case "null":
		switch value.(type) {
		case string:
			if value == "" {
				return nil
			}
		case float64:
			if value.(float64) == 0 {
				return nil
			}
		case bool:
			if !value.(bool) {
				return nil
			}
		}
		return value
	default:
		return value
	}
}

// trimFloat renders numbers the way TS String(number) does for the integer
// values coercion produces (String(42) === "42", not "42.0").
func trimFloat(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func applySchemaObjectCoercion(value map[string]any, schema *jsonSchema) {
	definedKeys := map[string]bool{}
	for key := range schema.Properties {
		definedKeys[key] = true
	}

	for key, propertySchema := range schema.Properties {
		if _, ok := value[key]; !ok {
			continue
		}
		value[key] = coerceWithJSONSchema(value[key], &propertySchema)
	}

	var additionalAsSchema jsonSchema
	if err := json.Unmarshal(schema.AdditionalProperties, &additionalAsSchema); err == nil && len(schema.AdditionalProperties) > 0 {
		for key, propertyValue := range value {
			if definedKeys[key] {
				continue
			}
			value[key] = coerceWithJSONSchema(propertyValue, &additionalAsSchema)
		}
	}
}

func applySchemaArrayCoercion(value []any, schema *jsonSchema) {
	var itemSchemas []jsonSchema
	if err := json.Unmarshal(schema.Items, &itemSchemas); err == nil && itemSchemas != nil {
		for index, itemSchema := range itemSchemas {
			if index >= len(value) {
				break
			}
			value[index] = coerceWithJSONSchema(value[index], &itemSchema)
		}
		return
	}

	var itemSchema jsonSchema
	if err := json.Unmarshal(schema.Items, &itemSchema); err == nil && len(schema.Items) > 0 {
		for index := range value {
			value[index] = coerceWithJSONSchema(value[index], &itemSchema)
		}
	}
}

func coerceWithUnionSchema(value any, schemas []jsonSchema) any {
	for i := range schemas {
		if subSchemaCheck(value, &schemas[i]) {
			return value
		}
	}

	for i := range schemas {
		candidate := deepCopyJSONValue(value)
		coerced := coerceWithJSONSchema(candidate, &schemas[i])
		if subSchemaCheck(coerced, &schemas[i]) {
			return coerced
		}
	}
	return value
}

func coerceWithJSONSchema(value any, schema *jsonSchema) any {
	nextValue := value

	for i := range schema.AllOf {
		nextValue = coerceWithJSONSchema(nextValue, &schema.AllOf[i])
	}

	if len(schema.AnyOf) > 0 {
		nextValue = coerceWithUnionSchema(nextValue, schema.AnyOf)
	}

	if len(schema.OneOf) > 0 {
		nextValue = coerceWithUnionSchema(nextValue, schema.OneOf)
	}

	schemaTypes := schemaTypeNames(schema)
	matchesUnionMember := len(schemaTypes) > 1 && matchesAnySchemaType(nextValue, schema)
	if len(schemaTypes) > 0 && !matchesUnionMember {
		for _, schemaType := range schemaTypes {
			candidate := coercePrimitiveByType(nextValue, schemaType)
			if !reflect.DeepEqual(candidate, nextValue) {
				nextValue = candidate
				break
			}
		}
	}

	if containsString(schemaTypes, "object") {
		if asMap, ok := nextValue.(map[string]any); ok {
			applySchemaObjectCoercion(asMap, schema)
		}
	}

	if containsString(schemaTypes, "array") {
		if asSlice, ok := nextValue.([]any); ok {
			applySchemaArrayCoercion(asSlice, schema)
		}
	}

	return nextValue
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func normalizeOptionalNulls(value any, schema *jsonSchema) {
	if asSlice, ok := value.([]any); ok {
		var itemSchemas []jsonSchema
		var itemSchema jsonSchema
		if err := json.Unmarshal(schema.Items, &itemSchemas); err == nil && itemSchemas != nil {
			for index := range itemSchemas {
				if index >= len(asSlice) {
					break
				}
				normalizeOptionalNulls(asSlice[index], &itemSchemas[index])
			}
		} else if err := json.Unmarshal(schema.Items, &itemSchema); err == nil && len(schema.Items) > 0 {
			for index := range asSlice {
				normalizeOptionalNulls(asSlice[index], &itemSchema)
			}
		}
		return
	}
	asMap, ok := value.(map[string]any)
	if !ok || schema.Properties == nil {
		return
	}

	required := map[string]bool{}
	for _, key := range schema.Required {
		required[key] = true
	}
	for key, propertyValue := range asMap {
		propertySchema, known := schema.Properties[key]
		if !known {
			continue
		}
		if propertyValue == nil && !required[key] && propertySchema.Ref == "" && !subSchemaCheck(nil, &propertySchema) {
			delete(asMap, key)
		} else {
			normalizeOptionalNulls(propertyValue, &propertySchema)
		}
	}
}

func checkValue(value any, schema *jsonSchema, path string) []schemaError {
	var errors []schemaError
	add := func(message string) {
		errors = append(errors, schemaError{path: path, message: message})
	}

	if len(schema.Type) > 0 && !matchesAnySchemaType(value, schema) {
		errors = append(errors, schemaTypeError(schema, value))
		return errors
	}

	if schema.Const != nil && !reflect.DeepEqual(schema.Const, value) {
		add(fmt.Sprintf("expected const value %s", renderJSON(schema.Const)))
	}
	if len(schema.Enum) > 0 {
		found := false
		for _, option := range schema.Enum {
			if reflect.DeepEqual(option, value) {
				found = true
				break
			}
		}
		if !found {
			add("value must be one of the enum options")
		}
	}

	for i := range schema.AllOf {
		errors = append(errors, checkValue(value, &schema.AllOf[i], path)...)
	}
	if len(schema.AnyOf) > 0 {
		ok := false
		for i := range schema.AnyOf {
			if len(checkValue(value, &schema.AnyOf[i], path)) == 0 {
				ok = true
				break
			}
		}
		if !ok {
			add("value does not match any anyOf schema")
		}
	}
	if len(schema.OneOf) > 0 {
		matches := 0
		for i := range schema.OneOf {
			if len(checkValue(value, &schema.OneOf[i], path)) == 0 {
				matches++
			}
		}
		if matches != 1 {
			add("value must match exactly one oneOf schema")
		}
	}

	switch v := value.(type) {
	case map[string]any:
		for key, propertySchema := range schema.Properties {
			propertyValue, present := v[key]
			propertyPath := joinPath(path, key)
			if !present {
				if containsString(schema.Required, key) {
					errors = append(errors, schemaError{path: propertyPath, message: "required property missing"})
				}
				continue
			}
			errors = append(errors, checkValue(propertyValue, &propertySchema, propertyPath)...)
		}

		var additionalAsSchema jsonSchema
		additionalBool := false
		hasSchema := len(schema.AdditionalProperties) > 0 && string(schema.AdditionalProperties) != "null" &&
			json.Unmarshal(schema.AdditionalProperties, &additionalAsSchema) == nil
		_ = json.Unmarshal(schema.AdditionalProperties, &additionalBool)
		if hasSchema {
			for key, propertyValue := range v {
				if _, known := schema.Properties[key]; known {
					continue
				}
				errors = append(errors, checkValue(propertyValue, &additionalAsSchema, joinPath(path, key))...)
			}
		} else if additionalBool == false && len(schema.AdditionalProperties) > 0 && string(schema.AdditionalProperties) != "null" {
			for key := range v {
				if _, known := schema.Properties[key]; known {
					continue
				}
				errors = append(errors, schemaError{path: joinPath(path, key), message: "additional property not allowed"})
			}
		}
	case []any:
		var itemSchemas []jsonSchema
		var itemSchema jsonSchema
		if err := json.Unmarshal(schema.Items, &itemSchemas); err == nil && itemSchemas != nil {
			for index := range itemSchemas {
				if index >= len(v) {
					break
				}
				errors = append(errors, checkValue(v[index], &itemSchemas[index], fmt.Sprintf("%s/%d", path, index))...)
			}
		} else if err := json.Unmarshal(schema.Items, &itemSchema); err == nil && len(schema.Items) > 0 {
			for index, item := range v {
				errors = append(errors, checkValue(item, &itemSchema, fmt.Sprintf("%s/%d", path, index))...)
			}
		}
	}

	return errors
}

func joinPath(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

func renderJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}

// formatValidationPath mirrors pi's formatValidationPath: dotted instance
// path, "root" when empty.
func formatValidationPath(path string) string {
	out := strings.ReplaceAll(strings.TrimPrefix(path, "/"), "/", ".")
	if out == "" {
		return "root"
	}
	return out
}

// ValidateToolCall finds a tool by name and validates the tool call arguments
// against its schema (pi: validateToolCall). Returns the validated arguments.
// Returns an error if the tool is not found or validation fails.
func ValidateToolCall(tools []types.Tool, toolCall *types.ToolCall) (any, error) {
	for i := range tools {
		if tools[i].Name == toolCall.Name {
			return ValidateToolArguments(&tools[i], toolCall)
		}
	}
	return nil, fmt.Errorf("tool %q not found", toolCall.Name)
}

// ValidateToolArguments validates tool call arguments against the tool's
// schema (pi: validateToolArguments), coercing near-miss values along the
// way. Returns the validated (and potentially coerced) arguments, or an error
// with the formatted message callers embed into error tool results.
func ValidateToolArguments(tool *types.Tool, toolCall *types.ToolCall) (any, error) {
	if toolCall.ArgumentsError != "" {
		return nil, fmt.Errorf("Invalid arguments for tool %q: %s", toolCall.Name, toolCall.ArgumentsError)
	}
	args := deepCopyJSONValue(toolCall.Arguments)

	var schema jsonSchema
	if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
		return nil, fmt.Errorf("invalid tool schema for %q: %w", toolCall.Name, err)
	}

	normalizeOptionalNulls(args, &schema)
	coerced := coerceWithJSONSchema(args, &schema)
	if !sameJSONIdentity(coerced, args) {
		coercedMap, coercedIsMap := coerced.(map[string]any)
		argsMap, argsIsMap := args.(map[string]any)
		if coercedIsMap && argsIsMap {
			// Fold the coerced root back onto the original object so callers
			// holding the map keep one identity (pi mutates args in place).
			for key := range argsMap {
				delete(argsMap, key)
			}
			for key, value := range coercedMap {
				argsMap[key] = value
			}
		} else {
			if len(checkValue(coerced, &schema, "")) == 0 {
				return coerced, nil
			}
			return args, nil
		}
	}

	if len(checkValue(args, &schema, "")) == 0 {
		return args, nil
	}

	// Deterministic error order (TS iterates the validator's error stream).
	paths := make([]string, 0)
	byPath := map[string][]string{}
	for _, err := range checkValue(args, &schema, "") {
		line := fmt.Sprintf("  - %s: %s", formatValidationPath(err.path), err.message)
		if _, seen := byPath[err.path]; !seen {
			paths = append(paths, err.path)
		}
		byPath[err.path] = append(byPath[err.path], line)
	}
	sort.Strings(paths)
	var lines []string
	for _, path := range paths {
		lines = append(lines, byPath[path]...)
	}
	joined := strings.Join(lines, "\n")
	if joined == "" {
		joined = "Unknown validation error"
	}

	return nil, fmt.Errorf("Validation failed for tool %q:\n%s\n\nReceived arguments:\n%s",
		toolCall.Name, joined, renderJSON(toolCall.Arguments))
}

// deepCopyJSONValue is pi's structuredClone for JSON-shaped values
// (maps/slices/scalars). Non-JSON scalars are copied by reference, matching
// structuredClone semantics for immutable values.
func deepCopyJSONValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = deepCopyJSONValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for index, item := range v {
			out[index] = deepCopyJSONValue(item)
		}
		return out
	default:
		return value
	}
}

// sameJSONIdentity reports whether two JSON values are the same reference
// (pi's `coerced !== args`). Maps and slices compare by runtime pointer;
// scalars compare by value.
func sameJSONIdentity(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		return ok && reflect.ValueOf(av).Pointer() == reflect.ValueOf(bv).Pointer()
	case []any:
		bv, ok := b.([]any)
		return ok && reflect.ValueOf(av).Pointer() == reflect.ValueOf(bv).Pointer()
	default:
		return reflect.DeepEqual(a, b)
	}
}
