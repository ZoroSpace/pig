// tui/wheel_scroll_test.go (pi: test/wheel-scroll.test.ts, #9758) — wheel
// acceleration semantics. The accelerator override mirrors pi's constructor
// second parameter; the settings path clamps to 1-100 so fixed fractional
// line counts (pi allows 0.5) have no pig consumer.
package tui

import (
	"testing"
)

func scrollSequence(t *testing.T, a *WheelScrollAccelerator, times []float64, direction int) []int {
	t.Helper()
	out := make([]int, 0, len(times))
	for _, ts := range times {
		out = append(out, a.Next(direction, ts))
	}
	return out
}

func expectLines(t *testing.T, got, want []int) {
	t.Helper()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lines = %v, want %v", got, want)
		}
	}
}

func TestWheelScrollAccelerator(t *testing.T) {
	t.Run("uses fixed line counts regardless of timing", func(t *testing.T) {
		a := NewWheelScrollAcceleratorWithAcceleration(3, true)
		expectLines(t, scrollSequence(t, a, []float64{0, 10, 20, 1000}, 1), []int{3, 3, 3, 3})
		a.SetLines(1)
		if got := a.Next(1, 2000); got != 1 {
			t.Fatalf("Next after SetLines(1) = %d", got)
		}
	})

	t.Run("keeps one line per event in auto mode when the terminal already accelerates", func(t *testing.T) {
		a := NewWheelScrollAcceleratorWithAcceleration(WheelScrollLinesAuto, false)
		expectLines(t, scrollSequence(t, a, []float64{0, 10, 20, 30}, 1), []int{1, 1, 1, 1})
	})

	t.Run("scales auto mode with wheel velocity", func(t *testing.T) {
		a := NewWheelScrollAcceleratorWithAcceleration(WheelScrollLinesAuto, true)
		expectLines(t, scrollSequence(t, a, []float64{0, 150, 300, 450}, 1), []int{1, 1, 1, 1})
		expectLines(t, scrollSequence(t, a, []float64{1000, 1050, 1100, 1150}, 1), []int{1, 2, 2, 2})
		expectLines(t, scrollSequence(t, a, []float64{2000, 2020, 2040, 2060}, 1), []int{1, 5, 5, 5})
		expectLines(t, scrollSequence(t, a, []float64{3000, 3010, 3020, 3030}, 1), []int{1, 6, 6, 6})
	})

	t.Run("does not accelerate bursts of events for a single notch", func(t *testing.T) {
		a := NewWheelScrollAcceleratorWithAcceleration(WheelScrollLinesAuto, true)
		expectLines(t, scrollSequence(t, a, []float64{0, 3, 6, 9}, 1), []int{1, 1, 1, 1})
	})

	t.Run("resets acceleration on direction changes and pauses", func(t *testing.T) {
		a := NewWheelScrollAcceleratorWithAcceleration(WheelScrollLinesAuto, true)
		expectLines(t, scrollSequence(t, a, []float64{0, 20, 40}, 1), []int{1, 5, 5})
		if got := a.Next(-1, 60); got != 1 {
			t.Fatalf("direction change = %d, want 1", got)
		}
		expectLines(t, scrollSequence(t, a, []float64{500, 520}, 1), []int{1, 5})
	})

	t.Run("carries fractional lines between events", func(t *testing.T) {
		a := NewWheelScrollAcceleratorWithAcceleration(WheelScrollLinesAuto, true)
		expectLines(t, scrollSequence(t, a, []float64{0, 40, 80, 120, 160}, 1), []int{1, 2, 3, 2, 3})
	})
}
