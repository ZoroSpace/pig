// renderers_write.go (pi: src/core/tools/renderers/write.ts) — presentation
// for the write tool.
//
// S5a note: pi hosts the renderers in core/tools/renderers/; pig keeps them
// in package tools (files named renderers_*.go) because the renderers
// depend on package-tools values (render-utils, theme adapters, detail
// types) while the tool files spread them back into their definitions — a
// Go subpackage would be an import cycle. Grep mapping: pi
// renderers/write.ts ↔ renderers_write.go.
package tools

import (
	"fmt"
	"strings"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// writePartialFullHighlightLines is the prefix re-highlighted after each
// incremental append (pi: WRITE_PARTIAL_FULL_HIGHLIGHT_LINES).
const writePartialFullHighlightLines = 50

// WriteHighlightCache carries the incremental syntax-highlight state (pi:
// WriteHighlightCache). rawPath nil = pi's null.
type WriteHighlightCache struct {
	rawPath          *string
	lang             string
	rawContent       string
	normalizedLines  []string
	highlightedLines []string
}

// WriteCallRenderComponent is the call component (pi: class
// WriteCallRenderComponent extends Text).
type WriteCallRenderComponent struct {
	*tui.Text
	Cache *WriteHighlightCache
}

// highlightSingleLine highlights one line, dropping the multi-line shape
// (pi: highlightSingleLine).
func highlightSingleLine(line string, lang string) string {
	highlighted := themeHighlightCode(line, lang)
	if len(highlighted) > 0 {
		return highlighted[0]
	}
	return ""
}

// themeHighlightCode adapts the theme package's global highlightCode (pi:
// highlightCode imported from theme).
func themeHighlightCode(code string, lang string) []string {
	if lang == "" {
		return nil
	}
	return theme.HighlightCode(code, lang)
}

// themeGetLanguageFromPath adapts the theme package's global
// getLanguageFromPath (pi: getLanguageFromPath imported from theme).
func themeGetLanguageFromPath(filePath string) string {
	lang, ok := theme.GetLanguageFromPath(filePath)
	if !ok {
		return ""
	}
	return lang
}

// refreshWriteHighlightPrefix re-highlights the leading lines in place (pi:
// refreshWriteHighlightPrefix).
func refreshWriteHighlightPrefix(cache *WriteHighlightCache) {
	prefixCount := writePartialFullHighlightLines
	if len(cache.normalizedLines) < prefixCount {
		prefixCount = len(cache.normalizedLines)
	}
	if prefixCount == 0 {
		return
	}
	prefixSource := strings.Join(cache.normalizedLines[:prefixCount], "\n")
	prefixHighlighted := themeHighlightCode(prefixSource, cache.lang)
	for i := 0; i < prefixCount; i++ {
		if i < len(prefixHighlighted) && prefixHighlighted[i] != "" {
			cache.highlightedLines[i] = prefixHighlighted[i]
		} else {
			var fallback string
			if i < len(cache.normalizedLines) {
				fallback = cache.normalizedLines[i]
			}
			cache.highlightedLines[i] = highlightSingleLine(fallback, cache.lang)
		}
	}
}

// rebuildWriteHighlightCacheFull rebuilds the cache from full content (pi:
// rebuildWriteHighlightCacheFull; nil = no language).
func rebuildWriteHighlightCacheFull(rawPath *string, fileContent string) *WriteHighlightCache {
	lang := ""
	if rawPath != nil {
		lang = themeGetLanguageFromPath(*rawPath)
	}
	if lang == "" {
		return nil
	}
	normalized := ReplaceTabs(NormalizeDisplayText(fileContent))
	return &WriteHighlightCache{
		rawPath:          rawPath,
		lang:             lang,
		rawContent:       fileContent,
		normalizedLines:  strings.Split(normalized, "\n"),
		highlightedLines: themeHighlightCode(normalized, lang),
	}
}

// updateWriteHighlightCacheIncremental appends the delta to the cache (pi:
// updateWriteHighlightCacheIncremental; nil = no language).
func updateWriteHighlightCacheIncremental(cache *WriteHighlightCache, rawPath *string, fileContent string) *WriteHighlightCache {
	lang := ""
	if rawPath != nil {
		lang = themeGetLanguageFromPath(*rawPath)
	}
	if lang == "" {
		return nil
	}
	if cache == nil {
		return rebuildWriteHighlightCacheFull(rawPath, fileContent)
	}
	if cache.lang != lang || !sameStringPtr(cache.rawPath, rawPath) {
		return rebuildWriteHighlightCacheFull(rawPath, fileContent)
	}
	if !strings.HasPrefix(fileContent, cache.rawContent) {
		return rebuildWriteHighlightCacheFull(rawPath, fileContent)
	}
	if len(fileContent) == len(cache.rawContent) {
		return cache
	}

	deltaRaw := fileContent[len(cache.rawContent):]
	deltaNormalized := ReplaceTabs(NormalizeDisplayText(deltaRaw))
	cache.rawContent = fileContent
	if len(cache.normalizedLines) == 0 {
		cache.normalizedLines = append(cache.normalizedLines, "")
		cache.highlightedLines = append(cache.highlightedLines, "")
	}

	segments := strings.Split(deltaNormalized, "\n")
	lastIndex := len(cache.normalizedLines) - 1
	cache.normalizedLines[lastIndex] += segments[0]
	cache.highlightedLines[lastIndex] = highlightSingleLine(cache.normalizedLines[lastIndex], cache.lang)
	for i := 1; i < len(segments); i++ {
		cache.normalizedLines = append(cache.normalizedLines, segments[i])
		cache.highlightedLines = append(cache.highlightedLines, highlightSingleLine(segments[i], cache.lang))
	}
	refreshWriteHighlightPrefix(cache)
	return cache
}

// trimTrailingEmptyLines drops trailing empty display lines (pi:
// trimTrailingEmptyLines; read.ts keeps its own local copy which pig
// reuses from here).
func trimTrailingEmptyLines(lines []string) []string {
	end := len(lines)
	for end > 0 && lines[end-1] == "" {
		end--
	}
	return lines[:end]
}

// stringArg is pi's str(args?.key): string → value; missing/null → "";
// other types → null (invalid, true returned).
func stringArg(args map[string]any, key string) (string, bool) {
	value, has := args[key]
	if !has || value == nil {
		return "", false
	}
	if s, ok := value.(string); ok {
		return s, false
	}
	return "", true
}

// formatWriteCall renders the call row (pi: formatWriteCall).
func formatWriteCall(theme UITheme, args map[string]any, options ToolRenderResultOptions, cache *WriteHighlightCache, cwd string) string {
	rawPath := rawPathFromArgs(args)
	fileContent, contentInvalid := stringArg(args, "content")
	pathDisplay := RenderToolPath(rawPath, theme, cwd, "")
	text := theme.Fg("toolTitle", theme.Bold("write")) + " " + pathDisplay

	if contentInvalid {
		// non-string content (pi: str() → null)
		text += "\n\n" + theme.Fg("error", "[invalid content arg - expected string]")
	} else if fileContent != "" {
		lang := ""
		if rawPath != nil {
			lang = themeGetLanguageFromPath(*rawPath)
		}
		var renderedLines []string
		if lang != "" {
			if cache != nil {
				renderedLines = cache.highlightedLines
			} else {
				renderedLines = themeHighlightCode(ReplaceTabs(NormalizeDisplayText(fileContent)), lang)
			}
		} else {
			renderedLines = strings.Split(NormalizeDisplayText(fileContent), "\n")
		}
		lines := trimTrailingEmptyLines(renderedLines)
		totalLines := len(lines)
		maxLines := 10
		if options.Expanded {
			maxLines = len(lines)
		}
		displayEnd := maxLines
		if displayEnd > len(lines) {
			displayEnd = len(lines)
		}
		displayLines := lines[:displayEnd]
		remaining := len(lines) - maxLines
		styled := make([]string, 0, len(displayLines))
		for _, line := range displayLines {
			if lang != "" {
				styled = append(styled, line)
			} else {
				styled = append(styled, theme.Fg("toolOutput", ReplaceTabs(line)))
			}
		}
		text += "\n\n" + strings.Join(styled, "\n")
		if remaining > 0 {
			text += theme.Fg("muted", fmt.Sprintf("\n... (%d more lines, %d total,", remaining, totalLines)) +
				" " + KeyHint("app.tools.expand", "to expand") + theme.Fg("muted", ")")
		}
	}

	return text
}

// formatWriteResult renders the error body (pi: formatWriteResult; "" =
// undefined).
func formatWriteResult(theme UITheme, result ToolRenderResult, isError bool) string {
	if !isError {
		return ""
	}
	var outputs []string
	for _, c := range result.Content {
		if t, ok := c.(types.TextContent); ok {
			outputs = append(outputs, t.Text)
		}
	}
	output := strings.Join(outputs, "\n")
	if output == "" {
		return ""
	}
	return "\n" + theme.Fg("error", output)
}

// writeRenderCall is the write call renderer (pi: writeRenderers.renderCall).
func writeRenderCall(args any, themeArg UITheme, context ToolRenderContext) tui.Component {
	renderArgs, _ := args.(map[string]any)
	if renderArgs == nil {
		renderArgs = map[string]any{}
	}
	rawPath := rawPathFromArgs(args)
	fileContent, contentInvalid := stringArg(renderArgs, "content")
	var component *WriteCallRenderComponent
	if c, ok := context.LastComponent.(*WriteCallRenderComponent); ok {
		component = c
	} else {
		component = &WriteCallRenderComponent{Text: tui.NewText("", 0, 0)}
	}
	if !contentInvalid {
		if context.ArgsComplete {
			component.Cache = rebuildWriteHighlightCacheFull(rawPath, fileContent)
		} else {
			component.Cache = updateWriteHighlightCacheIncremental(component.Cache, rawPath, fileContent)
		}
	} else {
		component.Cache = nil
	}
	component.SetText(formatWriteCall(themeArg, renderArgs,
		ToolRenderResultOptions{Expanded: context.Expanded, IsPartial: context.IsPartial},
		component.Cache, context.Cwd))
	return component
}

// writeRenderResult is the write result renderer (pi:
// writeRenderers.renderResult).
func writeRenderResult(result ToolRenderResult, _ ToolRenderResultOptions, themeArg UITheme, context ToolRenderContext) tui.Component {
	output := formatWriteResult(themeArg, result, context.IsError)
	if output == "" {
		var component *tui.Container
		if c, ok := context.LastComponent.(*tui.Container); ok {
			component = c
		} else {
			component = &tui.Container{}
		}
		component.Clear()
		return component
	}
	var text *tui.Text
	if c, ok := context.LastComponent.(*tui.Text); ok {
		text = c
	} else {
		text = tui.NewText("", 0, 0)
	}
	text.SetText(output)
	return text
}

// WriteRenderers is the write renderer pair (pi: writeRenderers; write.ts
// spreads it into its definition).
var WriteRenderers = ToolRenderers{
	RenderCall:   writeRenderCall,
	RenderResult: writeRenderResult,
}
