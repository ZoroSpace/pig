// renderers_ls.go (pi: src/core/tools/renderers/ls.ts) —
// presentation for the ls tool.
//
// S5a note: pi hosts the renderers in core/tools/renderers/; pig keeps them
// in package tools (files named renderers_*.go) because the renderers
// depend on package-tools values (render-utils, detail types) while the
// tool files spread them back into their definitions — a Go subpackage
// would be an import cycle. Grep mapping: pi renderers/ls.ts ↔
// renderers_ls.go.
package tools

import (
	"fmt"
	"strings"

	harnessutils "github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/tui"
)

// formatLsCall renders the call row (pi: formatLsCall).
func formatLsCall(theme UITheme, args map[string]any, cwd string) string {
	var limit *float64
	if v, ok := args["limit"].(float64); ok {
		limit = &v
	}
	pathDisplay := RenderToolPath(rawPathFromArgs(args), theme, cwd, ".")
	text := theme.Fg("toolTitle", theme.Bold("ls")) + " " + pathDisplay
	if limit != nil {
		text += theme.Fg("toolOutput", fmt.Sprintf(" (limit %d)", int64(*limit)))
	}
	return text
}

// formatLsResult renders the result body (pi: formatLsResult).
func formatLsResult(theme UITheme, result ToolRenderResult, options ToolRenderResultOptions, showImages bool) string {
	output := strings.Trim(GetTextOutput(&result, showImages), " \t\n\r")
	text := ""
	if output != "" {
		lines := strings.Split(output, "\n")
		maxLines := 20
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
			styled = append(styled, theme.Fg("toolOutput", line))
		}
		text += "\n" + strings.Join(styled, "\n")
		if remaining > 0 {
			text += theme.Fg("muted", fmt.Sprintf("\n... (%d more lines,", remaining)) +
				" " + KeyHint("app.tools.expand", "to expand") + theme.Fg("muted", ")")
		}
	}

	var details *LsToolDetails
	if result.Details != nil {
		details, _ = result.Details.(*LsToolDetails)
	}
	var entryLimit *int
	var truncation *harnessutils.TruncationResult
	if details != nil {
		entryLimit = details.EntryLimitReached
		truncation = details.Truncation
	}
	if entryLimit != nil || (truncation != nil && truncation.Truncated) {
		var warnings []string
		if entryLimit != nil {
			warnings = append(warnings, fmt.Sprintf("%d entries limit", *entryLimit))
		}
		if truncation != nil && truncation.Truncated {
			maxBytes := int64(harnessutils.DefaultMaxBytes)
			if truncation.MaxBytes != 0 {
				maxBytes = int64(truncation.MaxBytes)
			}
			warnings = append(warnings, harnessutils.FormatSize(maxBytes)+" limit")
		}
		text += "\n" + theme.Fg("warning", "[Truncated: "+strings.Join(warnings, ", ")+"]")
	}
	return text
}

// lsRenderCall is the ls call renderer (pi: lsRenderers.renderCall).
func lsRenderCall(args any, themeArg UITheme, context ToolRenderContext) tui.Component {
	var text *tui.Text
	if c, ok := context.LastComponent.(*tui.Text); ok {
		text = c
	} else {
		text = tui.NewText("", 0, 0)
	}
	m, _ := args.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	text.SetText(formatLsCall(themeArg, m, context.Cwd))
	return text
}

// lsRenderResult is the ls result renderer (pi:
// lsRenderers.renderResult).
func lsRenderResult(result ToolRenderResult, options ToolRenderResultOptions, themeArg UITheme, context ToolRenderContext) tui.Component {
	var text *tui.Text
	if c, ok := context.LastComponent.(*tui.Text); ok {
		text = c
	} else {
		text = tui.NewText("", 0, 0)
	}
	text.SetText(formatLsResult(themeArg, result, options, context.ShowImages))
	return text
}

// LsRenderers is the ls renderer pair (pi: lsRenderers; ls.ts
// spreads it into its definition).
var LsRenderers = ToolRenderers{
	RenderCall:   lsRenderCall,
	RenderResult: lsRenderResult,
}
