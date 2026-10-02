// ported from pi: packages/tui/test/regression-slice-by-column-ansi-order.test.ts
//
// https://github.com/earendil-works/pi/issues/10169
package tui

import "testing"

func TestSliceByColumnAnsiOrderRegression(t *testing.T) {
	t.Run("keeps a reset at the slice start after earlier style codes", func(t *testing.T) {
		line := "\x1b[32mfoo\x1b[39m bar"
		assertEqualStr(t, SliceByColumn(line, 3, 4, true), "\x1b[32m\x1b[39m bar")
	})

	t.Run("does not leak color into text after a highlighted token", func(t *testing.T) {
		line := "Another \x1b[35malpha\x1b[39m line with \x1b[35mbeta\x1b[39m later."
		after := SliceByColumn(line, 13, 100, true)
		assertEqualStr(t, after, "\x1b[35m\x1b[39m line with \x1b[35mbeta\x1b[39m later.")
	})
}
