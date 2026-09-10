// pi: src/utils/text.ts — extract and join text from message content.
package utils

import "github.com/ZoroSpace/pig/ai/types"

// ContentText extracts and joins text from message content (pi:
// contentText). Non-text blocks are skipped; separator defaults to "\n" at
// call sites in pi (pass it explicitly here).
func ContentText(content []types.Content, separator string) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		if text, ok := block.(types.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	result := ""
	for i, part := range parts {
		if i > 0 {
			result += separator
		}
		result += part
	}
	return result
}
