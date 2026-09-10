// tui/tui_alt_screen.go (pi: packages/tui/src/tui-alt-screen.ts) —
// alternate-screen TUI with a scrollable, application-owned viewport:
// mouse wheel routing/overscroll chaining, SGR mouse selection (character/
// word/line granularity + drag auto-scroll), scrollbar hit-testing and
// dragging, OSC 8 hyperlink activation, transcript search overlay, flash
// messages, Kitty image placement reuse/eviction, and full/differential
// rendering with search + selection + flash compositing.
//
// Porting notes (declared deviations):
//   - Concurrency: pi serializes all state on the JS event loop; every input
//     listener invocation, render, and timer callback shares one thread. pig
//     guards the subclass state with the inherited TuiBase mutex: input
//     dispatch and doRender run under b.lock(); the selection auto-scroll
//     interval and flash expiry run on goroutines that re-enter b.lock()
//     (same serialization). State accessors lock; doRender does NOT re-lock
//     (TuiMainScreen locking note applies).
//   - copySelection returns Promise<boolean> → synchronous callback (repo
//     rule: pi promise callbacks → sync signatures).
//   - process.platform → altScreenPlatform package var (test seam); env
//     probes use os.Getenv.
//   - The two Image-component-driven paths (constructing Image components)
//     are exercised only by tests skipped for Phase 8; the placement/crop
//     machinery itself is fully ported.
package tui

import (
	"encoding/base64"
	"math"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	enterAltScreen                           = "\x1b[?1049h"
	exitAltScreen                            = "\x1b[?1049l"
	disableAutowrap                          = "\x1b[?7l"
	enableAutowrap                           = "\x1b[?7h"
	enableButtonMotionMouse                  = "\x1b[?1000h\x1b[?1002h\x1b[?1004h\x1b[?1006h"
	enableAllMotionMouse                     = "\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1004h\x1b[?1006h"
	disableMouse                             = "\x1b[?1006l\x1b[?1004l\x1b[?1003l\x1b[?1002l\x1b[?1000l"
	focusIn                                  = "\x1b[I"
	focusOut                                 = "\x1b[O"
	beginSyncOutput                          = "\x1b[?2026h"
	endSyncOutput                            = "\x1b[?2026l"
	pageScrollOverlap                        = 4
	maxCachedOffscreenKittyImages            = 16
	maxCachedOffscreenKittyTransmissionBytes = 32 * 1024 * 1024
	maxCachedOffscreenKittyDecodedBytes      = 64 * 1024 * 1024
	doubleClickIntervalMs                    = 500
)

var osc133PromptStartRegex = regexp.MustCompile(`^\x1b\]133;A(?:\x07|\x1b\\)`)

// altScreenPlatform mirrors process.platform (package var = test seam).
var altScreenPlatform = runtime.GOOS

type cachedKittyImage struct {
	transmissionGeneration int
	transmissionBytes      int
	estimatedDecodedBytes  int
}

type altSelectionPoint struct {
	row        int
	col        int
	scrollView *ScrollView
	// boundary marks a point between terminal cells rather than on a cell.
	boundary bool
}

type altSelectionRange struct {
	start altSelectionPoint
	end   altSelectionPoint
}

type altSelectionGranularity = string

const (
	altGranularityCharacter = "character"
	altGranularityWord      = "word"
	altGranularityLine      = "line"
)

type altClickTarget struct {
	timestamp  int64
	count      int
	row        int
	scrollView *ScrollView
	wordStart  int
	wordEnd    int
}

type altSgrMouseEvent struct {
	button  int
	x       int
	y       int
	release bool
}

type altWheelEvent struct {
	direction int // -1 | 1
	x         int
	y         int
}

type altScrollbarDrag struct {
	scrollView *ScrollView
	grabOffset int
}

type altScrollbarTarget struct {
	scrollView *ScrollView
	geometry   *ScrollbarGeometry
}

type altSearchSelectionMode = string

const (
	altSearchModeQuery    = "query"
	altSearchModeRetain   = "retain"
	altSearchModeNext     = "next"
	altSearchModePrevious = "previous"
)

type altActiveSearch struct {
	component     *AltScreenSearchComponent
	overlay       OverlayHandle
	query         string
	matches       []AltScreenSearchMatch
	selectedIndex int
	selectedKey   string
	anchorRow     int
	selectionMode altSearchSelectionMode
}

type altSearchHighlightRange struct {
	startCol int
	endCol   int
	current  bool
}

// TuiAltScreenOptions configures the alternate-screen TUI (pi:
// TuiAltScreenOptions).
type TuiAltScreenOptions struct {
	// WheelScrollLines is the number of logical lines moved for each
	// mouse-wheel event.
	WheelScrollLines int
	// Mouse captures mouse events for viewport scrolling and
	// application-owned text selection.
	Mouse *bool
	// SearchMatchStyle styles a non-current transcript search match.
	SearchMatchStyle func(text string) string
	// SearchCurrentMatchStyle styles the current transcript search match.
	SearchCurrentMatchStyle func(text string) string
	// OpenUrl opens an OSC 8 hyperlink activated with a primary-button click.
	OpenUrl func(url string)
	// OnRightClickPaste handles an unmodified secondary-button press for
	// clipboard paste. Currently enabled on Windows only.
	OnRightClickPaste func()
	// CopySelection copies selected text to the system clipboard. Return
	// true on success; the caller flashes an error otherwise. When nil, the
	// selection is copied via an OSC 52 write. (pi: Promise<boolean> → sync.)
	CopySelection func(text string) bool
}

// altImplicitDocument renders the TuiBase container children (pi:
// implicitDocument with super.render).
type altImplicitDocument struct {
	t *TuiAltScreen
}

func (d *altImplicitDocument) Render(width int) []string {
	return d.t.TuiBase.Container.Render(width)
}

func (d *altImplicitDocument) Invalidate() {
	for _, child := range d.t.Children {
		child.Invalidate()
	}
}

// TuiAltScreen is the alternate-screen renderer (pi: TuiAltScreen extends
// TuiBase implements ViewportTUI).
type TuiAltScreen struct {
	TuiBase

	previousScreen       []string
	lastDocument         []string
	previousScreenWidth  int
	previousScreenHeight int
	layoutRoot           Component
	currentLayout        *LayoutFrame
	implicitDocument     *altImplicitDocument
	implicitScrollView   *ScrollView
	flashes              *AltScreenFlashContainer
	altScreenActive      bool
	imageProtocol        any // pi: ImageProtocol = boolean | "kitty" | "iterm2" | null
	savedCapabilities    *TerminalCapabilities
	uploadedKittyImages  map[int]*cachedKittyImage
	uploadedKittyOrder   []int // insertion order (JS Map iteration)

	selectionAnchor              *altSelectionPoint
	selectionFocus               *altSelectionPoint
	selectionGranularity         altSelectionGranularity
	selectionInitialRange        *altSelectionRange
	lastClick                    *altClickTarget
	selectionDragPointer         *altSgrMouseEvent
	selectionAutoScrollDirection int // -1 | 0 | 1
	selectionAutoScrollTicker    *time.Ticker
	selectionAutoScrollStop      chan struct{}
	selectionPressActive         bool
	scrollbarDrag                *altScrollbarDrag
	scrollbarHover               *ScrollView
	activeSearch                 *altActiveSearch
	pressedUrl                   string
	selectionDragged             bool

	wheelScrollLines        int
	mouseEnabled            bool
	searchMatchStyle        func(text string) string
	searchCurrentMatchStyle func(text string) string
	openUrl                 func(url string)
	onRightClickPaste       func()
	copySelection           func(text string) bool
}

// NewTuiAltScreen creates a TuiAltScreen (pi constructor; showHardwareCursor
// nil = env default, logDirectory "" = env or ~/.pi/agent, options nil =
// defaults).
func NewTuiAltScreen(terminal Terminal, showHardwareCursor *bool, logDirectory string, options *TuiAltScreenOptions) *TuiAltScreen {
	t := &TuiAltScreen{
		selectionGranularity: altGranularityCharacter,
		uploadedKittyImages:  map[int]*cachedKittyImage{},
	}
	t.TuiBase = *newTuiBase(terminal, TuiModeFullscreen, t, showHardwareCursor, logDirectory)
	t.implicitDocument = &altImplicitDocument{t: t}
	t.implicitScrollView = NewScrollView(t.implicitDocument, &ScrollViewOptions{Follow: "end", Primary: true})
	t.flashes = NewAltScreenFlashContainer(func() { t.RequestRender(false) })
	wheelScrollLines := 1
	mouseEnabled := true
	searchMatchStyle := func(text string) string { return "\x1b[4m" + text + "\x1b[24m" }
	searchCurrentMatchStyle := func(text string) string { return "\x1b[1;7m" + text + "\x1b[22;27m" }
	if options != nil {
		wheelScrollLines = maxInt(1, options.WheelScrollLines)
		if options.Mouse != nil {
			mouseEnabled = *options.Mouse
		}
		if options.SearchMatchStyle != nil {
			searchMatchStyle = options.SearchMatchStyle
		}
		if options.SearchCurrentMatchStyle != nil {
			searchCurrentMatchStyle = options.SearchCurrentMatchStyle
		}
		t.openUrl = options.OpenUrl
		t.onRightClickPaste = options.OnRightClickPaste
		t.copySelection = options.CopySelection
	}
	t.wheelScrollLines = wheelScrollLines
	t.mouseEnabled = mouseEnabled
	t.searchMatchStyle = searchMatchStyle
	t.searchCurrentMatchStyle = searchCurrentMatchStyle
	t.AddInputListener(func(data string) *TuiInputListenerResult { return t.handleViewportInput(data) })
	return t
}

func (t *TuiAltScreen) isViewportTUI() {}

// ViewportTop returns the primary scroll view's offset (pi: viewportTop
// getter).
func (t *TuiAltScreen) ViewportTop() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.getPrimaryScrollView().ScrollTop()
}

// IsFollowingOutput reports whether the primary scroll view tracks the end
// (pi: isFollowingOutput getter).
func (t *TuiAltScreen) IsFollowingOutput() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.getPrimaryScrollView().IsFollowingEnd()
}

// SetLayoutRoot replaces the layout root component (pi: setLayoutRoot).
func (t *TuiAltScreen) SetLayoutRoot(component Component) {
	t.lock(func() {
		if t.layoutRoot == component {
			return
		}
		t.layoutRoot = component
		t.currentLayout = nil
		t.requestRenderLocked(false)
	})
}

// Render implements Component (pi: render override).
func (t *TuiAltScreen) Render(width int) []string {
	if t.layoutRoot != nil {
		return t.layoutRoot.Render(width)
	}
	return t.TuiBase.Container.Render(width)
}

func (t *TuiAltScreen) getMountedRoots() []Component {
	if t.layoutRoot != nil {
		return []Component{t.layoutRoot}
	}
	return t.Children
}

func (t *TuiAltScreen) getPrimaryScrollView() *ScrollView {
	if t.currentLayout != nil && t.currentLayout.PrimaryScrollView != nil {
		return t.currentLayout.PrimaryScrollView
	}
	return t.implicitScrollView
}

func (t *TuiAltScreen) beforeTerminalStart() {
	t.stopSelectionAutoScroll()
	t.selectionPressActive = false
	t.stopScrollbarHover()
	t.stopScrollbarDrag()
	t.flashes.Dispose()
	t.altScreenActive = true
	capabilities := GetCapabilities()
	t.imageProtocol = capabilities.Images
	for id := range t.uploadedKittyImages {
		delete(t.uploadedKittyImages, id)
	}
	t.uploadedKittyOrder = nil
	if s, ok := capabilities.Images.(string); ok && s == "iterm2" {
		caps := capabilities
		caps.Images = nil
		t.savedCapabilities = &capabilities
		SetCapabilities(caps)
		t.Invalidate()
	}
	t.lastDocument = nil
	t.selectionAnchor = nil
	t.selectionFocus = nil
	t.selectionGranularity = altGranularityCharacter
	t.selectionInitialRange = nil
	t.lastClick = nil
	t.pressedUrl = ""
	t.selectionDragged = false
	t.resetRenderState()
	term := strings.ToLower(os.Getenv("TERM"))
	// Multiplexers can lag when every pointer movement is forwarded. Button-motion
	// tracking preserves clicks, wheel events, selections, and scrollbar dragging.
	multiplexer := os.Getenv("TMUX") != "" ||
		os.Getenv("ZELLIJ") != "" ||
		os.Getenv("STY") != "" ||
		strings.HasPrefix(term, "tmux") ||
		strings.HasPrefix(term, "screen")
	mouseSequence := enableAllMotionMouse
	if multiplexer {
		mouseSequence = enableButtonMotionMouse
	}
	mousePart := ""
	if t.mouseEnabled {
		mousePart = mouseSequence
	}
	t.terminal.Write(enterAltScreen + disableAutowrap + mousePart + "\x1b[2J\x1b[H\x1b[?25l")
}

// envForAltScreen reads an environment variable (indirection kept for the
// Windows right-click-paste TERM_PROGRAM probe; plain os.Getenv otherwise).
var envForAltScreen = os.Getenv

// beforeTerminalStop writes the mouse-disable + autowrap-restore sequences
// before the terminal stops.
func (t *TuiAltScreen) beforeTerminalStop(options TuiStopOptions) {
	t.closeSearch()
	t.stopSelectionAutoScroll()
	t.selectionPressActive = false
	t.stopScrollbarHover()
	t.stopScrollbarDrag()
	t.flashes.Dispose()
	if !t.altScreenActive {
		return
	}
	mousePart := ""
	if t.mouseEnabled {
		mousePart = disableMouse
	}
	t.terminal.Write(beginSyncOutput + t.deleteKittyImages() + mousePart + enableAutowrap + endSyncOutput)
	for id := range t.uploadedKittyImages {
		delete(t.uploadedKittyImages, id)
	}
	t.uploadedKittyOrder = nil
}

func (t *TuiAltScreen) afterTerminalStart() {}

func (t *TuiAltScreen) afterTerminalStop(options TuiStopOptions) {
	if !t.altScreenActive {
		return
	}
	t.altScreenActive = false
	if options.PreserveScreen {
		t.terminal.Write(beginSyncOutput + exitAltScreen + "\x1b[?25h" + endSyncOutput)
	} else {
		width := maxInt(1, t.terminal.Columns())
		documentLines := t.Render(width)
		for i, line := range documentLines {
			documentLines[i] = osc133ZonePrefixRegex.ReplaceAllString(line, "")
		}
		reset := t.applyLineResets(documentLines)
		t.lastDocument = make([]string, len(reset))
		for i, line := range reset {
			line = strings.ReplaceAll(line, CursorMarker, "")
			if IsImageLine(line) || VisibleWidth(line) <= width {
				t.lastDocument[i] = line
			} else {
				t.lastDocument[i] = SliceByColumn(line, 0, width, true)
			}
		}
		var buffer strings.Builder
		buffer.WriteString(beginSyncOutput)
		buffer.WriteString(exitAltScreen)
		buffer.WriteString(disableAutowrap)
		for row := 0; row < len(t.lastDocument); row++ {
			if row > 0 {
				buffer.WriteString("\r\n")
			}
			buffer.WriteString("\r\x1b[2K")
			if row < len(t.lastDocument) {
				buffer.WriteString(t.lastDocument[row])
			}
		}
		buffer.WriteString("\x1b[0m")
		buffer.WriteString(enableAutowrap)
		buffer.WriteString("\r\n\x1b[?25h")
		buffer.WriteString(endSyncOutput)
		t.terminal.Write(buffer.String())
	}
	if t.savedCapabilities != nil {
		SetCapabilities(*t.savedCapabilities)
		t.savedCapabilities = nil
	}
}

func (t *TuiAltScreen) deleteKittyImages() string {
	if s, ok := t.imageProtocol.(string); ok && s == "kitty" {
		return DeleteAllKittyImages()
	}
	return ""
}

func (t *TuiAltScreen) prepareKittyScreen(screen []string) ([]string, string) {
	visibleImageIds := map[int]bool{}
	lines := make([]string, len(screen))
	for i, line := range screen {
		lines[i] = line
		placement := GetKittyImagePlacement(line)
		if placement == nil {
			continue
		}
		visibleImageIds[placement.ImageId] = true

		cachedImage, hadCached := t.uploadedKittyImages[placement.ImageId]
		nextCachedImage := &cachedKittyImage{
			transmissionGeneration: placement.TransmissionGeneration,
			transmissionBytes:      placement.TransmissionBytes,
			estimatedDecodedBytes:  placement.EstimatedDecodedBytes,
		}
		if hadCached {
			t.removeUploadedKittyImage(placement.ImageId)
		}
		t.uploadedKittyImages[placement.ImageId] = nextCachedImage
		t.uploadedKittyOrder = append(t.uploadedKittyOrder, placement.ImageId)

		if hadCached && cachedImage.transmissionGeneration == placement.TransmissionGeneration {
			lines[i] = placement.ReplacementLine
		}
	}

	cachedOffscreenImageCount := 0
	cachedOffscreenTransmissionBytes := 0
	cachedOffscreenDecodedBytes := 0
	for _, imageId := range t.uploadedKittyOrder {
		if visibleImageIds[imageId] {
			continue
		}
		cachedOffscreenImageCount++
		cachedOffscreenTransmissionBytes += t.uploadedKittyImages[imageId].transmissionBytes
		cachedOffscreenDecodedBytes += t.uploadedKittyImages[imageId].estimatedDecodedBytes
	}

	evictedImageDeletion := ""
	for i := 0; i < len(t.uploadedKittyOrder); {
		imageId := t.uploadedKittyOrder[i]
		if cachedOffscreenImageCount <= maxCachedOffscreenKittyImages &&
			cachedOffscreenTransmissionBytes <= maxCachedOffscreenKittyTransmissionBytes &&
			cachedOffscreenDecodedBytes <= maxCachedOffscreenKittyDecodedBytes {
			break
		}
		if visibleImageIds[imageId] {
			i++
			continue
		}
		evictedImageDeletion += DeleteKittyImage(imageId)
		cachedOffscreenTransmissionBytes -= t.uploadedKittyImages[imageId].transmissionBytes
		cachedOffscreenDecodedBytes -= t.uploadedKittyImages[imageId].estimatedDecodedBytes
		t.removeUploadedKittyImage(imageId)
		cachedOffscreenImageCount--
		// do not advance i: the next entry slid into this slot
	}
	return lines, evictedImageDeletion
}

func (t *TuiAltScreen) removeUploadedKittyImage(imageId int) {
	delete(t.uploadedKittyImages, imageId)
	for i, id := range t.uploadedKittyOrder {
		if id == imageId {
			t.uploadedKittyOrder = append(t.uploadedKittyOrder[:i], t.uploadedKittyOrder[i+1:]...)
			return
		}
	}
}

func (t *TuiAltScreen) resetRenderState() {
	t.previousScreen = nil
	t.previousScreenWidth = 0
	t.previousScreenHeight = 0
	t.currentLayout = nil
}

// ScrollBy scrolls the primary scroll view by lines (pi: scrollBy).
func (t *TuiAltScreen) ScrollBy(lines int) {
	t.lock(func() { t.scrollByLocked(lines) })
}

func (t *TuiAltScreen) scrollByLocked(lines int) {
	t.getPrimaryScrollView().ScrollBy(lines)
	t.requestRenderLocked(false)
}

// ScrollToTop scrolls to the start (pi: scrollToTop).
func (t *TuiAltScreen) ScrollToTop() {
	t.lock(t.scrollToTopLocked)
}

func (t *TuiAltScreen) scrollToTopLocked() {
	t.getPrimaryScrollView().ScrollToStart()
	t.requestRenderLocked(false)
}

// ScrollToEnd scrolls to the end (pi: scrollToBottom).
func (t *TuiAltScreen) ScrollToBottom() {
	t.lock(t.scrollToBottomLocked)
}

func (t *TuiAltScreen) scrollToBottomLocked() {
	t.getPrimaryScrollView().ScrollToEnd()
	t.requestRenderLocked(false)
}

// flashLocked shows a flash message with the default duration (pi:
// this.flash(message)).
func (t *TuiAltScreen) flashLocked(message string) {
	t.Flash(message, 0)
}

func (t *TuiAltScreen) scrollToPrompt(direction int) {
	if t.currentLayout == nil {
		return
	}
	scrollView := t.getPrimaryScrollView()
	box := GetScrollViewBox(t.currentLayout, scrollView)
	if box == nil || box.ScrollContentLines == nil {
		return
	}
	lines := box.ScrollContentLines

	row := scrollView.ScrollTop() + direction
	for row >= 0 && row < len(lines) {
		if osc133PromptStartRegex.MatchString(lines[row]) {
			scrollView.ScrollTo(row, nil)
			t.requestRenderLocked(false)
			return
		}
		row += direction
	}
}

func (t *TuiAltScreen) openSearch() {
	if t.activeSearch != nil {
		if t.activeSearch.overlay != nil {
			t.activeSearch.overlay.Focus()
		}
		return
	}
	component := NewAltScreenSearchComponent(func(query string) { t.updateSearchQuery(query) })
	search := &altActiveSearch{
		component:     component,
		query:         "",
		matches:       nil,
		selectedIndex: -1,
		anchorRow:     t.getPrimaryScrollView().ScrollTop(),
		selectionMode: altSearchModeQuery,
	}
	t.activeSearch = search
	search.overlay = t.ShowOverlay(component, &OverlayOptions{
		Anchor:   OverlayAnchorTopRight,
		Width:    "40%",
		MinWidth: 24,
		Margin:   1,
	})
}

func (t *TuiAltScreen) closeSearch() {
	search := t.activeSearch
	if search == nil {
		return
	}
	t.activeSearch = nil
	if search.overlay != nil {
		search.overlay.Hide()
	}
	t.requestRenderLocked(false)
}

func (t *TuiAltScreen) updateSearchQuery(query string) {
	search := t.activeSearch
	if search == nil || query == search.query {
		return
	}
	if search.selectedIndex >= 0 && search.selectedIndex < len(search.matches) {
		search.anchorRow = search.matches[search.selectedIndex].Segments[0].Row
	} else {
		search.anchorRow = t.getPrimaryScrollView().ScrollTop()
	}
	search.query = query
	search.selectionMode = altSearchModeQuery
	search.component.SetResult(-1, 0)
	t.requestRenderLocked(false)
}

func (t *TuiAltScreen) navigateSearch(direction int) {
	search := t.activeSearch
	if search == nil || search.query == "" {
		return
	}
	if direction < 0 {
		search.selectionMode = altSearchModePrevious
	} else {
		search.selectionMode = altSearchModeNext
	}
	t.requestRenderLocked(false)
}

func (t *TuiAltScreen) refreshSearch(layout *LayoutFrame) bool {
	search := t.activeSearch
	if search == nil {
		return false
	}
	scrollView := layout.PrimaryScrollView
	if scrollView == nil {
		scrollView = t.implicitScrollView
	}
	box := GetScrollViewBox(layout, scrollView)
	var lines []string
	if box != nil {
		lines = box.ScrollContentLines
	}
	if lines == nil || strings.TrimSpace(search.query) == "" {
		search.matches = nil
		search.selectedIndex = -1
		search.selectedKey = ""
		search.selectionMode = altSearchModeRetain
		search.component.SetResult(-1, 0)
		return false
	}

	shouldRevealSelection := search.selectionMode != altSearchModeRetain
	matches := FindAltScreenSearchMatches(lines, search.query)
	exactIndex := -1
	if search.selectedKey != "" {
		for i, match := range matches {
			if GetAltScreenSearchMatchKey(match) == search.selectedKey {
				exactIndex = i
				break
			}
		}
	}
	selectedIndex := -1
	if len(matches) > 0 {
		if search.selectionMode == altSearchModeQuery {
			for i, match := range matches {
				row := 0
				if len(match.Segments) > 0 {
					row = match.Segments[0].Row
				}
				if row >= search.anchorRow {
					selectedIndex = i
					break
				}
			}
			if selectedIndex < 0 {
				selectedIndex = 0
			}
		} else if search.selectionMode == altSearchModeNext {
			baseIndex := search.selectedIndex
			if exactIndex >= 0 {
				baseIndex = exactIndex
			}
			baseIndex = minInt(baseIndex, len(matches)-1)
			if baseIndex < 0 {
				selectedIndex = 0
			} else {
				selectedIndex = (baseIndex + 1) % len(matches)
			}
		} else if search.selectionMode == altSearchModePrevious {
			baseIndex := search.selectedIndex
			if exactIndex >= 0 {
				baseIndex = exactIndex
			}
			baseIndex = minInt(baseIndex, len(matches)-1)
			if baseIndex < 0 {
				selectedIndex = len(matches) - 1
			} else {
				selectedIndex = (baseIndex - 1 + len(matches)) % len(matches)
			}
		} else {
			if exactIndex >= 0 {
				selectedIndex = exactIndex
			} else {
				selectedIndex = minInt(maxInt(0, search.selectedIndex), len(matches)-1)
			}
		}
	}

	search.matches = matches
	search.selectedIndex = selectedIndex
	if selectedIndex >= 0 {
		search.selectedKey = GetAltScreenSearchMatchKey(matches[selectedIndex])
	} else {
		search.selectedKey = ""
	}
	search.selectionMode = altSearchModeRetain
	search.component.SetResult(selectedIndex, len(matches))
	if !shouldRevealSelection {
		return false
	}

	var firstSegment, lastSegment *AltScreenSearchSegment
	var selected *AltScreenSearchMatch
	if selectedIndex >= 0 {
		selected = &matches[selectedIndex]
		if len(selected.Segments) > 0 {
			firstSegment = &selected.Segments[0]
			lastSegment = &selected.Segments[len(selected.Segments)-1]
		}
	}
	if box == nil || firstSegment == nil || lastSegment == nil || scrollView.ViewportHeight() <= 0 {
		return false
	}
	before := scrollView.ScrollTop()
	visibleBottom := before + scrollView.ViewportHeight() - 1
	target := before
	if firstSegment.Row < before || lastSegment.Row > visibleBottom {
		target = firstSegment.Row - scrollView.ViewportHeight()/3
	}
	scrollView.ScrollTo(target, &ScrollViewScrollToOptions{DisableFollow: true})
	return scrollView.ScrollTop() != before
}

// Flash shows a transient message in the alternate-screen flash stack (pi:
// flash). durationMs <= 0 uses the default duration.
func (t *TuiAltScreen) Flash(message string, durationMs int) {
	if durationMs <= 0 {
		durationMs = altScreenFlashDefaultDurationMs
	}
	t.flashes.Flash(message, durationMs)
}

func (t *TuiAltScreen) shouldDeferViewportInputToOverlay() bool {
	// pi: this.isOverlayFocused() && this.activeSearch?.overlay?.isFocused() !== true.
	// The overlay focus check reads focusedComponent directly (under the TUI
	// lock); overlayHandleImpl.IsFocused would re-lock the same mutex.
	if !t.isOverlayFocused() {
		return false
	}
	return !(t.activeSearch != nil && t.activeSearch.overlay != nil &&
		t.focusedComponent == t.activeSearch.component)
}

func (t *TuiAltScreen) handleViewportInput(data string) *TuiInputListenerResult {
	if data == focusOut {
		hadActiveSelection := t.selectionPressActive
		hadNonEmptyActiveSelection := hadActiveSelection && t.getSelectionBounds() != nil
		t.selectionPressActive = false
		t.stopSelectionAutoScroll()
		t.stopScrollbarHover()
		t.stopScrollbarDrag()
		t.pressedUrl = ""
		t.selectionDragged = false
		if hadActiveSelection {
			t.selectionAnchor = nil
			t.selectionFocus = nil
			t.selectionGranularity = altGranularityCharacter
			t.selectionInitialRange = nil
			if hadNonEmptyActiveSelection {
				t.requestRenderLocked(false)
			}
		}
		t.lastClick = nil
		return &TuiInputListenerResult{Consume: true}
	}
	if data == focusIn {
		return &TuiInputListenerResult{Consume: true}
	}

	if wheelEvent, ok := t.parseWheelEvent(data); ok {
		if t.shouldDeferViewportInputToOverlay() {
			return nil
		}
		t.routeWheel(wheelEvent)
		return &TuiInputListenerResult{Consume: true}
	}
	if mouseEvent, ok := t.parseSgrMouseEvent(data); ok {
		if t.handleRightClickPaste(mouseEvent) {
			return &TuiInputListenerResult{Consume: true}
		}
		handled := t.handleScrollbarMouseEvent(mouseEvent)
		if t.scrollbarDrag == nil {
			t.updateScrollbarHover(mouseEvent.x, mouseEvent.y)
		}
		if !handled {
			t.handleSelectionMouseEvent(mouseEvent)
		}
		return &TuiInputListenerResult{Consume: true}
	}
	if t.isMouseSequence(data) {
		return &TuiInputListenerResult{Consume: true}
	}

	keybindings := GetKeybindings()
	isRelease := IsKeyRelease(data)
	if keybindings.Matches(data, "tui.altScreen.search") {
		if !isRelease {
			t.openSearch()
		}
		return &TuiInputListenerResult{Consume: true}
	}
	if t.activeSearch != nil && t.activeSearch.overlay != nil &&
		t.focusedComponent == t.activeSearch.component {
		if keybindings.Matches(data, "tui.altScreen.searchNext") {
			if !isRelease {
				t.navigateSearch(1)
			}
			return &TuiInputListenerResult{Consume: true}
		}
		if keybindings.Matches(data, "tui.altScreen.searchPrevious") {
			if !isRelease {
				t.navigateSearch(-1)
			}
			return &TuiInputListenerResult{Consume: true}
		}
		if keybindings.Matches(data, "tui.altScreen.searchClose") {
			if !isRelease {
				t.closeSearch()
			}
			return &TuiInputListenerResult{Consume: true}
		}
	}
	if t.shouldDeferViewportInputToOverlay() {
		return nil
	}
	if keybindings.Matches(data, "tui.altScreen.pageUp") {
		if !isRelease {
			t.scrollByLocked(-maxInt(1, t.getPrimaryScrollView().ViewportHeight()-pageScrollOverlap))
		}
		return &TuiInputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.pageDown") {
		if !isRelease {
			t.scrollByLocked(maxInt(1, t.getPrimaryScrollView().ViewportHeight()-pageScrollOverlap))
		}
		return &TuiInputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.halfPageUp") {
		if !isRelease {
			t.scrollByLocked(-maxInt(1, t.getPrimaryScrollView().ViewportHeight()/2))
		}
		return &TuiInputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.halfPageDown") {
		if !isRelease {
			t.scrollByLocked(maxInt(1, t.getPrimaryScrollView().ViewportHeight()/2))
		}
		return &TuiInputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.lineUp") {
		if !isRelease {
			t.scrollByLocked(-1)
		}
		return &TuiInputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.lineDown") {
		if !isRelease {
			t.scrollByLocked(1)
		}
		return &TuiInputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.previousPrompt") {
		if !isRelease {
			t.scrollToPrompt(-1)
		}
		return &TuiInputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.nextPrompt") {
		if !isRelease {
			t.scrollToPrompt(1)
		}
		return &TuiInputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.top") {
		if !isRelease {
			t.scrollToTopLocked()
		}
		return &TuiInputListenerResult{Consume: true}
	}
	if keybindings.Matches(data, "tui.altScreen.bottom") {
		if !isRelease {
			t.scrollToBottomLocked()
		}
		return &TuiInputListenerResult{Consume: true}
	}
	return nil
}

var sgrWheelRegex = regexp.MustCompile(`^\x1b\[<(\d+);(\d+);(\d+)[Mm]$`)
var sgrMouseRegex = regexp.MustCompile(`^\x1b\[<(\d+);(\d+);(\d+)([Mm])$`)

func (t *TuiAltScreen) parseWheelEvent(data string) (altWheelEvent, bool) {
	if m := sgrWheelRegex.FindStringSubmatch(data); m != nil {
		button, _ := strconv.Atoi(m[1])
		if button&64 == 0 {
			return altWheelEvent{}, false
		}
		direction := button & 3
		if direction != 0 && direction != 1 {
			return altWheelEvent{}, false
		}
		x, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		dir := 1
		if direction == 0 {
			dir = -1
		}
		return altWheelEvent{direction: dir, x: x - 1, y: y - 1}, true
	}
	if len(data) == 6 && strings.HasPrefix(data, "\x1b[M") {
		button := int(data[3]) - 32
		if button&64 == 0 {
			return altWheelEvent{}, false
		}
		direction := button & 3
		if direction != 0 && direction != 1 {
			return altWheelEvent{}, false
		}
		dir := 1
		if direction == 0 {
			dir = -1
		}
		return altWheelEvent{direction: dir, x: int(data[4]) - 33, y: int(data[5]) - 33}, true
	}
	return altWheelEvent{}, false
}

func (t *TuiAltScreen) routeWheel(event altWheelEvent) {
	remaining := event.direction * t.wheelScrollLines
	seen := map[*ScrollView]bool{}
	var scrollViews []*ScrollView
	if t.currentLayout != nil {
		scrollViews = GetScrollViewsAt(t.currentLayout, event.x, event.y)
	}
	for _, scrollView := range scrollViews {
		seen[scrollView] = true
		remaining = scrollView.ScrollBy(remaining)
		if remaining == 0 || scrollView.Overscroll() == "contain" {
			break
		}
	}
	primary := t.getPrimaryScrollView()
	if remaining != 0 && !seen[primary] {
		primary.ScrollBy(remaining)
	}
	t.updateScrollbarHover(event.x, event.y)
	t.requestRenderLocked(false)
}

func (t *TuiAltScreen) parseSgrMouseEvent(data string) (altSgrMouseEvent, bool) {
	m := sgrMouseRegex.FindStringSubmatch(data)
	if m == nil {
		return altSgrMouseEvent{}, false
	}
	button, _ := strconv.Atoi(m[1])
	x, _ := strconv.Atoi(m[2])
	y, _ := strconv.Atoi(m[3])
	return altSgrMouseEvent{button: button, x: x - 1, y: y - 1, release: m[4] == "m"}, true
}

func (t *TuiAltScreen) handleRightClickPaste(event altSgrMouseEvent) bool {
	if t.onRightClickPaste == nil ||
		altScreenPlatform != "windows" ||
		strings.ToLower(envForAltScreen("TERM_PROGRAM")) == "vscode" ||
		event.release ||
		event.button != 2 {
		return false
	}
	func() {
		defer func() { recover() /* Clipboard paste is best-effort. */ }()
		t.onRightClickPaste()
	}()
	return true
}

func (t *TuiAltScreen) getScrollbarTargetAt(x int, y int) *altScrollbarTarget {
	if t.hasOverlayLocked() || t.currentLayout == nil {
		return nil
	}
	for _, scrollView := range GetScrollViewsAt(t.currentLayout, x, y) {
		box := GetScrollViewBox(t.currentLayout, scrollView)
		var geometry *ScrollbarGeometry
		if box != nil {
			geometry = GetScrollbarGeometry(box)
		}
		if geometry != nil &&
			x == geometry.Column &&
			y >= geometry.ThumbTop &&
			y < geometry.ThumbTop+geometry.ThumbHeight {
			return &altScrollbarTarget{scrollView: scrollView, geometry: geometry}
		}
	}
	return nil
}

func (t *TuiAltScreen) setScrollbarHover(scrollView *ScrollView) {
	if scrollView == t.scrollbarHover {
		return
	}
	if t.scrollbarHover != nil {
		t.scrollbarHover.SetScrollbarActive(false)
	}
	t.scrollbarHover = scrollView
	if t.scrollbarHover != nil {
		t.scrollbarHover.SetScrollbarActive(true)
	}
}

func (t *TuiAltScreen) updateScrollbarHover(x int, y int) {
	var target *ScrollView
	if hit := t.getScrollbarTargetAt(x, y); hit != nil {
		target = hit.scrollView
	}
	t.setScrollbarHover(target)
}

func (t *TuiAltScreen) stopScrollbarHover() {
	t.setScrollbarHover(nil)
}

func (t *TuiAltScreen) handleScrollbarMouseEvent(event altSgrMouseEvent) bool {
	if t.scrollbarDrag != nil {
		if event.release {
			t.stopScrollbarDrag()
			return true
		}
		var box *LayoutBox
		if t.currentLayout != nil {
			box = GetScrollViewBox(t.currentLayout, t.scrollbarDrag.scrollView)
		}
		var geometry *ScrollbarGeometry
		if box != nil {
			geometry = GetScrollbarGeometry(box)
		}
		if geometry != nil {
			maxThumbOffset := geometry.TrackHeight - geometry.ThumbHeight
			thumbOffset := clampInt(event.y-geometry.TrackTop-t.scrollbarDrag.grabOffset, 0, maxThumbOffset)
			scrollTop := 0
			if maxThumbOffset != 0 {
				scrollTop = int(math.Round(float64(thumbOffset) / float64(maxThumbOffset) * float64(geometry.MaxScrollTop)))
			}
			t.scrollbarDrag.scrollView.ScrollTo(scrollTop, nil)
		}
		return true
	}

	if event.release || (event.button&32) != 0 || (event.button&3) != 0 {
		return false
	}
	target := t.getScrollbarTargetAt(event.x, event.y)
	if target == nil {
		return false
	}
	t.stopSelectionAutoScroll()
	t.selectionPressActive = false
	t.selectionAnchor = nil
	t.selectionFocus = nil
	t.selectionGranularity = altGranularityCharacter
	t.selectionInitialRange = nil
	t.lastClick = nil
	t.pressedUrl = ""
	t.selectionDragged = false
	t.setScrollbarHover(target.scrollView)
	t.scrollbarDrag = &altScrollbarDrag{
		scrollView: target.scrollView,
		grabOffset: event.y - target.geometry.ThumbTop,
	}
	return true
}

func (t *TuiAltScreen) stopScrollbarDrag() {
	t.scrollbarDrag = nil
}

func (t *TuiAltScreen) getScrollSelectionPoint(scrollView *ScrollView, x int, y int) *altSelectionPoint {
	if t.currentLayout == nil {
		return nil
	}
	box := GetScrollViewBox(t.currentLayout, scrollView)
	if box == nil || box.Rect.Height <= 0 || box.Clip.Height <= 0 {
		return nil
	}
	visibleTop := maxInt(maxInt(0, box.Rect.Y), box.Clip.Y)
	visibleBottom := minInt(minInt(t.terminal.Rows()-1, box.Rect.Y+box.Rect.Height-1), box.Clip.Y+box.Clip.Height-1)
	if visibleBottom < visibleTop {
		return nil
	}
	pointerRow := clampInt(y, visibleTop, visibleBottom)
	contentLen := 1
	if box.ScrollContentLines != nil {
		contentLen = len(box.ScrollContentLines)
	}
	maxContentRow := maxInt(0, contentLen-1)
	return &altSelectionPoint{
		row:        clampInt(scrollView.ScrollTop()+pointerRow-box.Rect.Y, 0, maxContentRow),
		col:        clampInt(x-box.Rect.X, 0, box.Rect.Width-1),
		scrollView: scrollView,
	}
}

func (t *TuiAltScreen) getSelectionPoint(event altSgrMouseEvent, scrollView *ScrollView) altSelectionPoint {
	if scrollView != nil {
		if point := t.getScrollSelectionPoint(scrollView, event.x, event.y); point != nil {
			return *point
		}
	}
	return altSelectionPoint{
		row: clampInt(event.y, 0, t.terminal.Rows()-1),
		col: clampInt(event.x, 0, t.terminal.Columns()-1),
	}
}

func (t *TuiAltScreen) getSelectionSourceLine(point altSelectionPoint) string {
	if point.scrollView != nil && t.currentLayout != nil {
		if box := GetScrollViewBox(t.currentLayout, point.scrollView); box != nil && box.ScrollContentLines != nil {
			if point.row < len(box.ScrollContentLines) {
				return box.ScrollContentLines[point.row]
			}
			return ""
		}
	}
	if point.row < len(t.previousScreen) {
		return t.previousScreen[point.row]
	}
	return ""
}

func (t *TuiAltScreen) getWordSelection(point altSelectionPoint) *altSelectionRange {
	line := StripTerminalSequences(t.getSelectionSourceLine(point))
	start := 0
	segmenter := GetWordSegmenter()
	for _, segment := range segmenter.Segments(line) {
		end := start + VisibleWidth(segment)
		if point.col >= start && point.col < end {
			return &altSelectionRange{
				start: altSelectionPoint{row: point.row, col: start, scrollView: point.scrollView, boundary: point.boundary},
				end:   altSelectionPoint{row: point.row, col: end, scrollView: point.scrollView, boundary: true},
			}
		}
		start = end
	}
	return nil
}

func (t *TuiAltScreen) getLineSelection(point altSelectionPoint) altSelectionRange {
	return altSelectionRange{
		start: altSelectionPoint{row: point.row, col: 0, scrollView: point.scrollView, boundary: point.boundary},
		end:   altSelectionPoint{row: point.row, col: VisibleWidth(t.getSelectionSourceLine(point)), scrollView: point.scrollView, boundary: true},
	}
}

func (t *TuiAltScreen) updateSelectionFocus(point altSelectionPoint) {
	if t.selectionGranularity == altGranularityCharacter || t.selectionInitialRange == nil {
		t.selectionFocus = &point
		return
	}
	var rangeSel *altSelectionRange
	if t.selectionGranularity == altGranularityWord {
		rangeSel = t.getWordSelection(point)
	} else {
		line := t.getLineSelection(point)
		rangeSel = &line
	}
	if rangeSel == nil {
		return
	}
	initial := t.selectionInitialRange
	targetBeforeInitial := rangeSel.start.row < initial.start.row ||
		(rangeSel.start.row == initial.start.row && rangeSel.start.col < initial.start.col)
	if targetBeforeInitial {
		t.selectionAnchor = &initial.end
		t.selectionFocus = &rangeSel.start
	} else {
		t.selectionAnchor = &initial.start
		t.selectionFocus = &rangeSel.end
	}
}

func (t *TuiAltScreen) getClickCount(point altSelectionPoint, word *altSelectionRange) int {
	now := time.Now().UnixMilli()
	previous := t.lastClick
	count := 1
	if word != nil && previous != nil &&
		now-previous.timestamp <= doubleClickIntervalMs &&
		previous.row == point.row &&
		previous.scrollView == point.scrollView &&
		previous.wordStart == word.start.col &&
		previous.wordEnd == word.end.col {
		count = (previous.count % 3) + 1
	}
	if word != nil {
		t.lastClick = &altClickTarget{
			timestamp:  now,
			count:      count,
			row:        point.row,
			scrollView: point.scrollView,
			wordStart:  word.start.col,
			wordEnd:    word.end.col,
		}
	} else {
		t.lastClick = nil
	}
	return count
}

func (t *TuiAltScreen) updateSelectionAutoScroll(event altSgrMouseEvent) {
	var scrollView *ScrollView
	if t.selectionAnchor != nil {
		scrollView = t.selectionAnchor.scrollView
	}
	if scrollView == nil || t.currentLayout == nil {
		t.stopSelectionAutoScroll()
		return
	}
	box := GetScrollViewBox(t.currentLayout, scrollView)
	if box == nil || box.Rect.Height <= 0 || box.Clip.Height <= 0 {
		t.stopSelectionAutoScroll()
		return
	}
	visibleTop := maxInt(maxInt(0, box.Rect.Y), box.Clip.Y)
	visibleBottom := minInt(minInt(t.terminal.Rows()-1, box.Rect.Y+box.Rect.Height-1), box.Clip.Y+box.Clip.Height-1)
	pointer := event
	t.selectionDragPointer = &pointer
	if event.y <= visibleTop {
		t.selectionAutoScrollDirection = -1
	} else if event.y >= visibleBottom {
		t.selectionAutoScrollDirection = 1
	} else {
		t.selectionAutoScrollDirection = 0
	}
	if t.selectionAutoScrollDirection == 0 {
		t.stopSelectionAutoScroll()
		return
	}
	if t.selectionAutoScrollTicker != nil {
		return
	}
	t.selectionAutoScrollTicker = time.NewTicker(50 * time.Millisecond)
	t.selectionAutoScrollStop = make(chan struct{})
	ticker := t.selectionAutoScrollTicker
	stop := t.selectionAutoScrollStop
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				t.lock(func() { t.autoScrollSelection() })
			}
		}
	}()
}

func (t *TuiAltScreen) autoScrollSelection() {
	var scrollView *ScrollView
	if t.selectionAnchor != nil {
		scrollView = t.selectionAnchor.scrollView
	}
	pointer := t.selectionDragPointer
	direction := t.selectionAutoScrollDirection
	if scrollView == nil || pointer == nil || direction == 0 {
		t.stopSelectionAutoScroll()
		return
	}
	remaining := scrollView.ScrollBy(direction)
	if remaining == direction {
		t.stopSelectionAutoScroll()
		return
	}
	if point := t.getScrollSelectionPoint(scrollView, pointer.x, pointer.y); point != nil {
		t.updateSelectionFocus(*point)
	}
	t.requestRenderLocked(false)
}

func (t *TuiAltScreen) stopSelectionAutoScroll() {
	if t.selectionAutoScrollTicker != nil {
		t.selectionAutoScrollTicker.Stop()
		t.selectionAutoScrollTicker = nil
		close(t.selectionAutoScrollStop)
		t.selectionAutoScrollStop = nil
	}
	t.selectionAutoScrollDirection = 0
	t.selectionDragPointer = nil
}

func (t *TuiAltScreen) handleSelectionMouseEvent(event altSgrMouseEvent) {
	button := event.button & 3
	if button != 0 && !(event.release && button == 3) {
		return
	}
	var anchorScrollView *ScrollView
	if t.selectionAnchor != nil {
		anchorScrollView = t.selectionAnchor.scrollView
	}
	point := t.getSelectionPoint(event, anchorScrollView)
	if event.release {
		if !t.selectionPressActive {
			return
		}
		t.selectionPressActive = false
		t.stopSelectionAutoScroll()
		if t.selectionAnchor == nil {
			return
		}
		t.updateSelectionFocus(point)
		clickedUrl := ""
		if !t.selectionDragged &&
			t.selectionAnchor.scrollView == point.scrollView &&
			t.selectionAnchor.row == point.row &&
			t.selectionAnchor.col == point.col {
			clickedUrl = t.pressedUrl
		}
		t.pressedUrl = ""
		if clickedUrl != "" && t.openUrl != nil {
			t.selectionAnchor = nil
			t.selectionFocus = nil
			func() {
				defer func() { recover() /* URL activation is best-effort. */ }()
				t.openUrl(clickedUrl)
			}()
			t.requestRenderLocked(false)
			return
		}
		t.copySelectionToClipboard()
		t.requestRenderLocked(false)
		return
	}
	if (event.button & 32) != 0 {
		if !t.selectionPressActive || t.selectionAnchor == nil {
			return
		}
		t.selectionDragged = true
		t.lastClick = nil
		t.pressedUrl = ""
		t.updateSelectionFocus(point)
		t.updateSelectionAutoScroll(event)
		t.requestRenderLocked(false)
		return
	}
	t.stopSelectionAutoScroll()
	t.selectionPressActive = true
	var scrollView *ScrollView
	if !t.hasOverlayLocked() && t.currentLayout != nil {
		views := GetScrollViewsAt(t.currentLayout, event.x, event.y)
		if len(views) > 0 {
			scrollView = views[0]
		}
	}
	anchor := t.getSelectionPoint(event, scrollView)
	word := t.getWordSelection(anchor)
	clickCount := t.getClickCount(anchor, word)
	var rangeSel *altSelectionRange
	if clickCount == 2 {
		rangeSel = word
	} else if clickCount == 3 {
		line := t.getLineSelection(anchor)
		rangeSel = &line
	}
	if rangeSel != nil {
		if clickCount == 2 {
			t.selectionGranularity = altGranularityWord
		} else {
			t.selectionGranularity = altGranularityLine
		}
	} else {
		t.selectionGranularity = altGranularityCharacter
	}
	t.selectionInitialRange = rangeSel
	if rangeSel != nil {
		start := rangeSel.start
		end := rangeSel.end
		t.selectionAnchor = &start
		t.selectionFocus = &end
	} else {
		t.selectionAnchor = &anchor
		t.selectionFocus = &anchor
	}
	t.selectionDragged = false
	if rangeSel == nil {
		y := clampInt(event.y, 0, t.terminal.Rows()-1)
		x := clampInt(event.x, 0, t.terminal.Columns()-1)
		line := ""
		if y < len(t.previousScreen) {
			line = t.previousScreen[y]
		}
		t.pressedUrl = GetOsc8LinkAtColumn(line, x)
	} else {
		t.pressedUrl = ""
	}
	t.requestRenderLocked(false)
}

type altSelectionBounds struct {
	start altSelectionPoint
	end   altSelectionPoint
}

func (t *TuiAltScreen) getSelectionBounds() *altSelectionBounds {
	if t.selectionAnchor == nil || t.selectionFocus == nil {
		return nil
	}
	if t.selectionAnchor.scrollView != t.selectionFocus.scrollView {
		return nil
	}
	anchorBeforeFocus := t.selectionAnchor.row < t.selectionFocus.row ||
		(t.selectionAnchor.row == t.selectionFocus.row && t.selectionAnchor.col < t.selectionFocus.col)
	if t.selectionAnchor.row == t.selectionFocus.row && t.selectionAnchor.col == t.selectionFocus.col {
		return nil
	}
	if anchorBeforeFocus {
		return &altSelectionBounds{start: *t.selectionAnchor, end: *t.selectionFocus}
	}
	return &altSelectionBounds{start: *t.selectionFocus, end: *t.selectionAnchor}
}

// getSelectionColumns computes the highlighted column span for a row (pi:
// getSelectionColumns; the default maxColumn = visibleWidth(line) is passed
// explicitly by callers).
func (t *TuiAltScreen) getSelectionColumns(
	line string,
	row int,
	selection altSelectionBounds,
	minColumn int,
	maxColumn int,
) (int, int) {
	lineWidth := VisibleWidth(line)
	start := maxInt(0, minColumn)
	end := minInt(lineWidth, maxColumn)
	if row == selection.start.row {
		if r := GetGraphemeCellRange(line, selection.start.col); r != nil {
			start = r.Start
		} else {
			start = minInt(selection.start.col, lineWidth)
		}
	}
	if row == selection.end.row {
		if selection.end.boundary {
			end = minInt(selection.end.col, lineWidth)
		} else if r := GetGraphemeCellRange(line, selection.end.col); r != nil {
			end = r.End
		} else {
			end = minInt(selection.end.col+1, lineWidth)
		}
	}
	return maxInt(minColumn, start), minInt(maxColumn, end)
}

func (t *TuiAltScreen) copySelectionToClipboard() {
	selection := t.getSelectionBounds()
	if selection == nil {
		return
	}
	var sourceLines []string
	if selection.start.scrollView != nil {
		if t.currentLayout == nil {
			return
		}
		box := GetScrollViewBox(t.currentLayout, selection.start.scrollView)
		if box == nil || box.ScrollContentLines == nil {
			return
		}
		sourceLines = box.ScrollContentLines
	} else {
		sourceLines = t.previousScreen
	}
	var lines []string
	for row := selection.start.row; row <= selection.end.row; row++ {
		line := ""
		if row < len(sourceLines) {
			line = sourceLines[row]
		}
		start, end := t.getSelectionColumns(line, row, *selection, 0, VisibleWidth(line))
		text := StripTerminalSequences(SliceByColumn(line, start, maxInt(0, end-start), true))
		lines = append(lines, jsTrimEnd(text))
	}
	text := strings.Join(lines, "\n")
	if len(text) == 0 {
		return
	}
	// Prefer an injected clipboard implementation (native clipboard + platform tools with a
	// verified success path) when the host app provides one. A bare OSC 52 write can show
	// "Copied!" while leaving the system clipboard untouched (e.g. macOS Terminal.app, tmux
	// without OSC 52 clipboard passthrough), so only report success when it actually copies.
	if t.copySelection != nil {
		ok := t.copySelection(text)
		if ok {
			t.flashLocked("Copied!")
		} else {
			t.flashLocked("Copy failed")
		}
		return
	}
	t.terminal.Write("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07")
	t.flashLocked("Copied!")
}

func (t *TuiAltScreen) applySearchTextHighlight(text string, current bool) string {
	style := t.searchMatchStyle
	if current {
		style = t.searchCurrentMatchStyle
	}
	var result strings.Builder
	plainStart := 0
	index := 0
	for index < len(text) {
		ansi := ExtractAnsiCode(text, index)
		if ansi == nil {
			index++
			continue
		}
		if index > plainStart {
			result.WriteString(style(text[plainStart:index]))
		}
		result.WriteString(ansi.Code)
		index += ansi.Length
		plainStart = index
	}
	if plainStart < len(text) {
		result.WriteString(style(text[plainStart:]))
	}
	return result.String()
}

func (t *TuiAltScreen) applySearchHighlights(screen []string, layout *LayoutFrame) []string {
	search := t.activeSearch
	if search == nil || search.selectedIndex < 0 || len(search.matches) == 0 {
		return screen
	}
	scrollView := layout.PrimaryScrollView
	if scrollView == nil {
		scrollView = t.implicitScrollView
	}
	box := GetScrollViewBox(layout, scrollView)
	if box == nil {
		return screen
	}

	rangesByRow := map[int][]altSearchHighlightRange{}
	scrollbarColumn := -1
	if geometry := GetScrollbarGeometry(box); geometry != nil {
		scrollbarColumn = geometry.Column
	}
	minRow := maxInt(maxInt(0, box.Rect.Y), box.Clip.Y)
	maxRow := minInt(minInt(len(screen), box.Rect.Y+box.Rect.Height), box.Clip.Y+box.Clip.Height)
	minColumn := maxInt(maxInt(0, box.Rect.X), box.Clip.X)
	maxColumn := minInt(minInt(minInt(t.terminal.Columns(), box.Rect.X+box.Rect.Width), box.Clip.X+box.Clip.Width), scrollbarColumn)
	if scrollbarColumn < 0 {
		maxColumn = minInt(minInt(t.terminal.Columns(), box.Rect.X+box.Rect.Width), box.Clip.X+box.Clip.Width)
	}
	for matchIndex := range search.matches {
		for _, segment := range search.matches[matchIndex].Segments {
			row := box.Rect.Y + segment.Row - scrollView.ScrollTop()
			if row < minRow || row >= maxRow {
				continue
			}
			startCol := maxInt(minColumn, box.Rect.X+segment.StartCol)
			endCol := minInt(maxColumn, box.Rect.X+segment.EndCol)
			if endCol <= startCol {
				continue
			}
			rangesByRow[row] = append(rangesByRow[row], altSearchHighlightRange{
				startCol: startCol,
				endCol:   endCol,
				current:  matchIndex == search.selectedIndex,
			})
		}
	}

	result := make([]string, len(screen))
	copy(result, screen)
	for row, ranges := range rangesByRow {
		line := ""
		if row < len(result) {
			line = result[row]
		}
		if IsImageLine(line) {
			continue
		}
		lineWidth := VisibleWidth(line)
		// sort ranges by startCol descending
		for i := 1; i < len(ranges); i++ {
			for j := i; j > 0 && ranges[j].startCol > ranges[j-1].startCol; j-- {
				ranges[j], ranges[j-1] = ranges[j-1], ranges[j]
			}
		}
		for _, r := range ranges {
			startCol := minInt(r.startCol, lineWidth)
			endCol := minInt(r.endCol, lineWidth)
			if endCol <= startCol {
				continue
			}
			before := SliceByColumn(line, 0, startCol, true)
			highlighted := SliceByColumn(line, startCol, endCol-startCol, true)
			after := SliceByColumn(line, endCol, maxInt(0, lineWidth-endCol), true)
			line = before + t.applySearchTextHighlight(highlighted, r.current) + after
		}
		result[row] = line
	}
	return result
}

func (t *TuiAltScreen) applySelectionHighlight(text string) string {
	var result strings.Builder
	result.WriteString("\x1b[7m")
	index := 0
	for index < len(text) {
		ansi := ExtractAnsiCode(text, index)
		if ansi == nil {
			result.WriteString(text[index : index+1])
			index++
			continue
		}
		result.WriteString(ansi.Code)
		if strings.HasSuffix(ansi.Code, "m") {
			result.WriteString("\x1b[7m")
		}
		index += ansi.Length
	}
	result.WriteString("\x1b[27m")
	return result.String()
}

func (t *TuiAltScreen) applySelection(screen []string, layout *LayoutFrame) []string {
	selection := t.getSelectionBounds()
	if selection == nil {
		return screen
	}
	screenSelection := *selection
	minRow := 0
	maxRow := len(screen) - 1
	minColumn := 0
	maxColumn := t.terminal.Columns()
	if selection.start.scrollView != nil {
		if layout == nil {
			return screen
		}
		box := GetScrollViewBox(layout, selection.start.scrollView)
		if box == nil {
			return screen
		}
		minRow = maxInt(maxInt(0, box.Rect.Y), box.Clip.Y)
		maxRow = minInt(minInt(len(screen)-1, box.Rect.Y+box.Rect.Height-1), box.Clip.Y+box.Clip.Height-1)
		minColumn = maxInt(maxInt(0, box.Rect.X), box.Clip.X)
		maxColumn = minInt(minInt(t.terminal.Columns(), box.Rect.X+box.Rect.Width), box.Clip.X+box.Clip.Width)
		scrollTop := selection.start.scrollView.ScrollTop()
		screenSelection.start = altSelectionPoint{
			row: box.Rect.Y + selection.start.row - scrollTop,
			col: box.Rect.X + selection.start.col,
		}
		screenSelection.end = altSelectionPoint{
			row:      box.Rect.Y + selection.end.row - scrollTop,
			col:      box.Rect.X + selection.end.col,
			boundary: selection.end.boundary,
		}
	}
	result := make([]string, len(screen))
	copy(result, screen)
	for row := range screen {
		if row < minRow || row > maxRow || row < screenSelection.start.row || row > screenSelection.end.row ||
			IsImageLine(screen[row]) {
			continue
		}
		line := screen[row]
		lineWidth := VisibleWidth(line)
		start, end := t.getSelectionColumns(line, row, screenSelection, minColumn, maxColumn)
		if end <= start {
			continue
		}
		before := SliceByColumn(line, 0, start, true)
		selected := SliceByColumn(line, start, end-start, true)
		after := SliceByColumn(line, end, maxInt(0, lineWidth-end), true)
		result[row] = before + t.applySelectionHighlight(selected) + after
	}
	return result
}

func (t *TuiAltScreen) isMouseSequence(data string) bool {
	if sgrMouseRegex.MatchString(data) {
		return true
	}
	return len(data) == 6 && strings.HasPrefix(data, "\x1b[M")
}

func (t *TuiAltScreen) compositeFlashes(screen []string, width int, height int) []string {
	flashLines := t.flashes.Render(width)
	if len(flashLines) > height {
		flashLines = flashLines[len(flashLines)-height:]
	}
	if len(flashLines) == 0 {
		return screen
	}
	result := make([]string, len(screen))
	copy(result, screen)
	for len(result) < height {
		result = append(result, "")
	}
	for row := 0; row < len(flashLines); row++ {
		line := flashLines[row]
		flashWidth := VisibleWidth(line)
		if flashWidth == 0 {
			continue
		}
		base := ""
		if row < len(result) {
			base = result[row]
		}
		result[row] = CompositeTuiLine(base, line, width-flashWidth, flashWidth, width)
	}
	return result
}

func (t *TuiAltScreen) doRender() error {
	if t.stopped || !t.altScreenActive {
		return nil
	}
	width := maxInt(1, t.terminal.Columns())
	height := maxInt(1, t.terminal.Rows())
	root := Component(t.implicitScrollView)
	if t.layoutRoot != nil {
		root = t.layoutRoot
	}
	nextLayout := RenderLayoutFrame(root, width, height, func() { t.RequestRender(false) })
	if t.refreshSearch(nextLayout) {
		nextLayout = RenderLayoutFrame(root, width, height, func() { t.RequestRender(false) })
	}
	screen := make([]string, len(nextLayout.Lines))
	for i, line := range nextLayout.Lines {
		screen[i] = osc133ZonePrefixRegex.ReplaceAllString(line, "")
	}
	screen = t.applySearchHighlights(screen, nextLayout)
	screen = t.compositeOverlays(screen, width, height)
	if len(screen) > height {
		screen = screen[len(screen)-height:]
	}
	screen = t.applySelection(screen, nextLayout)
	screen = t.compositeFlashes(screen, width, height)

	cursorPos := t.extractCursorPosition(screen, height)
	resets := t.applyLineResets(screen)
	for i, line := range resets {
		if IsImageLine(line) || VisibleWidth(line) <= width {
			screen[i] = line
		} else {
			screen[i] = SliceByColumn(line, 0, width, true)
		}
	}

	fullRedraw := len(t.previousScreen) == 0 || t.previousScreenWidth != width || t.previousScreenHeight != height
	imagesNeedRedraw := false
	for row := range screen {
		if screen[row] != sliceOrEmpty(t.previousScreen, row) &&
			(IsImageLine(screen[row]) || IsImageLine(sliceOrEmpty(t.previousScreen, row))) {
			imagesNeedRedraw = true
			break
		}
	}
	redrawImages := fullRedraw || imagesNeedRedraw
	hadUploadedKittyImages := len(t.uploadedKittyImages) > 0
	preparedScreen := screen
	evictedImageDeletion := ""
	imageProtocolKitty := false
	if s, ok := t.imageProtocol.(string); ok {
		imageProtocolKitty = s == "kitty"
	}
	imageProtocolIterm2 := false
	if s, ok := t.imageProtocol.(string); ok {
		imageProtocolIterm2 = s == "iterm2"
	}
	if redrawImages && imageProtocolKitty {
		preparedScreen, evictedImageDeletion = t.prepareKittyScreen(screen)
	}

	var buffer strings.Builder
	buffer.WriteString(beginSyncOutput)
	if fullRedraw {
		t.fullRedrawCount++
		clearImages := ""
		if imageProtocolKitty && hadUploadedKittyImages {
			clearImages = DeleteAllKittyPlacements()
		} else {
			clearImages = t.deleteKittyImages()
		}
		buffer.WriteString(clearImages)
		buffer.WriteString("\x1b[2J")
	} else if imagesNeedRedraw {
		if imageProtocolIterm2 {
			buffer.WriteString("\x1b[2J")
		} else if imageProtocolKitty {
			buffer.WriteString(DeleteAllKittyPlacements())
		}
	}
	buffer.WriteString(evictedImageDeletion)

	for row := 0; row < height; row++ {
		if !fullRedraw && !imagesNeedRedraw && screen[row] == sliceOrEmpty(t.previousScreen, row) {
			continue
		}
		buffer.WriteString("\x1b[" + strconv.Itoa(row+1) + ";1H\x1b[2K")
		buffer.WriteString(sliceOrEmpty(preparedScreen, row))
	}

	if cursorPos != nil {
		buffer.WriteString("\x1b[" + strconv.Itoa(cursorPos.Row+1) + ";" + strconv.Itoa(minInt(width, cursorPos.Col)+1) + "H")
		if t.showHardwareCursor {
			buffer.WriteString("\x1b[?25h")
		} else {
			buffer.WriteString("\x1b[?25l")
		}
	} else {
		buffer.WriteString("\x1b[?25l")
	}
	buffer.WriteString(endSyncOutput)
	t.terminal.Write(buffer.String())

	t.previousScreen = screen
	t.previousScreenWidth = width
	t.previousScreenHeight = height
	t.currentLayout = nextLayout
	return nil
}

func sliceOrEmpty(lines []string, row int) string {
	if row >= 0 && row < len(lines) {
		return lines[row]
	}
	return ""
}
