// tui/select_list_test.go — ported from pi: packages/tui/test/select-list.test.ts

package tui

import (
	"strings"
	"testing"
)

// passthroughSelectListTheme is the identity theme (pi: testTheme).
func passthroughSelectListTheme() SelectListTheme {
	return SelectListTheme{
		SelectedPrefix: func(text string) string { return text },
		SelectedText:   func(text string) string { return text },
		Description:    func(text string) string { return text },
		ScrollInfo:     func(text string) string { return text },
		NoMatch:        func(text string) string { return text },
	}
}

// visibleIndexOf returns the visible column of text in line (pi:
// visibleIndexOf helper).
func visibleIndexOf(t *testing.T, line string, text string) int {
	t.Helper()
	index := strings.Index(line, text)
	if index == -1 {
		t.Fatalf("text %q not found in line %q", text, line)
	}
	return VisibleWidth(line[:index])
}

func TestSelectList(t *testing.T) {
	t.Run("normalizes multiline descriptions to single line", func(t *testing.T) {
		items := []SelectItem{
			{
				Value:       "test",
				Label:       "test",
				Description: "Line one\nLine two\nLine three",
			},
		}

		list := NewSelectList(items, 5, passthroughSelectListTheme(), nil)
		rendered := list.Render(100)

		if len(rendered) == 0 {
			t.Fatal("expected rendered lines")
		}
		if strings.Contains(rendered[0], "\n") {
			t.Error("rendered line must not contain a newline")
		}
		if !strings.Contains(rendered[0], "Line one Line two Line three") {
			t.Errorf("rendered[0] = %q", rendered[0])
		}
	})

	t.Run("keeps descriptions aligned when the primary text is truncated", func(t *testing.T) {
		items := []SelectItem{
			{Value: "short", Label: "short", Description: "short description"},
			{
				Value:       "very-long-command-name-that-needs-truncation",
				Label:       "very-long-command-name-that-needs-truncation",
				Description: "long description",
			},
		}

		list := NewSelectList(items, 5, passthroughSelectListTheme(), nil)
		rendered := list.Render(80)

		if visibleIndexOf(t, rendered[0], "short description") != visibleIndexOf(t, rendered[1], "long description") {
			t.Errorf("descriptions not aligned: %q vs %q", rendered[0], rendered[1])
		}
	})

	t.Run("uses the configured minimum primary column width", func(t *testing.T) {
		items := []SelectItem{
			{Value: "a", Label: "a", Description: "first"},
			{Value: "bb", Label: "bb", Description: "second"},
		}

		list := NewSelectList(items, 5, passthroughSelectListTheme(), &SelectListLayoutOptions{
			MinPrimaryColumnWidth: 12,
			MaxPrimaryColumnWidth: 20,
		})
		rendered := list.Render(80)

		// pi: indexOf = 14 UTF-16 units on both lines (→ is 1 unit); in the
		// byte domain the two lines start their descriptions at different byte
		// offsets (→ is 3 bytes), so assert on the visible column instead.
		if got := visibleIndexOf(t, rendered[0], "first"); got != 14 {
			t.Errorf("rendered[0] = %q, want 'first' at column 14, got %d", rendered[0], got)
		}
		if got := visibleIndexOf(t, rendered[1], "second"); got != 14 {
			t.Errorf("rendered[1] = %q, want 'second' at column 14, got %d", rendered[1], got)
		}
	})

	t.Run("uses the configured maximum primary column width", func(t *testing.T) {
		items := []SelectItem{
			{
				Value:       "very-long-command-name-that-needs-truncation",
				Label:       "very-long-command-name-that-needs-truncation",
				Description: "first",
			},
			{Value: "short", Label: "short", Description: "second"},
		}

		list := NewSelectList(items, 5, passthroughSelectListTheme(), &SelectListLayoutOptions{
			MinPrimaryColumnWidth: 12,
			MaxPrimaryColumnWidth: 20,
		})
		rendered := list.Render(80)

		if visibleIndexOf(t, rendered[0], "first") != 22 {
			t.Errorf("rendered[0] = %q, want 'first' at column 22", rendered[0])
		}
		if visibleIndexOf(t, rendered[1], "second") != 22 {
			t.Errorf("rendered[1] = %q, want 'second' at column 22", rendered[1])
		}
	})

	t.Run("allows overriding primary truncation while preserving description alignment", func(t *testing.T) {
		items := []SelectItem{
			{
				Value:       "very-long-command-name-that-needs-truncation",
				Label:       "very-long-command-name-that-needs-truncation",
				Description: "first",
			},
			{Value: "short", Label: "short", Description: "second"},
		}

		list := NewSelectList(items, 5, passthroughSelectListTheme(), &SelectListLayoutOptions{
			MinPrimaryColumnWidth: 12,
			MaxPrimaryColumnWidth: 12,
			TruncatePrimary: func(context SelectListTruncatePrimaryContext) string {
				if len(context.Text) <= context.MaxWidth {
					return context.Text
				}

				cut := context.MaxWidth - 1
				if cut < 0 {
					cut = 0
				}
				return context.Text[:cut] + "…"
			},
		})
		rendered := list.Render(80)

		if !strings.Contains(rendered[0], "…") {
			t.Errorf("rendered[0] = %q, want ellipsis", rendered[0])
		}
		if visibleIndexOf(t, rendered[0], "first") != visibleIndexOf(t, rendered[1], "second") {
			t.Errorf("descriptions not aligned: %q vs %q", rendered[0], rendered[1])
		}
	})
}
