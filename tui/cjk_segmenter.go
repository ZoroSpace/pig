// tui/cjk_segmenter.go — pig-only enhancement; no pi counterpart.
//
// pi relies on ICU's Intl.Segmenter word granularity, which applies a built-in
// CJ dictionary: 你好世界 → [你好][世界] (pi's word-navigation.test.ts numbers
// prove the grouping even though its comment claims per-character). Go's
// standard UAX #29 implementation (rivo/uniseg) has no dictionary and cuts Han
// text per character, so CJK word navigation (Ctrl+←/→, word deletion) moves
// one character at a time. This file adds dictionary-based regrouping for Han
// runs via github.com/go-ego/gse (pure Go; embedded zh_s + zh_t dictionary).
//
// Design (docs/feat/CJK-WORD-SEGMENTATION.md, all numbers measured):
//   - Hybrid alignment: UAX #29 stays the base segmenter (word_navigation.go);
//     only maximal runs of ≥2 consecutive all-Han segments are re-cut with
//     gse. Latin/code/punctuation boundaries stay byte-identical to UAX #29,
//     and the ported word-navigation/editor tests keep their non-CJK values.
//     gse must never see non-Han text: its tokenizer lowercases ASCII
//     ("English" → "english"), so its output only enters through
//     tiling-checked pure-Han runs.
//   - Async init: the dictionary parses ~15MB of text (~0.9s, ~286MB transient
//     allocation peak, ~15MB steady-state RSS) in a background goroutine —
//     fired on the first Han keystroke (Editor.HandleInput) and, as a
//     fallback, on the first word operation that encounters a Han run.
//     Until the load completes (or permanently after a failure), segmentation
//     falls back to UAX #29 per-character. No call path ever blocks.
//   - Vendored dictionary: the zh_s/zh_t files are embedded from
//     tui/assets/dict/ (see SOURCE.md there) and loaded via
//     gse.Segmenter.LoadDictStr — the same code path as gse's own
//     LoadDictEmbed("zh"). Vendoring avoids gse's package-level go:embed
//     blobs (22.6MB Japanese dictionary + 5.9MB IDF data), which the Go
//     linker keeps in the binary even when unreferenced (+31MB dead weight;
//     with vendoring the binary pays only the 8.3MB actually used).
//   - Single instance + mutex: gse's concurrency safety is undocumented, so
//     Cut calls are serialized (~1.5µs per cut; contention is negligible at
//     keystroke rates).
//
// Declared deviations from pi/ICU:
//   - Japanese kana stays on UAX #29 boundaries (gse's zh dictionaries do not
//     cover it); ICU also dictionary-segments Japanese.
//   - gse's grouping follows its dictionary frequencies, which occasionally
//     differs from ICU's exact choices (e.g. 今天天气 as one token vs ICU
//     今天/天气). Word-unit granularity, not exact tokens, is the goal.

package tui

import (
	_ "embed"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"

	"github.com/ZoroSpace/pig/utils"
	"github.com/go-ego/gse"
)

// The zh dictionaries are vendored from gse (see tui/assets/dict/SOURCE.md)
// and embedded here instead of using gse's LoadDictEmbed, which drags gse's
// package-level embed blobs (Japanese dictionary, IDF data) into the binary.
//
//go:embed assets/dict/zh_s.txt
var zhSDict string

//go:embed assets/dict/zh_t.txt
var zhTDict string

// cjkSegmenter holds the single dictionary-backed segmenter. seg publishes the
// loaded instance atomically and stays nil until the async load finishes (or
// forever if it failed — loadFailed then marks the permanent fallback).
type cjkSegmenter struct {
	once       sync.Once
	done       chan struct{} // closed when the load attempt ends (either way)
	mu         sync.Mutex    // serializes Cut (and any future AddToken writes)
	seg        atomic.Pointer[gse.Segmenter]
	loadFailed atomic.Bool
}

// theCJKSegmenter is the process-wide instance (pi has no equivalent: Intl.
// Segmenter ships inside the JS runtime).
var theCJKSegmenter cjkSegmenter

// prewarmCJKSegmenter starts the background dictionary load. Idempotent; cheap
// after the first call (a single atomic check inside sync.Once).
func prewarmCJKSegmenter() {
	theCJKSegmenter.once.Do(func() {
		c := &theCJKSegmenter
		c.done = make(chan struct{})
		go func() {
			// Panic containment per the panic-resilience convention: a loader
			// panic must degrade to the UAX #29 fallback, never kill the TUI.
			defer func() {
				if r := recover(); r != nil {
					c.loadFailed.Store(true)
					utils.LogPanic("tui.cjk-dictionary-load", r)
				}
				close(c.done)
			}()
			seg := &gse.Segmenter{}
			// Same path as gse's LoadDictEmbed("zh") → loadZh().
			if err := seg.LoadDictStr(zhSDict + zhTDict); err != nil {
				c.loadFailed.Store(true)
				return
			}
			c.seg.Store(seg)
		}()
	})
}

// cut segments text with the dictionary. ok=false when the dictionary is not
// loaded yet or failed; callers fall back to the UAX #29 result.
func (c *cjkSegmenter) cut(text string) (tokens []string, ok bool) {
	seg := c.seg.Load()
	if seg == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return seg.Cut(text), true
}

// containsHanRune reports whether s contains any Han character. Called with
// the typed chunk on every Editor.HandleInput; ASCII keystrokes (escape
// sequences, single letters) reject after a few bytes.
func containsHanRune(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// isHanSegment reports whether seg is non-empty and consists entirely of Han
// characters. Kana and Hangul never pass, so they keep their UAX #29 behavior
// (Hangul already forms word runs under UAX #29; see deviations above).
func isHanSegment(seg string) bool {
	if seg == "" {
		return false
	}
	for _, r := range seg {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return true
}

// mergeHanRunsWith re-groups maximal runs of ≥2 consecutive all-Han segments
// using the dictionary-backed segmenter. Everything else (Latin, digits,
// punctuation, whitespace, single Han characters) passes through unchanged.
// While the dictionary is not ready, the first Han run triggers the async load
// and the current call keeps the UAX #29 result; after a load failure it never
// tries again.
func mergeHanRunsWith(c *cjkSegmenter, segments []SegmentData) []SegmentData {
	// Fast path: no re-groupable Han run → input untouched, dictionary never
	// touched (pure non-CJK sessions pay nothing).
	hasRun := false
	for i := 0; i+1 < len(segments); i++ {
		if isHanSegment(segments[i].Segment) && isHanSegment(segments[i+1].Segment) {
			hasRun = true
			break
		}
	}
	if !hasRun {
		return segments
	}
	if c.seg.Load() == nil {
		if c.loadFailed.Load() {
			return segments
		}
		prewarmCJKSegmenter() // no-op if already loading; this call falls back
		return segments
	}

	out := make([]SegmentData, 0, len(segments))
	i := 0
	for i < len(segments) {
		if i+1 < len(segments) && isHanSegment(segments[i].Segment) && isHanSegment(segments[i+1].Segment) {
			j := i + 1
			for j < len(segments) && isHanSegment(segments[j].Segment) {
				j++
			}
			var sb strings.Builder
			for k := i; k < j; k++ {
				sb.WriteString(segments[k].Segment)
			}
			run := sb.String()
			if tokens, ok := c.cut(run); ok && tilesRun(run, tokens) {
				index := segments[i].Index
				for _, tok := range tokens {
					out = append(out, SegmentData{
						Segment:    tok,
						Index:      index,
						IsWordLike: segmentIsWordLike(tok),
					})
					index += len(tok)
				}
				i = j
				continue
			}
			for k := i; k < j; k++ {
				out = append(out, segments[k])
			}
			i = j
			continue
		}
		out = append(out, segments[i])
		i++
	}
	return out
}

// tilesRun reports whether tokens exactly tile run (concatenate back to it, in
// order). Defensive guard: any gse output transformation (e.g. its ASCII
// lowercasing) must never reach the navigation code.
func tilesRun(run string, tokens []string) bool {
	total := 0
	for _, tok := range tokens {
		total += len(tok)
	}
	return total == len(run) && strings.Join(tokens, "") == run
}
