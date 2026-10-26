// fuzzy_test.go (pi: test/fuzzy.test.ts) — contiguous-match ranking.
package tui

import "testing"

// TestFuzzyMatchPrefersContiguousOccurrence guards pi 8c03fef4d: greedy
// alignment took o, p, u from "openrouter", so the real "opus" scored worst.
func TestFuzzyMatchPrefersContiguousOccurrence(t *testing.T) {
	opus := FuzzyMatchScore("opus", "openrouter openrouter/anthropic/claude-opus-4.1")
	sonnet := FuzzyMatchScore("opus", "openrouter openrouter/anthropic/claude-sonnet-4.5")
	gptOss := FuzzyMatchScore("opus", "openrouter openrouter/openai/gpt-oss-120b")

	if !(opus.Score < sonnet.Score) {
		t.Fatalf("opus score %v should beat sonnet %v", opus.Score, sonnet.Score)
	}
	if !(opus.Score < gptOss.Score) {
		t.Fatalf("opus score %v should beat gpt-oss %v", opus.Score, gptOss.Score)
	}
}

// TestFuzzyMatchUsesBestContiguousOccurrence guards pi 8c03fef4d: the first
// embedded occurrence must not beat a later word-boundary occurrence.
func TestFuzzyMatchUsesBestContiguousOccurrence(t *testing.T) {
	embeddedFirst := FuzzyMatchScore("opus", "xxopus opus")
	boundaryOnly := FuzzyMatchScore("opus", "xxxxxx opus")
	if embeddedFirst.Score != boundaryOnly.Score {
		t.Fatalf("embeddedFirst=%v boundaryOnly=%v", embeddedFirst.Score, boundaryOnly.Score)
	}
}

// TestFuzzyFilterRanksQueryMatchAboveSharedProviderPrefix guards pi
// 8c03fef4d: "radius radius/gpt" contains a, s, t, r, a in order.
func TestFuzzyFilterRanksQueryMatchAboveSharedProviderPrefix(t *testing.T) {
	items := []string{
		"radius radius/gpt-5.4 radius gpt-5.4 GPT 5.4",
		"radius radius/agentic radius agentic Agentic",
		"radius radius/gpt-6-sol radius gpt-6-sol GPT 6 Sol",
		"radius radius/gpt-6-astra radius gpt-6-astra GPT 6 Astra",
	}
	result := FuzzyFilter(items, "astra", func(x string) string { return x })
	if len(result) != len(items) || result[0] != items[3] {
		t.Fatalf("result[0] = %q, want %q", result[0], items[3])
	}
}
