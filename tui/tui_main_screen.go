// tui/tui_main_screen.go (pi: packages/tui/src/tui-main-screen.ts) — TUI
// implementation that renders into the terminal's main screen and scrollback.
//
// Porting notes:
//   - pi's doRender throws on over-wide rendered lines (crashes the process);
//     the Go doRender returns the error and the render scheduler panics with
//     it (equivalent process-fatal behavior, see tui.go doRenderOrPanic).
//   - PI_TUI_DEBUG_REDRAW / PI_TUI_DEBUG file logging and the crash log are
//     kept (fs → os). logDirectory empty disables debug logging; the crash
//     log falls back to os.TempDir() (pi: logDirectory ?? os.tmpdir(),
//     pi-tui-crash.log).
//   - Kitty image handling uses the terminal_image.go subset (Phase 8 keeps
//     full image rendering out of scope; placement sequences remain opaque
//     strings and are diffed/cleared exactly as pi does).
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const kittySequencePrefix = "\x1b_G"
const maxRenderWriteChars = 1024 * 1024

// boundedTerminalWriter streams terminal output in 1 MiB chunks so a full
// render never forms one string large enough to stress terminal writers
// (pi: BoundedTerminalWriter).
//
// Deviation note: pi counts UTF-16 code units and preserves surrogate pairs
// at chunk boundaries; Go strings are UTF-8, so the equivalent constraint is
// never splitting a multi-byte rune across chunks (chunk size counted in
// bytes).
//
// append() fills the current chunk and flushes it when full. Oversized input
// is split at chunk boundaries on rune boundaries so each write remains valid
// UTF-8. Callers append synchronized-output begin/end sequences themselves;
// the final flush() writes any remainder, including the end sequence.
type boundedTerminalWriter struct {
	buffer       strings.Builder
	bufferedLen  int
	writtenChars int
	write        func(data string)
}

func newBoundedTerminalWriter(write func(data string)) *boundedTerminalWriter {
	return &boundedTerminalWriter{write: write}
}

// append appends terminal data, flushing full chunks as needed. Callers must
// call flush() after the final append.
func (w *boundedTerminalWriter) append(value string) {
	offset := 0
	for offset < len(value) {
		capacity := maxRenderWriteChars - w.bufferedLen
		if capacity == 0 {
			w.flush()
			continue
		}

		end := minInt(len(value), offset+capacity)
		// Back off to a rune boundary so no UTF-8 sequence is split.
		for end > offset && end < len(value) && !utf8.RuneStart(value[end]) {
			end--
		}
		if end == offset {
			w.flush()
			continue
		}

		w.buffer.WriteString(value[offset:end])
		w.bufferedLen += end - offset
		offset = end
		if w.bufferedLen == maxRenderWriteChars {
			w.flush()
		}
	}
}

// flush writes the current chunk, if any, and retains only its character
// count for debug output.
func (w *boundedTerminalWriter) flush() {
	if w.bufferedLen == 0 {
		return
	}
	w.write(w.buffer.String())
	w.writtenChars += w.bufferedLen
	w.buffer.Reset()
	w.bufferedLen = 0
}

func (w *boundedTerminalWriter) length() int {
	return w.writtenChars + w.bufferedLen
}

type kittyImageHeader struct {
	ids  []int
	rows int
}

func parseKittyImageHeader(line string) *kittyImageHeader {
	sequenceStart := strings.Index(line, kittySequencePrefix)
	if sequenceStart == -1 {
		return nil
	}
	paramsStart := sequenceStart + len(kittySequencePrefix)
	paramsEnd := strings.Index(line[paramsStart:], ";")
	if paramsEnd == -1 {
		return nil
	}
	paramsEnd += paramsStart

	ids := []int{}
	rows := 1
	for _, param := range strings.Split(line[paramsStart:paramsEnd], ",") {
		parts := strings.SplitN(param, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := parts[0], parts[1]
		var numberValue int
		if _, err := fmt.Sscanf(value, "%d", &numberValue); err != nil {
			continue
		}
		if numberValue <= 0 || numberValue > 0xffffffff {
			continue
		}
		if key == "i" {
			ids = append(ids, numberValue)
		} else if key == "r" {
			rows = numberValue
		}
	}
	return &kittyImageHeader{ids: ids, rows: rows}
}

func extractKittyImageIds(line string) []int {
	if h := parseKittyImageHeader(line); h != nil {
		return h.ids
	}
	return nil
}

func extractKittyImageRows(line string) int {
	if h := parseKittyImageHeader(line); h != nil {
		return h.rows
	}
	return 1
}

func isTermuxSession() bool {
	return os.Getenv("TERMUX_VERSION") != ""
}

// TuiMainScreenRenderState is a snapshot of the differential renderer state
// (used by session replacement / suspend-resume flows).
type TuiMainScreenRenderState struct {
	PreviousLines       []string
	PreviousWidth       int
	PreviousHeight      int
	CursorRow           int
	HardwareCursorRow   int
	MaxLinesRendered    int
	PreviousViewportTop int
}

// TuiMainScreen renders into the terminal's main screen and scrollback.
type TuiMainScreen struct {
	TuiBase

	previousLines         []string
	previousKittyImageIds map[int]bool
	previousWidth         int
	previousHeight        int
	cursorRow             int
	hardwareCursorRow     int
	maxLinesRendered      int
	previousViewportTop   int
}

// NewTuiMainScreen creates a TuiMainScreen. showHardwareCursor (nil = false,
// v0.85.1 removed the env default) and logDirectory ("" = debug logging
// disabled, crash log falls back to os.TempDir) mirror the pi constructor's
// optional parameters.
func NewTuiMainScreen(terminal Terminal, showHardwareCursor *bool, logDirectory string) *TuiMainScreen {
	m := &TuiMainScreen{previousKittyImageIds: map[int]bool{}}
	m.TuiBase = *newTuiBase(terminal, TuiModeRegular, m, showHardwareCursor, logDirectory)
	return m
}

// CaptureRenderState snapshots the renderer state.
//
// Locking note: the embedded TuiBase's mutex guards BOTH the base and the
// renderer state (m.mu resolves to the promoted TuiBase.mu). doRender always
// runs under b.lock() (scheduler/renderNow/stop guarantee this), so it must
// NOT re-lock; only externally-called state accessors lock here.
func (m *TuiMainScreen) CaptureRenderState() TuiMainScreenRenderState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return TuiMainScreenRenderState{
		PreviousLines:       append([]string(nil), m.previousLines...),
		PreviousWidth:       m.previousWidth,
		PreviousHeight:      m.previousHeight,
		CursorRow:           m.cursorRow,
		HardwareCursorRow:   m.hardwareCursorRow,
		MaxLinesRendered:    m.maxLinesRendered,
		PreviousViewportTop: m.previousViewportTop,
	}
}

// RestoreRenderState restores a snapshot taken by CaptureRenderState.
func (m *TuiMainScreen) RestoreRenderState(state TuiMainScreenRenderState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.previousLines = make([]string, len(state.PreviousLines))
	for i, line := range state.PreviousLines {
		if IsImageLine(line) {
			m.previousLines[i] = ""
		} else {
			m.previousLines[i] = line
		}
	}
	m.previousKittyImageIds = map[int]bool{}
	m.previousWidth = state.PreviousWidth
	m.previousHeight = state.PreviousHeight
	m.cursorRow = state.CursorRow
	m.hardwareCursorRow = state.HardwareCursorRow
	m.maxLinesRendered = state.MaxLinesRendered
	m.previousViewportTop = state.PreviousViewportTop
}

func (m *TuiMainScreen) resetRenderState() {
	m.previousLines = nil
	m.previousWidth = -1
	m.previousHeight = -1
	m.cursorRow = 0
	m.hardwareCursorRow = 0
	m.maxLinesRendered = 0
	m.previousViewportTop = 0
}

func (m *TuiMainScreen) beforeTerminalStart() {}

func (m *TuiMainScreen) getMountedRoots() []Component { return m.Children }

func (m *TuiMainScreen) afterTerminalStart() {}

func (m *TuiMainScreen) beforeTerminalStop(options TuiStopOptions) {
	if options.PreserveScreen || len(m.previousLines) == 0 {
		return
	}
	m.terminal.Write(" ")
	targetRow := len(m.previousLines)
	lineDiff := targetRow - m.hardwareCursorRow
	if lineDiff > 0 {
		m.terminal.Write(fmt.Sprintf("\x1b[%dB", lineDiff))
	} else if lineDiff < 0 {
		m.terminal.Write(fmt.Sprintf("\x1b[%dA", -lineDiff))
	}
	m.terminal.Write("\r\n")
}

func (m *TuiMainScreen) afterTerminalStop(options TuiStopOptions) {}

func (m *TuiMainScreen) collectKittyImageIds(lines []string) map[int]bool {
	ids := map[int]bool{}
	for _, line := range lines {
		for _, id := range extractKittyImageIds(line) {
			ids[id] = true
		}
	}
	return ids
}

func (m *TuiMainScreen) deleteKittyImages(ids map[int]bool) string {
	var buffer strings.Builder
	for id := range ids {
		buffer.WriteString(DeleteKittyImage(id))
	}
	return buffer.String()
}

func (m *TuiMainScreen) getKittyImageReservedRows(lines []string, index int, maxIndex int) int {
	rows := extractKittyImageRows(lines[index])
	if rows <= 1 {
		return 1
	}

	if maxIndex < 0 {
		maxIndex = len(lines) - 1
	}
	maxRows := minInt(rows, minInt(maxIndex-index+1, len(lines)-index))
	reservedRows := 1
	for reservedRows < maxRows {
		line := ""
		if index+reservedRows < len(lines) {
			line = lines[index+reservedRows]
		}
		if IsImageLine(line) || VisibleWidth(line) > 0 {
			break
		}
		reservedRows++
	}
	return reservedRows
}

func (m *TuiMainScreen) expandChangedRangeForKittyImages(firstChanged int, lastChanged int, newLines []string) (int, int) {
	expandedFirstChanged := firstChanged
	expandedLastChanged := lastChanged
	expandForLines := func(lines []string) {
		for i := 0; i < len(lines); i++ {
			if len(extractKittyImageIds(lines[i])) == 0 {
				continue
			}
			blockEnd := i + m.getKittyImageReservedRows(lines, i, len(lines)-1) - 1
			if i >= firstChanged || (i <= lastChanged && blockEnd >= firstChanged) {
				expandedFirstChanged = minInt(expandedFirstChanged, i)
				expandedLastChanged = maxInt(expandedLastChanged, blockEnd)
			}
		}
	}

	expandForLines(m.previousLines)
	expandForLines(newLines)
	return expandedFirstChanged, expandedLastChanged
}

func (m *TuiMainScreen) deleteChangedKittyImages(firstChanged int, lastChanged int) string {
	if firstChanged < 0 || lastChanged < firstChanged {
		return ""
	}

	ids := map[int]bool{}
	maxLine := minInt(lastChanged, len(m.previousLines)-1)
	for i := firstChanged; i <= maxLine; i++ {
		line := ""
		if i < len(m.previousLines) {
			line = m.previousLines[i]
		}
		for _, id := range extractKittyImageIds(line) {
			ids[id] = true
		}
	}

	return m.deleteKittyImages(ids)
}

func (m *TuiMainScreen) doRender() error {
	// Called under the TUI lock (see CaptureRenderState locking note).
	if m.stopped {
		return nil
	}
	width := m.terminal.Columns()
	height := m.terminal.Rows()
	widthChanged := m.previousWidth != 0 && m.previousWidth != width
	heightChanged := m.previousHeight != 0 && m.previousHeight != height
	previousBufferLength := height
	if m.previousHeight > 0 {
		previousBufferLength = m.previousViewportTop + m.previousHeight
	}
	prevViewportTop := m.previousViewportTop
	if heightChanged {
		prevViewportTop = maxInt(0, previousBufferLength-height)
	}
	viewportTop := prevViewportTop
	hardwareCursorRow := m.hardwareCursorRow
	computeLineDiff := func(targetRow int) int {
		currentScreenRow := hardwareCursorRow - prevViewportTop
		targetScreenRow := targetRow - viewportTop
		return targetScreenRow - currentScreenRow
	}

	// Render all components to get new lines
	newLines := m.Render(width)

	// Composite overlays into the rendered lines (before differential compare)
	if len(m.overlayStack) > 0 {
		newLines = m.compositeOverlays(newLines, width, height)
	}

	// Extract cursor position before applying line resets (marker must be found first)
	cursorPos := m.extractCursorPosition(newLines, height)

	newLines = m.applyLineResets(newLines)

	// Helper to clear scrollback and viewport and render all new lines
	fullRender := func(clear bool) {
		m.fullRedrawCount++
		output := newBoundedTerminalWriter(m.terminal.Write)
		output.append("\x1b[?2026h") // Begin synchronized output
		if clear {
			output.append(m.deleteKittyImages(m.previousKittyImageIds))
			output.append("\x1b[2J\x1b[H\x1b[3J") // Clear screen, home, then clear scrollback
		}
		for i := 0; i < len(newLines); i++ {
			if i > 0 {
				output.append("\r\n")
			}
			line := newLines[i]
			isImage := IsImageLine(line)
			imageReservedRows := 1
			if isImage {
				imageReservedRows = m.getKittyImageReservedRows(newLines, i, len(newLines)-1)
			}
			if imageReservedRows > 1 && imageReservedRows <= height {
				for row := 1; row < imageReservedRows; row++ {
					output.append("\r\n")
				}
				output.append(fmt.Sprintf("\x1b[%dA", imageReservedRows-1))
				output.append(line)
				output.append(fmt.Sprintf("\x1b[%dB", imageReservedRows-1))
				i += imageReservedRows - 1
				continue
			}
			output.append(line)
		}
		output.append("\x1b[?2026l") // End synchronized output
		output.flush()
		m.cursorRow = maxInt(0, len(newLines)-1)
		m.hardwareCursorRow = m.cursorRow
		// Reset max lines when clearing, otherwise track growth
		if clear {
			m.maxLinesRendered = len(newLines)
		} else {
			m.maxLinesRendered = maxInt(m.maxLinesRendered, len(newLines))
		}
		bufferLength := maxInt(height, len(newLines))
		m.previousViewportTop = maxInt(0, bufferLength-height)
		m.positionHardwareCursorLocked(cursorPos, len(newLines))
		m.previousLines = newLines
		m.previousKittyImageIds = m.collectKittyImageIds(newLines)
		m.previousWidth = width
		m.previousHeight = height
	}

	redrawLogDirectory := ""
	if os.Getenv("PI_TUI_DEBUG_REDRAW") == "1" {
		redrawLogDirectory = m.logDirectory
	}
	logRedraw := func(reason string) {
		if redrawLogDirectory == "" {
			return
		}
		logPath := filepath.Join(redrawLogDirectory, "pi-tui-debug.log")
		msg := fmt.Sprintf("[%s] fullRender: %s (prev=%d, new=%d, height=%d)\n",
			time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), reason, len(m.previousLines), len(newLines), height)
		_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = f.WriteString(msg)
	}

	// First render - just output everything without clearing (assumes clean screen)
	if len(m.previousLines) == 0 && !widthChanged && !heightChanged {
		logRedraw("first render")
		fullRender(false)
		return nil
	}

	// Width changes always need a full re-render because wrapping changes.
	if widthChanged {
		logRedraw(fmt.Sprintf("terminal width changed (%d -> %d)", m.previousWidth, width))
		fullRender(true)
		return nil
	}

	// Height changes normally need a full re-render to keep the visible viewport aligned,
	// but Termux changes height when the software keyboard shows or hides.
	// In that environment, a full redraw causes the entire history to replay on every toggle.
	if heightChanged && !isTermuxSession() {
		logRedraw(fmt.Sprintf("terminal height changed (%d -> %d)", m.previousHeight, height))
		fullRender(true)
		return nil
	}

	// Content shrunk below the working area and no overlays - re-render to clear empty rows
	// (overlays need the padding, so only do this when no overlays are active)
	// Configurable via setClearOnShrink()
	if m.clearOnShrink && len(newLines) < m.maxLinesRendered && len(m.overlayStack) == 0 {
		logRedraw(fmt.Sprintf("clearOnShrink (maxLinesRendered=%d)", m.maxLinesRendered))
		fullRender(true)
		return nil
	}

	// Find first and last changed lines
	firstChanged := -1
	lastChanged := -1
	maxLines := maxInt(len(newLines), len(m.previousLines))
	for i := 0; i < maxLines; i++ {
		oldLine := ""
		if i < len(m.previousLines) {
			oldLine = m.previousLines[i]
		}
		newLine := ""
		if i < len(newLines) {
			newLine = newLines[i]
		}

		if oldLine != newLine {
			if firstChanged == -1 {
				firstChanged = i
			}
			lastChanged = i
		}
	}
	appendedLines := len(newLines) > len(m.previousLines)
	if appendedLines {
		if firstChanged == -1 {
			firstChanged = len(m.previousLines)
		}
		lastChanged = len(newLines) - 1
	}
	if firstChanged != -1 {
		firstChanged, lastChanged = m.expandChangedRangeForKittyImages(firstChanged, lastChanged, newLines)
	}
	appendStart := appendedLines && firstChanged == len(m.previousLines) && firstChanged > 0

	// No changes - but still need to update hardware cursor position if it moved
	if firstChanged == -1 {
		m.positionHardwareCursorLocked(cursorPos, len(newLines))
		m.previousViewportTop = prevViewportTop
		m.previousHeight = height
		return nil
	}

	// All changes are in deleted lines (nothing to render, just clear)
	if firstChanged >= len(newLines) {
		if len(m.previousLines) > len(newLines) {
			output := newBoundedTerminalWriter(m.terminal.Write)
			output.append("\x1b[?2026h")
			output.append(m.deleteChangedKittyImages(firstChanged, lastChanged))
			// Move to end of new content (clamp to 0 for empty content)
			targetRow := maxInt(0, len(newLines)-1)
			if targetRow < prevViewportTop {
				logRedraw(fmt.Sprintf("deleted lines moved viewport up (%d < %d)", targetRow, prevViewportTop))
				fullRender(true)
				return nil
			}
			lineDiff := computeLineDiff(targetRow)
			if lineDiff > 0 {
				output.append(fmt.Sprintf("\x1b[%dB", lineDiff))
			} else if lineDiff < 0 {
				output.append(fmt.Sprintf("\x1b[%dA", -lineDiff))
			}
			output.append("\r")
			// Clear extra lines without scrolling
			extraLines := len(m.previousLines) - len(newLines)
			if extraLines > height {
				logRedraw(fmt.Sprintf("extraLines > height (%d > %d)", extraLines, height))
				fullRender(true)
				return nil
			}
			clearStartOffset := 0
			if len(newLines) != 0 {
				clearStartOffset = 1
			}
			if extraLines > 0 && clearStartOffset > 0 {
				output.append(fmt.Sprintf("\x1b[%dB", clearStartOffset))
			}
			for i := 0; i < extraLines; i++ {
				output.append("\r\x1b[2K")
				if i < extraLines-1 {
					output.append("\x1b[1B")
				}
			}
			moveBack := maxInt(0, extraLines-1+clearStartOffset)
			if moveBack > 0 {
				output.append(fmt.Sprintf("\x1b[%dA", moveBack))
			}
			output.append("\x1b[?2026l")
			output.flush()
			m.cursorRow = targetRow
			m.hardwareCursorRow = targetRow
		}
		m.positionHardwareCursorLocked(cursorPos, len(newLines))
		m.previousLines = newLines
		m.previousKittyImageIds = m.collectKittyImageIds(newLines)
		m.previousWidth = width
		m.previousHeight = height
		m.previousViewportTop = prevViewportTop
		return nil
	}

	// Differential rendering can only touch what was actually visible. If the
	// first changed line is above the previous viewport, the changed rows sit
	// in scrollback — repainting them is neither possible nor necessary.
	//
	// pig fix for the "terminal jumps to top then replays to bottom" bug (pi
	// issue #9255, still open upstream; pi's TuiMainScreen does fullRender(true)
	// here unconditionally, which on long transcripts with a live block taller
	// than the viewport wipes screen + scrollback and repaints the ENTIRE
	// buffer 3-5x per second). pig-only deviation from pi, recorded in
	// docs/fix/2026-09-13-main-screen-viewport-redraw-jump.md.
	if firstChanged < prevViewportTop {
		if lastChanged < prevViewportTop {
			// Every changed row is above the viewport — nothing on screen
			// differs; delete any fully-scrollback kitty image blocks (not
			// cursor-bound) and update bookkeeping without painting.
			logRedraw(fmt.Sprintf("all changes above viewportTop (%d..%d < %d)", firstChanged, lastChanged, prevViewportTop))
			if deleted := m.deleteChangedKittyImages(firstChanged, lastChanged); deleted != "" {
				output := newBoundedTerminalWriter(m.terminal.Write)
				output.append("\x1b[?2026h")
				output.append(deleted)
				output.append("\x1b[?2026l")
				output.flush()
			}
			m.previousLines = newLines
			m.previousKittyImageIds = m.collectKittyImageIds(newLines)
			m.previousWidth = width
			m.previousHeight = height
			m.cursorRow = maxInt(0, len(newLines)-1)
			m.maxLinesRendered = maxInt(m.maxLinesRendered, len(newLines))
			m.positionHardwareCursorLocked(cursorPos, len(newLines))
			m.previousViewportTop = prevViewportTop
			return nil
		}
		if len(newLines) >= len(m.previousLines) {
			// Stable or growing content with changes spanning into the
			// viewport: repaint the visible range only, starting at the
			// viewport top. Rows above stay stale in scrollback (a full
			// render would erase the scrollback entirely, which is worse).
			// appendStart is unreachable here: pure appends have
			// firstChanged == len(previousLines) > prevViewportTop.
			logRedraw(fmt.Sprintf("clamping firstChanged to viewportTop (%d < %d)", firstChanged, prevViewportTop))
			firstChanged = prevViewportTop
		} else {
			// Content shrank while changes start above the viewport: the
			// viewport itself must slide up, which relative-only painting
			// cannot express — a full repaint is required (rare: large
			// blocks collapsing, e.g. finished tool results).
			logRedraw(fmt.Sprintf("firstChanged < viewportTop with shrink (%d < %d)", firstChanged, prevViewportTop))
			fullRender(true)
			return nil
		}
	}

	// Render from first changed line to end
	// Keep updates wrapped in synchronized output while writing bounded chunks.
	output := newBoundedTerminalWriter(m.terminal.Write)
	output.append("\x1b[?2026h") // Begin synchronized output
	output.append(m.deleteChangedKittyImages(firstChanged, lastChanged))
	prevViewportBottom := prevViewportTop + height - 1
	moveTargetRow := firstChanged
	if appendStart {
		moveTargetRow = firstChanged - 1
	}
	if moveTargetRow > prevViewportBottom {
		currentScreenRow := maxInt(0, minInt(height-1, hardwareCursorRow-prevViewportTop))
		moveToBottom := height - 1 - currentScreenRow
		if moveToBottom > 0 {
			output.append(fmt.Sprintf("\x1b[%dB", moveToBottom))
		}
		scroll := moveTargetRow - prevViewportBottom
		output.append(strings.Repeat("\r\n", scroll))
		prevViewportTop += scroll
		viewportTop += scroll
		hardwareCursorRow = moveTargetRow
	}

	// Move cursor to first changed line (use hardwareCursorRow for actual position)
	lineDiff := computeLineDiff(moveTargetRow)
	if lineDiff > 0 {
		output.append(fmt.Sprintf("\x1b[%dB", lineDiff)) // Move down
	} else if lineDiff < 0 {
		output.append(fmt.Sprintf("\x1b[%dA", -lineDiff)) // Move up
	}

	if appendStart {
		output.append("\r\n") // Move to column 0
	} else {
		output.append("\r")
	}

	// Only render changed lines (firstChanged to lastChanged), not all lines to end
	// This reduces flicker when only a single line changes (e.g., spinner animation)
	renderEnd := minInt(lastChanged, len(newLines)-1)
	for i := firstChanged; i <= renderEnd; i++ {
		if i > firstChanged {
			output.append("\r\n")
		}
		line := newLines[i]
		isImage := IsImageLine(line)
		imageReservedRows := 1
		if isImage {
			imageReservedRows = m.getKittyImageReservedRows(newLines, i, renderEnd)
		}
		if imageReservedRows > 1 {
			imageStartScreenRow := i - viewportTop
			if imageStartScreenRow < 0 || imageStartScreenRow+imageReservedRows > height {
				logRedraw(fmt.Sprintf("kitty image pre-clear would scroll (%d + %d > %d)", imageStartScreenRow, imageReservedRows, height))
				fullRender(true)
				return nil
			}

			output.append("\x1b[2K")
			for row := 1; row < imageReservedRows; row++ {
				output.append("\r\n\x1b[2K")
			}
			output.append(fmt.Sprintf("\x1b[%dA", imageReservedRows-1))
			output.append(line)
			output.append(fmt.Sprintf("\x1b[%dB", imageReservedRows-1))
			i += imageReservedRows - 1
			continue
		}

		output.append("\x1b[2K") // Clear current line
		if !isImage && VisibleWidth(line) > width {
			// Log all lines to crash file for debugging
			crashLogPath := filepath.Join(m.logDirectory, "pi-tui-crash.log")
			if m.logDirectory == "" {
				crashLogPath = filepath.Join(os.TempDir(), "pi-tui-crash.log")
			}
			var crashData strings.Builder
			crashData.WriteString(fmt.Sprintf("Crash at %s\n", time.Now().UTC().Format("2006-01-02T15:04:05.000Z")))
			crashData.WriteString(fmt.Sprintf("Terminal width: %d\n", width))
			crashData.WriteString(fmt.Sprintf("Line %d visible width: %d\n", i, VisibleWidth(line)))
			crashData.WriteString("\n=== All rendered lines ===\n")
			for idx, l := range newLines {
				crashData.WriteString(fmt.Sprintf("[%d] (w=%d) %s\n", idx, VisibleWidth(l), l))
			}
			_ = os.MkdirAll(filepath.Dir(crashLogPath), 0o755)
			_ = os.WriteFile(crashLogPath, []byte(crashData.String()), 0o644)

			// Clean up terminal state before failing
			m.stopped = true
			m.cancelRenderTimerLocked()
			m.terminal.ShowCursor()
			m.terminal.Stop()

			return fmt.Errorf("Rendered line %d exceeds terminal width (%d > %d).\n\n"+
				"This is likely caused by a custom TUI component not truncating its output.\n"+
				"Use visibleWidth() to measure and truncateToWidth() to truncate lines.\n\n"+
				"Debug log written to: %s",
				i, VisibleWidth(line), width, crashLogPath)
		}
		output.append(line)
	}

	// Track where cursor ended up after rendering
	finalCursorRow := renderEnd

	// If we had more lines before, clear them and move cursor back
	if len(m.previousLines) > len(newLines) {
		// Move to end of new content first if we stopped before it
		if renderEnd < len(newLines)-1 {
			moveDown := len(newLines) - 1 - renderEnd
			output.append(fmt.Sprintf("\x1b[%dB", moveDown))
			finalCursorRow = len(newLines) - 1
		}
		extraLines := len(m.previousLines) - len(newLines)
		for i := len(newLines); i < len(m.previousLines); i++ {
			output.append("\r\n\x1b[2K")
		}
		// Move cursor back to end of new content
		output.append(fmt.Sprintf("\x1b[%dA", extraLines))
	}

	output.append("\x1b[?2026l") // End synchronized output

	if os.Getenv("PI_TUI_DEBUG") == "1" {
		debugDir := "/tmp/tui"
		_ = os.MkdirAll(debugDir, 0o755)
		debugPath := filepath.Join(debugDir, fmt.Sprintf("render-%d-%s.log", time.Now().UnixMilli(), randomToken()))
		var debugData strings.Builder
		debugData.WriteString(fmt.Sprintf("firstChanged: %d\nviewportTop: %d\ncursorRow: %d\nheight: %d\nlineDiff: %d\nhardwareCursorRow: %d\nrenderEnd: %d\nfinalCursorRow: %d\ncursorPos: %s\nnewLines.length: %d\npreviousLines.length: %d\n",
			firstChanged, viewportTop, m.cursorRow, height, lineDiff, hardwareCursorRow, renderEnd, finalCursorRow, cursorPosString(cursorPos), len(newLines), len(m.previousLines)))
		debugData.WriteString(fmt.Sprintf("\n=== buffer ===\n[%d chars written in bounded chunks]\n", output.length()))
		_ = os.WriteFile(debugPath, []byte(debugData.String()), 0o644)
	}

	output.flush()

	// Track cursor position for next render
	// cursorRow tracks end of content (for viewport calculation)
	// hardwareCursorRow tracks actual terminal cursor position (for movement)
	m.cursorRow = maxInt(0, len(newLines)-1)
	m.hardwareCursorRow = finalCursorRow
	// Track terminal's working area (grows but doesn't shrink unless cleared)
	m.maxLinesRendered = maxInt(m.maxLinesRendered, len(newLines))
	m.previousViewportTop = maxInt(prevViewportTop, finalCursorRow-height+1)

	// Position hardware cursor for IME
	m.positionHardwareCursorLocked(cursorPos, len(newLines))

	m.previousLines = newLines
	m.previousKittyImageIds = m.collectKittyImageIds(newLines)
	m.previousWidth = width
	m.previousHeight = height
	return nil
}

// positionHardwareCursorLocked positions the hardware cursor for IME
// candidate window.
func (m *TuiMainScreen) positionHardwareCursorLocked(cursorPos *CursorPosition, totalLines int) {
	if cursorPos == nil || totalLines <= 0 {
		m.terminal.HideCursor()
		return
	}

	// Clamp cursor position to valid range
	targetRow := maxInt(0, minInt(cursorPos.Row, totalLines-1))
	targetCol := maxInt(0, cursorPos.Col)

	// Move cursor from current position to target
	rowDelta := targetRow - m.hardwareCursorRow
	var buffer strings.Builder
	if rowDelta > 0 {
		buffer.WriteString(fmt.Sprintf("\x1b[%dB", rowDelta)) // Move down
	} else if rowDelta < 0 {
		buffer.WriteString(fmt.Sprintf("\x1b[%dA", -rowDelta)) // Move up
	}
	// Move to absolute column (1-indexed)
	buffer.WriteString(fmt.Sprintf("\x1b[%dG", targetCol+1))

	if buffer.Len() > 0 {
		m.terminal.Write(buffer.String())
	}

	m.hardwareCursorRow = targetRow
	if m.showHardwareCursor {
		m.terminal.ShowCursor()
	} else {
		m.terminal.HideCursor()
	}
}

func cursorPosString(pos *CursorPosition) string {
	if pos == nil {
		return "null"
	}
	return fmt.Sprintf("{\"row\":%d,\"col\":%d}", pos.Row, pos.Col)
}

func randomToken() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
