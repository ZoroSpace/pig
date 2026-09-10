// Minimal minimatch-compatible glob matcher (npm minimatch, as consumed by
// model-resolver.ts with { nocase: true }).
//
// Segment deviations (declared): pig implements the subset minimatch's
// consumers here use — `*` (within a path segment), `**` (across segments),
// `?`, `[...]`/`[!...]` character classes, `{a,b}` brace alternation, and
// case-insensitive matching. Extglob `+(...)` patterns and Windows path
// handling are not implemented (no pi test pins them on this surface).
package utils

import (
	"regexp"
	"strings"
)

// expandBraces expands top-level {...} alternations recursively (pi:
// minimatch's brace expansion).
func expandBraces(pattern string) []string {
	depth := 0
	start := -1
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			depth--
			if depth == 0 && start >= 0 {
				inner := pattern[start+1 : i]
				head := pattern[:start]
				tail := pattern[i+1:]
				var out []string
				for _, alt := range splitTopLevelAlternatives(inner) {
					out = append(out, expandBraces(head+alt+tail)...)
				}
				return out
			}
		}
	}
	return []string{pattern}
}

func splitTopLevelAlternatives(s string) []string {
	var out []string
	depth := 0
	last := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[last:i])
				last = i + 1
			}
		}
	}
	return append(out, s[last:])
}

// globToRegexp converts a brace-free minimatch pattern into a regular
// expression source string.
func globToRegexp(pattern string) string {
	var b strings.Builder
	b.WriteString("^")
	i := 0
	for i < len(pattern) {
		c := pattern[i]
		switch c {
		case '\\':
			if i+1 < len(pattern) {
				b.WriteString(regexp.QuoteMeta(string(pattern[i+1])))
				i += 2
				continue
			}
			b.WriteString(regexp.QuoteMeta("\\"))
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				// `**` crosses path segments.
				b.WriteString(".*")
				i += 2
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '[':
			// Pass character classes through, mapping [!x]/[^x] to [^x].
			j := i + 1
			negate := false
			if j < len(pattern) && (pattern[j] == '!' || pattern[j] == '^') {
				negate = true
				j++
			}
			classStart := j
			for j < len(pattern) && (pattern[j] != ']' || j == classStart) {
				j++
			}
			if j < len(pattern) {
				class := pattern[classStart:j]
				if negate {
					b.WriteString("[^" + class + "]")
				} else {
					b.WriteString("[" + class + "]")
				}
				i = j
			} else {
				// Unclosed class: literal bracket.
				b.WriteString(regexp.QuoteMeta("["))
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
		i++
	}
	b.WriteString("$")
	return b.String()
}

// Minimatch reports whether text matches the glob pattern (pi consumes npm
// minimatch with { nocase: true }).
func Minimatch(pattern, text string) bool {
	for _, expanded := range expandBraces(pattern) {
		re, err := regexp.Compile("(?i)" + globToRegexp(expanded))
		if err != nil {
			return false
		}
		if re.MatchString(text) {
			return true
		}
	}
	return false
}
