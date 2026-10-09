// tui/virtual_terminal_test.go — Go replacement for pi's test/virtual-terminal.ts
// (which wraps @xterm/headless). Implements the Terminal interface backed by a
// minimal VT emulator covering exactly the control sequences the TUI emits:
//
//	printable text (with autowrap + wide-char placeholders)
//	CSI A/B (cursor up/down), G (CHA), H (home), J (0/2/3 erase), K (erase line)
//	CSI ?2004h/l (bracketed paste), ?2026h/l (sync output), ?25l/h (cursor),
//	?2031h/l (color scheme), 16t (cell size query)
//	OSC ... BEL / OSC ... ST (titles, hyperlinks, color reports) — skipped
//	APC/DCS ... ST (cursor marker, Kitty graphics) — skipped
//	\r, \n (+ scroll at bottom), BEL
//
// Attribute tracking (SGR) is limited to italic/underline/bold/inverse —
// enough for applyLineResets assertions (pi uses cell.isItalic()).
package tui

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

type vtCell struct {
	ch        string // grapheme content; "" for wide-char continuation column
	italic    bool
	underline bool
	bold      bool
	inverse   bool
}

type vtLine struct {
	cells []vtCell
}

func (l *vtLine) width() int { return len(l.cells) }

func (l *vtLine) ensure(width int) {
	for len(l.cells) < width {
		l.cells = append(l.cells, vtCell{})
	}
}

func (l *vtLine) text() string {
	var b strings.Builder
	for _, c := range l.cells {
		b.WriteString(c.ch)
	}
	return b.String()
}

func (l *vtLine) eraseTo(from int) {
	l.ensure(from)
	for i := from; i < len(l.cells); i++ {
		l.cells[i] = vtCell{}
	}
}

func (l *vtLine) eraseAll() {
	for i := range l.cells {
		l.cells[i] = vtCell{}
	}
}

// VirtualTerminal is the in-memory terminal used by render tests.
type VirtualTerminal struct {
	mu            sync.Mutex
	cols          int
	rows          int
	buf           []vtLine
	viewportY     int
	cursorX       int
	cursorY       int // absolute buffer row
	lastCol       bool
	cursorVisible bool

	italic    bool
	underline bool
	bold      bool
	inverse   bool

	inputHandler  func(data string)
	resizeHandler func()

	// writes records raw output for LoggingVirtualTerminal-style assertions.
	writes []string
}

// NewVirtualTerminal creates a virtual terminal with the given dimensions.
func NewVirtualTerminal(columns, rows int) *VirtualTerminal {
	v := &VirtualTerminal{cols: columns, rows: rows, cursorVisible: true}
	for i := 0; i < rows; i++ {
		v.buf = append(v.buf, vtLine{cells: make([]vtCell, columns)})
	}
	return v
}

// --- Terminal interface ------------------------------------------------------

func (v *VirtualTerminal) Start(onInput func(data string), onResize func()) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.inputHandler = onInput
	v.resizeHandler = onResize
	// Enable bracketed paste mode for consistency with ProcessTerminal
	v.writeLocked("\x1b[?2004h")
}

func (v *VirtualTerminal) DrainInput(maxMs int, idleMs int) {
	// No-op for virtual terminal - no stdin to drain
}

func (v *VirtualTerminal) Stop() {
	v.mu.Lock()
	defer v.mu.Unlock()
	// Disable bracketed paste mode
	v.writeLocked("\x1b[?2004l")
	v.inputHandler = nil
	v.resizeHandler = nil
}

func (v *VirtualTerminal) Write(data string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.writeLocked(data)
}

func (v *VirtualTerminal) writeLocked(data string) {
	v.writes = append(v.writes, data)
	v.emulateLocked(data)
}

func (v *VirtualTerminal) Columns() int { return v.cols }
func (v *VirtualTerminal) Rows() int    { return v.rows }
func (v *VirtualTerminal) KittyProtocolActive() bool {
	// Virtual terminal always reports Kitty protocol as active for testing
	return true
}

func (v *VirtualTerminal) MoveBy(lines int) {
	if lines > 0 {
		v.Write("\x1b[" + strconv.Itoa(lines) + "B")
	} else if lines < 0 {
		v.Write("\x1b[" + strconv.Itoa(-lines) + "A")
	}
}

func (v *VirtualTerminal) HideCursor() { v.Write("\x1b[?25l") }
func (v *VirtualTerminal) ShowCursor() { v.Write("\x1b[?25h") }
func (v *VirtualTerminal) ClearLine()  { v.Write("\x1b[K") }
func (v *VirtualTerminal) ClearFromCursor() {
	v.Write("\x1b[J")
}
func (v *VirtualTerminal) ClearScreen() {
	v.Write("\x1b[2J\x1b[H")
}
func (v *VirtualTerminal) SetTitle(title string) {
	v.Write("\x1b]0;" + title + "\x07")
}
func (v *VirtualTerminal) SetProgress(active bool) {}

func (v *VirtualTerminal) SetProgramStatus(status ProgramStatus) {}

// --- Test-specific methods (pi: same names) ---------------------------------

// SendInput simulates keyboard input.
func (v *VirtualTerminal) SendInput(data string) {
	v.mu.Lock()
	handler := v.inputHandler
	v.mu.Unlock()
	if handler != nil {
		handler(data)
	}
}

// Resize changes the terminal dimensions and fires the resize handler.
func (v *VirtualTerminal) Resize(columns, rows int) {
	v.mu.Lock()
	v.cols = columns
	v.rows = rows
	for i := range v.buf {
		v.buf[i].ensure(columns)
	}
	v.resizeLocked()
	v.mu.Unlock()
	v.mu.Lock()
	handler := v.resizeHandler
	v.mu.Unlock()
	if handler != nil {
		handler()
	}
}

func (v *VirtualTerminal) resizeLocked() {}

// translateToStringTrimRight mimics xterm translateToString(true): trailing
// cells are trimmed only while they are EMPTY or whitespace-with-default
// attributes. An attribute-carrying space (e.g. the inverse-video trailing
// space of a flash message) is content and must survive — the alt-screen
// flash test locks this (pi depends on the same xterm behavior).
func (v *VirtualTerminal) translateToStringTrimRight(line *vtLine) string {
	end := len(line.cells)
	for end > 0 {
		c := line.cells[end-1]
		if c.ch == "" {
			end--
			continue
		}
		if c.ch == " " && !c.italic && !c.underline && !c.bold && !c.inverse {
			end--
			continue
		}
		break
	}
	var b strings.Builder
	for i := 0; i < end; i++ {
		b.WriteString(line.cells[i].ch)
	}
	return b.String()
}

// GetViewport returns the visible viewport (right-trimmed lines,
// translateToString(true) semantics).
func (v *VirtualTerminal) GetViewport() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	lines := make([]string, 0, v.rows)
	for i := 0; i < v.rows; i++ {
		idx := v.viewportY + i
		if idx < len(v.buf) {
			lines = append(lines, v.translateToStringTrimRight(&v.buf[idx]))
		} else {
			lines = append(lines, "")
		}
	}
	return lines
}

// GetScrollBuffer returns the entire scroll buffer (right-trimmed).
func (v *VirtualTerminal) GetScrollBuffer() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	lines := make([]string, 0, len(v.buf))
	for i := range v.buf {
		lines = append(lines, v.translateToStringTrimRight(&v.buf[i]))
	}
	return lines
}

// GetCursorPosition returns the 0-based cursor position within the viewport.
func (v *VirtualTerminal) GetCursorPosition() (x, y int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.cursorX, v.cursorY - v.viewportY
}

// GetCursorVisible reports the hardware cursor visibility state.
func (v *VirtualTerminal) GetCursorVisible() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.cursorVisible
}

// GetCellItalic reports the italic attribute of a cell (pi: cell.isItalic()).
func (v *VirtualTerminal) GetCellItalic(row, col int) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	idx := v.viewportY + row
	if idx >= len(v.buf) || col >= len(v.buf[idx].cells) {
		return false
	}
	return v.buf[idx].cells[col].italic
}

// GetCellUnderline reports the underline attribute of a cell (pi:
// cell.isUnderline — consumed by the markdown style-leak tests).
func (v *VirtualTerminal) GetCellUnderline(row, col int) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	idx := v.viewportY + row
	if idx >= len(v.buf) || col >= len(v.buf[idx].cells) {
		return false
	}
	return v.buf[idx].cells[col].underline
}

// GetCellBold reports the bold attribute of a cell (pi: cell.isBold).
func (v *VirtualTerminal) GetCellBold(row, col int) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	idx := v.viewportY + row
	if idx >= len(v.buf) || col >= len(v.buf[idx].cells) {
		return false
	}
	return v.buf[idx].cells[col].bold
}

// GetWrites returns all raw output written so far, concatenated.
func (v *VirtualTerminal) GetWrites() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return strings.Join(v.writes, "")
}

// ClearWrites forgets recorded output (test convenience).
func (v *VirtualTerminal) ClearWrites() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.writes = nil
}

// WaitForRender waits for the TUI's throttled render pipeline to settle
// (pi: nextTick + 20ms + flush; the Go pipeline needs a real 16ms-throttle
// window plus goroutine scheduling margin).
func (v *VirtualTerminal) WaitForRender() {
	time.Sleep(35 * time.Millisecond)
}

// --- Emulator ----------------------------------------------------------------

func (v *VirtualTerminal) writeCharLocked(grapheme string, width int) {
	if v.lastCol {
		// Pending wrap: move to next line before writing.
		v.lastCol = false
		v.lineFeedLocked()
		v.cursorX = 0
	}
	row := &v.buf[v.cursorY]
	row.ensure(v.cursorX + 1)
	row.cells[v.cursorX] = vtCell{ch: grapheme, italic: v.italic, underline: v.underline, bold: v.bold, inverse: v.inverse}
	for i := 1; i < width; i++ {
		row.ensure(v.cursorX + i + 1)
		row.cells[v.cursorX+i] = vtCell{} // wide-char continuation (no ch)
	}
	v.cursorX += width
	if v.cursorX >= v.cols {
		v.cursorX = v.cols - 1
		v.lastCol = true
	}
}

func (v *VirtualTerminal) lineFeedLocked() {
	if v.cursorY == v.viewportY+v.rows-1 {
		// Scroll: top viewport line flows into scrollback implicitly (buffer
		// keeps it); append a new bottom line and advance the viewport.
		v.viewportY++
		v.cursorY++
		v.buf = append(v.buf, vtLine{cells: make([]vtCell, v.cols)})
	} else {
		v.cursorY++
		if v.cursorY >= len(v.buf) {
			v.buf = append(v.buf, vtLine{cells: make([]vtCell, v.cols)})
		}
	}
}

func (v *VirtualTerminal) emulateLocked(data string) {
	i := 0
	for i < len(data) {
		c := data[i]
		switch {
		case c == 0x1b:
			i = v.escapeSequenceLocked(data, i)
		case c == '\r':
			v.cursorX = 0
			v.lastCol = false
			i++
		case c == '\n':
			v.lineFeedLocked()
			v.lastCol = false
			i++
		case c == 0x07: // BEL (outside OSC already handled)
			i++
		case c == '\t':
			// advance to next tab stop (8)
			next := (v.cursorX/8 + 1) * 8
			if next < v.cols {
				v.cursorX = next
			}
			i++
		case c < 0x20:
			i++ // ignore other control chars
		default:
			// Printable: take one grapheme-ish chunk (rune + following zero-width
			// combining runes).
			width := 1
			seg, size := takeGrapheme(data[i:])
			if runeWidthForVT(seg) == 2 {
				width = 2
			}
			v.writeCharLocked(seg, width)
			i += size
		}
	}
}

// takeGrapheme extracts a base rune plus trailing combining marks.
func takeGrapheme(s string) (string, int) {
	runes := []rune(s)
	if len(runes) == 0 {
		return "", 0
	}
	size := len(string(runes[0]))
	out := string(runes[0])
	for len(runes) > 1 {
		r := runes[1]
		if isCombiningVT(r) {
			out += string(r)
			size += len(string(r))
			runes = runes[1:]
		} else {
			break
		}
	}
	return out, size
}

func isCombiningVT(r rune) bool {
	return (r >= 0x0300 && r <= 0x036F) || (r >= 0x1AB0 && r <= 0x1AFF) ||
		(r >= 0x20D0 && r <= 0x20FF) || (r >= 0xFE20 && r <= 0xFE2F)
}

func runeWidthForVT(seg string) int {
	// Wide ranges (East Asian W/F) — enough for the test content used here.
	for _, r := range seg {
		if (r >= 0x1100 && r <= 0x115F) || (r >= 0x2E80 && r <= 0xA4CF) ||
			(r >= 0xAC00 && r <= 0xD7A3) || (r >= 0xF900 && r <= 0xFAFF) ||
			(r >= 0xFE30 && r <= 0xFE6F) || (r >= 0xFF00 && r <= 0xFF60) ||
			(r >= 0xFFE0 && r <= 0xFFE6) || (r >= 0x1F300 && r <= 0x1F64F) ||
			(r >= 0x1F900 && r <= 0x1F9FF) || (r >= 0x20000 && r <= 0x3FFFD) {
			return 2
		}
		return 1
	}
	return 1
}

// escapeSequenceLocked handles one escape sequence starting at data[i] and
// returns the index after it.
func (v *VirtualTerminal) escapeSequenceLocked(data string, i int) int {
	if i+1 >= len(data) {
		return len(data)
	}
	switch data[i+1] {
	case '[':
		return v.csiLocked(data, i+2)
	case ']':
		return v.oscLocked(data, i+2)
	case 'P', '_', '^', 'X':
		// DCS/APC/PM/SOS ... terminated by ST (ESC \) or BEL
		j := i + 2
		for j < len(data) {
			if data[j] == 0x07 {
				return j + 1
			}
			if data[j] == 0x1b && j+1 < len(data) && data[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return len(data)
	case 'O':
		// SS3 — consume one final byte
		if i+2 < len(data) {
			return i + 3
		}
		return len(data)
	default:
		return i + 2 // two-byte escape
	}
}

// oscLocked skips an OSC payload (terminated by BEL or ST).
func (v *VirtualTerminal) oscLocked(data string, i int) int {
	j := i
	for j < len(data) {
		if data[j] == 0x07 {
			return j + 1
		}
		if data[j] == 0x1b && j+1 < len(data) && data[j+1] == '\\' {
			return j + 2
		}
		j++
	}
	return len(data)
}

// csiLocked interprets one CSI sequence; returns the index after the final
// byte, or len(data) when the sequence is incomplete (ignored).
func (v *VirtualTerminal) csiLocked(data string, i int) int {
	j := i
	// private marker
	private := byte(0)
	if j < len(data) && (data[j] == '?' || data[j] == '>' || data[j] == '<' || data[j] == '=') {
		private = data[j]
		j++
	}
	paramStart := j
	final := byte(0)
	for j < len(data) {
		c := data[j]
		if c >= 0x40 && c <= 0x7e {
			final = c
			break
		}
		if c < 0x20 || c > 0x3f {
			// malformed — abort sequence silently
			return j + 1
		}
		j++
	}
	if final == 0 {
		return len(data) // incomplete; drop
	}
	params := data[paramStart:j]
	_ = private

	switch final {
	case 'A': // cursor up
		n := csiParam(params, 0, 1)
		v.cursorY = maxVT(v.viewportY, v.cursorY-n)
		v.lastCol = false
	case 'B': // cursor down
		n := csiParam(params, 0, 1)
		v.cursorY = minVT(v.viewportY+v.rows-1, v.cursorY+n)
		v.ensureRowLocked(v.cursorY)
		v.lastCol = false
	case 'C': // cursor right
		n := csiParam(params, 0, 1)
		v.cursorX = minVT(v.cols-1, v.cursorX+n)
		v.lastCol = false
	case 'D': // cursor left
		n := csiParam(params, 0, 1)
		v.cursorX = maxVT(0, v.cursorX-n)
		v.lastCol = false
	case 'G': // CHA — cursor horizontal absolute (1-based)
		n := csiParam(params, 0, 1)
		v.cursorX = minVT(v.cols-1, maxVT(0, n-1))
		v.lastCol = false
	case 'H', 'f': // CUP — home (1-based row;col)
		rowN := csiParam(csiFirstParam(params), 0, 1)
		colN := csiParam(csiSecondParam(params), 0, 1)
		v.cursorY = v.viewportY + minVT(v.rows-1, maxVT(0, rowN-1))
		v.ensureRowLocked(v.cursorY)
		v.cursorX = minVT(v.cols-1, maxVT(0, colN-1))
		v.lastCol = false
	case 'J': // ED
		mode := csiParam(params, 0, 0)
		switch mode {
		case 0: // cursor to end of screen
			if v.cursorY < len(v.buf) {
				v.buf[v.cursorY].ensure(v.cursorX)
				for k := v.cursorX; k < len(v.buf[v.cursorY].cells); k++ {
					v.buf[v.cursorY].cells[k] = vtCell{}
				}
			}
			for r := v.cursorY + 1; r < v.viewportY+v.rows; r++ {
				v.ensureRowLocked(r)
				v.buf[r].eraseAll()
			}
		case 1: // start to cursor
			for r := v.viewportY; r < v.cursorY && r < len(v.buf); r++ {
				v.buf[r].eraseAll()
			}
			if v.cursorY < len(v.buf) {
				v.buf[v.cursorY].ensure(v.cursorX + 1)
				for k := 0; k <= v.cursorX && k < len(v.buf[v.cursorY].cells); k++ {
					v.buf[v.cursorY].cells[k] = vtCell{}
				}
			}
		case 2, 3: // entire viewport (3 also clears scrollback in xterm)
			v.buf = v.buf[:v.viewportY]
			for r := 0; r < v.rows; r++ {
				v.buf = append(v.buf, vtLine{cells: make([]vtCell, v.cols)})
			}
			v.cursorY = v.viewportY
		}
	case 'K': // EL — erase in line
		mode := csiParam(params, 0, 0)
		if v.cursorY < len(v.buf) {
			line := &v.buf[v.cursorY]
			line.ensure(v.cols)
			switch mode {
			case 0:
				line.eraseTo(v.cursorX)
			case 1:
				for k := 0; k <= v.cursorX && k < len(line.cells); k++ {
					line.cells[k] = vtCell{}
				}
			case 2:
				line.eraseAll()
			}
		}
	case 'h', 'l':
		// modes (?25 cursor, ?2004 paste, ?2026 sync, ?2031 scheme) — track cursor only
		if private == '?' && strings.Contains(params, "25") {
			v.cursorVisible = final == 'h'
		}
	case 'm':
		v.sgrLocked(params)
	case 'n', 't', 'c', 's', 'u', 'q':
		// reports / save-restore — ignored
	}
	return j + 1
}

func (v *VirtualTerminal) ensureRowLocked(row int) {
	for len(v.buf) <= row {
		v.buf = append(v.buf, vtLine{cells: make([]vtCell, v.cols)})
	}
}

func (v *VirtualTerminal) sgrLocked(params string) {
	if params == "" {
		params = "0"
	}
	parts := strings.Split(params, ";")
	for k := 0; k < len(parts); k++ {
		p := parts[k]
		if p == "" {
			p = "0"
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			continue
		}
		switch {
		case n == 0:
			v.italic, v.underline, v.bold, v.inverse = false, false, false, false
		case n == 1 || n == 2:
			v.bold = true
		case n == 3:
			v.italic = true
		case n == 4:
			v.underline = true
		case n == 7:
			v.inverse = true
		case n == 21 || n == 22:
			v.bold = false
		case n == 23:
			v.italic = false
		case n == 24:
			v.underline = false
		case n == 27:
			v.inverse = false
		case (n >= 30 && n <= 38) || (n >= 40 && n <= 48) || n == 39 || n == 49:
			// extended color params: 38;5;N / 38;2;R;G;B consume extra params
			if n == 38 || n == 48 {
				if k+1 < len(parts) {
					switch parts[k+1] {
					case "5":
						k += 2
					case "2":
						k += 5
					}
				}
			}
		}
	}
}

// csiParam parses the n-th (0-based) parameter in a parameter string,
// returning def when absent.
func csiParam(params string, index int, def int) int {
	parts := strings.Split(params, ";")
	if index >= len(parts) {
		return def
	}
	p := parts[index]
	if p == "" {
		return def
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return def
	}
	return n
}

func csiFirstParam(params string) string {
	if idx := strings.IndexByte(params, ';'); idx != -1 {
		return params[:idx]
	}
	return params
}

func csiSecondParam(params string) string {
	if idx := strings.IndexByte(params, ';'); idx != -1 {
		return params[idx+1:]
	}
	return ""
}

func maxVT(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minVT(a, b int) int {
	if a < b {
		return a
	}
	return b
}
