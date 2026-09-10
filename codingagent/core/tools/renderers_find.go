// renderers_find.go (pi: src/core/tools/renderers/find.ts) —
// presentation for the find tool.
//
// S5a note: pi hosts the renderers in core/tools/renderers/; pig keeps them
// in package tools (files named renderers_*.go) because the renderers
// depend on package-tools values (render-utils, detail types) while the
// tool files spread them back into their definitions — a Go subpackage
// would be an import cycle. Grep mapping: pi renderers/find.ts ↔
// renderers_find.go.
package tools

import (
	"fmt"
	"strings"

	harnessutils "github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/tui"
)

// formatFindCall renders the call row (pi: formatFindCall).
func formatFindCall(theme UITheme, args map[string]any) string {
	pattern, patternNull := stringArg(args, "pattern")
	rawPathValue, hasPath := args["path"]
	pathNull := false
	path := ""
	if !hasPath || rawPathValue == nil {
		path = ShortenPath(".")
	} else if s, isStr := rawPathValue.(string); isStr {
		if s == "" {
			path = ShortenPath(".")
		} else {
			path = ShortenPath(s)
		}
	} else {
		pathNull = true
	}
	var limit *float64
	if v, ok := args["limit"].(float64); ok {
		limit = &v
	}
	invalidArg := InvalidArgText(theme)
	text := theme.Fg("toolTitle", theme.Bold("find")) + " "
	if patternNull {
		text += invalidArg
	} else {
		text += theme.Fg("accent", pattern)
	}
	if pathNull {
		text += theme.Fg("toolOutput", " in "+invalidArg)
	} else {
		text += theme.Fg("toolOutput", " in "+path)
	}
	if limit != nil {
		text += theme.Fg("toolOutput", fmt.Sprintf(" (limit %d)", int64(*limit)))
	}
	return text
}

// formatFindResult renders the result body (pi: formatFindResult).
func formatFindResult(theme UITheme, result ToolRenderResult, options ToolRenderResultOptions, showImages bool) string {
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

	var details *FindToolDetails
	if result.Details != nil {
		details, _ = result.Details.(*FindToolDetails)
	}
	var resultLimit *int
	var truncation *harnessutils.TruncationResult
	if details != nil {
		resultLimit = details.ResultLimitReached
		truncation = details.Truncation
	}
	if resultLimit != nil || (truncation != nil && truncation.Truncated) {
		var warnings []string
		if resultLimit != nil {
			warnings = append(warnings, fmt.Sprintf("%d results limit", *resultLimit))
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

// findRenderCall is the find call renderer (pi: findRenderers.renderCall).
func findRenderCall(args any, themeArg UITheme, context ToolRenderContext) tui.Component {
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
	text.SetText(formatFindCall(themeArg, m))
	return text
}

// findRenderResult is the find result renderer (pi:
// findRenderers.renderResult).
func findRenderResult(result ToolRenderResult, options ToolRenderResultOptions, themeArg UITheme, context ToolRenderContext) tui.Component {
	var text *tui.Text
	if c, ok := context.LastComponent.(*tui.Text); ok {
		text = c
	} else {
		text = tui.NewText("", 0, 0)
	}
	text.SetText(formatFindResult(themeArg, result, options, context.ShowImages))
	return text
}

// FindRenderers is the find renderer pair (pi: findRenderers; find.ts
// spreads it into its definition).
var FindRenderers = ToolRenderers{
	RenderCall:   findRenderCall,
	RenderResult: findRenderResult,
}
