// tui/spacer.go (pi: packages/tui/src/components/spacer.ts)
//
// Spacer component that renders empty lines.
package tui

// Spacer is the empty-lines component (pi: class Spacer implements Component).
type Spacer struct {
	lines int
}

// NewSpacer mirrors pi's constructor default (lines 1).
func NewSpacer(lines int) *Spacer {
	return &Spacer{lines: lines}
}

func (s *Spacer) SetLines(lines int) {
	s.lines = lines
}

func (s *Spacer) Invalidate() {
	// No cached state to invalidate currently
}

func (s *Spacer) Render(_ int) []string {
	result := make([]string, s.lines)
	for i := range result {
		result[i] = ""
	}
	return result
}
