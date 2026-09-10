// renderers_grep.go (pi: src/core/tools/renderers/grep.ts) —
// presentation for the grep tool.
//
// S5a note: pi hosts the renderers in core/tools/renderers/; pig keeps them
// in package tools (files named renderers_*.go) because the renderers
// depend on package-tools values (render-utils, detail types) while the
// tool files spread them back into their definitions — a Go subpackage
// would be an import cycle. Grep mapping: pi renderers/grep.ts ↔
// renderers_grep.go.
package tools

import (
	"fmt"
	"strings"

	harnessutils "github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/tui"
)

// formatGrepCall renders the call row (pi: formatGrepCall).
func formatGrepCall(theme UITheme, args map[string]any) string {
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
	glob, _ := stringArg(args, "glob")
	var limit *float64
	if v, ok := args["limit"].(float64); ok {
		limit = &v
	}
	invalidArg := InvalidArgText(theme)
	text := theme.Fg("toolTitle", theme.Bold("grep")) + " "
	if patternNull {
		text += invalidArg
	} else {
		text += theme.Fg("accent", "/"+pattern+"/")
	}
	if pathNull {
		text += theme.Fg("toolOutput", " in "+invalidArg)
	} else {
		text += theme.Fg("toolOutput", " in "+path)
	}
	if glob != "" {
		text += theme.Fg("toolOutput", " ("+glob+")")
	}
	if limit != nil {
		text += theme.Fg("toolOutput", fmt.Sprintf(" limit %d", int64(*limit)))
	}
	return text
}

// formatGrepResult renders the result body (pi: formatGrepResult).
func formatGrepResult(theme UITheme, result ToolRenderResult, options ToolRenderResultOptions, showImages bool) string {
	output := strings.Trim(GetTextOutput(&result, showImages), " \t\n\r")
	text := ""
	if output != "" {
		lines := strings.Split(output, "\n")
		maxLines := 15
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

	var details *GrepToolDetails
	if result.Details != nil {
		details, _ = result.Details.(*GrepToolDetails)
	}
	var matchLimit *int
	var truncation *harnessutils.TruncationResult
	linesTruncated := false
	if details != nil {
		matchLimit = details.MatchLimitReached
		truncation = details.Truncation
		if details.LinesTruncated != nil {
			linesTruncated = *details.LinesTruncated
		}
	}
	if matchLimit != nil || (truncation != nil && truncation.Truncated) || linesTruncated {
		var warnings []string
		if matchLimit != nil {
			warnings = append(warnings, fmt.Sprintf("%d matches limit", *matchLimit))
		}
		if truncation != nil && truncation.Truncated {
			maxBytes := int64(harnessutils.DefaultMaxBytes)
			if truncation.MaxBytes != 0 {
				maxBytes = int64(truncation.MaxBytes)
			}
			warnings = append(warnings, harnessutils.FormatSize(maxBytes)+" limit")
		}
		if linesTruncated {
			warnings = append(warnings, "some lines truncated")
		}
		text += "\n" + theme.Fg("warning", "[Truncated: "+strings.Join(warnings, ", ")+"]")
	}
	return text
}

// grepRenderCall is the grep call renderer (pi: grepRenderers.renderCall).
func grepRenderCall(args any, themeArg UITheme, context ToolRenderContext) tui.Component {
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
	text.SetText(formatGrepCall(themeArg, m))
	return text
}

// grepRenderResult is the grep result renderer (pi:
// grepRenderers.renderResult).
func grepRenderResult(result ToolRenderResult, options ToolRenderResultOptions, themeArg UITheme, context ToolRenderContext) tui.Component {
	var text *tui.Text
	if c, ok := context.LastComponent.(*tui.Text); ok {
		text = c
	} else {
		text = tui.NewText("", 0, 0)
	}
	text.SetText(formatGrepResult(themeArg, result, options, context.ShowImages))
	return text
}

// GrepRenderers is the grep renderer pair (pi: grepRenderers; grep.ts
// spreads it into its definition).
var GrepRenderers = ToolRenderers{
	RenderCall:   grepRenderCall,
	RenderResult: grepRenderResult,
}
