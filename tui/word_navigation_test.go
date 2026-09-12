// tui/word_navigation_test.go — ported from pi: packages/tui/test/word-navigation.test.ts
//
// Declared deviations:
//   - Indices are UTF-8 byte offsets (pi uses UTF-16 code units). All ASCII
//     expectations are unchanged; CJK positions are converted to bytes.
//   - The CJK cases rely on ICU's dictionary-based word segmentation
//     ([你好][世界]); pig reproduces it via the gse dictionary
//     (cjk_segmenter.go) and keeps pi's original expectations. (Between the
//     initial port and the dictionary landing, they were temporarily adapted
//     to UAX #29 per-character values.)

package tui

import "testing"

func TestFindWordBackward(t *testing.T) {
	t.Run("basic words: hello world", func(t *testing.T) {
		text := "hello world"
		if got := FindWordBackward(text, 11, nil); got != 6 {
			t.Errorf("FindWordBackward(%q, 11) = %d, want 6", text, got)
		}
		if got := FindWordBackward(text, 6, nil); got != 0 {
			t.Errorf("FindWordBackward(%q, 6) = %d, want 0", text, got)
		}
	})

	t.Run("dotted: foo.bar", func(t *testing.T) {
		text := "foo.bar"
		for _, c := range []struct{ cursor, want int }{{7, 4}, {4, 3}, {3, 0}} {
			if got := FindWordBackward(text, c.cursor, nil); got != c.want {
				t.Errorf("FindWordBackward(%q, %d) = %d, want %d", text, c.cursor, got, c.want)
			}
		}
	})

	t.Run("colon: foo:bar", func(t *testing.T) {
		text := "foo:bar"
		for _, c := range []struct{ cursor, want int }{{7, 4}, {4, 3}, {3, 0}} {
			if got := FindWordBackward(text, c.cursor, nil); got != c.want {
				t.Errorf("FindWordBackward(%q, %d) = %d, want %d", text, c.cursor, got, c.want)
			}
		}
	})

	t.Run("path: path/to/file", func(t *testing.T) {
		text := "path/to/file"
		for _, c := range []struct{ cursor, want int }{{12, 8}, {8, 7}, {7, 5}, {5, 4}, {4, 0}} {
			// "/to" is one word-like segment with "/" as punctuation boundary
			if got := FindWordBackward(text, c.cursor, nil); got != c.want {
				t.Errorf("FindWordBackward(%q, %d) = %d, want %d", text, c.cursor, got, c.want)
			}
		}
	})

	t.Run("CJK mixed", func(t *testing.T) {
		requireCJKDict(t)
		text := "你好世界 test"
		// pi (ICU dictionary): findWordBackward(text, text.length) = 5 (UTF-16);
		// byte domain: 4 Han chars (12 bytes) + space = 13.
		if got := FindWordBackward(text, len(text), nil); got != 13 {
			t.Errorf("FindWordBackward(%q, len) = %d, want 13", text, got)
		}
		// pi: findWordBackward(text, 5) = 2 — ICU dictionary groups [你好][世界]:
		// skip space (1) + 世界 (6 bytes) → 13 - 7 = 6. (Was 9 under the UAX
		// #29 per-char adaptation; restored via gse, see cjk_segmenter.go.)
		if got := FindWordBackward(text, 13, nil); got != 6 {
			t.Errorf("FindWordBackward(%q, 13) = %d, want 6", text, got)
		}
		// pi: findWordBackward(text, 2) = 0 — skip 你好 (6 bytes) → 0.
		if got := FindWordBackward(text, 6, nil); got != 0 {
			t.Errorf("FindWordBackward(%q, 6) = %d, want 0", text, got)
		}
	})

	t.Run("whitespace at boundaries", func(t *testing.T) {
		text := "  hello  "
		if got := FindWordBackward(text, 9, nil); got != 2 {
			t.Errorf("FindWordBackward(%q, 9) = %d, want 2", text, got)
		}
		if got := FindWordBackward(text, 2, nil); got != 0 {
			t.Errorf("FindWordBackward(%q, 2) = %d, want 0", text, got)
		}
	})

	t.Run("punctuation run: foo...bar", func(t *testing.T) {
		text := "foo...bar"
		for _, c := range []struct{ cursor, want int }{{9, 6}, {6, 3}, {3, 0}} {
			if got := FindWordBackward(text, c.cursor, nil); got != c.want {
				t.Errorf("FindWordBackward(%q, %d) = %d, want %d", text, c.cursor, got, c.want)
			}
		}
	})

	t.Run("cursor at 0 returns 0", func(t *testing.T) {
		if got := FindWordBackward("hello", 0, nil); got != 0 {
			t.Errorf("FindWordBackward = %d, want 0", got)
		}
	})
}

func TestFindWordForward(t *testing.T) {
	t.Run("basic words: hello world", func(t *testing.T) {
		text := "hello world"
		if got := FindWordForward(text, 0, nil); got != 5 {
			t.Errorf("FindWordForward(%q, 0) = %d, want 5", text, got)
		}
		if got := FindWordForward(text, 5, nil); got != 11 {
			t.Errorf("FindWordForward(%q, 5) = %d, want 11", text, got)
		}
	})

	t.Run("dotted: foo.bar", func(t *testing.T) {
		text := "foo.bar"
		for _, c := range []struct{ cursor, want int }{{0, 3}, {3, 4}, {4, 7}} {
			if got := FindWordForward(text, c.cursor, nil); got != c.want {
				t.Errorf("FindWordForward(%q, %d) = %d, want %d", text, c.cursor, got, c.want)
			}
		}
	})

	t.Run("colon: foo:bar", func(t *testing.T) {
		text := "foo:bar"
		for _, c := range []struct{ cursor, want int }{{0, 3}, {3, 4}, {4, 7}} {
			if got := FindWordForward(text, c.cursor, nil); got != c.want {
				t.Errorf("FindWordForward(%q, %d) = %d, want %d", text, c.cursor, got, c.want)
			}
		}
	})

	t.Run("path: path/to/file", func(t *testing.T) {
		text := "path/to/file"
		for _, c := range []struct{ cursor, want int }{{0, 4}, {4, 5}, {5, 7}, {7, 8}, {8, 12}} {
			if got := FindWordForward(text, c.cursor, nil); got != c.want {
				t.Errorf("FindWordForward(%q, %d) = %d, want %d", text, c.cursor, got, c.want)
			}
		}
	})

	t.Run("CJK mixed", func(t *testing.T) {
		text := "你好世界 test"
		firstEnd := FindWordForward(text, 0, nil)
		if firstEnd <= 0 {
			t.Errorf("FindWordForward(%q, 0) = %d, want > 0", text, firstEnd)
		}
		// pi (UTF-16): firstEnd <= 4; byte domain: 4 units = 12 bytes.
		if firstEnd > 12 {
			t.Errorf("FindWordForward(%q, 0) = %d, want <= 12", text, firstEnd)
		}
		// Walk to end
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

	t.Run("whitespace at boundaries", func(t *testing.T) {
		text := "  hello  "
		if got := FindWordForward(text, 0, nil); got != 7 {
			t.Errorf("FindWordForward(%q, 0) = %d, want 7", text, got)
		}
		if got := FindWordForward(text, 7, nil); got != 9 {
			t.Errorf("FindWordForward(%q, 7) = %d, want 9", text, got)
		}
	})

	t.Run("punctuation run: foo...bar", func(t *testing.T) {
		text := "foo...bar"
		for _, c := range []struct{ cursor, want int }{{0, 3}, {3, 6}, {6, 9}} {
			if got := FindWordForward(text, c.cursor, nil); got != c.want {
				t.Errorf("FindWordForward(%q, %d) = %d, want %d", text, c.cursor, got, c.want)
			}
		}
	})

	t.Run("cursor at end returns end", func(t *testing.T) {
		if got := FindWordForward("hello", 5, nil); got != 5 {
			t.Errorf("FindWordForward = %d, want 5", got)
		}
	})
}

func TestWordNavigationAtomicSegments(t *testing.T) {
	marker := "[paste #1 +5 lines]"
	text := "hello " + marker + " world"
	isAtomic := func(s string) bool { return s == marker }

	// The functions slice text before calling segment(), so we map each expected
	// substring to its pre-split segments.
	segmentsFor := func(input string) []SegmentData {
		switch input {
		case text, text[:len(text)]:
			return []SegmentData{
				{Segment: "hello", Index: 0, IsWordLike: true},
				{Segment: " ", Index: 5},
				{Segment: marker, Index: 6, IsWordLike: true},
				{Segment: " ", Index: 25},
				{Segment: "world", Index: 26, IsWordLike: true},
			}
		case text[:26]: // backward from 26: slice(0, 26) = "hello [paste #1 +5 lines] "
			return []SegmentData{
				{Segment: "hello", Index: 0, IsWordLike: true},
				{Segment: " ", Index: 5},
				{Segment: marker, Index: 6, IsWordLike: true},
				{Segment: " ", Index: 25},
			}
		case text[6:]: // forward from 6: slice(6) = "[paste #1 +5 lines] world"
			return []SegmentData{
				{Segment: marker, Index: 0, IsWordLike: true},
				{Segment: " ", Index: 19},
				{Segment: "world", Index: 20, IsWordLike: true},
			}
		}
		return nil
	}

	opts := &WordNavigationOptions{
		Segment:         segmentsFor,
		IsAtomicSegment: isAtomic,
	}

	t.Run("backward skips word then stops before atomic marker", func(t *testing.T) {
		if got := FindWordBackward(text, len(text), opts); got != 26 {
			t.Errorf("FindWordBackward = %d, want 26", got)
		}
	})

	t.Run("backward skips whitespace then atomic marker as one unit", func(t *testing.T) {
		if got := FindWordBackward(text, 26, opts); got != 6 {
			t.Errorf("FindWordBackward = %d, want 6", got)
		}
	})

	t.Run("forward skips atomic marker as one unit", func(t *testing.T) {
		if got := FindWordForward(text, 6, opts); got != 6+len(marker) {
			t.Errorf("FindWordForward = %d, want %d", got, 6+len(marker))
		}
	})
}
