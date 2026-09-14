// tui/word_navigation.go — ported from pi: packages/tui/src/word-navigation.ts
//
// Declared deviations:
//   - Intl.Segmenter word granularity → UAX #29 via rivo/uniseg as the base.
//     Han runs are re-grouped by a dictionary-backed CJK segmenter
//     (cjk_segmenter.go, pig-only) to match ICU's CJ-dictionary behavior for
//     Chinese; Japanese kana stays per-character (no kana dictionary).
//   - isWordLike approximates ICU's word-like classification (any letter or
//     number rune; Han/Hiragana/Katakana/Hangul are letters and thus covered).
//     Standalone ExtendNumLet runs (e.g. "_") classify as non-word-like here.
//   - All indices are UTF-8 byte offsets (pi uses UTF-16 code units).

package tui

import (
	"unicode"

	"github.com/rivo/uniseg"
)

// SegmentData is the Go equivalent of the Intl.SegmentData entries produced by
// Intl.Segmenter.segment() (pi consumes { segment, index, isWordLike }).
type SegmentData struct {
	// Segment is the segment text.
	Segment string
	// Index is the byte offset of the segment start in the segmented input.
	Index int
	// IsWordLike reports whether the segment is word-like (letters/numbers).
	// Always false for grapheme segmentation (pi: undefined for graphemes).
	IsWordLike bool
}

// wordSegmentData segments s into words: UAX #29 base (uaxWordSegmentData)
// with all-Han runs re-grouped by the pluggable CJK segmenter
// (cjk_segmenter.go — pi gets this from ICU's built-in CJ dictionary;
// pig's backend is selectable via PIG_CJK_SEGMENTER, default bigram).
func wordSegmentData(s string) []SegmentData {
	return mergeHanRunsWith(theCJKSegmenter, uaxWordSegmentData(s))
}

// uaxWordSegmentData segments s into words per UAX #29 (rivo/uniseg),
// mirroring the base of getWordSegmenter().segment(s) plus the isWordLike flag.
func uaxWordSegmentData(s string) []SegmentData {
	var out []SegmentData
	index := 0
	state := -1
	for len(s) > 0 {
		word, rest, newState := uniseg.FirstWordInString(s, state)
		if word == "" {
			break
		}
		out = append(out, SegmentData{
			Segment:    word,
			Index:      index,
			IsWordLike: segmentIsWordLike(word),
		})
		index += len(word)
		s, state = rest, newState
	}
	return out
}

// segmentIsWordLike approximates Intl.Segmenter's isWordLike: the segment
// contains any letter or number rune (covers Han/Hiragana/Katakana/Hangul,
// which are Lo).
func segmentIsWordLike(segment string) bool {
	for _, r := range segment {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

// WordNavigationOptions are options for word navigation functions.
// When nil, uses the default word segmentation.
type WordNavigationOptions struct {
	// Segment returns word segments for the given text.
	// (pi: custom segmenter returning word segments.)
	Segment func(text string) []SegmentData
	// IsAtomicSegment identifies atomic segments that should be treated as
	// single units (e.g. paste markers).
	IsAtomicSegment func(segment string) bool
}

// FindWordBackward finds the cursor position after moving one word backward
// from cursor in text. Skips trailing whitespace, then stops at the next
// word/punctuation boundary.
//
// Pure function - does not mutate any state.
func FindWordBackward(text string, cursor int, options *WordNavigationOptions) int {
	if cursor <= 0 {
		return 0
	}

	textBeforeCursor := text[:cursor]
	var segments []SegmentData
	if options != nil && options.Segment != nil {
		segments = options.Segment(textBeforeCursor)
	} else {
		segments = wordSegmentData(textBeforeCursor)
	}
	newCursor := cursor
	isAtomic := func(seg string) bool {
		return options != nil && options.IsAtomicSegment != nil && options.IsAtomicSegment(seg)
	}
	isWs := func(seg string) bool { return seg != "" && IsWhitespaceChar(seg) }

	// Skip trailing whitespace
	for len(segments) > 0 &&
		!isAtomic(segments[len(segments)-1].Segment) &&
		isWs(segments[len(segments)-1].Segment) {
		newCursor -= len(segments[len(segments)-1].Segment)
		segments = segments[:len(segments)-1]
	}

	if len(segments) == 0 {
		return newCursor
	}

	last := segments[len(segments)-1]

	if isAtomic(last.Segment) {
		// Skip one atomic segment.
		newCursor -= len(last.Segment)
	} else if last.IsWordLike {
		// Skip inside one word-like segment, preserving ASCII punctuation boundaries.
		segment := last.Segment
		matches := PUNCTUATION_REGEX.FindAllStringIndex(segment, -1)
		if len(matches) <= 0 {
			newCursor -= len(segment)
		} else {
			lastMatch := matches[len(matches)-1]
			newCursor -= len(segment) - lastMatch[1]
		}
	} else {
		// Skip non-word non-whitespace run (punctuation)
		for len(segments) > 0 &&
			!isAtomic(segments[len(segments)-1].Segment) &&
			!segments[len(segments)-1].IsWordLike &&
			!isWs(segments[len(segments)-1].Segment) {
			newCursor -= len(segments[len(segments)-1].Segment)
			segments = segments[:len(segments)-1]
		}
	}

	return newCursor
}

// FindWordForward finds the cursor position after moving one word forward from
// cursor in text. Skips leading whitespace, then stops at the next
// word/punctuation boundary.
//
// Pure function - does not mutate any state.
func FindWordForward(text string, cursor int, options *WordNavigationOptions) int {
	if cursor >= len(text) {
		return len(text)
	}

	textAfterCursor := text[cursor:]
	var segments []SegmentData
	if options != nil && options.Segment != nil {
		segments = options.Segment(textAfterCursor)
	} else {
		segments = wordSegmentData(textAfterCursor)
	}
	newCursor := cursor
	isAtomic := func(seg string) bool {
		return options != nil && options.IsAtomicSegment != nil && options.IsAtomicSegment(seg)
	}
	isWs := func(seg string) bool { return seg != "" && IsWhitespaceChar(seg) }

	// Iterator position (pi iterates the segment iterator lazily).
	pos := 0
	next := func() bool { pos++; return pos < len(segments) }

	// Skip leading whitespace
	for pos < len(segments) && !isAtomic(segments[pos].Segment) && isWs(segments[pos].Segment) {
		newCursor += len(segments[pos].Segment)
		if !next() {
			return newCursor
		}
	}

	if pos >= len(segments) {
		return newCursor
	}

	if isAtomic(segments[pos].Segment) {
		// Skip one atomic segment.
		newCursor += len(segments[pos].Segment)
	} else if segments[pos].IsWordLike {
		// Skip inside one word-like segment, preserving ASCII punctuation boundaries.
		loc := PUNCTUATION_REGEX.FindStringIndex(segments[pos].Segment)
		if loc != nil {
			newCursor += loc[0]
		} else {
			newCursor += len(segments[pos].Segment)
		}
	} else {
		// Skip non-word non-whitespace run (punctuation)
		for pos < len(segments) && !isAtomic(segments[pos].Segment) && !segments[pos].IsWordLike && !isWs(segments[pos].Segment) {
			newCursor += len(segments[pos].Segment)
			if !next() {
				return newCursor
			}
		}
	}

	return newCursor
}
