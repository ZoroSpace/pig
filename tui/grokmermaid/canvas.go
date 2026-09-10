// Ported from pi node_modules/grok-mermaid/src/canvas.ts (grok-mermaid v0.2.2,
// Apache-2.0) as part of T5b-4. A grid of cells edges accumulate into, with
// direction bits resolved into box-drawing glyphs at finalize time.
package grokmermaid

import "strings"

// CONT is the sentinel occupying the trailing column of a wide glyph. Never
// emitted: the line builder skips it so a CJK character claims two cells of
// layout but contributes one character of output.
const CONT = "\x00"

// Connection direction bits, combined into a box-drawing glyph by maskChar.
const (
	BitU = 1
	BitD = 2
	BitL = 4
	BitR = 8
)

// Line styles, tracked per cell so crossing edges keep their own stroke.
const (
	StyDot   = 1
	StyThick = 2
	StySolid = 4
)

// Canvas is a grid of cells. Edges accumulate as direction bits rather than
// glyphs so that crossings and junctions resolve correctly whatever order they
// are drawn in; FinalizeMask turns the accumulated bits into characters at the
// end.
//
// Occupied marks cells claimed by a box, which edge bits must not overwrite.
type Canvas struct {
	w, h     int
	ch       []string
	cls      []Cls
	mask     []uint8
	style    []uint8
	occupied []uint8
	curStyle int
}

// NewCanvas creates a w×h canvas filled with blanks.
func NewCanvas(w, h int) *Canvas {
	n := w * h
	c := &Canvas{
		w:        w,
		h:        h,
		ch:       make([]string, n),
		cls:      make([]Cls, n),
		mask:     make([]uint8, n),
		style:    make([]uint8, n),
		occupied: make([]uint8, n),
		curStyle: StySolid,
	}
	for i := range c.ch {
		c.ch[i] = " "
		c.cls[i] = ClsNone
	}
	return c
}

// W and H report the canvas dimensions.
func (c *Canvas) W() int { return c.w }
func (c *Canvas) H() int { return c.h }

func (c *Canvas) idx(x, y int) int { return y*c.w + x }

// Set paints one cell.
func (c *Canvas) Set(x, y int, ch string, cls Cls) {
	if x >= c.w || y >= c.h {
		return
	}
	i := c.idx(x, y)
	c.ch[i] = ch
	c.cls[i] = cls
}

// AddBits accumulates direction bits on a free cell.
//
// cls is the class to claim the cell for; border cells are never
// reclassified, so a connector meeting a box keeps the box's styling.
func (c *Canvas) AddBits(x, y int, bits int, cls Cls) {
	if x >= c.w || y >= c.h {
		return
	}
	i := c.idx(x, y)
	if c.occupied[i] != 0 {
		return
	}
	c.mask[i] |= uint8(bits)
	c.style[i] |= uint8(c.curStyle)
	if c.cls[i] != ClsBorder {
		c.cls[i] = cls
	}
}

// Blit stamps a finished sub-canvas (a subgraph frame's contents) at an
// offset.
func (c *Canvas) Blit(sub *Canvas, ox, oy int) {
	for sy := 0; sy < sub.h; sy++ {
		for sx := 0; sx < sub.w; sx++ {
			x := ox + sx
			y := oy + sy
			if x >= c.w || y >= c.h {
				continue
			}
			si := sub.idx(sx, sy)
			di := c.idx(x, y)
			c.ch[di] = sub.ch[si]
			c.cls[di] = sub.cls[si]
			c.style[di] = sub.style[si]
			c.occupied[di] = 1
		}
	}
}

// Junction adds direction bits even to an occupied cell, so an edge can meet a
// border.
func (c *Canvas) Junction(x, y int, bits int) {
	if x >= c.w || y >= c.h {
		return
	}
	i := c.idx(x, y)
	c.mask[i] |= uint8(bits)
	if c.cls[i] != ClsBorder {
		c.cls[i] = ClsEdge
	}
}

// SegV draws a vertical segment (inclusive bounds).
func (c *Canvas) SegV(x, y0, y1 int) {
	a := min(y0, y1)
	b := max(y0, y1)
	for y := a; y <= b; y++ {
		bits := 0
		if y > a {
			bits |= BitU
		}
		if y < b {
			bits |= BitD
		}
		c.AddBits(x, y, bits, ClsEdge)
	}
}

// SegH draws a horizontal segment (inclusive bounds).
func (c *Canvas) SegH(y, x0, x1 int) {
	a := min(x0, x1)
	b := max(x0, x1)
	for x := a; x <= b; x++ {
		bits := 0
		if x > a {
			bits |= BitL
		}
		if x < b {
			bits |= BitR
		}
		c.AddBits(x, y, bits, ClsEdge)
	}
}

// FinalizeMask resolves accumulated direction bits into glyphs, honouring line
// style.
func (c *Canvas) FinalizeMask() {
	for i := range c.ch {
		if c.mask[i] != 0 && c.ch[i] == " " {
			ch := maskChar(int(c.mask[i]))
			switch c.style[i] {
			case StyDot:
				ch = dottedChar(ch)
			case StyThick:
				ch = thickChar(ch)
			}
			c.ch[i] = ch
		}
	}
}

// FlipVertical mirrors top-to-bottom for BT. Rows reorder but within-row text
// does not, so labels stay readable; box-drawing glyphs flip to match.
func (c *Canvas) FlipVertical() {
	for y := 0; y < c.h/2; y++ {
		y2 := c.h - 1 - y
		for x := 0; x < c.w; x++ {
			i := c.idx(x, y)
			j := c.idx(x, y2)
			c.ch[i], c.ch[j] = c.ch[j], c.ch[i]
			c.cls[i], c.cls[j] = c.cls[j], c.cls[i]
		}
	}
	for i := range c.ch {
		c.ch[i] = flipGlyphV(c.ch[i])
	}
}

// FlipHorizontal mirrors left-to-right for RL. Mirroring reverses each row, so
// after flipping glyphs each text/label run is reversed back to reading order.
func (c *Canvas) FlipHorizontal() {
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w/2; x++ {
			x2 := c.w - 1 - x
			i := c.idx(x, y)
			j := c.idx(x2, y)
			c.ch[i], c.ch[j] = c.ch[j], c.ch[i]
			c.cls[i], c.cls[j] = c.cls[j], c.cls[i]
		}
	}
	for i := range c.ch {
		c.ch[i] = flipGlyphH(c.ch[i])
	}
	for y := 0; y < c.h; y++ {
		x := 0
		for x < c.w {
			cls := c.cls[c.idx(x, y)]
			if cls == ClsText || cls == ClsEdgeLabel {
				start := c.idx(x, y)
				for x < c.w && c.cls[c.idx(x, y)] == cls {
					x++
				}
				end := c.idx(x, y)
				reverseSlice(c.ch, start, end)
			} else {
				x++
			}
		}
	}
}

// ToLines groups each row into runs of one class, dropping wide-glyph
// continuations.
func (c *Canvas) ToLines() (plain []string, styled [][]Span, width int) {
	plain = make([]string, 0, c.h)
	styled = make([][]Span, 0, c.h)
	for y := 0; y < c.h; y++ {
		// A trailing CONT counts as painted: it is the second cell of a wide
		// glyph, so the row really does reach that column.
		last := 0
		for x := c.w - 1; x >= 0; x-- {
			if c.ch[c.idx(x, y)] != " " {
				last = x + 1
				break
			}
		}
		width = max(width, last)
		var spans []Span
		var plainRow strings.Builder
		run := ""
		runCls := Cls(ClsNone)
		for x := 0; x < last; x++ {
			i := c.idx(x, y)
			ch := c.ch[i]
			if ch == CONT {
				continue
			}
			cls := c.cls[i]
			plainRow.WriteString(ch)
			if cls != runCls && run != "" {
				spans = append(spans, Span{Text: run, Cls: runCls})
				run = ""
			}
			runCls = cls
			run += ch
		}
		if run != "" {
			spans = append(spans, Span{Text: run, Cls: runCls})
		}
		styled = append(styled, spans)
		// Only ASCII spaces, which is all a blank cell ever holds. Trimming \s
		// would eat a trailing NBSP that styled keeps, desyncing the two.
		plain = append(plain, rightTrimSpaces(plainRow.String()))
	}
	first := 0
	for first < len(plain) && plain[first] == "" {
		first++
	}
	end := len(plain)
	for end > first && plain[end-1] == "" {
		end--
	}
	return plain[first:end], styled[first:end], width
}

// rightTrimSpaces trims trailing ASCII spaces only (pi: / +$/).
func rightTrimSpaces(s string) string {
	n := len(s)
	for n > 0 && s[n-1] == ' ' {
		n--
	}
	return s[:n]
}

func reverseSlice(arr []string, start, end int) {
	for i, j := start, end-1; i < j; i, j = i+1, j-1 {
		arr[i], arr[j] = arr[j], arr[i]
	}
}

// DrawText paints text at x, y, one grapheme cluster per cell.
//
// A wide cluster claims a second cell, marked with CONT so the line builder
// emits one character for it rather than a stray space.
func DrawText(canvas *Canvas, text string, x, y int, cls Cls) {
	cur := x
	for _, ch := range measured(text) {
		if ch.w == 0 {
			continue
		}
		canvas.Set(cur, y, ch.text, cls)
		for k := 1; k < ch.w; k++ {
			canvas.Set(cur+k, y, CONT, cls)
		}
		cur += ch.w
	}
}

// DrawTextOverEdges paints text at x, y, clearing any edge bits underneath
// first.
//
// Used where text sits on top of a drawn line (sequence messages, dividers,
// compartment rows) and must win over it.
func DrawTextOverEdges(canvas *Canvas, text string, x, y int, cls Cls) {
	cur := x
	for _, ch := range measured(text) {
		if ch.w == 0 {
			continue
		}
		for k := 0; k < ch.w; k++ {
			if cur+k < canvas.w && y < canvas.h {
				canvas.mask[canvas.idx(cur+k, y)] = 0
			}
			cell := CONT
			if k == 0 {
				cell = ch.text
			}
			canvas.Set(cur+k, y, cell, cls)
		}
		cur += ch.w
	}
}

// maskChar resolves a direction-bit mask into a box-drawing glyph.
func maskChar(mask int) string {
	switch mask {
	case 0:
		return " "
	case BitU, BitD, BitU | BitD:
		return "│"
	case BitL, BitR, BitL | BitR:
		return "─"
	case BitD | BitR:
		return "┌"
	case BitD | BitL:
		return "┐"
	case BitU | BitR:
		return "└"
	case BitU | BitL:
		return "┘"
	case BitU | BitD | BitR:
		return "├"
	case BitU | BitD | BitL:
		return "┤"
	case BitD | BitL | BitR:
		return "┬"
	case BitU | BitL | BitR:
		return "┴"
	default:
		return "┼"
	}
}

var dotted = map[string]string{"─": "╌", "│": "╎"}

var thick = map[string]string{
	"─": "━", "│": "┃", "┌": "┏", "┐": "┓", "└": "┗", "┘": "┛",
	"├": "┣", "┤": "┫", "┬": "┳", "┴": "┻", "┼": "╋",
}

var flipV = map[string]string{
	"┌": "└", "└": "┌", "┐": "┘", "┘": "┐",
	"┏": "┗", "┗": "┏", "┓": "┛", "┛": "┓",
	"╭": "╰", "╰": "╭", "╮": "╯", "╯": "╮",
	"┬": "┴", "┴": "┬", "┳": "┻", "┻": "┳",
	"▼": "▲", "▲": "▼", "▽": "△", "△": "▽",
}

var flipH = map[string]string{
	"┌": "┐", "┐": "┌", "└": "┘", "┘": "└",
	"┏": "┓", "┓": "┏", "┗": "┛", "┛": "┗",
	"╭": "╮", "╮": "╭", "╰": "╯", "╯": "╰",
	"├": "┤", "┤": "├", "┣": "┫", "┫": "┣",
	"▶": "◄", "◄": "▶", "▷": "◁", "◁": "▷",
}

func dottedChar(c string) string {
	if d, ok := dotted[c]; ok {
		return d
	}
	return c
}

func thickChar(c string) string {
	if d, ok := thick[c]; ok {
		return d
	}
	return c
}

func flipGlyphV(c string) string {
	if d, ok := flipV[c]; ok {
		return d
	}
	return c
}

func flipGlyphH(c string) string {
	if d, ok := flipH[c]; ok {
		return d
	}
	return c
}
