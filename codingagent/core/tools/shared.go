// Shared helpers for the coding-agent tools (pi: inline helpers in each tool
// file; the JSON param decode replaces Static<TParameters>' compile-time
// typing).
package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
)

// decodeToolParams converts the raw tool-call params (schema-validated
// map[string]any) into the typed input struct (pi: Static<TParameters> — TS
// validates at the type level; pig re-decodes through the JSON data model,
// which the schema has already gated).
func decodeToolParams[T any](params any) (T, error) {
	var input T
	data, err := json.Marshal(params)
	if err != nil {
		return input, err
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return input, err
	}
	return input, nil
}

// formatOptionalNumber renders an optional TS number the way `${offset}`
// would (undefined for nil, shortest round-trip form otherwise).
func formatOptionalNumber(value *float64) string {
	if value == nil {
		return "undefined"
	}
	return fmt.Sprintf("%v", *value)
}

// splitLines/joinLines mirror TS split("\n")/join("\n") (identical for Go
// strings).
func splitLines(content string) []string { return strings.Split(content, "\n") }
func joinLines(lines []string) string    { return strings.Join(lines, "\n") }

// asTextContent builds a text content block (pi: { type: "text", text }).
func asTextContent(text string) types.Content {
	return &types.TextContent{Kind: types.KindText, Text: text}
}

// contentOf wraps the content slice for an AgentToolResult.
func resultOf(content []types.Content, details any) agent.AgentToolResult {
	return agent.AgentToolResult{Content: content, Details: details}
}
