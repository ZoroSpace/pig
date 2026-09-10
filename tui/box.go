// tui/box.go (pi: packages/tui/src/components/box.ts)
//
// Box component - a container that applies padding and background to all children.
package tui

import "strings"

// boxRenderCache is pi's RenderCache type.
type boxRenderCache struct {
	childLines  []string
	width       int
	bgSample    string
	bgSampleSet bool
	lines       []string
}

// Box is the padding/background container (pi: class Box implements Component).
type Box struct {
	Children []Component

	paddingX int
	paddingY int
	bgFn     func(text string) string

	// Cache for rendered output
	cache *boxRenderCache
}

// NewBox mirrors pi's constructor defaults (paddingX 1, paddingY 1).
func NewBox(paddingX int, paddingY int, bgFn func(text string) string) *Box {
	return &Box{paddingX: paddingX, paddingY: paddingY, bgFn: bgFn}
}

func (b *Box) AddChild(component Component) {
	b.Children = append(b.Children, component)
	b.invalidateCache()
}

func (b *Box) RemoveChild(component Component) {
	for i, child := range b.Children {
		if child == component {
			b.Children = append(b.Children[:i], b.Children[i+1:]...)
			b.invalidateCache()
			return
		}
	}
}

func (b *Box) Clear() {
	b.Children = nil
	b.invalidateCache()
}

func (b *Box) SetBgFn(bgFn func(text string) string) {
	b.bgFn = bgFn
	// Don't invalidate here - we'll detect bgFn changes by sampling output
}

func (b *Box) invalidateCache() {
	b.cache = nil
}

func (b *Box) matchCache(width int, childLines []string, bgSample string, bgSampleSet bool) bool {
	cache := b.cache
	if cache == nil || cache.width != width || cache.bgSampleSet != bgSampleSet || cache.bgSample != bgSample ||
		len(cache.childLines) != len(childLines) {
		return false
	}
	for i, line := range cache.childLines {
		if line != childLines[i] {
			return false
		}
	}
	return true
}

func (b *Box) Invalidate() {
	b.invalidateCache()
	for _, child := range b.Children {
		child.Invalidate()
	}
}

func (b *Box) Render(width int) []string {
	if len(b.Children) == 0 {
		return []string{}
	}

	contentWidth := max(1, width-b.paddingX*2)
	leftPad := strings.Repeat(" ", b.paddingX)

	// Render all children
	childLines := []string{}
	for _, child := range b.Children {
		lines := child.Render(contentWidth)
		for _, line := range lines {
			childLines = append(childLines, leftPad+line)
		}
	}

	if len(childLines) == 0 {
		return []string{}
	}

	// Check if bgFn output changed by sampling
	bgSample := ""
	bgSampleSet := false
	if b.bgFn != nil {
		bgSample = b.bgFn("test")
		bgSampleSet = true
	}

	// Check cache validity
	if b.matchCache(width, childLines, bgSample, bgSampleSet) {
		return b.cache.lines
	}

	// Apply background and padding
	result := []string{}

	// Top padding
	for i := 0; i < b.paddingY; i++ {
		result = append(result, b.applyBg("", width))
	}

	// Content
	for _, line := range childLines {
		result = append(result, b.applyBg(line, width))
	}

	// Bottom padding
	for i := 0; i < b.paddingY; i++ {
		result = append(result, b.applyBg("", width))
	}

	// Update cache
	b.cache = &boxRenderCache{childLines: childLines, width: width, bgSample: bgSample, bgSampleSet: bgSampleSet, lines: result}

	return result
}

func (b *Box) applyBg(line string, width int) string {
	visLen := VisibleWidth(line)
	padNeeded := max(0, width-visLen)
	padded := line + strings.Repeat(" ", padNeeded)

	if b.bgFn != nil {
		return ApplyBackgroundToLine(padded, width, b.bgFn)
	}
	return padded
}
