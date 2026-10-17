// renderers_read.go (pi: src/core/tools/renderers/read.ts) — presentation
// for the read tool.
//
// S5a note: pi hosts the renderers in core/tools/renderers/; pig keeps them
// in package tools (files named renderers_*.go) because the renderers
// depend on package-tools values (render-utils, detail types) while the
// tool files spread them back into their definitions — a Go subpackage
// would be an import cycle. Grep mapping: pi renderers/read.ts ↔
// renderers_read.go.
package tools

import (
	"fmt"
	"path/filepath"
	"strings"

	harnessutils "github.com/ZoroSpace/pig/agent/harness/utils"
	codingagent "github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// compactReadClassification is pi's CompactReadClassification.
type compactReadClassification struct {
	kind  string // "docs" | "resource" | "skill"
	label string
}

// compactResourceFileNames is pi's COMPACT_RESOURCE_FILE_NAMES.
var compactResourceFileNames = map[string]bool{
	"AGENTS.override.md": true,
	"AGENTS.md":          true,
	"AGENTS.MD":          true,
	"CLAUDE.md":          true,
	"CLAUDE.MD":          true,
}

// readRenderArgs is pi's ReadRenderArgs over raw args.
type readRenderArgs struct {
	path   *string
	offset any
	limit  any
}

// readRenderArgsFromArgs extracts the render args (pi: args as
// ReadRenderArgs).
func readRenderArgsFromArgs(args any) readRenderArgs {
	out := readRenderArgs{}
	m, ok := args.(map[string]any)
	if !ok {
		return out
	}
	out.path = rawPathFromArgs(args)
	if v, has := m["offset"]; has {
		out.offset = v
	}
	if v, has := m["limit"]; has {
		out.limit = v
	}
	return out
}

// styledReadLineRange renders the :start-end suffix (pi: styledReadLineRange).
func styledReadLineRange(theme UITheme, args readRenderArgs) string {
	lineRange := FormatReadLineRange(args.offset, args.limit)
	if lineRange == "" {
		return ""
	}
	return theme.Fg("warning", lineRange)
}

// formatReadCall renders the call row (pi: formatReadCall).
func formatReadCall(theme UITheme, args readRenderArgs, cwd string) string {
	pathDisplay := RenderToolPath(args.path, theme, cwd, "")
	return theme.Fg("toolTitle", theme.Bold("read")) + " " + pathDisplay + styledReadLineRange(theme, args)
}

// trimTrailingEmptyLinesRead is read.ts's local trimTrailingEmptyLines
// (shared with write.ts in pi; pig reuses the renderers_write.go copy).

// toPosixPath converts separators to posix (pi: toPosixPath).
func toPosixPath(filePath string) string {
	return filepath.ToSlash(filePath)
}

// getPiDocsClassification classifies pi's own documentation files (pi:
// getPiDocsClassification; nil = undefined).
func getPiDocsClassification(absolutePath string) *compactReadClassification {
	packageRoot := filepath.Dir(codingagent.GetReadmePath())
	rootAbs, err := filepath.Abs(packageRoot)
	if err != nil {
		rootAbs = packageRoot
	}
	pathAbs, err := filepath.Abs(absolutePath)
	if err != nil {
		pathAbs = absolutePath
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return nil
	}
	rel = filepath.ToSlash(rel)
	if rel == "" || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return nil
	}

	if rel == "README.md" || strings.HasPrefix(rel, "docs/") || strings.HasPrefix(rel, "examples/") {
		return &compactReadClassification{kind: "docs", label: rel}
	}
	return nil
}

// getCompactReadClassification resolves the compact call display (pi:
// getCompactReadClassification; nil = undefined).
func getCompactReadClassification(args readRenderArgs, cwd string) *compactReadClassification {
	if args.path == nil || *args.path == "" {
		return nil
	}
	rawPath := *args.path

	absolutePath := ResolveToCwd(rawPath, cwd)
	fileName := filepath.Base(absolutePath)
	if fileName == "SKILL.md" {
		label := filepath.Base(filepath.Dir(absolutePath))
		if label == "" || label == "." || label == string(filepath.Separator) {
			label = fileName
		}
		return &compactReadClassification{kind: "skill", label: label}
	}

	if docsClassification := getPiDocsClassification(absolutePath); docsClassification != nil {
		return docsClassification
	}

	if compactResourceFileNames[fileName] {
		return &compactReadClassification{kind: "resource", label: utils.FormatPathRelativeToCwdOrAbsolute(absolutePath, cwd)}
	}

	return nil
}

// formatCompactReadCall renders the collapsed call row (pi:
// formatCompactReadCall).
func formatCompactReadCall(theme UITheme, classification compactReadClassification, args readRenderArgs) string {
	expandHint := theme.Fg("dim", " ("+KeyText("app.tools.expand")+" to expand)")
	if classification.kind == "skill" {
		return theme.Fg("customMessageLabel", "\x1b[1m[skill]\x1b[22m ") +
			theme.Fg("customMessageText", classification.label) +
			styledReadLineRange(theme, args) +
			expandHint
	}

	return theme.Fg("toolTitle", theme.Bold("read "+classification.kind)) +
		" " +
		theme.Fg("accent", classification.label) +
		styledReadLineRange(theme, args) +
		expandHint
}

// formatReadResult renders the result body (pi: formatReadResult; "" =
// collapsed).
func formatReadResult(
	theme UITheme,
	args readRenderArgs,
	result ToolRenderResult,
	options ToolRenderResultOptions,
	showImages bool,
	isError bool,
) string {
	if !options.Expanded && !isError {
		return ""
	}

	var rawPath *string
	if args.path != nil {
		rawPath = args.path
	}
	output := GetTextOutput(&result, showImages)
	lang := ""
	if !isError && rawPath != nil {
		lang = themeGetLanguageFromPath(*rawPath)
	}
	var renderedLines []string
	if lang != "" {
		renderedLines = themeHighlightCode(ReplaceTabs(output), lang)
	} else {
		renderedLines = strings.Split(output, "\n")
	}
	lines := trimTrailingEmptyLines(renderedLines)
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
			styled = append(styled, ReplaceTabs(line))
		} else {
			styled = append(styled, theme.Fg("toolOutput", ReplaceTabs(line)))
		}
	}
	text := "\n" + strings.Join(styled, "\n")
	if remaining > 0 {
		text += theme.Fg("muted", fmt.Sprintf("\n... (%d more lines,", remaining)) +
			" " + KeyHint("app.tools.expand", "to expand") + theme.Fg("muted", ")")
	}

	var details *ReadToolDetails
	if result.Details != nil {
		details, _ = result.Details.(*ReadToolDetails)
	}
	if details != nil && details.Truncation != nil && details.Truncation.Truncated {
		truncation := details.Truncation
		if truncation.FirstLineExceedsLimit {
			maxBytes := int64(harnessutils.DefaultMaxBytes)
			if truncation.MaxBytes != 0 {
				maxBytes = int64(truncation.MaxBytes)
			}
			text += "\n" + theme.Fg("warning", fmt.Sprintf("[First line exceeds %s limit]", harnessutils.FormatSize(maxBytes)))
		} else if truncation.TruncatedBy == "lines" {
			maxLinesLimit := harnessutils.DefaultMaxLines
			if truncation.MaxLines != 0 {
				maxLinesLimit = truncation.MaxLines
			}
			text += "\n" + theme.Fg("warning", fmt.Sprintf("[Truncated: showing %d of %d lines (%d line limit)]", truncation.OutputLines, truncation.TotalLines, maxLinesLimit))
		} else {
			maxBytes := int64(harnessutils.DefaultMaxBytes)
			if truncation.MaxBytes != 0 {
				maxBytes = int64(truncation.MaxBytes)
			}
			text += "\n" + theme.Fg("warning", fmt.Sprintf("[Truncated: %d lines shown (%s limit)]", truncation.OutputLines, harnessutils.FormatSize(maxBytes)))
		}
	}
	return text
}

// readRenderCall is the read call renderer (pi: readRenderers.renderCall).
func readRenderCall(args any, themeArg UITheme, context ToolRenderContext) tui.Component {
	var text *tui.Text
	if c, ok := context.LastComponent.(*tui.Text); ok {
		text = c
	} else {
		text = tui.NewText("", 0, 0)
	}
	renderArgs := readRenderArgsFromArgs(args)
	var classification *compactReadClassification
	if !context.Expanded {
		classification = getCompactReadClassification(renderArgs, context.Cwd)
	}
	if classification != nil {
		text.SetText(formatCompactReadCall(themeArg, *classification, renderArgs))
	} else {
		text.SetText(formatReadCall(themeArg, renderArgs, context.Cwd))
	}
	return text
}

// readRenderResult is the read result renderer (pi:
// readRenderers.renderResult).
func readRenderResult(result ToolRenderResult, options ToolRenderResultOptions, themeArg UITheme, context ToolRenderContext) tui.Component {
	var text *tui.Text
	if c, ok := context.LastComponent.(*tui.Text); ok {
		text = c
	} else {
		text = tui.NewText("", 0, 0)
	}
	text.SetText(formatReadResult(themeArg, readRenderArgsFromArgs(context.Args), result, options, context.ShowImages, context.IsError))
	return text
}

// ReadRenderers is the read renderer pair (pi: readRenderers; read.ts
// spreads it into its definition).
var ReadRenderers = ToolRenderers{
	RenderCall:   readRenderCall,
	RenderResult: readRenderResult,
}
