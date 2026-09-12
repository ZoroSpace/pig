// tui/cjk_segmenter_test.go — tests for the pig-only CJK dictionary
// regrouping (cjk_segmenter.go). Token expectations are pinned to the gse
// v1.0.2 zh (zh_s + zh_t) embedded dictionary; grouping may shift on
// dictionary upgrades — re-pin those cases if it does.

package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// requireCJKDict blocks until the background dictionary load finished and
// fails the test if it did not succeed. (Production code never waits; tests
// need determinism.)
func requireCJKDict(t *testing.T) {
	t.Helper()
	prewarmCJKSegmenter()
	select {
	case <-theCJKSegmenter.done:
	case <-time.After(120 * time.Second):
		t.Fatal("cjk dictionary load timed out")
	}
	if theCJKSegmenter.loadFailed.Load() || theCJKSegmenter.seg.Load() == nil {
		t.Fatal("cjk dictionary failed to load")
	}
}

// renderSegments formats segments as [seg]... with an Index contiguity check
// (segments must tile the input with no gaps/overlaps).
func renderSegments(t *testing.T, text string, segments []SegmentData) string {
	t.Helper()
	var sb strings.Builder
	index := 0
	for _, s := range segments {
		sb.WriteString("[" + s.Segment + "]")
		if s.Index != index {
			t.Errorf("segment %q Index = %d, want %d (segments must tile the input)", s.Segment, s.Index, index)
		}
		index += len(s.Segment)
	}
	if index != len(text) {
		t.Errorf("segments cover %d bytes, want %d (full input)", index, len(text))
	}
	return sb.String()
}

func TestWordSegmentDataCJKDictionary(t *testing.T) {
	requireCJKDict(t)
	cases := []struct {
		text string
		want string
	}{
		// Dictionary grouping matches pi/ICU for pi's own test fixture.
		{"你好世界", "[你好][世界]"},
		// Pinned to the gse v1.0.2 zh dictionary (see file header).
		{"今天天气真好", "[今天天气][真好]"},
		// Latin runs pass through untouched (UAX #29 base). gse lowercases
		// ASCII ("English" → "english"), so any leak of non-Han text into the
		// dictionary path shows up here.
		{"中文English混排", "[中文][English][混排]"},
		// Fullwidth punctuation keeps its own segment (issue #4972 intent:
		// word movement never jumps over it into a word).
		{"你好，世界", "[你好][，][世界]"},
		// Rare Han characters and non-dictionary words fall back to
		// per-character inside the run (still tiling).
		{"㵘", "[㵘]"},
		{"修复了切词的bug", "[修复][了][切][词][的][bug]"},
		// Mixed scripts around every Han run.
		{"hello你好，world世界", "[hello][你好][，][world][世界]"},
	}
	for _, tc := range cases {
		if got := renderSegments(t, tc.text, wordSegmentData(tc.text)); got != tc.want {
			t.Errorf("wordSegmentData(%q) = %s, want %s", tc.text, got, tc.want)
		}
	}

	// isWordLike follows the same rule as the UAX #29 path (Han letters are
	// word-like; punctuation is not).
	for _, s := range wordSegmentData("你好，世界") {
		want := s.Segment != "，"
		if s.IsWordLike != want {
			t.Errorf("segment %q IsWordLike = %v, want %v", s.Segment, s.IsWordLike, want)
		}
	}
}

func TestWordSegmentDataCJKFallbackBeforeLoad(t *testing.T) {
	// Isolated never-loaded instance → UAX #29 passthrough (and the first
	// Han run triggers the global async load; this call must not block).
	in := uaxWordSegmentData("你好世界 test")
	if got := mergeHanRunsWith(&cjkSegmenter{}, in); !reflect.DeepEqual(got, in) {
		t.Errorf("pre-load merge altered segments: got %v, want %v", got, in)
	}
	// A failed load marks the permanent fallback.
	failed := &cjkSegmenter{done: make(chan struct{})}
	failed.loadFailed.Store(true)
	close(failed.done)
	if got := mergeHanRunsWith(failed, in); !reflect.DeepEqual(got, in) {
		t.Errorf("failed-load merge altered segments: got %v, want %v", got, in)
	}
	// No Han run → dictionary never consulted even on the fallback path.
	ascii := uaxWordSegmentData("hello world")
	if got := mergeHanRunsWith(&cjkSegmenter{}, ascii); !reflect.DeepEqual(got, ascii) {
		t.Errorf("non-Han merge altered segments: got %v, want %v", got, ascii)
	}
}

func TestFindWordBackwardForwardCJKDictionary(t *testing.T) {
	requireCJKDict(t)
	text := "你好世界 test"

	// Backward walk (pi UTF-16: 9→5→2→0 → bytes 17→13→6→0).
	for _, c := range []struct{ cursor, want int }{{17, 13}, {13, 6}, {6, 0}} {
		if got := FindWordBackward(text, c.cursor, nil); got != c.want {
			t.Errorf("FindWordBackward(%q, %d) = %d, want %d", text, c.cursor, got, c.want)
		}
	}

	// Forward: first word ends after 你好 (pi: firstEnd <= 4 units = 12 bytes;
	// dictionary pins it exactly to byte 6).
	if got := FindWordForward(text, 0, nil); got != 6 {
		t.Errorf("FindWordForward(%q, 0) = %d, want 6", text, got)
	}

	// Walk to end (pi parity).
	pos := 0
	for pos < len(text) {
		next := FindWordForward(text, pos, nil)
		if next == pos {
			break
		}
		pos = next
	}
	if pos != len(text) {
		t.Errorf("walk to end: pos = %d, want %d", pos, len(text))
	}
}

func TestWordSegmentDataCJKTilingProperty(t *testing.T) {
	requireCJKDict(t)
	// Nasty mixed input: the merged segment list must always tile the input.
	inputs := []string{
		"中文withEnglish混排and数字42",
		"「引号里的词」and「more」",
		"路径/home/用户/目录混合",
		"emoji🎉中文🎉emoji",
		"全角１２３与半角123",
		"\n\n多行\n中文文本\n\n",
	}
	for _, text := range inputs {
		renderSegments(t, text, wordSegmentData(text))
	}
}

func TestContainsHanRune(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{{"hello", false}, {"\x1b[1;5D", false}, {"", false}, {"中", true}, {"a中b", true}, {"世界", true}, {"カナ", false}, {"한글", false}}
	for _, tc := range cases {
		if got := containsHanRune(tc.in); got != tc.want {
			t.Errorf("containsHanRune(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
