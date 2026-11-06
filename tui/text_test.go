// tui/text_test.go (pi: packages/tui/test/text.test.ts)
package tui

import "testing"

func TestTextKeepsWrappedLinesForUnchangedText(t *testing.T) {
	text := NewText("hello world", 0, 0)
	first := text.Render(5)
	text.SetText("hello world")
	second := text.Render(5)
	if len(first) != len(second) {
		t.Fatalf("render changed for same text: %q vs %q", first, second)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("render changed for same text: %q vs %q", first, second)
		}
	}
}
