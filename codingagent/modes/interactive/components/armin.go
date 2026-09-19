// armin.go (pi: components/armin.ts) — "Armin says hi!" easter egg with
// animated half-block XBM art and 7 random effects.
//
// Concurrency adaptation (declared): pi uses setInterval on the JS event
// loop; pig runs a ticker goroutine with panic containment
// (docs/feat/PANIC-RESILIENCE.md). The animation self-stops when its effect
// completes (pi: stopAnimation from the interval callback); Dispose is
// idempotent and optional.
package components

import (
	"math/rand"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
	"github.com/ZoroSpace/pig/utils"
)

// XBM image: 31x36 pixels, LSB first, 1=background, 0=foreground
const (
	arminWidth  = 31
	arminHeight = 36
)

var arminBits = []byte{
	0xff, 0xff, 0xff, 0x7f, 0xff, 0xf0, 0xff, 0x7f, 0xff, 0xed, 0xff, 0x7f, 0xff, 0xdb, 0xff, 0x7f, 0xff, 0xb7, 0xff,
	0x7f, 0xff, 0x77, 0xfe, 0x7f, 0x3f, 0xf8, 0xfe, 0x7f, 0xdf, 0xff, 0xfe, 0x7f, 0xdf, 0x3f, 0xfc, 0x7f, 0x9f, 0xc3,
	0xfb, 0x7f, 0x6f, 0xfc, 0xf4, 0x7f, 0xf7, 0x0f, 0xf7, 0x7f, 0xf7, 0xff, 0xf7, 0x7f, 0xf7, 0xff, 0xe3, 0x7f, 0xf7,
	0x07, 0xe8, 0x7f, 0xef, 0xf8, 0x67, 0x70, 0x0f, 0xff, 0xbb, 0x6f, 0xf1, 0x00, 0xd0, 0x5b, 0xfd, 0x3f, 0xec, 0x53,
	0xc1, 0xff, 0xef, 0x57, 0x9f, 0xfd, 0xee, 0x5f, 0x9f, 0xfc, 0xae, 0x5f, 0x1f, 0x78, 0xac, 0x5f, 0x3f, 0x00, 0x50,
	0x6c, 0x7f, 0x00, 0xdc, 0x77, 0xff, 0xc0, 0x3f, 0x78, 0xff, 0x01, 0xf8, 0x7f, 0xff, 0x03, 0x9c, 0x78, 0xff, 0x07,
	0x8c, 0x7c, 0xff, 0x0f, 0xce, 0x78, 0xff, 0xff, 0xcf, 0x7f, 0xff, 0xff, 0xcf, 0x78, 0xff, 0xff, 0xdf, 0x78, 0xff,
	0xff, 0xdf, 0x7d, 0xff, 0xff, 0x3f, 0x7e, 0xff, 0xff, 0xff, 0x7f,
}

const arminBytesPerRow = (arminWidth + 7) / 8

const arminDisplayHeight = (arminHeight + 1) / 2 // Half-block rendering

type arminEffect string

const (
	arminEffectTypewriter arminEffect = "typewriter"
	arminEffectScanline   arminEffect = "scanline"
	arminEffectRain       arminEffect = "rain"
	arminEffectFade       arminEffect = "fade"
	arminEffectCrt        arminEffect = "crt"
	arminEffectGlitch     arminEffect = "glitch"
	arminEffectDissolve   arminEffect = "dissolve"
)

var arminEffects = []arminEffect{
	arminEffectTypewriter, arminEffectScanline, arminEffectRain, arminEffectFade,
	arminEffectCrt, arminEffectGlitch, arminEffectDissolve,
}

// arminGetPixel gets the pixel at (x, y): true = foreground, false = background
func arminGetPixel(x int, y int) bool {
	if y >= arminHeight {
		return false
	}
	byteIndex := y*arminBytesPerRow + x/8
	bitIndex := x % 8
	return (arminBits[byteIndex]>>bitIndex)&1 == 0
}

// arminGetChar gets the character for a cell (2 vertical pixels packed)
func arminGetChar(x int, row int) string {
	upper := arminGetPixel(x, row*2)
	lower := arminGetPixel(x, row*2+1)
	if upper && lower {
		return "█"
	}
	if upper {
		return "▀"
	}
	if lower {
		return "▄"
	}
	return " "
}

// arminBuildFinalGrid builds the final image grid
func arminBuildFinalGrid() [][]string {
	grid := make([][]string, arminDisplayHeight)
	for row := 0; row < arminDisplayHeight; row++ {
		line := make([]string, arminWidth)
		for x := 0; x < arminWidth; x++ {
			line[x] = arminGetChar(x, row)
		}
		grid[row] = line
	}
	return grid
}

type arminDrop struct {
	y       int
	settled int
}

// ArminComponent is the animated easter-egg art (pi: class ArminComponent).
type ArminComponent struct {
	mu            sync.Mutex
	ui            tui.TUI
	stopCh        chan struct{}
	stopped       bool
	effect        arminEffect
	finalGrid     [][]string
	currentGrid   [][]string
	effectState   any
	cachedLines   []string
	cachedWidth   int
	gridVersion   int
	cachedVersion int
}

// NewArminComponent builds the component and starts the animation (pi:
// constructor).
func NewArminComponent(ui tui.TUI) *ArminComponent {
	a := &ArminComponent{
		ui:          ui,
		stopCh:      make(chan struct{}),
		effect:      arminEffects[rand.Intn(len(arminEffects))],
		finalGrid:   arminBuildFinalGrid(),
		currentGrid: make([][]string, arminDisplayHeight),
	}
	for row := range a.currentGrid {
		a.currentGrid[row] = make([]string, arminWidth)
		for x := range a.currentGrid[row] {
			a.currentGrid[row][x] = " "
		}
	}

	a.initEffect()
	a.startAnimation()
	return a
}

// Invalidate clears the render cache (tui.Component).
func (a *ArminComponent) Invalidate() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cachedWidth = 0
}

// Render renders the current grid plus the message line (tui.Component).
func (a *ArminComponent) Render(width int) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if width == a.cachedWidth && a.cachedVersion == a.gridVersion {
		return a.cachedLines
	}

	const padding = 1
	availableWidth := width - padding

	a.cachedLines = make([]string, 0, len(a.currentGrid)+1)
	for _, row := range a.currentGrid {
		// Clip row to available width before applying color
		clipped := ""
		for i, cell := range row {
			if i >= availableWidth {
				break
			}
			clipped += cell
		}
		padRight := max(0, width-padding-len(clipped))
		a.cachedLines = append(a.cachedLines, " "+theme.Fg("accent", clipped)+spaces(padRight))
	}

	// Add "ARMIN SAYS HI" at the end
	message := "ARMIN SAYS HI"
	msgPadRight := max(0, width-padding-len(message))
	a.cachedLines = append(a.cachedLines, " "+theme.Fg("accent", message)+spaces(msgPadRight))

	a.cachedWidth = width
	a.cachedVersion = a.gridVersion

	return a.cachedLines
}

func spaces(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

func (a *ArminComponent) initEffect() {
	switch a.effect {
	case arminEffectTypewriter:
		a.effectState = &arminTypewriterState{pos: 0}
	case arminEffectScanline:
		a.effectState = &arminScanlineState{row: 0}
	case arminEffectRain:
		// Track falling position for each column
		drops := make([]arminDrop, arminWidth)
		for i := range drops {
			drops[i] = arminDrop{y: -rand.Intn(arminDisplayHeight * 2), settled: 0}
		}
		a.effectState = &arminRainState{drops: drops}
	case arminEffectFade:
		// Shuffle all pixel positions
		positions := a.shufflePositions()
		a.effectState = &arminProgressState{positions: positions, idx: 0}
	case arminEffectCrt:
		a.effectState = &arminCrtState{expansion: 0}
	case arminEffectGlitch:
		a.effectState = &arminGlitchState{phase: 0, glitchFrames: 8}
	case arminEffectDissolve:
		// Start with random noise
		chars := []string{" ", "░", "▒", "▓", "█", "▀", "▄"}
		for row := range a.currentGrid {
			for x := range a.currentGrid[row] {
				a.currentGrid[row][x] = chars[rand.Intn(len(chars))]
			}
		}
		// Shuffle positions for gradual resolve
		a.effectState = &arminProgressState{positions: a.shufflePositions(), idx: 0}
	}
}

func (a *ArminComponent) shufflePositions() [][2]int {
	positions := make([][2]int, 0, arminDisplayHeight*arminWidth)
	for row := 0; row < arminDisplayHeight; row++ {
		for x := 0; x < arminWidth; x++ {
			positions = append(positions, [2]int{row, x})
		}
	}
	// Fisher-Yates shuffle
	for i := len(positions) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		positions[i], positions[j] = positions[j], positions[i]
	}
	return positions
}

func (a *ArminComponent) startAnimation() {
	fps := 30
	if a.effect == arminEffectGlitch {
		fps = 60
	}
	interval := time.Second / time.Duration(fps)
	go func() {
		// pig panic containment (see docs/feat/PANIC-RESILIENCE.md).
		defer utils.RecoverPanic("components.armin")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-a.stopCh:
				return
			case <-ticker.C:
				// tick 写 currentGrid/effectState 必须持锁（pig 并发面：pi 是
				// 单线程事件循环无此问题）：Render 在锁内读网格，动画 goroutine
				// 在锁外写会被 -race 逮到（2026-09-19 全量回归实况）。
				a.mu.Lock()
				done := a.tickEffect()
				a.gridVersion++
				a.mu.Unlock()
				if a.ui != nil {
					a.ui.RequestRender(false)
				}
				if done {
					a.stopAnimation()
					return
				}
			}
		}
	}()
}

func (a *ArminComponent) stopAnimation() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.stopped {
		a.stopped = true
		close(a.stopCh)
	}
}

// Dispose stops the animation (pi: dispose). Safe to call twice.
func (a *ArminComponent) Dispose() {
	a.stopAnimation()
}

func (a *ArminComponent) tickEffect() bool {
	switch a.effect {
	case arminEffectTypewriter:
		return a.tickTypewriter()
	case arminEffectScanline:
		return a.tickScanline()
	case arminEffectRain:
		return a.tickRain()
	case arminEffectFade:
		return a.tickProgress(15)
	case arminEffectCrt:
		return a.tickCrt()
	case arminEffectGlitch:
		return a.tickGlitch()
	case arminEffectDissolve:
		return a.tickProgress(20)
	default:
		return true
	}
}

type arminTypewriterState struct{ pos int }

func (a *ArminComponent) tickTypewriter() bool {
	state := a.effectState.(*arminTypewriterState)
	const pixelsPerFrame = 3

	for i := 0; i < pixelsPerFrame; i++ {
		row := state.pos / arminWidth
		x := state.pos % arminWidth
		if row >= arminDisplayHeight {
			return true
		}
		a.currentGrid[row][x] = a.finalGrid[row][x]
		state.pos++
	}
	return false
}

type arminScanlineState struct{ row int }

func (a *ArminComponent) tickScanline() bool {
	state := a.effectState.(*arminScanlineState)
	if state.row >= arminDisplayHeight {
		return true
	}

	// Copy row
	for x := 0; x < arminWidth; x++ {
		a.currentGrid[state.row][x] = a.finalGrid[state.row][x]
	}
	state.row++
	return false
}

type arminRainState struct{ drops []arminDrop }

func (a *ArminComponent) tickRain() bool {
	state := a.effectState.(*arminRainState)

	allSettled := true
	for row := range a.currentGrid {
		a.currentGrid[row] = make([]string, arminWidth)
		for x := range a.currentGrid[row] {
			a.currentGrid[row][x] = " "
		}
	}

	for x := 0; x < arminWidth; x++ {
		drop := &state.drops[x]

		// Draw settled pixels
		for row := arminDisplayHeight - 1; row >= arminDisplayHeight-drop.settled; row-- {
			if row >= 0 {
				a.currentGrid[row][x] = a.finalGrid[row][x]
			}
		}

		// Check if this column is done
		if drop.settled >= arminDisplayHeight {
			continue
		}

		allSettled = false

		// Find the target row for this column (lowest non-space pixel)
		targetRow := -1
		for row := arminDisplayHeight - 1 - drop.settled; row >= 0; row-- {
			if a.finalGrid[row][x] != " " {
				targetRow = row
				break
			}
		}

		// Move drop down
		drop.y++

		// Draw falling drop
		if drop.y >= 0 && drop.y < arminDisplayHeight {
			if targetRow >= 0 && drop.y >= targetRow {
				// Settle
				drop.settled = arminDisplayHeight - targetRow
				drop.y = -rand.Intn(5) - 1
			} else {
				// Still falling
				a.currentGrid[drop.y][x] = "▓"
			}
		}
	}

	return allSettled
}

type arminProgressState struct {
	positions [][2]int
	idx       int
}

func (a *ArminComponent) tickProgress(pixelsPerFrame int) bool {
	state := a.effectState.(*arminProgressState)

	for i := 0; i < pixelsPerFrame; i++ {
		if state.idx >= len(state.positions) {
			return true
		}
		pos := state.positions[state.idx]
		a.currentGrid[pos[0]][pos[1]] = a.finalGrid[pos[0]][pos[1]]
		state.idx++
	}
	return false
}

type arminCrtState struct{ expansion int }

func (a *ArminComponent) tickCrt() bool {
	state := a.effectState.(*arminCrtState)
	midRow := arminDisplayHeight / 2

	for row := range a.currentGrid {
		a.currentGrid[row] = make([]string, arminWidth)
		for x := range a.currentGrid[row] {
			a.currentGrid[row][x] = " "
		}
	}

	// Draw from middle expanding outward
	top := midRow - state.expansion
	bottom := midRow + state.expansion

	for row := max(0, top); row <= min(arminDisplayHeight-1, bottom); row++ {
		for x := 0; x < arminWidth; x++ {
			a.currentGrid[row][x] = a.finalGrid[row][x]
		}
	}

	state.expansion++
	return state.expansion > arminDisplayHeight
}

type arminGlitchState struct {
	phase        int
	glitchFrames int
}

func (a *ArminComponent) tickGlitch() bool {
	state := a.effectState.(*arminGlitchState)

	if state.phase < state.glitchFrames {
		// Glitch phase: show corrupted version
		for row := range a.currentGrid {
			offset := rand.Intn(7) - 3
			glitchRow := make([]string, arminWidth)
			copy(glitchRow, a.finalGrid[row])

			// Random horizontal offset
			if rand.Float64() < 0.3 {
				shifted := make([]string, 0, arminWidth)
				for x := offset; x < arminWidth; x++ {
					shifted = append(shifted, glitchRow[x])
				}
				for x := 0; x < offset && x < arminWidth; x++ {
					shifted = append(shifted, glitchRow[x])
				}
				glitchRow = shifted[:arminWidth]
			}

			// Random vertical swap
			if rand.Float64() < 0.2 {
				swapRow := rand.Intn(arminDisplayHeight)
				copy(glitchRow, a.finalGrid[swapRow])
			}

			a.currentGrid[row] = glitchRow
		}
		state.phase++
		return false
	}

	// Final frame: show clean image
	for row := range a.currentGrid {
		cleanRow := make([]string, arminWidth)
		copy(cleanRow, a.finalGrid[row])
		a.currentGrid[row] = cleanRow
	}
	return true
}

// updateDisplay 已内联进 startAnimation 的 tick 分支（gridVersion 递增须与
// tickEffect 的网格写同锁保护，见循环内注释）。
