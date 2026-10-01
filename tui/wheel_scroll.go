// tui/wheel_scroll.go (pi: packages/tui/src/wheel-scroll.ts) — wheel event
// acceleration: converts wheel events into line counts, with `"auto"` mode
// following event velocity for terminals that send one event per notch.
package tui

import (
	"math"
	"os"
	"runtime"
	"time"
)

// WheelScrollLinesAuto is the sentinel for `"auto"`: lines per wheel event
// follow event velocity (pi: WheelScrollLines = number | "auto"). Fixed
// settings use 1-100; values below 1 behave as 1.
const WheelScrollLinesAuto = 0

const (
	// Several events closer than this belong to one physical notch (Ghostty
	// emits them ~4 ms apart) or come from a high-resolution source. They
	// move one line each and do not accelerate.
	wheelBurstGapMs = 5
	// A pause longer than this ends a scroll gesture.
	wheelGestureGapMs = 200
	// Average event gap that maps to one line per event. Faster events scale
	// up proportionally.
	wheelReferenceGapMs = 100
	wheelMaxAutoLines   = 6
)

// terminalAcceleratesWheel reports whether the terminal receives wheel and
// trackpad deltas that the OS has already accelerated, emitting one event per
// line: local macOS terminals. Other platforms, and SSH sessions where the
// client platform is unknown, usually send one event per wheel notch.
func terminalAcceleratesWheel() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	return os.Getenv("SSH_CONNECTION") == "" &&
		os.Getenv("SSH_CLIENT") == "" &&
		os.Getenv("SSH_TTY") == ""
}

// WheelScrollAccelerator converts wheel events into line counts (pi:
// WheelScrollAccelerator).
//
// In `"auto"` mode on terminals that do not accelerate wheel input, the count
// follows event velocity: an isolated notch moves one line, while a fast spin
// moves up to six lines per event. For example, notches 100 ms apart move 1
// line each, 50 ms apart move 2, and 20 ms apart move 5.
type WheelScrollAccelerator struct {
	lines      int // WheelScrollLinesAuto = auto
	accelerate bool

	lastTime      float64 // milliseconds; -Inf = none
	lastDirection int
	averageGap    float64
	hasAverageGap bool
	carry         float64
}

// NewWheelScrollAccelerator builds the accelerator. lines == 0 ("auto")
// accelerates unless the terminal accelerates wheel input itself.
func NewWheelScrollAccelerator(lines int) *WheelScrollAccelerator {
	return NewWheelScrollAcceleratorWithAcceleration(lines, !terminalAcceleratesWheel())
}

// NewWheelScrollAcceleratorWithAcceleration is the constructor with the
// acceleration override (pi's constructor second parameter; used by tests).
func NewWheelScrollAcceleratorWithAcceleration(lines int, accelerate bool) *WheelScrollAccelerator {
	return &WheelScrollAccelerator{
		lines:      lines,
		accelerate: accelerate,
		lastTime:   negInf(),
	}
}

func negInf() float64 {
	return math.Inf(-1)
}

// SetLines updates the lines setting and resets the gesture state (pi:
// setLines).
func (a *WheelScrollAccelerator) SetLines(lines int) {
	a.lines = lines
	a.reset()
}

// Next returns the positive line count for a wheel event in `direction` at
// time `now` (milliseconds) (pi: next).
func (a *WheelScrollAccelerator) Next(direction int, now float64) int {
	if a.lines != WheelScrollLinesAuto {
		if a.lines >= 1 {
			return a.lines
		}
		return 1
	}
	if !a.accelerate {
		return 1
	}

	gap := now - a.lastTime
	sameGesture := direction == a.lastDirection && gap <= wheelGestureGapMs
	a.lastTime = now
	a.lastDirection = direction
	if !sameGesture {
		a.averageGap = 0
		a.hasAverageGap = false
		a.carry = 0
		return 1
	}
	if gap < wheelBurstGapMs {
		return 1
	}

	if !a.hasAverageGap {
		a.averageGap = gap
		a.hasAverageGap = true
	} else {
		a.averageGap = (a.averageGap + gap) / 2
	}
	lines := wheelReferenceGapMs / a.averageGap
	if lines < 1 {
		lines = 1
	}
	if lines > wheelMaxAutoLines {
		lines = wheelMaxAutoLines
	}
	lines += a.carry
	whole := float64(int(lines))
	a.carry = lines - whole
	if whole < 1 {
		return 1
	}
	return int(whole)
}

// NowMillis returns the current wall clock in milliseconds (pi's callers use
// performance.now(); the seam keeps the unit explicit for tests).
func NowMillis() float64 {
	return float64(time.Now().UnixMilli())
}

func (a *WheelScrollAccelerator) reset() {
	a.lastTime = negInf()
	a.lastDirection = 0
	a.averageGap = 0
	a.hasAverageGap = false
	a.carry = 0
}
