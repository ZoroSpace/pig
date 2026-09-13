package tui

import (
	"testing"
)

// TestStressViewportFixtureRound2 is a throwaway fixture for the second
// verification round. It will be removed and the branch reset.
func TestStressViewportFixtureRound2(t *testing.T) {
	if len("stress") != 6 {
		t.Fatal("length broke")
	}
}
