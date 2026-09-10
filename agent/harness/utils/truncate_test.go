// pi: test/harness/truncate.test.ts — truncation utility behavior.
//
// Deviations:
//   - The TS suite feeds lone UTF-16 surrogates ("\ud83d", "\ude42", ...)
//     to exercise Buffer's tail truncation on invalid scalar values. Go
//     strings cannot hold lone surrogates (they decode to U+FFFD), so those
//     alphabet members are dropped from the fuzz alphabet; the astral pair
//     🙂 and the ZWJ family 👩‍💻 remain.
package utils

import (
	"strings"
	"testing"

	"github.com/samber/lo"
)

func byteLength(content string) int {
	return len(content)
}

func bufferTail(content string, maxBytes int) string {
	if len(content) <= maxBytes {
		return content
	}
	start := len(content) - maxBytes
	for start < len(content) && isUTF8Continuation(content[start]) {
		start++
	}
	return content[start:]
}

func isUTF8Continuation(b byte) bool { return b&0xc0 == 0x80 }

func assertMatchesBufferTail(t *testing.T, input string, maxByteValues []int) {
	t.Helper()
	totalBytes := len(input)
	values := maxByteValues
	if values == nil {
		values = make([]int, 0, totalBytes+6)
		for maxBytes := 0; maxBytes <= totalBytes+5; maxBytes++ {
			values = append(values, maxBytes)
		}
	}
	for _, maxBytes := range values {
		result := TruncateTail(input, &TruncationOptions{MaxBytes: &maxBytes, MaxLines: lo.ToPtr(10)})
		expected := bufferTail(input, maxBytes)
		if result.Content != expected {
			t.Fatalf("tail mismatch input=%q maxBytes=%d expected=%q actual=%q", input, maxBytes, expected, result.Content)
		}
		outputBytes := len(result.Content)
		if outputBytes > maxBytes {
			t.Fatalf("tail output exceeded byte limit input=%q maxBytes=%d outputBytes=%d", input, maxBytes, outputBytes)
		}
	}
}

func sampledByteLimits(input string) []int {
	totalBytes := len(input)
	seen := map[int]bool{}
	candidates := []int{
		0, 1, 2, 3, 4, 5, 8,
		totalBytes/2 - 1,
		totalBytes / 2,
		totalBytes/2 + 1,
		totalBytes - 8,
		totalBytes - 5,
		totalBytes - 4,
		totalBytes - 3,
		totalBytes - 2,
		totalBytes - 1,
		totalBytes,
		totalBytes + 1,
		totalBytes + 4,
	}
	var values []int
	for _, v := range candidates {
		if v >= 0 && !seen[v] {
			seen[v] = true
			values = append(values, v)
		}
	}
	return values
}

func TestCountsUTF8BytesWithoutNodeBuffer(t *testing.T) {
	content := "aé🙂\nb"
	result := TruncateHead(content, &TruncationOptions{MaxBytes: lo.ToPtr(100), MaxLines: lo.ToPtr(10)})

	if result.Truncated {
		t.Fatal("expected truncated=false")
	}
	if result.TotalBytes != byteLength(content) || result.OutputBytes != byteLength(content) {
		t.Fatalf("byte accounting mismatch: total=%d output=%d want=%d", result.TotalBytes, result.OutputBytes, byteLength(content))
	}
	if result.TotalBytes != 9 {
		t.Fatalf("expected 9 bytes, got %d", result.TotalBytes)
	}
}

func TestDoesNotCountTrailingNewlineAsExtraLine(t *testing.T) {
	content := strings.Repeat("line\n", 3)
	head := TruncateHead(content, &TruncationOptions{MaxBytes: lo.ToPtr(100), MaxLines: lo.ToPtr(3)})
	tail := TruncateTail(content, &TruncationOptions{MaxBytes: lo.ToPtr(100), MaxLines: lo.ToPtr(3)})

	if head.Truncated || head.TotalLines != 3 || head.OutputLines != 3 {
		t.Fatalf("head mismatch: %+v", head)
	}
	if tail.Truncated || tail.TotalLines != 3 || tail.OutputLines != 3 {
		t.Fatalf("tail mismatch: %+v", tail)
	}
}

func TestTruncatesHeadOnUTF8ByteLimitsWithoutPartialLines(t *testing.T) {
	result := TruncateHead("éé\nabc", &TruncationOptions{MaxBytes: lo.ToPtr(4), MaxLines: lo.ToPtr(10)})

	if result.Content != "éé" || !result.Truncated || result.TruncatedBy != TruncatedByBytes {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.OutputBytes != 4 || result.FirstLineExceedsLimit {
		t.Fatalf("unexpected accounting: %+v", result)
	}
}

func TestReportsHeadTruncationWhenFirstLineExceedsByteLimit(t *testing.T) {
	result := TruncateHead("éé\nabc", &TruncationOptions{MaxBytes: lo.ToPtr(3), MaxLines: lo.ToPtr(10)})

	if result.Content != "" || !result.Truncated || result.TruncatedBy != TruncatedByBytes {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !result.FirstLineExceedsLimit {
		t.Fatal("expected firstLineExceedsLimit=true")
	}
}

func TestTruncatesTailOnUTF8BoundariesWhenOnlyPartialLastLineFits(t *testing.T) {
	result := TruncateTail("aé🙂b", &TruncationOptions{MaxBytes: lo.ToPtr(5), MaxLines: lo.ToPtr(10)})

	if result.Content != "🙂b" || !result.Truncated || result.TruncatedBy != TruncatedByBytes {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !result.LastLinePartial || result.OutputBytes != 5 {
		t.Fatalf("unexpected accounting: %+v", result)
	}
}

func TestTruncatesOversizedSingleLineWithTrailingNewline(t *testing.T) {
	input := strings.Repeat("X", 300_000) + "\n"
	result := TruncateTail(input, &TruncationOptions{MaxBytes: lo.ToPtr(1024), MaxLines: lo.ToPtr(100)})

	if result.Content != strings.Repeat("X", 1024) || result.OutputBytes != 1024 {
		t.Fatalf("unexpected result: outputBytes=%d", result.OutputBytes)
	}
	if result.OutputLines != 1 || !result.LastLinePartial || result.TruncatedBy != TruncatedByBytes {
		t.Fatalf("unexpected accounting: %+v", result)
	}
}

func TestDropsOversizedTrailingCharacterWhenItCannotFit(t *testing.T) {
	result := TruncateTail("abc🙂", &TruncationOptions{MaxBytes: lo.ToPtr(3), MaxLines: lo.ToPtr(10)})

	if result.Content != "" || !result.Truncated || result.TruncatedBy != TruncatedByBytes {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !result.LastLinePartial || result.OutputBytes != 0 {
		t.Fatalf("unexpected accounting: %+v", result)
	}
}

func TestMatchesBufferTailSemanticsForSurrogateEdgeCases(t *testing.T) {
	// Lone-surrogate inputs from the TS suite ("\ud83d", "\ude42", ...) are
	// unrepresentable in Go; the representable multibyte edge cases remain.
	inputs := []string{"🙂", "👩‍💻"}
	for _, input := range inputs {
		assertMatchesBufferTail(t, input, nil)
	}
}

func TestMatchesBufferTailSemanticsAcrossDeterministicFuzzCases(t *testing.T) {
	alphabet := []string{
		"a", "\u007f", "\u0080", "é", "\u07ff", "\u0800", "中",
		"\ud7ff", "🙂", "\ue000", "\uffff", "👩‍💻",
	}

	var checkExhaustive func(prefix string, depth int)
	checkExhaustive = func(prefix string, depth int) {
		assertMatchesBufferTail(t, prefix, sampledByteLimits(prefix))
		if depth == 0 {
			return
		}
		for _, character := range alphabet {
			checkExhaustive(prefix+character, depth-1)
		}
	}
	checkExhaustive("", 3)

	seed := uint32(0x12345678)
	random := func() float64 {
		seed = seed*1664525 + 1013904223
		return float64(seed) / 0x100000000
	}
	for i := 0; i < 1000; i++ {
		input := ""
		length := int(random() * 80)
		for j := 0; j < length; j++ {
			input += alphabet[int(random()*float64(len(alphabet)))%len(alphabet)]
		}
		assertMatchesBufferTail(t, input, sampledByteLimits(input))
	}
}
