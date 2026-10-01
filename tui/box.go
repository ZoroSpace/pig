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

	// Cached child heights for mouse hit-testing (pi: mouseLayout)
	mouseLayout *containerMouseLayout
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

// HandleMouse implements MouseHandler: translate pointer coordinates into
// the padded content area and hit-test children by their rendered heights
// (pi: Box.handleMouse).
func (b *Box) HandleMouse(event *TuiMouseEvent) *TuiMouseEventResult {
	contentWidth := maxInt(1, event.Width-b.paddingX*2)
	contentY := event.Y - b.paddingY
	contentX := event.X - b.paddingX
	if contentY < 0 || contentX < 0 || contentX >= contentWidth {
		return nil
	}

	var mouseChildren []containerMouseChild
	if b.mouseLayout != nil && b.mouseLayout.width == contentWidth {
		mouseChildren = b.mouseLayout.children
	} else {
		for _, component := range b.Children {
			mouseChildren = append(mouseChildren, containerMouseChild{component: component, height: len(component.Render(contentWidth))})
		}
	}
	childY := 0
	for _, child := range mouseChildren {
		if contentY >= childY && contentY < childY+child.height {
			childEvent := *event
			childEvent.X = contentX
			childEvent.Y = contentY - childY
			childEvent.Width = contentWidth
			childEvent.Height = child.height
			return DispatchMouseEvent(child.component, &childEvent)
		}
		childY += child.height
	}
	return nil
}

func (b *Box) Render(width int) []string {
	if len(b.Children) == 0 {
		return []string{}
	}

	contentWidth := max(1, width-b.paddingX*2)
	leftPad := strings.Repeat(" ", b.paddingX)

	// Render all children. Keep the child lines unpadded: children usually
	// return the same string objects every frame, so the cache check below is
	// a cheap identity comparison per line (pi v0.99.1). Padding here would
	// create new strings that must be compared character by character.
	childLines := []string{}
	var mouseChildren []containerMouseChild
	for _, child := range b.Children {
		lines := child.Render(contentWidth)
		mouseChildren = append(mouseChildren, containerMouseChild{component: child, height: len(lines)})
		childLines = append(childLines, lines...)
	}
	b.mouseLayout = &containerMouseLayout{width: contentWidth, children: mouseChildren}

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
		result = append(result, b.applyBg(leftPad+line, width))
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
	// Already padded to width, so apply the background directly instead of
	// measuring the line again (pi v0.99.1).
	if b.bgFn != nil {
		return b.bgFn(padded)
	}
	return padded
}
