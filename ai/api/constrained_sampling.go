// pi: api/constrained-sampling.ts.
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/ZoroSpace/pig/ai/types"
)

// UnsupportedStrictJSONSchemaError marks a schema that the strict subset
// cannot represent (pi: UnsupportedStrictJsonSchemaError).
type UnsupportedStrictJSONSchemaError struct{ Message string }

func (e *UnsupportedStrictJSONSchemaError) Error() string { return e.Message }

var UNSUPPORTED_STRICT_SCHEMA_KEYS = []string{
	"$ref", "$defs", "definitions", "allOf", "oneOf", "patternProperties",
	"dependentSchemas", "dependencies", "unevaluatedProperties", "propertyNames",
	"contains", "prefixItems", "not", "if", "then", "else",
}

func isJsonSchemaObject(value any) bool {
	_, ok := value.(map[string]any)
	return ok
}

func schemaTypes(schema any) []string {
	m, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	switch t := m["type"].(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, v := range t {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func isStructuredSchema(schema any) bool {
	if !isJsonSchemaObject(schema) {
		return false
	}
	m := schema.(map[string]any)
	types := schemaTypes(schema)
	for _, t := range types {
		if t == "object" || t == "array" {
			return true
		}
	}
	_, hasProperties := m["properties"]
	_, hasItems := m["items"]
	return hasProperties || hasItems
}

func schemaAllowsNull(schema any) bool {
	if !isJsonSchemaObject(schema) {
		return false
	}
	m := schema.(map[string]any)
	if t, ok := m["type"].(string); ok && t == "null" {
		return true
	}
	if types, ok := m["type"].([]any); ok {
		for _, v := range types {
			if v == "null" {
				return true
			}
		}
	}
	if v, ok := m["const"]; ok && v == nil {
		return true
	}
	if enum, ok := m["enum"].([]any); ok {
		for _, v := range enum {
			if v == nil {
				return true
			}
		}
	}
	if anyOf, ok := m["anyOf"].([]any); ok {
		for _, variant := range anyOf {
			if schemaAllowsNull(variant) {
				return true
			}
		}
	}
	return false
}

func makeJSONSchemaNodeStrict(schema any, orders map[uintptr][]string) error {
	m, ok := schema.(map[string]any)
	if !ok {
		return &UnsupportedStrictJSONSchemaError{Message: "boolean schemas are unsupported"}
	}
	for _, key := range UNSUPPORTED_STRICT_SCHEMA_KEYS {
		if _, present := m[key]; present {
			return &UnsupportedStrictJSONSchemaError{Message: key + " schemas are unsupported"}
		}
	}
	if anyOf, present := m["anyOf"]; present {
		arr, ok := anyOf.([]any)
		if !ok || len(arr) == 0 {
			return &UnsupportedStrictJSONSchemaError{Message: "anyOf must contain at least one schema"}
		}
		for _, variant := range arr {
			if isStructuredSchema(variant) {
				return &UnsupportedStrictJSONSchemaError{Message: "object and array unions are unsupported"}
			}
			if err := makeJSONSchemaNodeStrict(variant, orders); err != nil {
				return err
			}
		}
	}
	if items, present := m["items"]; present {
		if _, isTuple := items.([]any); isTuple {
			return &UnsupportedStrictJSONSchemaError{Message: "tuple schemas are unsupported"}
		}
		if err := makeJSONSchemaNodeStrict(items, orders); err != nil {
			return err
		}
	}
	isObjectSchema := m["type"] == "object"
	if properties, present := m["properties"]; present && !isObjectSchema {
		_ = properties
		return &UnsupportedStrictJSONSchemaError{Message: "properties require type object"}
	}
	if !isObjectSchema {
		return nil
	}
	if additional, present := m["additionalProperties"]; present && additional != false {
		return &UnsupportedStrictJSONSchemaError{Message: "schema-valued or true additionalProperties is unsupported"}
	}
	var properties map[string]any
	if p, present := m["properties"]; present {
		var ok bool
		properties, ok = p.(map[string]any)
		if !ok {
			return &UnsupportedStrictJSONSchemaError{Message: "object properties must be a schema map"}
		}
	}
	if required, present := m["required"]; present {
		arr, ok := required.([]any)
		if !ok {
			return &UnsupportedStrictJSONSchemaError{Message: "object required must be a string array"}
		}
		for _, key := range arr {
			if _, isString := key.(string); !isString {
				return &UnsupportedStrictJSONSchemaError{Message: "object required must be a string array"}
			}
		}
	}
	if properties == nil {
		properties = map[string]any{}
	}
	requiredSet := map[string]bool{}
	if required, present := m["required"]; present {
		for _, key := range required.([]any) {
			requiredSet[key.(string)] = true
		}
	}
	for key := range requiredSet {
		if _, present := properties[key]; !present {
			return &UnsupportedStrictJSONSchemaError{Message: "required contains an unknown property"}
		}
	}
	// Property order follows the schema document (pi: Object.keys insertion
	// order). Map iteration would randomize the generated required array
	// per request, breaking provider prompt-cache prefix stability.
	propertyNames := orders[reflect.ValueOf(properties).Pointer()]
	if propertyNames == nil {
		propertyNames = make([]string, 0, len(properties))
		for key := range properties {
			propertyNames = append(propertyNames, key)
		}
		sort.Strings(propertyNames)
	}
	for _, key := range propertyNames {
		property, present := properties[key]
		if !present {
			continue
		}
		if err := makeJSONSchemaNodeStrict(property, orders); err != nil {
			return err
		}
		if !requiredSet[key] && !schemaAllowsNull(property) {
			properties[key] = map[string]any{"anyOf": []any{property, map[string]any{"type": "null"}}}
		}
	}
	m["required"] = propertyNames
	m["additionalProperties"] = false
	return nil
}

// collectJSONKeyOrders records the document order of every object's keys so
// the strict transform can reproduce pi's Object.keys iteration.
func collectJSONKeyOrders(node any, dec *json.Decoder, orders map[uintptr][]string) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	return collectJSONKeyOrdersToken(tok, node, dec, orders)
}

func collectJSONKeyOrdersToken(tok json.Token, node any, dec *json.Decoder, orders map[uintptr][]string) error {
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return nil
	}
	switch delim {
	case '{':
		m, ok := node.(map[string]any)
		if !ok {
			return fmt.Errorf("schema shape mismatch")
		}
		var keyOrder []string
		for {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			if keyTok == json.Delim('}') {
				break
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("schema shape mismatch")
			}
			keyOrder = append(keyOrder, key)
			if err := collectJSONKeyOrders(m[key], dec, orders); err != nil {
				return err
			}
		}
		orders[reflect.ValueOf(m).Pointer()] = keyOrder
	case '[':
		arr, ok := node.([]any)
		if !ok {
			return fmt.Errorf("schema shape mismatch")
		}
		for i := 0; ; i++ {
			tok, err := dec.Token()
			if err != nil {
				return err
			}
			if tok == json.Delim(']') {
				break
			}
			var element any = nil
			if i < len(arr) {
				element = arr[i]
			}
			if err := collectJSONKeyOrdersToken(tok, element, dec, orders); err != nil {
				return err
			}
		}
	}
	return nil
}

// MakeStrictJSONSchema converts a tool schema to the strict subset expected by
// provider constrained sampling (pi: makeStrictJsonSchema). Property order
// follows the schema document (pi: Object.keys).
func MakeStrictJSONSchema(schema json.RawMessage) (map[string]any, error) {
	var cloned any
	if err := json.Unmarshal(schema, &cloned); err != nil {
		return nil, err
	}
	if !isJsonSchemaObject(cloned) {
		return nil, &UnsupportedStrictJSONSchemaError{Message: "root schema must have type object"}
	}
	orders := map[uintptr][]string{}
	if err := collectJSONKeyOrders(cloned, json.NewDecoder(bytes.NewReader(schema)), orders); err != nil {
		return nil, err
	}
	if err := makeJSONSchemaNodeStrict(cloned, orders); err != nil {
		return nil, err
	}
	m := cloned.(map[string]any)
	if m["type"] != "object" {
		return nil, &UnsupportedStrictJSONSchemaError{Message: "root schema must have type object"}
	}
	return m, nil
}

// GetJSONSchemaToolParameters returns the tool parameters, made strict when
// strict is true (pi: getJsonSchemaToolParameters).
func GetJSONSchemaToolParameters(tool *types.Tool, strict bool) (json.RawMessage, error) {
	if !strict {
		return tool.Parameters, nil
	}
	m, err := MakeStrictJSONSchema(tool.Parameters)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// GrammarConstrainedSampling is the resolved grammar constraint of one tool
// (pi: GrammarConstrainedSampling).
type GrammarConstrainedSampling struct {
	Format        string
	Definition    string
	InputProperty string
}

// GrammarToolInputJSONBuffer accumulates the streamed grammar tool input (pi:
// GrammarToolInputJsonBuffer).
type GrammarToolInputJSONBuffer struct {
	Input   string
	Started bool
	Closed  bool
}

// GetGrammarToolInput extracts the grammar tool's string argument (pi:
// getGrammarToolInput).
func GetGrammarToolInput(toolName string, arguments map[string]any, inputProperty string) (string, error) {
	input, ok := arguments[inputProperty].(string)
	if !ok {
		return "", fmt.Errorf("Grammar tool call %q requires argument %q to be a string.", toolName, inputProperty)
	}
	return input, nil
}

// AppendGrammarToolInputJSONDelta appends the next streamed input, returning
// the JSON delta to emit (pi: appendGrammarToolInputJsonDelta). A nil return
// means no delta.
func AppendGrammarToolInputJSONDelta(buffer *GrammarToolInputJSONBuffer, inputProperty, nextInput string, close bool) (*string, error) {
	if buffer.Closed {
		if close && nextInput == buffer.Input {
			return nil, nil
		}
		return nil, fmt.Errorf("grammar tool input for property %q changed after it was closed", inputProperty)
	}
	if len(nextInput) < len(buffer.Input) || nextInput[:len(buffer.Input)] != buffer.Input {
		return nil, fmt.Errorf("grammar tool input for property %q changed non-monotonically", inputProperty)
	}
	inputDelta := nextInput[len(buffer.Input):]
	if !close && len(inputDelta) == 0 {
		return nil, nil
	}
	var delta string
	if !buffer.Started {
		delta += `{` + jsonStringProperty(inputProperty) + `:"`
		buffer.Started = true
	}
	delta += jsonStringInner(inputDelta)
	buffer.Input = nextInput
	if close {
		delta += `"}`
		buffer.Closed = true
	}
	return &delta, nil
}

// jsonStringProperty renders `"name"` (JSON-quoted key).
func jsonStringProperty(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// jsonStringInner renders the JSON-escaped form of a string without the
// surrounding quotes (pi: JSON.stringify(inputDelta).slice(1, -1)).
func jsonStringInner(s string) string {
	b, _ := json.Marshal(s)
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return ""
}

func inferGrammarInputProperty(tool *types.Tool) (string, error) {
	var schema map[string]any
	if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
		return "", err
	}
	if schema["type"] != "object" {
		return "", fmt.Errorf("grammar constrained sampling requires an object parameter schema")
	}
	required, ok := schema["required"].([]any)
	if !ok || len(required) != 1 {
		return "", fmt.Errorf("grammar constrained sampling requires exactly one required string property")
	}
	inputProperty, ok := required[0].(string)
	if !ok {
		return "", fmt.Errorf("grammar constrained sampling requires exactly one required string property")
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("grammar constrained sampling requires a properties entry for %s", inputProperty)
	}
	prop, present := properties[inputProperty]
	if !present {
		return "", fmt.Errorf("grammar constrained sampling requires a properties entry for %s", inputProperty)
	}
	propMap, ok := prop.(map[string]any)
	if !ok || propMap["type"] != "string" {
		return "", fmt.Errorf("grammar constrained sampling property %s must have type string", inputProperty)
	}
	return inputProperty, nil
}

// ResolveJSONSchemaStrictSampling decides whether a tool's json_schema
// constrained sampling must be strict (pi:
// resolveJsonSchemaStrictSampling). A nil result means no strict constraint;
// an error means the tool requires strict sampling the provider cannot offer
// (pi throws in both cases).
func ResolveJSONSchemaStrictSampling(tool *types.Tool, supportsStrictMode bool) (*bool, error) {
	config, ok := tool.ConstrainedSampling.(map[string]any)
	if !ok {
		return nil, nil
	}
	if config["type"] != "json_schema" {
		return nil, nil
	}
	strict, _ := config["strict"].(string)
	if supportsStrictMode {
		if _, err := MakeStrictJSONSchema(tool.Parameters); err == nil {
			v := true
			return &v, nil
		} else if _, isUnsupported := err.(*UnsupportedStrictJSONSchemaError); !isUnsupported {
			return nil, err
		} else {
			if strict != "require" {
				return nil, nil
			}
			return nil, fmt.Errorf("Tool %q requires JSON-schema constrained sampling, but %s.", tool.Name, err.Error())
		}
	}
	if strict == "require" {
		return nil, fmt.Errorf("Tool %q requires JSON-schema constrained sampling, but strict tools are unsupported.", tool.Name)
	}
	return nil, nil
}

// ResolveGrammarConstrainedSampling resolves a tool's grammar constrained
// sampling, or nil when the provider cannot offer it (pi:
// resolveGrammarConstrainedSampling).
func ResolveGrammarConstrainedSampling(tool *types.Tool, supportsOpenAIGrammarTools bool) (*GrammarConstrainedSampling, error) {
	config, ok := tool.ConstrainedSampling.(map[string]any)
	if !ok || config["type"] != "grammar" {
		return nil, nil
	}
	if !supportsOpenAIGrammarTools {
		return nil, nil
	}
	variants, _ := config["variants"].(map[string]any)
	larkDefinition, _ := variants["openai_lark"].(string)
	regexDefinition, _ := variants["openai_regex"].(string)
	hasLarkDefinition := larkDefinition != "" && strings.TrimSpace(larkDefinition) != ""
	hasRegexDefinition := regexDefinition != "" && strings.TrimSpace(regexDefinition) != ""
	if !hasLarkDefinition && !hasRegexDefinition {
		return nil, fmt.Errorf("Tool %q cannot use grammar constrained sampling: no supported grammar variant was provided.", tool.Name)
	}
	inputProperty, err := inferGrammarInputProperty(tool)
	if err != nil {
		return nil, fmt.Errorf("Tool %q cannot use grammar constrained sampling: %s.", tool.Name, err.Error())
	}
	format := "regex"
	definition := regexDefinition
	if hasLarkDefinition {
		format = "lark"
		definition = larkDefinition
	}
	return &GrammarConstrainedSampling{
		Format:        format,
		Definition:    definition,
		InputProperty: inputProperty,
	}, nil
}

// CreateGrammarToolInputProperties maps each grammar-constrained tool name to
// its input property (pi: createGrammarToolInputProperties).
func CreateGrammarToolInputProperties(tools []types.Tool, supportsOpenAIGrammarTools bool) map[string]string {
	properties := map[string]string{}
	for _, tool := range tools {
		grammar, err := ResolveGrammarConstrainedSampling(&tool, supportsOpenAIGrammarTools)
		if err == nil && grammar != nil {
			properties[tool.Name] = grammar.InputProperty
		}
	}
	return properties
}
