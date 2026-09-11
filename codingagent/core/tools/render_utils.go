// render_utils.go (pi: src/core/tools/render-utils.ts) — shared helpers for
// the built-in tools' renderCall/renderResult implementations.
package tools

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ZoroSpace/pig/ai/types"
	codingagentutils "github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// ShortenPath replaces the home directory prefix with "~" (pi:
// shortenPath). Non-string input → "" (pi: typeof check; pig's callers pass
// strings, the empty fallback covers the invalid-arg path).
func ShortenPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	if home != "" && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// LinkPath wraps the styled path in an OSC 8 hyperlink to the absolute
// path when the terminal supports hyperlinks (pi: linkPath).
func LinkPath(styledText string, rawPath string, cwd string) string {
	if !tui.GetCapabilities().Hyperlinks {
		return styledText
	}
	absolutePath := codingagentutils.ResolvePath(rawPath, cwd, nil)
	return tui.Hyperlink(styledText, pathToFileURL(absolutePath))
}

// pathToFileURL converts an absolute path to a file:// URL (pi:
// pathToFileURL from node:url).
func pathToFileURL(path string) string {
	// Same encoding shape as Node's pathToFileURL for filesystem paths:
	// preserve "/" and leave other characters percent-encoded minimally.
	const hex = "0123456789ABCDEF"
	var sb strings.Builder
	sb.WriteString("file://")
	for _, r := range path {
		if r == '/' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '~' {
			sb.WriteRune(r)
		} else if r == filepath.Separator {
			sb.WriteRune('/')
		} else {
			for _, b := range []byte(string(r)) {
				sb.WriteByte('%')
				sb.WriteByte(hex[b>>4])
				sb.WriteByte(hex[b&0xf])
			}
		}
	}
	return sb.String()
}

// Str is pi's str(): the string value of an argument, "" for null/undefined,
// and null (ok=false) for non-string values.
func Str(value any) (string, bool) {
	if s, ok := value.(string); ok {
		return s, true
	}
	if value == nil {
		return "", true
	}
	return "", false
}

// ReplaceTabs replaces tabs with three spaces for consistent rendering (pi:
// replaceTabs).
func ReplaceTabs(text string) string {
	return strings.ReplaceAll(text, "\t", "   ")
}

// NormalizeDisplayText strips carriage returns (pi: normalizeDisplayText).
func NormalizeDisplayText(text string) string {
	return strings.ReplaceAll(text, "\r", "")
}

// capabilityImagesEnabled mirrors pi's truthiness check on
// getCapabilities().images (boolean | "kitty" | "iterm2").
func capabilityImagesEnabled(caps tui.TerminalCapabilities) bool {
	return caps.Images == true || caps.Images == "kitty" || caps.Images == "iterm2"
}

// GetTextOutput joins the text blocks of a result, appending image
// indicators when images are hidden or unsupported (pi: getTextOutput).
func GetTextOutput(result *ToolRenderResult, showImages bool) string {
	if result == nil {
		return ""
	}

	var textBlocks []types.TextContent
	var imageBlocks []types.ImageContent
	for _, c := range result.Content {
		switch block := c.(type) {
		case types.TextContent:
			textBlocks = append(textBlocks, block)
		case *types.TextContent:
			// Builtin tools emit pointer blocks (asTextContent); values come
			// from the JSONL/LLM decoders — both must match
			// (docs/fix/2026-09-11-tool-execution-live-output-empty.md).
			if block != nil {
				textBlocks = append(textBlocks, *block)
			}
		case types.ImageContent:
			imageBlocks = append(imageBlocks, block)
		case *types.ImageContent:
			if block != nil {
				imageBlocks = append(imageBlocks, *block)
			}
		}
	}

	parts := make([]string, 0, len(textBlocks))
	for _, c := range textBlocks {
		parts = append(parts, strings.ReplaceAll(
			codingagentutils.SanitizeBinaryOutput(codingagentutils.StripAnsi(c.Text)), "\r", ""))
	}
	output := strings.Join(parts, "\n")

	caps := tui.GetCapabilities()
	if len(imageBlocks) > 0 && (!capabilityImagesEnabled(caps) || !showImages) {
		indicators := make([]string, 0, len(imageBlocks))
		for _, img := range imageBlocks {
			mimeType := img.MimeType
			if mimeType == "" {
				mimeType = "image/unknown"
			}
			var dims *tui.ImageDimensions
			if img.Data != "" && img.MimeType != "" {
				dims = tui.GetImageDimensions(img.Data, img.MimeType)
			}
			indicators = append(indicators, tui.ImageFallback(mimeType, dims, ""))
		}
		imageIndicators := strings.Join(indicators, "\n")
		if output != "" {
			output = output + "\n" + imageIndicators
		} else {
			output = imageIndicators
		}
	}

	return output
}

// ToolRenderResultLike is the result shape the render helpers accept (pi:
// ToolRenderResultLike<TDetails>); pig passes the full AgentToolResult.
type ToolRenderResultLike = ToolRenderResult

// InvalidArgText renders the "[invalid arg]" marker (pi: invalidArgText).
func InvalidArgText(theme UITheme) string {
	return theme.Fg("error", "[invalid arg]")
}

// RenderToolPath renders a tool path argument with hyperlink, shortening,
// and the invalid/empty fallbacks (pi: renderToolPath).
func RenderToolPath(rawPath *string, theme UITheme, cwd string, emptyFallback string) string {
	if rawPath == nil {
		return InvalidArgText(theme)
	}
	value := *rawPath
	if value == "" {
		value = emptyFallback
	}
	if value == "" {
		return theme.Fg("toolOutput", "...")
	}
	return LinkPath(theme.Fg("accent", ShortenPath(value)), value, cwd)
}

// rawPathFromArgs is str(args?.file_path ?? args?.path) over raw args (pig
// helper; pi inlines the expression per renderer). pi's str() maps
// undefined/null — a missing key or an absent args object, both common while
// streaming partial args — to "" (rendered as the emptyFallback, e.g. "..."),
// and only a present-but-non-string value to null ("[invalid arg]"). The
// pointer form keeps that distinction: "" = absent, nil = invalid. Mapping
// a missing key to nil instead made the call row flash/stick on
// "[invalid arg]" during streaming (pi shows "..."; see
// docs/fix/2026-09-11-toolcall-title-invalid-arg.md).
func rawPathFromArgs(args any) *string {
	empty := ""
	m, ok := args.(map[string]any)
	if !ok {
		return &empty
	}
	value, has := m["path"]
	if filePathValue, is := m["file_path"]; is && filePathValue != nil {
		// pi: args?.file_path ?? args?.path (?? skips null/undefined)
		value, has = filePathValue, true
	}
	if !has || value == nil {
		return &empty
	}
	if s, isString := value.(string); isString {
		return &s
	}
	return nil
}

// sameStringPtr compares optional keys (pi: !== on string | undefined).
func sameStringPtr(a *string, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
