// tui/cjk_segmenter_test.go — tests for the pig-only CJK word re-grouping
// backends (cjk_segmenter.go, cjk_segmenter_sst.go). Dictionary token
// expectations are pinned per backend: gse v1.0.2 zh (zh_s + zh_t) for the
// gse mode, the SST build of the same files for sst mode; grouping may
// shift on dictionary upgrades — re-pin those cases if it does.

package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// useCJKSegmenter swaps the process-wide segmenter for a fresh instance
// pinned to mode and waits for dictionary backends to finish loading
// (production never waits; tests need determinism). The previous instance
// is restored via t.Cleanup — tests using this helper must not call
// t.Parallel (they mutate package globals).
func useCJKSegmenter(t *testing.T, mode cjkSegmenterMode) {
	t.Helper()
	saved := theCJKSegmenter
	t.Cleanup(func() { theCJKSegmenter = saved })
	seg := &cjkSegmenter{}
	seg.once.Do(func() {
		seg.mode = mode
		seg.ready = make(chan struct{})
		switch mode {
		case cjkModeBigram:
			close(seg.ready) // nothing to load
		case cjkModeSST:
			go seg.loadSST()
		case cjkModeGSE:
			go seg.loadGSE()
		default:
			t.Fatalf("unhandled test mode %d", mode)
		}
	})
	theCJKSegmenter = seg
	select {
	case <-theCJKSegmenter.ready:
	case <-time.After(120 * time.Second):
		t.Fatal("cjk dictionary load timed out")
	}
	if theCJKSegmenter.loadFailed.Load() {
		t.Fatal("cjk dictionary failed to load")
	}
	switch mode {
	case cjkModeSST:
		if theCJKSegmenter.sstDict.Load() == nil {
			t.Fatal("sst dictionary not loaded")
		}
	case cjkModeGSE:
		if theCJKSegmenter.gseSeg.Load() == nil {
			t.Fatal("gse dictionary not loaded")
		}
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

func TestResolveCJKSegmenterMode(t *testing.T) {
	cases := []struct {
		value string
		want  cjkSegmenterMode
	}{
		{"", cjkModeBigram},       // unset → default
		{"bigram", cjkModeBigram}, // explicit default
		{"  Bigram ", cjkModeBigram},
		{"sst", cjkModeSST},
		{"SST", cjkModeSST},
		{"gse", cjkModeGSE},
		{"nonsense", cjkModeBigram}, // unknown → default, never an error
	}
	for _, tc := range cases {
		if got := resolveCJKSegmenterMode(tc.value); got != tc.want {
			t.Errorf("resolveCJKSegmenterMode(%q) = %d, want %d", tc.value, got, tc.want)
		}
	}
}

// TestWordSegmentDataCJKBigram pins the default backend's fixed 2-character
// grouping (odd tail → 1 char). 你好世界 happens to match the gse/ICU
// grouping; longer runs diverge by design.
func TestWordSegmentDataCJKBigram(t *testing.T) {
	useCJKSegmenter(t, cjkModeBigram)
	cases := []struct {
		text string
		want string
	}{
		{"你好世界", "[你好][世界]"},
		// 6 characters = 3 pairs; the pairing coincides with the dictionary
		// grouping for this fixture.
		{"今天天气真好", "[今天][天气][真好]"},
		{"快捷键", "[快捷][键]"},
		// Latin runs pass through untouched (UAX #29 base).
		{"中文English混排", "[中文][English][混排]"},
		// Fullwidth punctuation keeps its own segment (issue #4972 intent:
		// word movement never jumps over it into a word).
		{"你好，世界", "[你好][，][世界]"},
		// Single Han characters never form a run (needs ≥2 segments).
		{"㵘", "[㵘]"},
		{"修复了切词的bug", "[修复][了切][词的][bug]"},
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

// TestWordSegmentDataCJKSST pins the SST backend's dictionary grouping —
// the same DAG/Viterbi shape as gse (no HMM): dictionary words win,
// out-of-vocabulary runs stay per-character.
func TestWordSegmentDataCJKSST(t *testing.T) {
	useCJKSegmenter(t, cjkModeSST)
	cases := []struct {
		text string
		want string
	}{
		{"你好世界", "[你好][世界]"},
		{"今天天气真好", "[今天天气][真好]"},
		{"中文English混排", "[中文][English][混排]"},
		{"你好，世界", "[你好][，][世界]"},
		{"㵘", "[㵘]"},
		{"修复了切词的bug", "[修复][了][切][词][的][bug]"},
		{"hello你好，world世界", "[hello][你好][，][world][世界]"},
	}
	for _, tc := range cases {
		if got := renderSegments(t, tc.text, wordSegmentData(tc.text)); got != tc.want {
			t.Errorf("wordSegmentData(%q) = %s, want %s", tc.text, got, tc.want)
		}
	}
}

// TestWordSegmentDataCJKGSE pins the gse backend's dictionary grouping —
// the values pig shipped with in 2026-09 and the ICU-parity reference.
func TestWordSegmentDataCJKGSE(t *testing.T) {
	useCJKSegmenter(t, cjkModeGSE)
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
}

// TestWordSegmentDataCJKFallbackBeforeLoad covers the not-ready and
// failed-load fallbacks on a dictionary backend (isolated instances — the
// global is untouched, no loader goroutine is started).
func TestWordSegmentDataCJKFallbackBeforeLoad(t *testing.T) {
	in := uaxWordSegmentData("你好世界 test")

	// Isolated never-loaded sst instance → UAX #29 passthrough.
	neverLoaded := &cjkSegmenter{}
	neverLoaded.once.Do(func() {
		neverLoaded.mode = cjkModeSST
		neverLoaded.ready = make(chan struct{}) // open: load never finishes here
	})
	if got := mergeHanRunsWith(neverLoaded, in); !reflect.DeepEqual(got, in) {
		t.Errorf("pre-load merge altered segments: got %v, want %v", got, in)
	}

	// A failed load marks the permanent fallback.
	failed := &cjkSegmenter{}
	failed.once.Do(func() {
		failed.mode = cjkModeSST
		failed.ready = make(chan struct{})
		close(failed.ready)
	})
	failed.loadFailed.Store(true)
	if got := mergeHanRunsWith(failed, in); !reflect.DeepEqual(got, in) {
		t.Errorf("failed-load merge altered segments: got %v, want %v", got, in)
	}

	// No Han run → backend never consulted even on the fallback path.
	ascii := uaxWordSegmentData("hello world")
	if got := mergeHanRunsWith(neverLoaded, ascii); !reflect.DeepEqual(got, ascii) {
		t.Errorf("non-Han merge altered segments: got %v, want %v", got, ascii)
	}
}

// TestFindWordBackwardForwardCJK walks word navigation over 你好世界 test in
// every backend — all three group this fixture as [你好][世界], so the pi
// positions (UTF-16 9→5→2→0 → bytes 17→13→6→0) hold across modes.
func TestFindWordBackwardForwardCJK(t *testing.T) {
	for _, mode := range []cjkSegmenterMode{cjkModeBigram, cjkModeSST, cjkModeGSE} {
		t.Run(mode.String(), func(t *testing.T) {
			useCJKSegmenter(t, mode)
			text := "你好世界 test"

			// Backward walk (pi UTF-16: 9→5→2→0 → bytes 17→13→6→0).
			for _, c := range []struct{ cursor, want int }{{17, 13}, {13, 6}, {6, 0}} {
				if got := FindWordBackward(text, c.cursor, nil); got != c.want {
					t.Errorf("FindWordBackward(%q, %d) = %d, want %d", text, c.cursor, got, c.want)
				}
			}

			// Forward: first word ends after 你好 (pi: firstEnd <= 4 units
			// = 12 bytes; the dictionary pins it exactly to byte 6).
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
		})
	}
}

// TestWordSegmentDataCJKTilingProperty runs the nasty mixed inputs through
// every backend: the merged segment list must always tile the input.
func TestWordSegmentDataCJKTilingProperty(t *testing.T) {
	inputs := []string{
		"中文withEnglish混排and数字42",
		"「引号里的词」and「more」",
		"路径/home/用户/目录混合",
		"emoji🎉中文🎉emoji",
		"全角１２３与半角123",
		"\n\n多行\n中文文本\n\n",
	}
	for _, mode := range []cjkSegmenterMode{cjkModeBigram, cjkModeSST, cjkModeGSE} {
		t.Run(mode.String(), func(t *testing.T) {
			useCJKSegmenter(t, mode)
			for _, text := range inputs {
				renderSegments(t, text, wordSegmentData(text))
			}
		})
	}
}

func TestContainsHanRune(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{{"hello", false}, {"\x1b[1;5D", false}, {"", false}, {"中", true}, {"a中b", true}, {"世界", true}, {"カナ", false}, {"글", false}}
	for _, tc := range cases {
		if got := containsHanRune(tc.in); got != tc.want {
			t.Errorf("containsHanRune(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
