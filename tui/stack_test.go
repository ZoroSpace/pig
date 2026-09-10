// tui/stack_test.go — the stack-related subset of pi's
// test/layout.test.ts that does not go through layout.ts's
// renderLayoutFrame / ScrollView (those are T4 scope: scroll-view + layout
// engine land with the interactive components; the remaining layout.test.ts
// cases port with them).
package tui

import (
	"strings"
	"testing"

	"github.com/samber/lo"
)

func visibleLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(StripTerminalSequences(line), " \t")
	}
	return out
}

func TestVStackOmitsGapsAroundInvisibleEntries(t *testing.T) {
	hidden := NewText("hidden", 0, 0)
	stack := NewVStack(
		[]StackChild{
			NewText("one", 0, 0),
			StackEntryOf(hidden, &StackEntryOptions{Visible: func(LayoutViewport) bool { return false }}),
			NewText("two", 0, 0),
		},
		&StackOptions{Gap: lo.ToPtr(1)},
	)
	got := stack.Render(10)
	want := []string{"one", "", "two"}
	if len(got) != len(want) {
		t.Fatalf("lines = %v, want %v", got, want)
	}
	for i := range want {
		if strings.TrimRight(got[i], " ") != want[i] {
			t.Fatalf("lines = %v, want %v", got, want)
		}
	}
}
