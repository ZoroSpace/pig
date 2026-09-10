// pi: src/utils/text.ts — BOM helpers.
package utils

import "strings"

// SplitBom splits a leading UTF-8 BOM from the content (pi: splitBom).
func SplitBom(content string) (bom string, text string) {
	if strings.HasPrefix(content, "\uFEFF") {
		return "\uFEFF", content[len("\uFEFF"):]
	}
	return "", content
}

// StripBom removes a leading UTF-8 byte order mark from decoded text (pi:
// stripBom).
func StripBom(content string) string {
	_, text := SplitBom(content)
	return text
}
