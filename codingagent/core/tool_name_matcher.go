package core

import (
	"regexp"
	"strings"
)

// toolPatternRegExp compiles one `--tools`/`--exclude-tools` entry where `*`
// matches any characters (pi: toolPatternRegExp in mcp-servers.ts, v1.0.4).
// Splitting on `*` and QuoteMeta-ing the parts leaves every other character
// literal.
func toolPatternRegExp(pattern string) *regexp.Regexp {
	var source strings.Builder
	for i, part := range strings.Split(pattern, "*") {
		if i > 0 {
			source.WriteString(".*")
		}
		source.WriteString(regexp.QuoteMeta(part))
	}
	return regexp.MustCompile("^" + source.String() + "$")
}

// toolNameMatcher reports whether a tool name matches any of the entries,
// each an exact name or a `*` pattern (pi: createToolNameMatcher in
// mcp-servers.ts, v1.0.4). Exact names win over patterns by lookup order.
func toolNameMatcher(entries []string) func(string) bool {
	names := map[string]struct{}{}
	var patterns []*regexp.Regexp
	for _, entry := range entries {
		if strings.Contains(entry, "*") {
			patterns = append(patterns, toolPatternRegExp(entry))
		} else {
			names[entry] = struct{}{}
		}
	}
	return func(name string) bool {
		if _, ok := names[name]; ok {
			return true
		}
		for _, pattern := range patterns {
			if pattern.MatchString(name) {
				return true
			}
		}
		return false
	}
}
