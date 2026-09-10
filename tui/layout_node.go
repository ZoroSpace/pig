// tui/layout_node.go (pi: packages/tui/src/layout-node.ts)
//
// Layout-node protocol shared between Stack components (this batch, T3) and
// the ScrollView / renderLayoutFrame machinery (pi: layout.ts — deferred to
// T4 with the interactive components; the scroll half of this file is
// deferred with it, see the section comment below).
package tui

// LayoutViewport is the visible region passed to entry visibility predicates.
type LayoutViewport struct {
	Width  int
	Height int
}

// StackLayoutEntry is one child of a Stack with its flex options
// (pi: StackLayoutEntry).
type StackLayoutEntry struct {
	Component Component
	// Basis is pi's `basis?: number | "auto"`: nil = not provided (use the
	// intrinsic size), int = fixed basis, "auto" = same as nil.
	Basis any
	// Normalized flex options. pi stores them as optional keys and defaults
	// at read sites (`entry.grow ?? 0` / `?? 1` / `?? 0` / `?? MAX`); since
	// Stack.AddChild normalizes with exactly those fallbacks, absent keys and
	// defaults collapse to the same values, so plain ints suffice.
	Grow    int
	Shrink  int
	MinSize int
	MaxSize int
	// Visible hides the entry from layout when it returns false
	// (pi: visible?: (viewport) => boolean). nil = always visible.
	Visible func(LayoutViewport) bool
}

// StackLayoutNode is the layout description produced by VStack/HStack.
type StackLayoutNode struct {
	Type    string // "vstack" | "hstack"
	Entries []StackLayoutEntry
	Gap     int
	Align   string // "stretch" | "start" | "center" | "end"
}

func (n *StackLayoutNode) isLayoutNode() {}

// ScrollLayoutState is the state protocol a scroll container exposes to the
// layout engine (pi: ScrollLayoutState). *ScrollView is the only
// implementation.
type ScrollLayoutState interface {
	ScrollTop() int
	Primary() bool
	Overscroll() string // "chain" | "contain"
	ViewportHeight() int
	GetContentWidth(width int) int
	UpdateLayout(contentHeight int, viewportHeight int, requestRender func())
}

// ScrollLayoutNode is the layout description produced by ScrollView.
type ScrollLayoutNode struct {
	Type      string // "scroll"
	Component Component
	State     ScrollLayoutState
}

func (n *ScrollLayoutNode) isLayoutNode() {}

// LayoutNode is pi's `StackLayoutNode | ScrollLayoutNode` union.
type LayoutNode interface {
	isLayoutNode()
}

// LayoutComponent is implemented by components that expose a layout node
// (pi: LayoutComponent with the LAYOUT_NODE symbol; Go has no symbol
// injection, so the protocol is an interface method).
type LayoutComponent interface {
	Component
	LayoutNode() LayoutNode
}

// GetLayoutNode returns the layout node of a component implementing
// LayoutComponent, or nil (pi: getLayoutNode).
func GetLayoutNode(component Component) LayoutNode {
	if lc, ok := component.(LayoutComponent); ok {
		return lc.LayoutNode()
	}
	return nil
}
