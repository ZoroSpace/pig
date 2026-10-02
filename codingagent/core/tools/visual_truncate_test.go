// Tests for TruncateToVisualLines' keep direction (pi 0582d9c11: pi
// truncateToVisualLines gained keep: "start" | "end"; the start half backs
// the MCP result preview).
package tools

import (
	"strings"
	"testing"
)

// trimmedLines right-trims each rendered line (tui.Text pads lines to the
// render width).
func trimmedLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(line, " ")
	}
	return out
}

func TestTruncateToVisualLinesKeep(t *testing.T) {
	text := strings.Repeat("a", 200) // wraps to 4 visual lines at width 60
	t.Run("end keeps the last visual lines", func(t *testing.T) {
		result := TruncateToVisualLines(text, 2, 60, 0, VisualTruncateKeepEnd)
		if result.SkippedCount != 2 {
			t.Fatalf("skippedCount = %d, want 2", result.SkippedCount)
		}
		lines := trimmedLines(result.VisualLines)
		if lines[len(lines)-1] != strings.Repeat("a", 20) {
			t.Fatalf("expected the tail visual lines, got %q", lines)
		}
	})

	t.Run("start keeps the first visual lines", func(t *testing.T) {
		result := TruncateToVisualLines(text, 2, 60, 0, VisualTruncateKeepStart)
		if result.SkippedCount != 2 {
			t.Fatalf("skippedCount = %d, want 2", result.SkippedCount)
		}
		lines := trimmedLines(result.VisualLines)
		if lines[0] != strings.Repeat("a", 60) {
			t.Fatalf("expected the head visual lines, got %q", lines)
		}
	})

	t.Run("fits without truncation regardless of direction", func(t *testing.T) {
		for _, keep := range []VisualTruncateKeep{VisualTruncateKeepEnd, VisualTruncateKeepStart} {
			result := TruncateToVisualLines("short", 5, 60, 0, keep)
			lines := trimmedLines(result.VisualLines)
			if result.SkippedCount != 0 || len(lines) != 1 || lines[0] != "short" {
				t.Fatalf("keep %q: got %q (skipped %d)", keep, lines, result.SkippedCount)
			}
		}
	})
}
