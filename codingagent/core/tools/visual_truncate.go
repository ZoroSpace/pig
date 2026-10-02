// visual_truncate.go (pi: components/visual-truncate.ts) — shared utility
// for truncating text to visual lines (accounting for line wrapping). Used
// by tool-execution and bash-execution for consistent behavior.
//
// Declared deviation: pi keeps this file in modes/interactive/components,
// but pig's bash tool renderer needs it and core/tools cannot import
// components (components → core/tools for tool-execution would cycle);
// moved here, components/bash-execution consumes it via the tools import.
package tools

import (
	"github.com/ZoroSpace/pig/tui"
)

// VisualTruncateResult is pi's VisualTruncateResult.
type VisualTruncateResult struct {
	// VisualLines are the visual lines to display.
	VisualLines []string
	// SkippedCount is the number of visual lines that were skipped
	// (hidden).
	SkippedCount int
}

// VisualTruncateKeep selects which visual lines survive truncation.
type VisualTruncateKeep string

const (
	// VisualTruncateKeepEnd keeps the last visual lines (bash-style: the
	// newest output matters).
	VisualTruncateKeepEnd VisualTruncateKeep = "end"
	// VisualTruncateKeepStart keeps the first visual lines (MCP-style result
	// previews; pi VisualLinePreview keep:"start").
	VisualTruncateKeepStart VisualTruncateKeep = "start"
)

// TruncateToVisualLines truncates text to a maximum number of visual lines,
// accounting for line wrapping at the given width (pi: truncateToVisualLines).
// keep selects whether the first or last lines survive. paddingX: use 0 when
// the result is placed in a Box (Box adds its own padding), 1 for a plain
// Container.
func TruncateToVisualLines(text string, maxVisualLines int, width int, paddingX int, keep VisualTruncateKeep) VisualTruncateResult {
	if text == "" {
		return VisualTruncateResult{}
	}

	// Create a temporary Text component to render and get visual lines
	tempText := tui.NewText(text, paddingX, 0)
	allVisualLines := tempText.Render(width)

	if len(allVisualLines) <= maxVisualLines {
		return VisualTruncateResult{VisualLines: allVisualLines, SkippedCount: 0}
	}

	var truncatedLines []string
	if keep == VisualTruncateKeepStart {
		truncatedLines = allVisualLines[:maxVisualLines]
	} else {
		truncatedLines = allVisualLines[len(allVisualLines)-maxVisualLines:]
	}
	return VisualTruncateResult{
		VisualLines:  truncatedLines,
		SkippedCount: len(allVisualLines) - maxVisualLines,
	}
}
