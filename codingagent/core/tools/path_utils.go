// pi: src/core/tools/path-utils.ts — coding-agent path resolution (built on
// utils/paths.ts; the agent package has its own env-based copy and pig
// mirrors the duplication).
package tools

import (
	"context"
	"os"
	"regexp"
	"strings"

	"github.com/ZoroSpace/pig/codingagent/utils"
	"golang.org/x/text/unicode/norm"
)

// narrowNoBreakSpace replaces the space in AM/PM variants (pi:
// NARROW_NO_BREAK_SPACE).
const narrowNoBreakSpace = "\u202F"

// tryMacOSScreenshotPath rewrites " AM."/" PM." to the narrow no-break space
// form (pi: tryMacOSScreenshotPath).
func tryMacOSScreenshotPath(filePath string) string {
	re := regexp.MustCompile(`(?i) (AM|PM)\.`)
	return re.ReplaceAllString(filePath, narrowNoBreakSpace+"${1}.")
}

// tryNFDVariant converts to NFD (pi: tryNFDVariant; macOS stores filenames
// in decomposed form).
func tryNFDVariant(filePath string) string {
	return norm.NFD.String(filePath)
}

// tryCurlyQuoteVariant replaces straight apostrophes with U+2019 (pi:
// tryCurlyQuoteVariant).
func tryCurlyQuoteVariant(filePath string) string {
	return strings.ReplaceAll(filePath, "'", "’")
}

// fileExists reports whether the path is accessible (pi: fileExists).
func fileExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return err == nil
}

// PathExists reports whether the path is accessible (pi: pathExists).
func PathExists(filePath string) bool {
	return fileExists(filePath)
}

// ExpandPath normalizes a user path input with unicode-space folding and
// @-prefix stripping (pi: expandPath).
func ExpandPath(filePath string) string {
	return utils.NormalizePath(filePath, &utils.PathInputOptions{
		NormalizeUnicodeSpaces: boolPtr(true),
		StripAtPrefix:          boolPtr(true),
	})
}

// ResolveToCwd resolves a path relative to the given cwd, handling ~
// expansion and absolute paths (pi: resolveToCwd).
func ResolveToCwd(filePath string, cwd string) string {
	return utils.ResolvePath(filePath, cwd, &utils.PathInputOptions{
		NormalizeUnicodeSpaces: boolPtr(true),
		StripAtPrefix:          boolPtr(true),
	})
}

// resolveReadPathVariants returns the read fallback variants for a resolved
// path (pi: the inline variant probes of resolveReadPath/
// resolveReadPathAsync).
func resolveReadPathVariants(resolved string) []string {
	nfdVariant := tryNFDVariant(resolved)
	return []string{
		tryMacOSScreenshotPath(resolved),
		nfdVariant,
		tryCurlyQuoteVariant(resolved),
		tryCurlyQuoteVariant(nfdVariant), // combined NFD + curly quote
	}
}

// ResolveReadPath resolves a read path, probing macOS filename variants
// synchronously (pi: resolveReadPath).
func ResolveReadPath(filePath string, cwd string) string {
	resolved := ResolveToCwd(filePath, cwd)
	if fileExists(resolved) {
		return resolved
	}
	for _, variant := range resolveReadPathVariants(resolved) {
		if variant != resolved && fileExists(variant) {
			return variant
		}
	}
	return resolved
}

// ResolveReadPathAsync resolves a read path, probing macOS filename variants
// (pi: resolveReadPathAsync).
func ResolveReadPathAsync(ctx context.Context, filePath string, cwd string) string {
	resolved := ResolveToCwd(filePath, cwd)
	if fileExists(resolved) {
		return resolved
	}
	for _, variant := range resolveReadPathVariants(resolved) {
		if variant != resolved && fileExists(variant) {
			return variant
		}
	}
	return resolved
}

func boolPtr(v bool) *bool { return &v }
