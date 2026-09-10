// pi: src/harness/tools/path-utils.ts — tool path resolution helpers.
//
// Segment deviations:
//   - signal parameters → ctx (AbortSignal → context rule).
//   - resolved.normalize("NFD") → golang.org/x/text/unicode/norm.NFD.
//   - The variant dedupe uses a seen set preserving first-occurrence order
//     (pi: Set iteration).
package tools

import (
	"context"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/ZoroSpace/pig/agent/harness"
)

// NARROW_NO_BREAK_SPACE replaces the space in " AM."/" PM." variants (pi:
// NARROW_NO_BREAK_SPACE).
const narrowNoBreakSpace = "\u202F"

// unicodeSpacesRe matches the unicode space run replaced during path
// normalization (pi: UNICODE_SPACES).
var unicodeSpacesRe = regexp.MustCompile("[\u00A0\u2000-\u200A\u202F\u205F\u3000]")

// normalizeToolPath replaces unicode spaces and strips a leading "@" (pi:
// normalizeToolPath).
func normalizeToolPath(path string) string {
	normalized := unicodeSpacesRe.ReplaceAllString(path, " ")
	if strings.HasPrefix(normalized, "@") {
		return normalized[1:]
	}
	return normalized
}

// ResolveToolPath resolves a tool path to an absolute addressed path (pi:
// resolveToolPath). Errors propagate as returned by the environment (pi:
// getOrThrow).
func ResolveToolPath(ctx context.Context, env harness.ExecutionEnv, path string) (string, error) {
	return env.AbsolutePath(ctx, normalizeToolPath(path))
}

// ResolveReadToolPath resolves a read-tool path, probing spelling variants
// for the requested file (pi: resolveReadToolPath).
func ResolveReadToolPath(ctx context.Context, env harness.ExecutionEnv, path string) (string, error) {
	resolved, err := ResolveToolPath(ctx, env, path)
	if err != nil {
		return "", err
	}
	amPmRe := regexp.MustCompile(`(?i) (AM|PM)\.`)
	variants := []string{
		resolved,
		amPmRe.ReplaceAllString(resolved, narrowNoBreakSpace+"${1}."),
		norm.NFD.String(resolved),
		strings.ReplaceAll(resolved, "'", "’"),
		norm.NFD.String(strings.ReplaceAll(resolved, "'", "’")),
	}

	seen := map[string]struct{}{}
	for _, variant := range variants {
		if _, ok := seen[variant]; ok {
			continue
		}
		seen[variant] = struct{}{}
		exists, err := env.Exists(ctx, variant)
		if err != nil {
			return "", err
		}
		if exists {
			return variant, nil
		}
	}
	return resolved, nil
}
