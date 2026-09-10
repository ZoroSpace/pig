// tui/truncated_text.go (pi: packages/tui/src/components/truncated-text.ts)
//
// Text component that truncates to fit viewport width.
package tui

import (
	"strings"
)

// TruncatedText is the single-line truncating text component
// (pi: class TruncatedText implements Component).
type TruncatedText struct {
	text     string
	paddingX int
	paddingY int
}

// NewTruncatedText mirrors pi's constructor defaults (paddingX 0, paddingY 0).
func NewTruncatedText(text string, paddingX int, paddingY int) *TruncatedText {
	return &TruncatedText{text: text, paddingX: paddingX, paddingY: paddingY}
}

func (t *TruncatedText) Invalidate() {
	// No cached state to invalidate currently
}

func (t *TruncatedText) Render(width int) []string {
	result := []string{}

	// Empty line padded to width
	emptyLine := strings.Repeat(" ", width)

	// Add vertical padding above
	for i := 0; i < t.paddingY; i++ {
		result = append(result, emptyLine)
	}

	// Calculate available width after horizontal padding
	availableWidth := max(1, width-t.paddingX*2)

	// Take only the first line (stop at newline)
	singleLineText := t.text
	if newlineIndex := strings.Index(t.text, "\n"); newlineIndex != -1 {
		singleLineText = t.text[:newlineIndex]
	}

	// Truncate text if needed (accounting for ANSI codes)
	displayText := TruncateToWidth(singleLineText, availableWidth, "...", false)

	// Add horizontal padding
	leftPadding := strings.Repeat(" ", t.paddingX)
	rightPadding := strings.Repeat(" ", t.paddingX)
	lineWithPadding := leftPadding + displayText + rightPadding

	// Pad line to exactly width characters
	lineVisibleWidth := VisibleWidth(lineWithPadding)
	paddingNeeded := max(0, width-lineVisibleWidth)
	finalLine := lineWithPadding + strings.Repeat(" ", paddingNeeded)

	result = append(result, finalLine)

	// Add vertical padding below
	for i := 0; i < t.paddingY; i++ {
		result = append(result, emptyLine)
	}

	return result
}
