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

// TruncateToVisualLines truncates text to a maximum number of visual lines
// from the end, accounting for line wrapping at the given width (pi:
// truncateToVisualLines). paddingX: use 0 when the result is placed in a
// Box (Box adds its own padding), 1 for a plain Container.
func TruncateToVisualLines(text string, maxVisualLines int, width int, paddingX int) VisualTruncateResult {
	if text == "" {
		return VisualTruncateResult{}
	}

	// Create a temporary Text component to render and get visual lines
	tempText := tui.NewText(text, paddingX, 0)
	allVisualLines := tempText.Render(width)

	if len(allVisualLines) <= maxVisualLines {
		return VisualTruncateResult{VisualLines: allVisualLines, SkippedCount: 0}
	}

	// Take the last N visual lines
	truncatedLines := allVisualLines[len(allVisualLines)-maxVisualLines:]
	return VisualTruncateResult{
		VisualLines:  truncatedLines,
		SkippedCount: len(allVisualLines) - maxVisualLines,
	}
}
