// pi: src/utils/json.ts — strip `//` line comments and trailing commas from
// JSON, leaving string literals untouched.
package utils

import (
	"regexp"
	"strings"
)

var (
	jsonStringOrCommentRe = regexp.MustCompile(`"(?:\\.|[^"\\])*"|//[^
]*`)
	jsonStringOrTrailingRe = regexp.MustCompile(`"(?:\\.|[^"\\])*"|,(\s*[}\]])`)
)

// StripJsonComments removes // line comments and trailing commas (pi:
// stripJsonComments).
func StripJsonComments(input string) string {
	withoutComments := jsonStringOrCommentRe.ReplaceAllStringFunc(input, func(match string) string {
		if match[0] == '"' {
			return match
		}
		return ""
	})
	var b strings.Builder
	last := 0
	for _, loc := range jsonStringOrTrailingRe.FindAllStringSubmatchIndex(withoutComments, -1) {
		if loc[2] >= 0 {
			// A trailing comma: drop it, keep the closing brace/bracket tail.
			b.WriteString(withoutComments[last:loc[0]])
			b.WriteString(withoutComments[loc[2]:loc[3]])
		} else {
			// A string literal: keep verbatim.
			b.WriteString(withoutComments[last:loc[1]])
		}
		last = loc[1]
	}
	b.WriteString(withoutComments[last:])
	return b.String()
}
