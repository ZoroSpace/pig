// tui/tui.go (pi: packages/tui/src/tui.ts) — minimal TUI implementation with
// differential rendering: Component model, overlay stack, focus handling,
// render scheduling, and terminal query helpers.
//
// Porting notes (declared deviations):
//   - Concurrency: pi serializes everything on the JS event loop. pig uses a
//     single TUI mutex; TUI methods are executed through tui.lock(), which
//     runs inline when already dispatching on the same goroutine — this
//     reproduces the event-loop reentrancy (component input handlers may call
//     back into TUI methods) without deadlocks.
//   - process.nextTick → goroutine; the 16ms render throttle uses
//     time.AfterFunc. The JS microtask coalescing order is preserved by the
//     same renderRequested/immediateRenderScheduled flags.
//   - Component optional members (handleInput/wantsKeyRelease/focused) →
//     capability interfaces InputHandler / KeyReleaseOptIn / Focusable.
//   - The `Key` struct of overlay options unions `number | "50%"` → any
//     (int or percent string), type-switched in parseSizeValue.
//   - queryTerminalBackgroundColor/queryTerminalColorScheme return Promises →
//     blocking calls resolving on reply or timeout (channel + timer).
//   - ViewportTUI is the marker interface for viewport-scrolling TUIs (pi:
//     VIEWPORT_TUI symbol); TuiAltScreen implements it (T4); the RPC client
//     consumes it (Phase 5, deferred).
//   - containsComponent traverses children through the childContainer
//     interface (Container embedders expose it via method promotion); pi
//     duck-types on the .children property, which covers the same set of
//     components in practice.
//   - terminal-image detection is a Phase 8 stub (see terminal_image.go);
//     queryCellSize is a no-op until image capabilities exist.
package tui

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ZoroSpace/pig/utils"
)

// minRenderInterval is the minimum interval between throttled renders
// (pi: TuiBase.MIN_RENDER_INTERVAL_MS).
const minRenderInterval = 16 * time.Millisecond

// =============================================================================
// Mouse events
// =============================================================================

// TuiMouseEventType is the normalized mouse event kind (pi:
// TuiMouseEventType).
type TuiMouseEventType = string

const (
	MouseEventTypePress   TuiMouseEventType = "press"
	MouseEventTypeRelease TuiMouseEventType = "release"
	MouseEventTypeMove    TuiMouseEventType = "move"
	MouseEventTypeDrag    TuiMouseEventType = "drag"
	MouseEventTypeClick   TuiMouseEventType = "click"
	MouseEventTypeWheel   TuiMouseEventType = "wheel"
)

// TuiMouseButton is the normalized mouse button (pi: TuiMouseButton).
type TuiMouseButton = string

const (
	MouseButtonLeft   TuiMouseButton = "left"
	MouseButtonMiddle TuiMouseButton = "middle"
	MouseButtonRight  TuiMouseButton = "right"
	MouseButtonNone   TuiMouseButton = "none"
)

// TuiMouseEvent is a normalized cell-based mouse event. Coordinates are
// zero-based (pi: TuiMouseEvent).
type TuiMouseEvent struct {
	Type   TuiMouseEventType
	Button TuiMouseButton
	// Coordinates local to the receiving component.
	X int
	Y int
	// Absolute terminal coordinates.
	ScreenX int
	ScreenY int
	// Current component bounds.
	Width  int
	Height int
	Shift  bool
	Alt    bool
	Ctrl   bool
	// Logical lines. Negative values scroll up. (pi: wheelDelta?)
	WheelDelta *int
	// Consecutive click count when type is click. (pi: clickCount?)
	ClickCount *int
}

// TuiMouseEventResult is the result returned by a component's mouse handler
// (pi: TuiMouseEventResult). The dispatch-result members (HasTarget/Target/
// FocusTarget) are only set by DispatchMouseEvent; handlers themselves return
// the plain result and DispatchMouseEvent folds it into a dispatch result.
type TuiMouseEventResult struct {
	// Handled stops propagation and suppresses renderer-level fallback
	// behavior. (pi: handled?)
	Handled bool
	// Capture routes subsequent drag/release events to this component.
	// Implies handled. (pi: capture?)
	Capture bool
	// Focus gives keyboard focus to this component. Implies handled.
	// (pi: focus?)
	Focus bool
	// Render explicitly requests or suppresses a render. Move and release
	// default to false; press, click, drag, and wheel default to true.
	// Nil = unset (pi: render? → *bool).
	Render *bool

	// --- Dispatch result members (pi: TuiMouseDispatchResult) ---

	// HasTarget mirrors pi's `"target" in result` discrimination.
	HasTarget bool
	// Target is the exact dispatch target retained for later
	// retargeting (pi: TuiMouseDispatchResult.target).
	Target *TuiMouseDispatchTarget
	// FocusTarget is the keyboard focus target, which may be a delegating
	// parent container (pi: TuiMouseDispatchResult.focusTarget?).
	FocusTarget Component
}

// TuiMouseDispatchTarget is internal target metadata used by containers and
// alternate-screen dispatch (pi: TuiMouseDispatchTarget).
type TuiMouseDispatchTarget struct {
	Component Component
	OriginX   int
	OriginY   int
	Width     int
	Height    int
}

// DispatchMouseEvent dispatches an event to a component and retains the exact
// target and coordinate transform. Containers use this when forwarding events
// to nested children (pi: dispatchMouseEvent).
func DispatchMouseEvent(component Component, event *TuiMouseEvent) *TuiMouseEventResult {
	handler, ok := component.(MouseHandler)
	if !ok {
		return nil
	}
	result := handler.HandleMouse(event)
	if result == nil {
		return nil
	}
	if result.HasTarget {
		return result
	}
	if !result.Handled && !result.Capture && !result.Focus {
		return nil
	}
	out := *result
	out.Handled = true
	if result.Focus {
		out.FocusTarget = component
	}
	out.Target = &TuiMouseDispatchTarget{
		Component: component,
		OriginX:   event.ScreenX - event.X,
		OriginY:   event.ScreenY - event.Y,
		Width:     event.Width,
		Height:    event.Height,
	}
	out.HasTarget = true
	return &out
}

// RetargetMouseEvent recreates local coordinates for a previously dispatched
// mouse target (pi: retargetMouseEvent).
func RetargetMouseEvent(event *TuiMouseEvent, target *TuiMouseDispatchTarget) *TuiMouseEvent {
	out := *event
	out.X = event.ScreenX - target.OriginX
	out.Y = event.ScreenY - target.OriginY
	out.Width = target.Width
	out.Height = target.Height
	return &out
}

// =============================================================================
// Component model
// =============================================================================

// Component is the interface all TUI components must implement.
type Component interface {
	// Render the component to lines for the given viewport width.
	Render(width int) []string

	// Invalidate any cached rendering state. Called when theme changes or
	// when the component needs to re-render from scratch.
	Invalidate()
}

// InputHandler is the optional capability for components that receive
// keyboard input when they have focus (pi: handleInput?).
type InputHandler interface {
	HandleInput(data string)
}

// MouseHandler is the optional capability for components that receive
// normalized mouse events (pi: handleMouse?).
type MouseHandler interface {
	Component
	HandleMouse(event *TuiMouseEvent) *TuiMouseEventResult
}

// KeyReleaseOptIn is the optional capability marking components that want to
// receive key release events (Kitty protocol). Default is filtered out
// (pi: wantsKeyRelease?: boolean).
type KeyReleaseOptIn interface {
	WantsKeyRelease() bool
}

// Focusable is implemented by components that can receive focus and display
// a hardware cursor. When focused, the component should emit CURSOR_MARKER at
// the cursor position in its render output. TUI finds this marker and
// positions the hardware cursor there for proper IME candidate window
// positioning (pi: Focusable interface + focused property).
type Focusable interface {
	Component
	// GetFocused reports the current focus state (set by TUI on focus change).
	GetFocused() bool
	// SetFocused is called by the TUI when focus changes.
	SetFocused(focused bool)
}

// AsFocusable returns the component as Focusable, or nil (pi: isFocusable).
func AsFocusable(component Component) Focusable {
	if f, ok := component.(Focusable); ok {
		return f
	}
	return nil
}

// CursorMarker is a zero-width APC escape sequence that terminals ignore.
// Components emit this at the cursor position when focused. TUI finds and
// strips this marker, then positions the hardware cursor there.
// (pi: CURSOR_MARKER)
const CursorMarker = "\x1b_pi:c\x07"

// OverlayAnchor is the anchor position for overlays.
type OverlayAnchor = string

const (
	OverlayAnchorCenter       OverlayAnchor = "center"
	OverlayAnchorTopLeft      OverlayAnchor = "top-left"
	OverlayAnchorTopRight     OverlayAnchor = "top-right"
	OverlayAnchorBottomLeft   OverlayAnchor = "bottom-left"
	OverlayAnchorBottomRight  OverlayAnchor = "bottom-right"
	OverlayAnchorTopCenter    OverlayAnchor = "top-center"
	OverlayAnchorBottomCenter OverlayAnchor = "bottom-center"
	OverlayAnchorLeftCenter   OverlayAnchor = "left-center"
	OverlayAnchorRightCenter  OverlayAnchor = "right-center"
)

// OverlayMargin is margin configuration for overlays. Nil members mean unset.
type OverlayMargin struct {
	Top    *int
	Right  *int
	Bottom *int
	Left   *int
}

// OverlayOptions configures overlay positioning and sizing. Size fields
// (Width/MaxHeight/Row/Col) accept an absolute int or a percentage string
// like "50%" (pi: SizeValue = number | `${number}%`).
type OverlayOptions struct {
	// === Sizing ===
	// Width in columns, or percentage of terminal width (e.g., "50%").
	Width any
	// Minimum width in columns.
	MinWidth int
	// Maximum height in rows, or percentage of terminal height.
	MaxHeight any

	// === Positioning - anchor-based ===
	// Anchor point for positioning (default: center).
	Anchor OverlayAnchor
	// Horizontal offset from anchor position (positive = right).
	OffsetX int
	// Vertical offset from anchor position (positive = down).
	OffsetY int

	// === Positioning - percentage or absolute ===
	// Row position: absolute number, or percentage (e.g., "25%" = 25% from top).
	Row any
	// Column position: absolute number, or percentage (e.g., "50%" = centered).
	Col any

	// === Margin from terminal edges ===
	// Margin from terminal edges: int applies to all sides, or *OverlayMargin.
	Margin any

	// === Visibility ===
	// Visible controls overlay visibility based on terminal dimensions.
	// If provided, the overlay is only rendered when it returns true.
	// Called each render cycle with current terminal dimensions.
	Visible func(termWidth int, termHeight int) bool
	// NonCapturing: if true, don't capture keyboard focus when shown.
	NonCapturing bool
}

// OverlayUnfocusOptions carries the explicit target for OverlayHandle.Unfocus.
type OverlayUnfocusOptions struct {
	// Target is the component to focus after releasing this overlay.
	Target Component
}

// OverlayBounds is the last rendered terminal-relative overlay rectangle
// (pi: OverlayBounds).
type OverlayBounds struct {
	Row    int
	Col    int
	Width  int
	Height int
}

// OverlayHandle is returned by ShowOverlay for controlling the overlay.
type OverlayHandle interface {
	// Hide permanently removes the overlay (cannot be shown again).
	Hide()
	// SetHidden temporarily hides or shows the overlay.
	SetHidden(hidden bool)
	// IsHidden reports whether the overlay is temporarily hidden.
	IsHidden() bool
	// Focus focuses this overlay and brings it to the visual front.
	Focus()
	// Unfocus releases focus to the next visible capturing overlay or
	// previous target, or to an explicit target when provided.
	Unfocus(options *OverlayUnfocusOptions)
	// IsFocused reports whether this overlay currently has focus.
	IsFocused() bool
	// GetBounds returns the most recent rendered bounds for a visible
	// overlay (pi: getBounds). Nil when not currently rendered/visible.
	GetBounds() *OverlayBounds
}

// TuiInputListenerResult is the result of an input listener
// (pi: { consume?: boolean; data?: string } | undefined). Nil means undefined.
type TuiInputListenerResult struct {
	Consume bool
	// Data replaces the input being forwarded. Nil = leave unchanged.
	Data *string
}

// TuiInputListener intercepts input before it reaches the focused component.
type TuiInputListener func(data string) *TuiInputListenerResult

// TuiMode selects the rendering mode.
type TuiMode = string

// ViewportTUI marks TUI implementations with a scrollable,
// application-owned viewport (pi: VIEWPORT_TUI symbol + ViewportTUI
// interface). TuiAltScreen implements it.
type ViewportTUI interface {
	isViewportTUI()
}

const (
	TuiModeRegular    TuiMode = "regular"
	TuiModeFullscreen TuiMode = "fullscreen"
)

// TuiStopOptions configure TUI stop behavior.
type TuiStopOptions struct {
	// PreserveScreen leaves renderer output in place for another TUI taking
	// over the same terminal.
	PreserveScreen bool
}

// CursorPosition is a hardware cursor position extracted from rendered lines.
type CursorPosition struct {
	Row int
	Col int
}

// containerMouseChild is one child entry in the cached mouse layout.
type containerMouseChild struct {
	component Component
	height    int
}

// containerMouseLayout caches child heights for mouse hit-testing (pi:
// Container.mouseLayout).
type containerMouseLayout struct {
	width    int
	children []containerMouseChild
}

// Container is a component that contains other components.
type Container struct {
	Children []Component

	mouseLayout *containerMouseLayout
}

func (c *Container) AddChild(component Component) {
	c.Children = append(c.Children, component)
}

func (c *Container) RemoveChild(component Component) {
	for i, child := range c.Children {
		if child == component {
			c.Children = append(c.Children[:i], c.Children[i+1:]...)
			return
		}
	}
}

func (c *Container) Clear() {
	c.Children = nil
}

// childContainer is the traversal surface for containsComponent: any
// component embedding Container exposes it via method promotion.
type childContainer interface {
	containerChildren() []Component
}

func (c *Container) containerChildren() []Component { return c.Children }

func (c *Container) Invalidate() {
	for _, child := range c.Children {
		child.Invalidate()
	}
}

// HandleMouse implements MouseHandler: hit-test children by their rendered
// heights and forward the event with child-local coordinates (pi:
// Container.handleMouse).
//
// Deviation note: pi's focus override check reads `this.handleInput` on the
// outermost instance; in Go, promoted Container.HandleMouse only sees the
// *Container method set. No current component embeds Container while defining
// HandleInput; if one appears it should override HandleMouse.
func (c *Container) HandleMouse(event *TuiMouseEvent) *TuiMouseEventResult {
	if event.Y < 0 || event.Y >= event.Height {
		return nil
	}
	var mouseChildren []containerMouseChild
	if c.mouseLayout != nil && c.mouseLayout.width == event.Width {
		mouseChildren = c.mouseLayout.children
	} else {
		for _, component := range c.Children {
			mouseChildren = append(mouseChildren, containerMouseChild{component: component, height: len(component.Render(event.Width))})
		}
	}
	childY := 0
	for _, child := range mouseChildren {
		if event.Y >= childY && event.Y < childY+child.height {
			childEvent := *event
			childEvent.Y = event.Y - childY
			childEvent.Height = child.height
			result := DispatchMouseEvent(child.component, &childEvent)
			if result != nil && result.Focus {
				if _, ok := Component(c).(InputHandler); ok {
					out := *result
					out.FocusTarget = Component(c)
					return &out
				}
			}
			return result
		}
		childY += child.height
	}
	return nil
}

func (c *Container) Render(width int) []string {
	var lines []string
	var mouseChildren []containerMouseChild
	for _, child := range c.Children {
		childLines := child.Render(width)
		mouseChildren = append(mouseChildren, containerMouseChild{component: child, height: len(childLines)})
		lines = append(lines, childLines...)
	}
	c.mouseLayout = &containerMouseLayout{width: width, children: mouseChildren}
	return lines
}

// =============================================================================
// Overlay compositing
// =============================================================================

const segmentReset = "\x1b[0m\x1b]8;;\x07"

// CompositeTuiLine composites overlay content into a terminal line at a
// fixed column.
func CompositeTuiLine(baseLine string, overlayLine string, startCol int, overlayWidth int, totalWidth int) string {
	if IsImageLine(baseLine) {
		return baseLine
	}

	afterStart := startCol + overlayWidth
	base := ExtractSegments(baseLine, startCol, afterStart, totalWidth-afterStart, true)
	overlay := SliceWithWidth(overlayLine, 0, overlayWidth, true)
	beforePad := maxInt(0, startCol-base.BeforeWidth)
	overlayPad := maxInt(0, overlayWidth-overlay.Width)
	actualBeforeWidth := maxInt(startCol, base.BeforeWidth)
	actualOverlayWidth := maxInt(overlayWidth, overlay.Width)
	afterTarget := maxInt(0, totalWidth-actualBeforeWidth-actualOverlayWidth)
	afterPad := maxInt(0, afterTarget-base.AfterWidth)
	result := base.Before +
		strings.Repeat(" ", beforePad) +
		segmentReset +
		overlay.Text +
		strings.Repeat(" ", overlayPad) +
		segmentReset +
		base.After +
		strings.Repeat(" ", afterPad)

	if VisibleWidth(result) <= totalWidth {
		return result
	}
	return SliceByColumn(result, 0, totalWidth, true)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// =============================================================================
// TUI interface
// =============================================================================

// TUI is the main interface for managing terminal UI with differential
// rendering.
type TUI interface {
	Component
	Mode() TuiMode
	Terminal() Terminal
	FullRedraws() int
	AddChild(component Component)
	RemoveChild(component Component)
	Clear()
	GetShowHardwareCursor() bool
	SetShowHardwareCursor(enabled bool)
	GetClearOnShrink() bool
	SetClearOnShrink(enabled bool)
	SetFocus(component Component)
	ShowOverlay(component Component, options *OverlayOptions) OverlayHandle
	HideOverlay()
	HasOverlay() bool
	Start()
	Stop(options *TuiStopOptions)
	RenderNow(force bool)
	RequestRender(force bool)
	AddInputListener(listener TuiInputListener) func()
	RemoveInputListener(listener TuiInputListener)
	OnTerminalColorSchemeChange(listener func(scheme TerminalColorScheme)) func()
	SetTerminalColorSchemeNotifications(enabled bool)
	QueryTerminalBackgroundColor(timeoutMs int) *RgbColor
	QueryTerminalColorScheme(timeoutMs int) *TerminalColorScheme
}

// =============================================================================
// TuiBase
// =============================================================================

// tuiSubclass is the abstract-member hook surface (pi: abstract class TuiBase
// with abstract doRender/mode and protected overridables). The concrete
// subclass registers itself as `self` at construction.
type tuiSubclass interface {
	doRender() error
	resetRenderState()
	beforeTerminalStart()
	afterTerminalStart()
	beforeTerminalStop(options TuiStopOptions)
	afterTerminalStop(options TuiStopOptions)
	// getMountedRoots is pi's protected getMountedRoots: the component roots
	// that are mounted (invalidate/mount checks traverse these).
	getMountedRoots() []Component
}

type overlayStackEntry struct {
	component  Component
	options    *OverlayOptions
	preFocus   Component
	hidden     bool
	focusOrder int
	// bounds is the last rendered terminal-relative rectangle (pi:
	// OverlayStackEntry.bounds).
	bounds *OverlayBounds
}

// renderedOverlayLayout is one visible overlay's rendered geometry (pi:
// RenderedOverlayLayout).
type renderedOverlayLayout struct {
	entry  *overlayStackEntry
	row    int
	col    int
	width  int
	height int
}

const (
	overlayFocusInactive = iota
	overlayFocusEligible
	overlayFocusBlocked
)

type overlayFocusRestoreState struct {
	status int
	// eligible/blocked:
	overlay *overlayStackEntry
	// blocked:
	blockedBy Component
	// blocked resume: true = restore-overlay, false = focus-target
	resumeRestoreOverlay bool
	resumeTarget         Component
}

type pendingOsc11BackgroundQuery struct {
	// settled is CAS-guarded instead of guarded by b.mu (pig addition):
	// the timeout timer fires on its own goroutine, while a query issued
	// from inside input dispatch (component HandleInput → theme detection)
	// holds b.mu across the resolve-channel wait — the timer taking b.mu
	// there deadlocks the whole TUI (pi never blocks its event loop, so
	// this timer path is pig-only concurrency surface).
	settled atomic.Bool
	resolve chan *RgbColor
	timer   *time.Timer
}

// TuiBase is the shared TUI machinery; concrete renderers subclass it
// (pi: abstract class TuiBase).
type TuiBase struct {
	Container

	mode     TuiMode
	terminal Terminal
	self     tuiSubclass

	// OnDebug is the global callback for debug key (Shift+Ctrl+D). Called
	// before input is forwarded to the focused component.
	OnDebug func()

	mu sync.Mutex
	// lockOwner tracks the goroutine currently holding mu, for reentrant
	// lock() calls (pi: JS event loop reentrancy; see goid.go).
	lockOwner reentrantLockOwner

	focusedComponent Component
	inputListeners   []TuiInputListener

	renderRequested          bool
	immediateRenderScheduled bool
	renderTimer              *time.Timer
	lastRenderAt             time.Time

	showHardwareCursor bool
	clearOnShrink      bool
	fullRedrawCount    int
	stopped            bool

	pendingOsc11BackgroundReplies           int
	pendingOsc11BackgroundQueries           []*pendingOsc11BackgroundQuery
	terminalColorSchemeListeners            []func(scheme TerminalColorScheme)
	terminalColorSchemeNotificationsEnabled bool

	logDirectory string

	// Overlay stack for modal components rendered on top of base content
	focusOrderCounter      int
	overlayStack           []*overlayStackEntry
	renderedOverlayLayouts []renderedOverlayLayout
	overlayFocusRestore    overlayFocusRestoreState
}

func newTuiBase(terminal Terminal, mode TuiMode, self tuiSubclass, showHardwareCursor *bool, logDirectory string) *TuiBase {
	b := &TuiBase{
		mode:     mode,
		terminal: terminal,
		self:     self,
	}
	// v0.85.1: PI_HARDWARE_CURSOR / PI_CLEAR_ON_SHRINK env reads removed —
	// both default to false and are only settable via the setters.
	b.showHardwareCursor = false
	b.clearOnShrink = false
	// v0.85.1: logDirectory has no env fallback (pi: logDirectory?: string).
	// An empty logDirectory disables debug logging; crash dumps fall back to
	// the OS temp directory (pi-tui-crash.log, see tui_main_screen.go).
	b.logDirectory = logDirectory
	if showHardwareCursor != nil {
		b.showHardwareCursor = *showHardwareCursor
	}
	return b
}

// Mode returns the TUI rendering mode (pi: abstract readonly mode).
func (b *TuiBase) Mode() TuiMode { return b.mode }

// Terminal returns the underlying terminal.
func (b *TuiBase) Terminal() Terminal { return b.terminal }

// FullRedraws returns the number of full redraws performed so far.
func (b *TuiBase) FullRedraws() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.fullRedrawCount
}

func (b *TuiBase) GetShowHardwareCursor() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.showHardwareCursor
}

func (b *TuiBase) SetShowHardwareCursor(enabled bool) {
	b.lock(func() {
		if b.showHardwareCursor == enabled {
			return
		}
		b.showHardwareCursor = enabled
		if !enabled {
			b.terminal.HideCursor()
		}
		b.requestRenderLocked(false)
	})
}

func (b *TuiBase) GetClearOnShrink() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.clearOnShrink
}

// SetClearOnShrink sets whether to trigger full re-render when content
// shrinks. When true, empty rows are cleared when content shrinks. When
// false (default), empty rows remain (reduces redraws on slower terminals).
func (b *TuiBase) SetClearOnShrink(enabled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.clearOnShrink = enabled
}

func (b *TuiBase) GetFocusedComponent() Component {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.focusedComponent
}

func (b *TuiBase) SetFocus(component Component) {
	b.lock(func() {
		b.setFocusInternalLocked(component, true /* clearRestore */)
	})
}

// setFocusInternalLocked ports pi's setFocusInternal. clearRestore mirrors
// the overlayFocusRestore: "clear" policy argument.
func (b *TuiBase) setFocusInternalLocked(component Component, clearRestore bool) {
	previousFocus := b.focusedComponent
	nextFocus := component
	var previousFocusedOverlay *overlayStackEntry
	if previousFocus != nil {
		for _, entry := range b.overlayStack {
			if entry.component == previousFocus && b.isOverlayVisibleLocked(entry) {
				previousFocusedOverlay = entry
				break
			}
		}
	}
	nextFocusIsOverlay := false
	if nextFocus != nil {
		for _, entry := range b.overlayStack {
			if entry.component == nextFocus {
				nextFocusIsOverlay = true
				break
			}
		}
	}
	restoreState := b.getVisibleOverlayFocusRestoreLocked()
	if nextFocus != nil && !nextFocusIsOverlay {
		if restoreState.status == overlayFocusBlocked && restoreState.blockedBy == previousFocus {
			if restoreState.resumeRestoreOverlay || !b.isComponentMounted(restoreState.blockedBy) {
				nextFocus = b.resolveBlockedOverlayFocusResumeLocked(restoreState)
			} else {
				b.overlayFocusRestore = overlayFocusRestoreState{
					status:               overlayFocusBlocked,
					overlay:              restoreState.overlay,
					blockedBy:            nextFocus,
					resumeRestoreOverlay: false,
					resumeTarget:         restoreState.resumeTarget,
				}
			}
		} else if previousFocusedOverlay != nil &&
			restoreState.status != overlayFocusInactive &&
			restoreState.overlay == previousFocusedOverlay &&
			!b.isOverlayFocusAncestorLocked(previousFocusedOverlay, nextFocus) {
			b.overlayFocusRestore = overlayFocusRestoreState{
				status:               overlayFocusBlocked,
				overlay:              previousFocusedOverlay,
				blockedBy:            nextFocus,
				resumeRestoreOverlay: true,
			}
		}
	} else if nextFocus == nil {
		if restoreState.status == overlayFocusBlocked && restoreState.blockedBy == previousFocus {
			nextFocus = b.resolveBlockedOverlayFocusResumeLocked(restoreState)
		} else if clearRestore {
			b.clearOverlayFocusRestoreLocked()
		}
	}

	if f := AsFocusable(b.focusedComponent); f != nil {
		f.SetFocused(false)
	}

	b.focusedComponent = nextFocus

	if f := AsFocusable(nextFocus); f != nil {
		f.SetFocused(true)
	}

	var focusedOverlay *overlayStackEntry
	if nextFocus != nil {
		for _, entry := range b.overlayStack {
			if entry.component == nextFocus && b.isOverlayVisibleLocked(entry) {
				focusedOverlay = entry
				break
			}
		}
	}
	if focusedOverlay != nil {
		b.overlayFocusRestore = overlayFocusRestoreState{status: overlayFocusEligible, overlay: focusedOverlay}
	}
}

func (b *TuiBase) clearOverlayFocusRestoreLocked() {
	b.overlayFocusRestore = overlayFocusRestoreState{status: overlayFocusInactive}
}

func (b *TuiBase) clearOverlayFocusRestoreForLocked(overlay *overlayStackEntry) {
	if b.overlayFocusRestore.status != overlayFocusInactive && b.overlayFocusRestore.overlay == overlay {
		b.clearOverlayFocusRestoreLocked()
	}
}

func (b *TuiBase) resolveBlockedOverlayFocusResumeLocked(restoreState overlayFocusRestoreState) Component {
	if restoreState.resumeRestoreOverlay {
		return restoreState.overlay.component
	}
	b.clearOverlayFocusRestoreLocked()
	return restoreState.resumeTarget
}

func (b *TuiBase) getVisibleOverlayFocusRestoreLocked() overlayFocusRestoreState {
	restoreState := b.overlayFocusRestore
	if restoreState.status == overlayFocusInactive {
		return restoreState
	}
	found := false
	for _, entry := range b.overlayStack {
		if entry == restoreState.overlay {
			found = true
			break
		}
	}
	if !found || !b.isOverlayVisibleLocked(restoreState.overlay) {
		return overlayFocusRestoreState{status: overlayFocusInactive}
	}
	return restoreState
}

func (b *TuiBase) isOverlayFocusAncestorLocked(entry *overlayStackEntry, component Component) bool {
	visited := map[Component]bool{}
	current := entry.preFocus
	for current != nil && !visited[current] {
		visited[current] = true
		if current == component {
			return true
		}
		next := Component(nil)
		for _, overlay := range b.overlayStack {
			if overlay.component == current {
				next = overlay.preFocus
				break
			}
		}
		current = next
	}
	return false
}

func (b *TuiBase) retargetOverlayPreFocusLocked(removed *overlayStackEntry) {
	for _, overlay := range b.overlayStack {
		if overlay != removed && overlay.preFocus == removed.component {
			overlay.preFocus = removed.preFocus
		}
	}
}

func (b *TuiBase) isComponentMounted(component Component) bool {
	for _, child := range b.self.getMountedRoots() {
		if b.containsComponent(child, component) {
			return true
		}
	}
	return false
}

func (b *TuiBase) containsComponent(root Component, target Component) bool {
	if root == target {
		return true
	}
	cc, ok := root.(childContainer)
	if !ok {
		return false
	}
	for _, child := range cc.containerChildren() {
		if b.containsComponent(child, target) {
			return true
		}
	}
	return false
}

// ShowOverlay shows an overlay component with configurable positioning and
// sizing. Returns a handle to control the overlay's visibility.
func (b *TuiBase) ShowOverlay(component Component, options *OverlayOptions) OverlayHandle {
	handle := &overlayHandleImpl{t: b, component: component}
	b.lock(func() {
		b.focusOrderCounter++
		entry := &overlayStackEntry{
			component:  component,
			options:    options,
			preFocus:   b.focusedComponent,
			focusOrder: b.focusOrderCounter,
		}
		handle.entry = entry
		b.overlayStack = append(b.overlayStack, entry)
		// Only focus if overlay is actually visible
		if (options == nil || !options.NonCapturing) && b.isOverlayVisibleLocked(entry) {
			b.setFocusInternalLocked(component, true)
		}
		b.terminal.HideCursor()
		b.requestRenderLocked(false)
	})
	return handle
}

type overlayHandleImpl struct {
	t         *TuiBase
	entry     *overlayStackEntry
	component Component
}

func (h *overlayHandleImpl) Hide() {
	h.t.lock(func() {
		entry := h.entry
		for i, e := range h.t.overlayStack {
			if e == entry {
				h.t.clearOverlayFocusRestoreForLocked(entry)
				h.t.retargetOverlayPreFocusLocked(entry)
				h.t.overlayStack = append(h.t.overlayStack[:i], h.t.overlayStack[i+1:]...)
				// Restore focus if this overlay had focus
				if h.t.focusedComponent == h.component {
					topVisible := h.t.getTopmostVisibleOverlayLocked()
					if topVisible != nil {
						h.t.setFocusInternalLocked(topVisible.component, true)
					} else {
						h.t.setFocusInternalLocked(entry.preFocus, true)
					}
				}
				if len(h.t.overlayStack) == 0 {
					h.t.terminal.HideCursor()
				}
				h.t.requestRenderLocked(false)
				return
			}
		}
	})
}

func (h *overlayHandleImpl) SetHidden(hidden bool) {
	h.t.lock(func() {
		entry := h.entry
		options := entry.options
		if entry.hidden == hidden {
			return
		}
		entry.hidden = hidden
		// Update focus when hiding/showing
		if hidden {
			h.t.clearOverlayFocusRestoreForLocked(entry)
			// If this overlay had focus, move focus to next visible or preFocus
			if h.t.focusedComponent == h.component {
				topVisible := h.t.getTopmostVisibleOverlayLocked()
				if topVisible != nil {
					h.t.setFocusInternalLocked(topVisible.component, true)
				} else {
					h.t.setFocusInternalLocked(entry.preFocus, true)
				}
			}
		} else {
			// Restore focus to this overlay when showing (if it's actually visible)
			if (options == nil || !options.NonCapturing) && h.t.isOverlayVisibleLocked(entry) {
				h.t.focusOrderCounter++
				entry.focusOrder = h.t.focusOrderCounter
				h.t.setFocusInternalLocked(h.component, true)
			}
		}
		h.t.requestRenderLocked(false)
	})
}

func (h *overlayHandleImpl) IsHidden() bool {
	h.t.mu.Lock()
	defer h.t.mu.Unlock()
	return h.entry.hidden
}

func (h *overlayHandleImpl) Focus() {
	h.t.lock(func() {
		entry := h.entry
		found := false
		for _, e := range h.t.overlayStack {
			if e == entry {
				found = true
				break
			}
		}
		if !found || !h.t.isOverlayVisibleLocked(entry) {
			return
		}
		h.t.focusOrderCounter++
		entry.focusOrder = h.t.focusOrderCounter
		h.t.setFocusInternalLocked(h.component, true)
		h.t.requestRenderLocked(false)
	})
}

func (h *overlayHandleImpl) Unfocus(unfocusOptions *OverlayUnfocusOptions) {
	h.t.lock(func() {
		entry := h.entry
		isFocused := h.t.focusedComponent == h.component
		restoreState := h.t.overlayFocusRestore
		hasPendingRestore := restoreState.status != overlayFocusInactive && restoreState.overlay == entry
		if !isFocused && !hasPendingRestore {
			return
		}
		if restoreState.status == overlayFocusBlocked &&
			restoreState.overlay == entry &&
			h.t.focusedComponent == restoreState.blockedBy {
			if unfocusOptions != nil {
				h.t.overlayFocusRestore = overlayFocusRestoreState{
					status:               overlayFocusBlocked,
					overlay:              entry,
					blockedBy:            restoreState.blockedBy,
					resumeRestoreOverlay: false,
					resumeTarget:         unfocusOptions.Target,
				}
			} else {
				h.t.clearOverlayFocusRestoreLocked()
			}
			h.t.requestRenderLocked(false)
			return
		}
		h.t.clearOverlayFocusRestoreForLocked(entry)
		if isFocused || unfocusOptions != nil {
			topVisible := h.t.getTopmostVisibleOverlayLocked()
			var fallbackTarget Component
			if topVisible != nil && topVisible != entry {
				fallbackTarget = topVisible.component
			} else {
				fallbackTarget = entry.preFocus
			}
			if unfocusOptions != nil {
				h.t.setFocusInternalLocked(unfocusOptions.Target, true)
			} else {
				h.t.setFocusInternalLocked(fallbackTarget, true)
			}
		}
		h.t.requestRenderLocked(false)
	})
}

func (h *overlayHandleImpl) IsFocused() bool {
	h.t.mu.Lock()
	defer h.t.mu.Unlock()
	return h.t.focusedComponent == h.component
}

// GetBounds returns the most recent rendered bounds for a visible overlay
// (pi: OverlayHandle.getBounds).
func (h *overlayHandleImpl) GetBounds() *OverlayBounds {
	h.t.mu.Lock()
	defer h.t.mu.Unlock()
	found := false
	for _, e := range h.t.overlayStack {
		if e == h.entry {
			found = true
			break
		}
	}
	if !found || !h.t.isOverlayVisibleLocked(h.entry) || h.entry.bounds == nil {
		return nil
	}
	out := *h.entry.bounds
	return &out
}

// HideOverlay hides the topmost overlay and restores previous focus.
func (b *TuiBase) HideOverlay() {
	b.lock(func() {
		if len(b.overlayStack) == 0 {
			return
		}
		overlay := b.overlayStack[len(b.overlayStack)-1]
		b.clearOverlayFocusRestoreForLocked(overlay)
		b.retargetOverlayPreFocusLocked(overlay)
		b.overlayStack = b.overlayStack[:len(b.overlayStack)-1]
		if b.focusedComponent == overlay.component {
			// Find topmost visible overlay, or fall back to preFocus
			topVisible := b.getTopmostVisibleOverlayLocked()
			if topVisible != nil {
				b.setFocusInternalLocked(topVisible.component, true)
			} else {
				b.setFocusInternalLocked(overlay.preFocus, true)
			}
		}
		if len(b.overlayStack) == 0 {
			b.terminal.HideCursor()
		}
		b.requestRenderLocked(false)
	})
}

// hasOverlayLocked is the lock-held form of HasOverlay.
func (b *TuiBase) hasOverlayLocked() bool {
	for _, o := range b.overlayStack {
		if b.isOverlayVisibleLocked(o) {
			return true
		}
	}
	return false
}

// HasOverlay checks if there are any visible overlays.
func (b *TuiBase) HasOverlay() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hasOverlayLocked()
}

// HasOverlayEntries reports whether any overlay entries exist (visible or not).
func (b *TuiBase) HasOverlayEntries() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.overlayStack) > 0
}

// isOverlayFocused checks if the focused component is a visible overlay.
func (b *TuiBase) isOverlayFocused() bool {
	for _, entry := range b.overlayStack {
		if entry.component == b.focusedComponent && b.isOverlayVisibleLocked(entry) {
			return true
		}
	}
	return false
}

// resolveMouseFocusTarget keeps overlay containers as keyboard focus owners
// when a nested control is clicked (pi: TuiBase.resolveMouseFocusTarget).
// Called with b.mu held (input dispatch path).
func (b *TuiBase) resolveMouseFocusTargetLocked(component Component) Component {
	for index := len(b.overlayStack) - 1; index >= 0; index-- {
		overlay := b.overlayStack[index]
		if b.isOverlayVisibleLocked(overlay) && b.containsComponent(overlay.component, component) {
			return overlay.component
		}
	}
	return component
}

// dispatchMouseToOverlayLocked dispatches to the visually topmost overlay
// under the pointer (pi: TuiBase.dispatchMouseToOverlay). Called with b.mu
// held (input dispatch path).
func (b *TuiBase) dispatchMouseToOverlayLocked(event *TuiMouseEvent) (hit bool, result *TuiMouseEventResult) {
	for index := len(b.renderedOverlayLayouts) - 1; index >= 0; index-- {
		layout := b.renderedOverlayLayouts[index]
		if event.ScreenX < layout.col || event.ScreenX >= layout.col+layout.width ||
			event.ScreenY < layout.row || event.ScreenY >= layout.row+layout.height {
			continue
		}
		layoutEvent := *event
		layoutEvent.X = event.ScreenX - layout.col
		layoutEvent.Y = event.ScreenY - layout.row
		layoutEvent.Width = layout.width
		layoutEvent.Height = layout.height
		result := DispatchMouseEvent(layout.entry.component, &layoutEvent)
		if result == nil {
			return true, nil
		}
		if result.Focus {
			out := *result
			out.FocusTarget = layout.entry.component
			result = &out
		}
		return true, result
	}
	return false, nil
}

// isOverlayVisibleLocked checks if an overlay entry is currently visible.
func (b *TuiBase) isOverlayVisibleLocked(entry *overlayStackEntry) bool {
	if entry.hidden {
		return false
	}
	if entry.options != nil && entry.options.Visible != nil {
		return entry.options.Visible(b.terminal.Columns(), b.terminal.Rows())
	}
	return true
}

// getTopmostVisibleOverlayLocked finds the visual-frontmost visible capturing
// overlay, if any.
func (b *TuiBase) getTopmostVisibleOverlayLocked() *overlayStackEntry {
	var topmost *overlayStackEntry
	for _, overlay := range b.overlayStack {
		if (overlay.options != nil && overlay.options.NonCapturing) || !b.isOverlayVisibleLocked(overlay) {
			continue
		}
		if topmost == nil || overlay.focusOrder > topmost.focusOrder {
			topmost = overlay
		}
	}
	return topmost
}

// Invalidate overrides Container.Invalidate to also invalidate overlays.
// Uses the reentrant b.lock: it is invoked both externally and from within
// the dispatch (e.g. beforeTerminalStart under Start's lock).
func (b *TuiBase) Invalidate() {
	b.lock(func() {
		for _, root := range b.self.getMountedRoots() {
			root.Invalidate()
		}
		for _, overlay := range b.overlayStack {
			overlay.component.Invalidate()
		}
	})
}

// Start begins the terminal session.
func (b *TuiBase) Start() {
	b.lock(func() {
		b.stopped = false
		b.self.beforeTerminalStart()
		b.terminal.Start(
			func(data string) { b.handleTerminalInput(data) },
			func() { b.RequestRender(false) },
		)
		b.self.afterTerminalStart()
		b.terminal.HideCursor()
		// terminalColorSchemeNotificationsEnabled is read under the dispatch
		// lock already held by b.lock() — no re-lock.
		if b.terminalColorSchemeNotificationsEnabled {
			b.terminal.Write("\x1b[?2031h")
		}
		b.queryCellSizeLocked()
		b.requestRenderLocked(false)
	})
}

// AddInputListener registers an input interceptor; returns the unsubscribe fn.
func (b *TuiBase) AddInputListener(listener TuiInputListener) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inputListeners = append(b.inputListeners, listener)
	return func() { b.RemoveInputListener(listener) }
}

// RemoveInputListener unregisters a previously added listener.
func (b *TuiBase) RemoveInputListener(listener TuiInputListener) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, l := range b.inputListeners {
		// Listeners are function values; compare by identity via pointer.
		if utils.SameFuncValue(l, listener) {
			b.inputListeners = append(b.inputListeners[:i], b.inputListeners[i+1:]...)
			return
		}
	}
}

func (b *TuiBase) OnTerminalColorSchemeChange(listener func(scheme TerminalColorScheme)) func() {
	// b.lock (reentrant), not b.mu.Lock (pig addition): QueryTerminal-
	// ColorScheme registers its settle listener from inside input dispatch
	// when theme detection runs in a key handler (b.mu already held by the
	// dispatching goroutine) — a direct Lock() there self-deadlocks the
	// whole TUI.
	b.lock(func() {
		b.terminalColorSchemeListeners = append(b.terminalColorSchemeListeners, listener)
	})
	return func() { b.removeTerminalColorSchemeListener(listener) }
}

func (b *TuiBase) removeTerminalColorSchemeListener(listener func(scheme TerminalColorScheme)) {
	// b.lock (reentrant): the unsubscribe closure runs right after the
	// query resolves — possibly still inside input dispatch (same reason
	// as OnTerminalColorSchemeChange).
	b.lock(func() {
		for i, l := range b.terminalColorSchemeListeners {
			if utils.SameFuncValue(l, listener) {
				b.terminalColorSchemeListeners = append(b.terminalColorSchemeListeners[:i], b.terminalColorSchemeListeners[i+1:]...)
				return
			}
		}
	})
}

func (b *TuiBase) SetTerminalColorSchemeNotifications(enabled bool) {
	b.lock(func() {
		if b.terminalColorSchemeNotificationsEnabled == enabled {
			return
		}
		b.terminalColorSchemeNotificationsEnabled = enabled
		if !b.stopped {
			if enabled {
				b.terminal.Write("\x1b[?2031h")
			} else {
				b.terminal.Write("\x1b[?2031l")
			}
		}
	})
}

func (b *TuiBase) queryCellSizeLocked() {
	// Only query if terminal supports images (cell size is only used for
	// image rendering)
	if !capabilitiesHaveImages() {
		return
	}
	// Query terminal for cell size in pixels: CSI 16 t
	// Response format: CSI 6 ; height ; width t
	b.terminal.Write("\x1b[16t")
}

// Stop ends the terminal session and restores terminal state.
func (b *TuiBase) Stop(options *TuiStopOptions) {
	b.lock(func() {
		b.stopped = true
		b.cancelRenderTimerLocked()
		if b.terminalColorSchemeNotificationsEnabled {
			b.terminal.Write("\x1b[?2031l")
		}
		if options == nil {
			options = &TuiStopOptions{}
		}
		b.self.beforeTerminalStop(*options)
		b.terminal.ShowCursor()
		b.terminal.Stop()
		b.self.afterTerminalStop(*options)
	})
}

// RenderNow renders immediately, bypassing the throttle.
func (b *TuiBase) RenderNow(force bool) {
	b.lock(func() {
		if force {
			b.self.resetRenderState()
		}
		b.renderRequested = false
		b.cancelRenderTimerLocked()
		b.lastRenderAt = time.Now()
		b.doRenderOrPanic()
	})
}

// RequestRender schedules a render (throttled), or renders immediately when
// force is set.
func (b *TuiBase) RequestRender(force bool) {
	b.lock(func() {
		b.requestRenderLocked(force)
	})
}

func (b *TuiBase) requestRenderLocked(force bool) {
	if force {
		b.self.resetRenderState()
		b.requestImmediateRenderLocked()
		return
	}
	if b.renderRequested {
		return
	}
	b.renderRequested = true
	go b.scheduleRender()
}

func (b *TuiBase) requestImmediateRenderLocked() {
	b.cancelRenderTimerLocked()
	b.renderRequested = true
	if b.immediateRenderScheduled {
		return
	}
	b.immediateRenderScheduled = true
	go func() {
		// pig panic containment: a render panic is logged and contained;
		// the next RequestRender retries. See docs/feat/PANIC-RESILIENCE.md.
		defer utils.RecoverPanic("tui.immediateRender")
		b.mu.Lock()
		b.immediateRenderScheduled = false
		if b.stopped || !b.renderRequested {
			b.mu.Unlock()
			return
		}
		// A previously queued scheduleRender() can create a timer before this
		// callback runs. User input must preempt that throttled frame.
		b.cancelRenderTimerLocked()
		b.renderRequested = false
		b.lastRenderAt = time.Now()
		b.mu.Unlock()
		b.doRenderOrPanic()
	}()
}

func (b *TuiBase) cancelRenderTimerLocked() {
	if b.renderTimer == nil {
		return
	}
	b.renderTimer.Stop()
	b.renderTimer = nil
}

func (b *TuiBase) scheduleRender() {
	b.mu.Lock()
	if b.stopped || b.renderTimer != nil || !b.renderRequested {
		b.mu.Unlock()
		return
	}
	delay := minRenderInterval - time.Since(b.lastRenderAt)
	if delay < 0 {
		delay = 0
	}
	b.renderTimer = time.AfterFunc(delay, func() {
		// pig panic containment: same contract as the immediate-render
		// goroutine above.
		defer utils.RecoverPanic("tui.renderTimer")
		b.mu.Lock()
		b.renderTimer = nil
		if b.stopped || !b.renderRequested {
			b.mu.Unlock()
			return
		}
		b.renderRequested = false
		b.lastRenderAt = time.Now()
		b.mu.Unlock()
		b.doRenderOrPanic()
		b.mu.Lock()
		again := b.renderRequested
		b.mu.Unlock()
		if again {
			b.scheduleRender()
		}
	})
	b.mu.Unlock()
}

// doRenderOrPanic invokes the subclass renderer. pi's render errors throw
// inside a timer callback and crash the process; a panic in the render
// goroutine is the equivalent.
func (b *TuiBase) doRenderOrPanic() {
	b.lock(func() {
		if err := b.self.doRender(); err != nil {
			panic(err)
		}
	})
}

// lock runs fn under the TUI mutex. When the calling goroutine already holds
// the mutex (inside a dispatch), fn runs inline — reproducing JS event-loop
// reentrancy. Re-entrancy is detected by goroutine ID (goid.go): a plain
// in-dispatch flag cannot distinguish same-goroutine re-entry from a
// concurrent call on another goroutine (e.g. the editor autocomplete
// continuation calling RequestRender while dispatch is in progress), which
// would run the callback unlocked.
// The mutex release is panic-safe (pig panic containment): a panicking fn
// must not brick the renderer mutex — the panic keeps unwinding (the
// spawn-site RecoverPanic guard logs and contains it), but lock state is
// restored here. pi's single-threaded event loop has no equivalent failure
// mode. See docs/feat/PANIC-RESILIENCE.md.
// RunExclusive runs fn while holding the renderer lock (pig addition: the
// serializer for cross-goroutine component-tree mutations — pi performs
// them on the single event loop; pig serializes against the render/input
// pipeline instead). Re-entrant on the calling goroutine via lock.
func (b *TuiBase) RunExclusive(fn func()) { b.lock(fn) }

func (b *TuiBase) lock(fn func()) {
	if b.lockOwner.isCurrent() {
		fn()
		return
	}
	b.mu.Lock()
	b.lockOwner.acquire()
	defer func() {
		b.lockOwner.release()
		b.mu.Unlock()
	}()
	fn()
}

// =============================================================================
// Input handling
// =============================================================================

func (b *TuiBase) handleTerminalInput(data string) {
	b.lock(func() { b.handleTerminalInputLocked(data) })
}

func (b *TuiBase) handleTerminalInputLocked(data string) {
	if b.consumeOsc11BackgroundResponseLocked(data) {
		return
	}
	if b.consumeTerminalColorSchemeReportLocked(data) {
		return
	}

	if len(b.inputListeners) > 0 {
		current := data
		for _, listener := range b.inputListeners {
			result := listener(current)
			if result != nil && result.Consume {
				return
			}
			if result != nil && result.Data != nil {
				current = *result.Data
			}
		}
		if len(current) == 0 {
			return
		}
		data = current
	}

	// Consume terminal cell size responses without blocking unrelated input.
	if b.consumeCellSizeResponseLocked(data) {
		return
	}

	// Global debug key handler (Shift+Ctrl+D)
	if MatchesKey(data, "shift+ctrl+d") && b.OnDebug != nil {
		b.OnDebug()
		return
	}

	// If focused component is an overlay, verify it's still visible
	// (visibility can change due to terminal resize or visible() callback)
	var focusedOverlay *overlayStackEntry
	for _, o := range b.overlayStack {
		if o.component == b.focusedComponent {
			focusedOverlay = o
			break
		}
	}
	if focusedOverlay != nil && !b.isOverlayVisibleLocked(focusedOverlay) {
		// Focused overlay is no longer visible, redirect to topmost visible overlay
		topVisible := b.getTopmostVisibleOverlayLocked()
		if topVisible != nil {
			b.setFocusInternalLocked(topVisible.component, true)
		} else {
			b.setFocusInternalLocked(focusedOverlay.preFocus, false /* preserve */)
		}
	}

	focusIsOverlay := false
	for _, o := range b.overlayStack {
		if o.component == b.focusedComponent {
			focusIsOverlay = true
			break
		}
	}
	if !focusIsOverlay {
		restoreState := b.getVisibleOverlayFocusRestoreLocked()
		if restoreState.status == overlayFocusEligible {
			b.setFocusInternalLocked(restoreState.overlay.component, true)
		} else if restoreState.status == overlayFocusBlocked && restoreState.blockedBy != b.focusedComponent {
			if restoreState.resumeRestoreOverlay {
				b.setFocusInternalLocked(restoreState.overlay.component, true)
			} else {
				b.clearOverlayFocusRestoreLocked()
				b.setFocusInternalLocked(restoreState.resumeTarget, true)
			}
		}
	}

	// Pass input to focused component (including Ctrl+C)
	// The focused component can decide how to handle Ctrl+C
	if b.focusedComponent != nil {
		if handler, ok := b.focusedComponent.(InputHandler); ok {
			// Filter out key release events unless component opts in
			if IsKeyRelease(data) {
				optIn := false
				if kr, ok := b.focusedComponent.(KeyReleaseOptIn); ok && kr.WantsKeyRelease() {
					optIn = true
				}
				if !optIn {
					return
				}
			}
			handler.HandleInput(data)
			// Keyboard input is latency-sensitive. Avoid the throttled timer path,
			// where even setTimeout(0) can take a full 16 ms tick on Windows.
			b.requestImmediateRenderLocked()
		}
	}
}
func (b *TuiBase) consumeOsc11BackgroundResponseLocked(data string) bool {
	if b.pendingOsc11BackgroundReplies <= 0 {
		return false
	}

	if !IsOsc11BackgroundColorResponse(data) {
		return false
	}

	rgb := ParseOsc11BackgroundColor(data)
	b.pendingOsc11BackgroundReplies--
	if len(b.pendingOsc11BackgroundQueries) > 0 {
		query := b.pendingOsc11BackgroundQueries[0]
		b.pendingOsc11BackgroundQueries = b.pendingOsc11BackgroundQueries[1:]
		if query.settled.CompareAndSwap(false, true) {
			if query.timer != nil {
				query.timer.Stop()
				query.timer = nil
			}
			query.resolve <- rgb
		}
	}
	return true
}

func (b *TuiBase) consumeTerminalColorSchemeReportLocked(data string) bool {
	scheme, ok := ParseTerminalColorSchemeReport(data)
	if !ok {
		return false
	}

	for _, listener := range b.terminalColorSchemeListeners {
		listener(scheme)
	}
	return true
}

var cellSizeResponseRegex = regexp.MustCompile(`^\x1b\[6;(\d+);(\d+)t$`)

func (b *TuiBase) consumeCellSizeResponseLocked(data string) bool {
	// Response format: ESC [ 6 ; height ; width t
	m := cellSizeResponseRegex.FindStringSubmatch(data)
	if m == nil {
		return false
	}

	heightPx, _ := strconv.Atoi(m[1])
	widthPx, _ := strconv.Atoi(m[2])
	if heightPx <= 0 || widthPx <= 0 {
		return true
	}

	SetCellDimensions(CellDimensions{WidthPx: widthPx, HeightPx: heightPx})
	// Invalidate all components so images re-render with correct dimensions.
	for _, root := range b.Children {
		root.Invalidate()
	}
	for _, overlay := range b.overlayStack {
		overlay.component.Invalidate()
	}
	b.requestRenderLocked(false)
	return true
}

// =============================================================================
// Terminal queries
// =============================================================================

// QueryTerminalBackgroundColor queries the terminal's default background
// color with OSC 11 and blocks until the reply or timeoutMs elapses.
// Returns nil on timeout or unparsable reply (pi: Promise → undefined).
// Dispatch-safe (pig addition): when called from inside input dispatch
// (b.mu held by the dispatching goroutine, e.g. a key handler changing the
// theme), it still returns after timeoutMs — the timeout settle path uses
// CAS and never takes b.mu. The reply that arrives later is consumed by a
// subsequent dispatch (a no-op once the query has settled).
func (b *TuiBase) QueryTerminalBackgroundColor(timeoutMs int) *RgbColor {
	query := &pendingOsc11BackgroundQuery{resolve: make(chan *RgbColor, 1)}
	b.lock(func() {
		query.timer = time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() {
			// Must not take b.mu: the querying goroutine may be the input
			// dispatch itself, holding b.mu across the resolve-channel wait.
			// CAS-only settle keeps the timeout path deadlock-free.
			if !query.settled.CompareAndSwap(false, true) {
				return
			}
			query.resolve <- nil
		})
		b.pendingOsc11BackgroundQueries = append(b.pendingOsc11BackgroundQueries, query)
		b.pendingOsc11BackgroundReplies++
		b.terminal.Write("\x1b]11;?\x07")
	})
	return <-query.resolve
}

// QueryTerminalColorScheme queries the terminal's color-scheme preference
// with DSR (CSI ? 996 n) and blocks until the reply or timeoutMs elapses.
// Terminals that support the color palette notification protocol reply with
// CSI ? 997 ; 1 n for dark or CSI ? 997 ; 2 n for light.
// Returns nil on timeout (pi: Promise → undefined).
func (b *TuiBase) QueryTerminalColorScheme(timeoutMs int) *TerminalColorScheme {
	settled := make(chan TerminalColorScheme, 1)
	var once sync.Once
	settle := func(scheme TerminalColorScheme) {
		once.Do(func() { settled <- scheme })
	}
	unsubscribe := b.OnTerminalColorSchemeChange(settle)
	time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() { settle("") })
	b.terminal.Write("\x1b[?996n")
	scheme := <-settled
	unsubscribe()
	if scheme == "" {
		return nil
	}
	return &scheme
}

// =============================================================================
// Overlay layout resolution
// =============================================================================

var percentSizeRegex = regexp.MustCompile(`^(\d+(?:\.\d+)?)%$`)

// parseSizeValue parses a SizeValue (int or percent string) into an absolute
// value given a reference size. ok=false when unset or invalid.
func parseSizeValue(value any, referenceSize int) (int, bool) {
	if value == nil {
		return 0, false
	}
	switch v := value.(type) {
	case int:
		return v, true
	case string:
		// Parse percentage string like "50%"
		if m := percentSizeRegex.FindStringSubmatch(v); m != nil {
			pct, err := strconv.ParseFloat(m[1], 64)
			if err == nil {
				return int(float64(referenceSize) * pct / 100), true
			}
		}
		return 0, false
	default:
		return 0, false
	}
}

// resolvedOverlayLayout carries the resolved overlay geometry.
type resolvedOverlayLayout struct {
	width     int
	row       int
	col       int
	maxHeight int // -1 = unset
}

func (b *TuiBase) resolveOverlayLayout(options *OverlayOptions, overlayHeight int, termWidth int, termHeight int) resolvedOverlayLayout {
	opt := options
	if opt == nil {
		opt = &OverlayOptions{}
	}

	// Parse margin (clamp to non-negative)
	marginTop, marginRight, marginBottom, marginLeft := 0, 0, 0, 0
	switch m := opt.Margin.(type) {
	case int:
		marginTop, marginRight, marginBottom, marginLeft = maxInt(0, m), maxInt(0, m), maxInt(0, m), maxInt(0, m)
	case *OverlayMargin:
		if m != nil {
			if m.Top != nil {
				marginTop = maxInt(0, *m.Top)
			}
			if m.Right != nil {
				marginRight = maxInt(0, *m.Right)
			}
			if m.Bottom != nil {
				marginBottom = maxInt(0, *m.Bottom)
			}
			if m.Left != nil {
				marginLeft = maxInt(0, *m.Left)
			}
		}
	}

	// Available space after margins
	availWidth := maxInt(1, termWidth-marginLeft-marginRight)
	availHeight := maxInt(1, termHeight-marginTop-marginBottom)

	// === Resolve width ===
	width, ok := parseSizeValue(opt.Width, termWidth)
	if !ok {
		width = minInt(80, availWidth)
	}
	// Apply minWidth
	if opt.MinWidth != 0 {
		width = maxInt(width, opt.MinWidth)
	}
	// Clamp to available space
	width = maxInt(1, minInt(width, availWidth))

	// === Resolve maxHeight ===
	maxHeight := -1
	if mh, ok := parseSizeValue(opt.MaxHeight, termHeight); ok {
		maxHeight = maxInt(1, minInt(mh, availHeight))
	}

	// Effective overlay height (may be clamped by maxHeight)
	effectiveHeight := overlayHeight
	if maxHeight != -1 {
		effectiveHeight = minInt(overlayHeight, maxHeight)
	}

	// === Resolve position ===
	var row int
	var col int

	if opt.Row != nil {
		if s, isStr := opt.Row.(string); isStr {
			// Percentage: 0% = top, 100% = bottom (overlay stays within bounds)
			if m := percentSizeRegex.FindStringSubmatch(s); m != nil {
				maxRow := maxInt(0, availHeight-effectiveHeight)
				percent, _ := strconv.ParseFloat(m[1], 64)
				row = marginTop + int(float64(maxRow)*percent/100)
			} else {
				// Invalid format, fall back to center
				row = resolveAnchorRow(OverlayAnchorCenter, effectiveHeight, availHeight, marginTop)
			}
		} else if r, isInt := opt.Row.(int); isInt {
			// Absolute row position
			row = r
		} else {
			row = resolveAnchorRow(OverlayAnchorCenter, effectiveHeight, availHeight, marginTop)
		}
	} else {
		// Anchor-based (default: center)
		anchor := opt.Anchor
		if anchor == "" {
			anchor = OverlayAnchorCenter
		}
		row = resolveAnchorRow(anchor, effectiveHeight, availHeight, marginTop)
	}

	if opt.Col != nil {
		if s, isStr := opt.Col.(string); isStr {
			// Percentage: 0% = left, 100% = right (overlay stays within bounds)
			if m := percentSizeRegex.FindStringSubmatch(s); m != nil {
				maxCol := maxInt(0, availWidth-width)
				percent, _ := strconv.ParseFloat(m[1], 64)
				col = marginLeft + int(float64(maxCol)*percent/100)
			} else {
				// Invalid format, fall back to center
				col = resolveAnchorCol(OverlayAnchorCenter, width, availWidth, marginLeft)
			}
		} else if c, isInt := opt.Col.(int); isInt {
			// Absolute column position
			col = c
		} else {
			col = resolveAnchorCol(OverlayAnchorCenter, width, availWidth, marginLeft)
		}
	} else {
		// Anchor-based (default: center)
		anchor := opt.Anchor
		if anchor == "" {
			anchor = OverlayAnchorCenter
		}
		col = resolveAnchorCol(anchor, width, availWidth, marginLeft)
	}

	// Apply offsets
	row += opt.OffsetY
	col += opt.OffsetX

	// Clamp to terminal bounds (respecting margins)
	row = maxInt(marginTop, minInt(row, termHeight-marginBottom-effectiveHeight))
	col = maxInt(marginLeft, minInt(col, termWidth-marginRight-width))

	return resolvedOverlayLayout{width: width, row: row, col: col, maxHeight: maxHeight}
}

func resolveAnchorRow(anchor OverlayAnchor, height int, availHeight int, marginTop int) int {
	switch anchor {
	case OverlayAnchorTopLeft, OverlayAnchorTopCenter, OverlayAnchorTopRight:
		return marginTop
	case OverlayAnchorBottomLeft, OverlayAnchorBottomCenter, OverlayAnchorBottomRight:
		return marginTop + availHeight - height
	default: // left-center, center, right-center
		return marginTop + (availHeight-height)/2
	}
}

func resolveAnchorCol(anchor OverlayAnchor, width int, availWidth int, marginLeft int) int {
	switch anchor {
	case OverlayAnchorTopLeft, OverlayAnchorLeftCenter, OverlayAnchorBottomLeft:
		return marginLeft
	case OverlayAnchorTopRight, OverlayAnchorRightCenter, OverlayAnchorBottomRight:
		return marginLeft + availWidth - width
	default: // top-center, center, bottom-center
		return marginLeft + (availWidth-width)/2
	}
}

type renderedOverlay struct {
	entry        *overlayStackEntry
	overlayLines []string
	row          int
	col          int
	w            int
}

// compositeOverlays composites all overlays into content lines (sorted by
// focusOrder, higher = on top).
func (b *TuiBase) compositeOverlays(lines []string, termWidth int, termHeight int) []string {
	if len(b.overlayStack) == 0 {
		b.renderedOverlayLayouts = nil
		return lines
	}
	result := make([]string, len(lines))
	copy(result, lines)

	for _, entry := range b.overlayStack {
		entry.bounds = nil
	}

	// Pre-render all visible overlays and calculate positions
	var rendered []renderedOverlay
	minLinesNeeded := len(result)

	var visibleEntries []*overlayStackEntry
	for _, e := range b.overlayStack {
		if b.isOverlayVisibleLocked(e) {
			visibleEntries = append(visibleEntries, e)
		}
	}
	// sort by focusOrder ascending (stable)
	for i := 1; i < len(visibleEntries); i++ {
		for j := i; j > 0 && visibleEntries[j].focusOrder < visibleEntries[j-1].focusOrder; j-- {
			visibleEntries[j], visibleEntries[j-1] = visibleEntries[j-1], visibleEntries[j]
		}
	}
	for _, entry := range visibleEntries {
		component := entry.component
		options := entry.options

		// Get layout with height=0 first to determine width and maxHeight
		// (width and maxHeight don't depend on overlay height)
		layout := b.resolveOverlayLayout(options, 0, termWidth, termHeight)

		// Render component at calculated width
		overlayLines := component.Render(layout.width)

		// Apply maxHeight if specified
		if layout.maxHeight != -1 && len(overlayLines) > layout.maxHeight {
			overlayLines = overlayLines[:layout.maxHeight]
		}

		// Get final row/col with actual overlay height
		finalLayout := b.resolveOverlayLayout(options, len(overlayLines), termWidth, termHeight)
		entry.bounds = &OverlayBounds{Row: finalLayout.row, Col: finalLayout.col, Width: layout.width, Height: len(overlayLines)}

		rendered = append(rendered, renderedOverlay{entry: entry, overlayLines: overlayLines, row: finalLayout.row, col: finalLayout.col, w: layout.width})
		minLinesNeeded = maxInt(minLinesNeeded, finalLayout.row+len(overlayLines))
	}
	b.renderedOverlayLayouts = b.renderedOverlayLayouts[:0]
	for _, r := range rendered {
		b.renderedOverlayLayouts = append(b.renderedOverlayLayouts, renderedOverlayLayout{
			entry: r.entry, row: r.row, col: r.col, width: r.w, height: len(r.overlayLines),
		})
	}

	// Pad to at least terminal height so overlays have screen-relative positions.
	// Excludes maxLinesRendered: the historical high-water mark caused self-reinforcing
	// inflation that pushed content into scrollback on terminal widen.
	workingHeight := maxInt(maxInt(len(result), termHeight), minLinesNeeded)

	// Extend result with empty lines if content is too short for overlay placement or working area
	for len(result) < workingHeight {
		result = append(result, "")
	}

	viewportStart := maxInt(0, workingHeight-termHeight)

	// Composite each overlay
	for _, r := range rendered {
		for i, overlayLine := range r.overlayLines {
			idx := viewportStart + r.row + i
			if idx >= 0 && idx < len(result) {
				// Defensive: truncate overlay line to declared width before compositing
				// (components should already respect width, but this ensures it)
				truncatedOverlayLine := overlayLine
				if VisibleWidth(overlayLine) > r.w {
					truncatedOverlayLine = SliceByColumn(overlayLine, 0, r.w, true)
				}
				result[idx] = b.compositeLineAt(result[idx], truncatedOverlayLine, r.col, r.w, termWidth)
			}
		}
	}

	return result
}

func (b *TuiBase) applyLineResets(lines []string) []string {
	reset := segmentReset
	for i, line := range lines {
		if !IsImageLine(line) {
			lines[i] = NormalizeTerminalOutput(line) + reset
		}
	}
	return lines
}

func (b *TuiBase) compositeLineAt(baseLine string, overlayLine string, startCol int, overlayWidth int, totalWidth int) string {
	return CompositeTuiLine(baseLine, overlayLine, startCol, overlayWidth, totalWidth)
}

// extractCursorPosition finds and extracts cursor position from rendered
// lines. Searches for CursorMarker, calculates its position, and strips it
// from the output. Only scans the bottom terminal height lines (visible
// viewport). Mutates lines in place; returns nil when no marker is found.
func (b *TuiBase) extractCursorPosition(lines []string, height int) *CursorPosition {
	// Only scan the bottom `height` lines (visible viewport)
	viewportTop := maxInt(0, len(lines)-height)
	for row := len(lines) - 1; row >= viewportTop; row-- {
		line := lines[row]
		markerIndex := strings.Index(line, CursorMarker)
		if markerIndex != -1 {
			// Calculate visual column (width of text before marker)
			beforeMarker := line[:markerIndex]
			col := VisibleWidth(beforeMarker)

			// Strip marker from the line
			lines[row] = line[:markerIndex] + line[markerIndex+len(CursorMarker):]

			return &CursorPosition{Row: row, Col: col}
		}
	}
	return nil
}

// capabilitiesHaveImages reports whether image rendering is available
// (pi: getCapabilities().images truthiness; Phase 8 detection stub).
func capabilitiesHaveImages() bool {
	caps := GetCapabilities()
	switch v := caps.Images.(type) {
	case bool:
		return v
	case string:
		return v != ""
	default:
		return false
	}
}
