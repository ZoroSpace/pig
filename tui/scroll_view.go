// tui/scroll_view.go (pi: packages/tui/src/components/scroll-view.ts)
//
// Porting notes (declared deviations):
//   - Concurrency: pi serializes ScrollView access on the JS event loop. pig
//     guards the state with its own mutex. requestRenderCallback is always
//     invoked OUTSIDE the lock: the layout engine calls UpdateLayout while
//     holding the TUI lock, and a concurrent ScrollBy holding the ScrollView
//     lock must not block on the TUI lock inside the callback (deadlock).
//   - NodeJS.Timeout.unref() has no Go equivalent (timers don't keep a Go
//     process alive); dropped.
package tui

import (
	"sync"
	"time"
)

// ScrollViewScrollbar selects scrollbar visibility (pi: ScrollViewScrollbar).
type ScrollViewScrollbar = string

const (
	ScrollViewScrollbarHidden ScrollViewScrollbar = "hidden"
	ScrollViewScrollbarAuto   ScrollViewScrollbar = "auto"
	ScrollViewScrollbarAlways ScrollViewScrollbar = "always"
)

// ScrollViewOptions configures a ScrollView (pi: ScrollViewOptions).
type ScrollViewOptions struct {
	Axis                 any    // "vertical" only; nil = default. Any other value panics in the constructor.
	Follow               string // "none" | "end"; "" = "none"
	Primary              bool
	Overscroll           string // "chain" | "contain"; "" = "chain"
	Scrollbar            ScrollViewScrollbar
	ScrollbarStyle       func(text string) string
	ScrollbarHideDelayMs *int
}

// ScrollViewScrollToOptions carries pi's scrollTo options.
type ScrollViewScrollToOptions struct {
	// DisableFollow keeps follow-end disabled even when the target is the
	// current content end.
	DisableFollow bool
}

// ScrollView is a vertical scrolling container for a single child
// (pi: ScrollView extends Container).
type ScrollView struct {
	Container

	mu sync.Mutex

	child                     Component
	followEnd                 bool
	primary                   bool
	overscroll                string
	scrollbarStyle            func(text string) string
	currentScrollbar          ScrollViewScrollbar
	scrollbarHideDelayMs      int
	currentScrollTop          int
	contentHeight             int
	currentViewportHeight     int
	followingEnd              bool
	followSuppressedAtEnd     bool
	requestRenderCallback     func()
	transientScrollbarVisible bool
	scrollbarActive           bool
	scrollbarHideTimer        *time.Timer
}

// NewScrollView creates a ScrollView wrapping component.
func NewScrollView(component Component, options *ScrollViewOptions) *ScrollView {
	s := &ScrollView{}
	if options != nil && options.Axis != nil && options.Axis != "vertical" {
		panic("Unsupported ScrollView axis")
	}
	s.child = component
	s.Children = []Component{component}
	followEnd := options != nil && options.Follow == "end"
	s.followEnd = followEnd
	s.followingEnd = followEnd
	if options != nil {
		s.primary = options.Primary
	}
	s.overscroll = "chain"
	if options != nil && options.Overscroll != "" {
		s.overscroll = options.Overscroll
	}
	s.currentScrollbar = ScrollViewScrollbarHidden
	if options != nil && options.Scrollbar != "" {
		s.currentScrollbar = options.Scrollbar
	}
	s.scrollbarStyle = func(text string) string { return "\x1b[100m" + text + "\x1b[49m" }
	if options != nil && options.ScrollbarStyle != nil {
		s.scrollbarStyle = options.ScrollbarStyle
	}
	s.scrollbarHideDelayMs = 1000
	if options != nil && options.ScrollbarHideDelayMs != nil {
		s.scrollbarHideDelayMs = maxInt(0, *options.ScrollbarHideDelayMs)
	}
	return s
}

// ScrollTop returns the current scroll offset (pi: scrollTop getter).
func (s *ScrollView) ScrollTop() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentScrollTop
}

// IsFollowingEnd reports whether the view tracks the content end (pi:
// isFollowingEnd getter).
func (s *ScrollView) IsFollowingEnd() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.followingEnd
}

// ViewportHeight returns the last laid-out viewport height (pi:
// viewportHeight getter).
func (s *ScrollView) ViewportHeight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentViewportHeight
}

// Scrollbar returns the current scrollbar mode (pi: scrollbar getter).
func (s *ScrollView) Scrollbar() ScrollViewScrollbar {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentScrollbar
}

// IsScrollbarVisible reports whether the scrollbar paints this frame (pi:
// isScrollbarVisible getter).
func (s *ScrollView) IsScrollbarVisible() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isScrollbarVisibleLocked()
}

func (s *ScrollView) isScrollbarVisibleLocked() bool {
	if s.currentScrollbar == ScrollViewScrollbarAlways {
		return s.currentViewportHeight > 0
	}
	return s.currentScrollbar == ScrollViewScrollbarAuto &&
		s.contentHeight > s.currentViewportHeight &&
		s.transientScrollbarVisible
}

// SetScrollbar switches the scrollbar mode (pi: setScrollbar).
func (s *ScrollView) SetScrollbar(scrollbar ScrollViewScrollbar) {
	notify := false
	s.mu.Lock()
	if scrollbar != s.currentScrollbar {
		s.currentScrollbar = scrollbar
		if scrollbar != ScrollViewScrollbarAuto {
			s.hideTransientScrollbarLocked()
		} else if s.scrollbarActive {
			s.markScrollbarActivityLocked()
		}
		notify = true
	}
	s.mu.Unlock()
	if notify && s.requestRenderCallback != nil {
		s.requestRenderCallback()
	}
}

// GetContentWidth returns the content width given the viewport width,
// reserving one column for an always-visible scrollbar (pi: getContentWidth).
func (s *ScrollView) GetContentWidth(width int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentScrollbar == ScrollViewScrollbarAlways && width > 1 {
		return width - 1
	}
	return width
}

func (s *ScrollView) markScrollbarActivityLocked() {
	if s.currentScrollbar != ScrollViewScrollbarAuto || s.contentHeight <= s.currentViewportHeight {
		return
	}
	s.transientScrollbarVisible = true
	if s.scrollbarHideTimer != nil {
		s.scrollbarHideTimer.Stop()
		s.scrollbarHideTimer = nil
	}
	if s.scrollbarActive {
		return
	}
	s.scrollbarHideTimer = time.AfterFunc(time.Duration(s.scrollbarHideDelayMs)*time.Millisecond, func() {
		var notify func()
		s.mu.Lock()
		s.scrollbarHideTimer = nil
		s.transientScrollbarVisible = false
		notify = s.requestRenderCallback
		s.mu.Unlock()
		if notify != nil {
			notify()
		}
	})
}

func (s *ScrollView) hideTransientScrollbarLocked() {
	s.transientScrollbarVisible = false
	if s.scrollbarHideTimer == nil {
		return
	}
	s.scrollbarHideTimer.Stop()
	s.scrollbarHideTimer = nil
}

// SetScrollbarActive marks pointer interaction with the scrollbar (pi:
// setScrollbarActive).
func (s *ScrollView) SetScrollbarActive(active bool) {
	s.mu.Lock()
	if active == s.scrollbarActive {
		s.mu.Unlock()
		return
	}
	s.scrollbarActive = active
	s.markScrollbarActivityLocked()
	s.mu.Unlock()
}

// ScrollTo scrolls to an absolute offset (pi: scrollTo).
func (s *ScrollView) ScrollTo(scrollTop int, options *ScrollViewScrollToOptions) {
	s.mu.Lock()
	requested := scrollTop
	maxScrollTop := maxInt(0, s.contentHeight-s.currentViewportHeight)
	next := clampInt(requested, 0, maxScrollTop)
	nextFollowSuppressedAtEnd := options != nil && options.DisableFollow && next == maxScrollTop
	nextFollowingEnd := !nextFollowSuppressedAtEnd && s.followEnd && next == maxScrollTop
	if next == s.currentScrollTop && nextFollowingEnd == s.followingEnd &&
		nextFollowSuppressedAtEnd == s.followSuppressedAtEnd {
		s.mu.Unlock()
		return
	}
	moved := next != s.currentScrollTop
	s.currentScrollTop = next
	s.followingEnd = nextFollowingEnd
	s.followSuppressedAtEnd = nextFollowSuppressedAtEnd
	if moved {
		s.markScrollbarActivityLocked()
	}
	notify := s.requestRenderCallback
	s.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// ScrollBy scrolls by a relative amount and returns the unused delta (pi:
// scrollBy).
func (s *ScrollView) ScrollBy(lines int) int {
	s.mu.Lock()
	requested := lines
	if requested == 0 {
		s.mu.Unlock()
		return 0
	}
	maxScrollTop := maxInt(0, s.contentHeight-s.currentViewportHeight)
	start := s.currentScrollTop
	if s.followingEnd {
		start = maxScrollTop
	}
	next := clampInt(start+requested, 0, maxScrollTop)
	moved := next - start
	wasFollowingEnd := s.followingEnd
	s.currentScrollTop = next
	s.followingEnd = s.followEnd && next == maxScrollTop
	s.followSuppressedAtEnd = false
	if moved != 0 {
		s.markScrollbarActivityLocked()
	}
	var notify func()
	if moved != 0 || s.followingEnd != wasFollowingEnd {
		notify = s.requestRenderCallback
	}
	s.mu.Unlock()
	if notify != nil {
		notify()
	}
	return requested - moved
}

// ScrollToStart scrolls to the top (pi: scrollToStart).
func (s *ScrollView) ScrollToStart() {
	s.mu.Lock()
	expectedFollowing := s.followEnd && s.contentHeight <= s.currentViewportHeight
	changed := s.currentScrollTop != 0 || s.followingEnd != expectedFollowing
	s.currentScrollTop = 0
	s.followingEnd = expectedFollowing
	s.followSuppressedAtEnd = false
	var notify func()
	if changed {
		s.markScrollbarActivityLocked()
		notify = s.requestRenderCallback
	}
	s.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// ScrollToEnd scrolls to the bottom (pi: scrollToEnd).
func (s *ScrollView) ScrollToEnd() {
	s.mu.Lock()
	next := maxInt(0, s.contentHeight-s.currentViewportHeight)
	changed := s.currentScrollTop != next || s.followingEnd != s.followEnd
	s.currentScrollTop = next
	s.followingEnd = s.followEnd
	s.followSuppressedAtEnd = false
	var notify func()
	if changed {
		s.markScrollbarActivityLocked()
		notify = s.requestRenderCallback
	}
	s.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// UpdateLayout is the ScrollLayoutState callback invoked by the layout engine
// (pi: updateLayout).
func (s *ScrollView) UpdateLayout(contentHeight int, viewportHeight int, requestRender func()) {
	s.mu.Lock()
	s.contentHeight = maxInt(0, contentHeight)
	s.currentViewportHeight = maxInt(0, viewportHeight)
	s.requestRenderCallback = requestRender
	maxScrollTop := maxInt(0, s.contentHeight-s.currentViewportHeight)
	if s.followingEnd {
		s.currentScrollTop = maxScrollTop
	} else {
		s.currentScrollTop = clampInt(s.currentScrollTop, 0, maxScrollTop)
	}
	if s.currentScrollTop < maxScrollTop {
		s.followSuppressedAtEnd = false
	}
	if s.followEnd && s.currentScrollTop == maxScrollTop && !s.followSuppressedAtEnd {
		s.followingEnd = true
	}
	if s.contentHeight <= s.currentViewportHeight {
		s.hideTransientScrollbarLocked()
	}
	s.mu.Unlock()
}

// AddChild overrides Container.AddChild: ScrollView has exactly one child.
func (s *ScrollView) AddChild(_ Component) {
	panic("ScrollView has exactly one child")
}

// RemoveChild overrides Container.RemoveChild.
func (s *ScrollView) RemoveChild(_ Component) {
	panic("ScrollView child cannot be removed")
}

// Clear overrides Container.Clear.
func (s *ScrollView) Clear() {
	panic("ScrollView child cannot be cleared")
}

// Render implements Component (pi: render override).
func (s *ScrollView) Render(width int) []string {
	contentWidth := s.GetContentWidth(width)
	lines := s.child.Render(contentWidth)
	if contentWidth == width {
		return lines
	}
	padded := make([]string, len(lines))
	for i, line := range lines {
		padded[i] = line + " "
	}
	return padded
}

// Primary reports whether this is the primary scroll view (pi: readonly
// primary).
func (s *ScrollView) Primary() bool { return s.primary }

// Overscroll returns the overscroll chaining mode (pi: readonly overscroll).
func (s *ScrollView) Overscroll() string { return s.overscroll }

// ScrollbarStyle returns the scrollbar cell styler (pi: readonly
// scrollbarStyle).
func (s *ScrollView) ScrollbarStyle(text string) string { return s.scrollbarStyle(text) }

// LayoutNode implements LayoutComponent (pi: [LAYOUT_NODE]()).
func (s *ScrollView) LayoutNode() LayoutNode {
	return &ScrollLayoutNode{Type: "scroll", Component: s.child, State: s}
}
