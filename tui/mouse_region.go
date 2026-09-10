// tui/mouse_region.go (pi: packages/tui/src/components/mouse-region.ts)
package tui

// MouseRegionHandler handles a mouse event targeted at the wrapped child
// region (pi: MouseRegionHandler).
type MouseRegionHandler = func(event *TuiMouseEvent) *TuiMouseEventResult

// MouseRegion adds mouse handling to an existing component without changing
// its rendering (pi: MouseRegion implements Component).
type MouseRegion struct {
	child   Component
	onMouse MouseRegionHandler
}

// NewMouseRegion wraps child with a mouse handler.
func NewMouseRegion(child Component, onMouse MouseRegionHandler) *MouseRegion {
	return &MouseRegion{child: child, onMouse: onMouse}
}

// Render implements Component (delegates to the child).
func (m *MouseRegion) Render(width int) []string {
	return m.child.Render(width)
}

// HandleMouse implements MouseHandler: try the child first, then the
// region's own handler (pi: handleMouse).
func (m *MouseRegion) HandleMouse(event *TuiMouseEvent) *TuiMouseEventResult {
	childResult := DispatchMouseEvent(m.child, event)
	if childResult != nil {
		return childResult
	}
	if m.onMouse == nil {
		return nil
	}
	return m.onMouse(event)
}

// Invalidate implements Component (delegates to the child).
func (m *MouseRegion) Invalidate() {
	m.child.Invalidate()
}
