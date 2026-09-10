// tui/text.go (pi: packages/tui/src/components/text.ts)
//
// Text component - displays multi-line text with word wrapping.
//
// Concurrency note (pig adaptation, declared): pi runs everything on the JS
// event loop, so the render cache needs no guarding. Loader embeds Text and
// advances its spinner from a timer goroutine that calls SetText while the
// render goroutine may be inside Render — the cache fields are guarded by a
// mutex here (behavior identical in single-threaded use).
package tui

import (
	"strings"
	"sync"
)

// Text is the text component (pi: class Text implements Component).
type Text struct {
	mu         sync.Mutex
	text       string
	paddingX   int // Left/right padding
	paddingY   int // Top/bottom padding
	customBgFn func(text string) string

	// Cache for rendered output
	cachedText     string
	cachedTextSet  bool
	cachedWidth    int
	cachedWidthSet bool
	cachedLines    []string
}

// NewText mirrors pi's constructor defaults (text "", paddingX 1, paddingY 1);
// customBgFn is optional (pi: customBgFn? → variadic).
func NewText(text string, paddingX int, paddingY int, customBgFn ...func(text string) string) *Text {
	t := &Text{text: text, paddingX: paddingX, paddingY: paddingY}
	if len(customBgFn) > 0 {
		t.customBgFn = customBgFn[0]
	}
	return t
}

func (t *Text) SetText(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.text = text
	t.invalidateLocked()
}

func (t *Text) SetCustomBgFn(customBgFn func(text string) string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.customBgFn = customBgFn
	t.invalidateLocked()
}

func (t *Text) Invalidate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.invalidateLocked()
}

func (t *Text) invalidateLocked() {
	t.cachedTextSet = false
	t.cachedWidthSet = false
	t.cachedLines = nil
}

func (t *Text) Render(width int) []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check cache
	if t.cachedLines != nil && t.cachedTextSet && t.cachedText == t.text && t.cachedWidthSet && t.cachedWidth == width {
		return t.cachedLines
	}

	// Don't render anything if there's no actual text
	if t.text == "" || strings.TrimSpace(t.text) == "" {
		result := []string{}
		t.cachedText = t.text
		t.cachedTextSet = true
		t.cachedWidth = width
		t.cachedWidthSet = true
		t.cachedLines = result
		return result
	}

	// Replace tabs with 3 spaces
	normalizedText := strings.ReplaceAll(t.text, "\t", "   ")

	// Reduce margins when necessary so content and padding fit within the available width.
	paddingX := t.paddingX
	if max := max(0, (width-1)/2); paddingX > max {
		paddingX = max
	}
	contentWidth := max(1, width-paddingX*2)

	// Wrap text (this preserves ANSI codes but does NOT pad)
	wrappedLines := WrapTextWithAnsi(normalizedText, contentWidth)

	// Add margins and background to each line
	leftMargin := strings.Repeat(" ", paddingX)
	rightMargin := strings.Repeat(" ", paddingX)
	contentLines := []string{}

	for _, line := range wrappedLines {
		// Add margins
		lineWithMargins := leftMargin + line + rightMargin

		// Apply background if specified (this also pads to full width)
		if t.customBgFn != nil {
			contentLines = append(contentLines, ApplyBackgroundToLine(lineWithMargins, width, t.customBgFn))
		} else {
			// No background - just pad to width with spaces
			visibleLen := VisibleWidth(lineWithMargins)
			paddingNeeded := max(0, width-visibleLen)
			contentLines = append(contentLines, lineWithMargins+strings.Repeat(" ", paddingNeeded))
		}
	}

	// Add top/bottom padding (empty lines)
	emptyLine := strings.Repeat(" ", width)
	emptyLines := []string{}
	for i := 0; i < t.paddingY; i++ {
		line := emptyLine
		if t.customBgFn != nil {
			line = ApplyBackgroundToLine(emptyLine, width, t.customBgFn)
		}
		emptyLines = append(emptyLines, line)
	}

	result := append(append(append([]string{}, emptyLines...), contentLines...), emptyLines...)

	// Update cache
	t.cachedText = t.text
	t.cachedTextSet = true
	t.cachedWidth = width
	t.cachedWidthSet = true
	t.cachedLines = result

	if len(result) > 0 {
		return result
	}
	return []string{""}
}
