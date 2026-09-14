// tui/cjk_segmenter.go — pig-only CJK word re-grouping for word navigation
// (word_navigation.go); no pi counterpart. pi relies on ICU's Intl.Segmenter
// word granularity, which applies a built-in CJ dictionary: 你好世界 →
// [你好][世界] (pi's word-navigation.test.ts numbers prove the grouping even
// though its comment claims per-character). Go's standard UAX #29
// implementation (rivo/uniseg) has no dictionary and cuts Han text per
// character, so CJK word navigation (Ctrl+←/→, word deletion) moves one
// character at a time. This file regroups maximal runs of ≥2 consecutive
// all-Han segments through a pluggable CJK backend.
//
// Backends (selected once per process at first use via PIG_CJK_SEGMENTER):
//
//   - bigram (default): groups Han runs into fixed 2-character chunks
//     (odd tail → 1 char). Zero dependencies, zero state, zero startup
//     cost — 人工智能 becomes 人工|智能, which is coarser than ICU but
//     far better than per-character, and it eliminates the dictionary
//     memory/startup bill entirely (see cjk_segmenter_sst.go for numbers).
//   - sst: dictionary-backed, zero-dependency implementation over the same
//     vendored zh dictionaries — sorted word table + DAG/Viterbi, ~12MB
//     steady state (cjk_segmenter_sst.go).
//   - gse: the original github.com/go-ego/gse backend, kept as the
//     ICU-parity reference (and the HMM-for-OOV behavior pig shipped with
//     in 2026-09). ~206MB live heap on this dictionary — the reason the
//     default moved to bigram.
//
// Shared design (all backends):
//   - Hybrid alignment: UAX #29 stays the base segmenter (word_navigation.go);
//     only maximal runs of ≥2 consecutive all-Han segments are re-cut.
//     Latin/code/punctuation boundaries stay byte-identical to UAX #29,
//     and the ported word-navigation/editor tests keep their non-CJK values.
//     Dictionary backends must never see non-Han text: gse lowercases ASCII
//     ("English" → "english"), so its output only enters through
//     tiling-checked pure-Han runs; bigram is trivially safe.
//   - Async init: the dictionary parses ~8.3MB of text (gse: ~0.9s,
//     ~300MB transient allocation peak; sst: ~0.4s, ~50MB transient) in a
//     background goroutine — fired on the first Han keystroke
//     (Editor.HandleInput) and, as a fallback, on the first word operation
//     that encounters a Han run. Until the load completes (or permanently
//     after a failure), segmentation falls back to UAX #29 per-character.
//     No call path ever blocks. bigram has nothing to load.
//   - Vendored dictionary: the zh_s/zh_t files are embedded from
//     tui/assets/dict/ (see SOURCE.md there). The gse backend loads them
//     via gse.Segmenter.LoadDictStr — the same code path as gse's own
//     LoadDictEmbed("zh"). Vendoring avoids gse's package-level go:embed
//     blobs (22.6MB Japanese dictionary + 5.9MB IDF data), which the Go
//     linker keeps in the binary even when unreferenced (+31MB dead weight;
//     with vendoring the binary pays only the 8.3MB actually used).
//   - Single instance + mutex: gse's concurrency safety is undocumented and
//     the SST scratch buffers are reusable, so backend Cut calls are
//     serialized (~1.5µs per cut; contention is negligible at keystroke
//     rates). bigram is stateless and lock-free.
//
// Declared deviations from pi/ICU:
//   - The default bigram grouping is coarser than ICU's dictionary
//     granularity (今天天气: ICU/gse 今天/天气-ish, bigram 今天|天天|气真).
//     This is a deliberate pig default (user decision 2026-09-14); set
//     PIG_CJK_SEGMENTER=gse for ICU-parity groupings.
//   - Japanese kana stays on UAX #29 boundaries (the zh dictionaries do not
//     cover it); ICU also dictionary-segments Japanese.
//   - Dictionary grouping follows word frequencies, which occasionally
//     differs from ICU's exact choices (e.g. 今天天气 as one token vs ICU
//     今天/天气). Word-unit granularity, not exact tokens, is the goal.

package tui

import (
	_ "embed"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/ZoroSpace/pig/utils"
	"github.com/go-ego/gse"
)

// The zh dictionaries are vendored from gse (see assets/dict/SOURCE.md) and
// embedded here instead of using gse's LoadDictEmbed, which drags gse's
// package-level embed blobs (Japanese dictionary, IDF data) into the binary.
//
//go:embed assets/dict/zh_s.txt
var zhSDict string

//go:embed assets/dict/zh_t.txt
var zhTDict string

// ============================================================================
// Backend selection
// ============================================================================

// cjkSegmenterMode selects the CJK regrouping backend.
type cjkSegmenterMode uint8

const (
	// cjkModeBigram re-cuts Han runs into fixed 2-character chunks. Default.
	cjkModeBigram cjkSegmenterMode = iota
	// cjkModeSST uses the vendored dictionaries via a sorted word table
	// (cjk_segmenter_sst.go) — dictionary quality without gse's memory bill.
	cjkModeSST
	// cjkModeGSE uses github.com/go-ego/gse — the original backend, kept as
	// the ICU-parity reference.
	cjkModeGSE
)

// cjkSegmenterEnv names the environment variable that selects the backend
// (tui is a leaf package and cannot reach the settings manager, so — like
// tui.go's own knobs — this is env-only):
//
//	PIG_CJK_SEGMENTER=bigram|sst|gse
//
// Unset, empty, and unrecognized values fall back to the bigram default
// (configuration can never break word navigation; it can only degrade it).
const cjkSegmenterEnv = "PIG_CJK_SEGMENTER"

// String returns the PIG_CJK_SEGMENTER value spelling of the mode.
func (m cjkSegmenterMode) String() string {
	switch m {
	case cjkModeBigram:
		return "bigram"
	case cjkModeSST:
		return "sst"
	case cjkModeGSE:
		return "gse"
	}
	return "unknown"
}

// resolveCJKSegmenterMode maps a raw PIG_CJK_SEGMENTER value to a mode.
// Matching is case-insensitive and whitespace-tolerant; anything
// unrecognized resolves to the bigram default.
func resolveCJKSegmenterMode(value string) cjkSegmenterMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "sst":
		return cjkModeSST
	case "gse":
		return cjkModeGSE
	default:
		// "" (unset), "bigram", and unknown values all land here.
		return cjkModeBigram
	}
}

// ============================================================================
// Segmenter facade
// ============================================================================

// cjkSegmenter owns the selected backend: it resolves the mode once, starts
// the dictionary backends' async loads, and dispatches Cut calls. The zero
// value is not usable — mode resolution happens in ensureInit (once), which
// every entry point funnels through.
type cjkSegmenter struct {
	once       sync.Once // guards mode resolution + backend start
	mode       cjkSegmenterMode
	ready      chan struct{} // closed when the load attempt ends (bigram: immediately)
	mu         sync.Mutex    // serializes dict-backend Cut calls (see header)
	loadFailed atomic.Bool   // dict backends: set on load failure (permanent UAX fallback)
	gseSeg     atomic.Pointer[gse.Segmenter]
	sstDict    atomic.Pointer[sstDictionary]
	sstScratch sstScratch // reusable cut buffers; guarded by mu
}

// theCJKSegmenter is the process-wide instance (pi has no equivalent:
// Intl.Segmenter ships inside the JS runtime).
var theCJKSegmenter = &cjkSegmenter{}

// prewarmCJKSegmenter starts backend initialization. Idempotent; cheap
// after the first call (a single atomic check inside sync.Once).
func prewarmCJKSegmenter() {
	theCJKSegmenter.ensureInit()
}

// ensureInit resolves the mode on first use and starts the backend. For
// dictionary backends the load runs in a background goroutine; ready is
// closed when the attempt ends either way.
func (c *cjkSegmenter) ensureInit() {
	c.once.Do(func() {
		c.mode = resolveCJKSegmenterMode(os.Getenv(cjkSegmenterEnv))
		c.ready = make(chan struct{})
		switch c.mode {
		case cjkModeBigram:
			close(c.ready) // nothing to load
		case cjkModeSST:
			go c.loadSST()
		case cjkModeGSE:
			go c.loadGSE()
		}
	})
}

// loadGSE parses the vendored dictionaries through gse in the background.
func (c *cjkSegmenter) loadGSE() {
	// Panic containment per the panic-resilience convention: a loader panic
	// must degrade to the UAX #29 fallback, never kill the TUI.
	defer func() {
		if r := recover(); r != nil {
			c.loadFailed.Store(true)
			utils.LogPanic("tui.cjk-dictionary-load", r)
		}
		close(c.ready)
	}()
	seg := &gse.Segmenter{}
	// Same path as gse's LoadDictEmbed("zh") → loadZh().
	if err := seg.LoadDictStr(zhSDict + zhTDict); err != nil {
		c.loadFailed.Store(true)
		return
	}
	c.gseSeg.Store(seg)
}

// cutReady reports whether cut can serve immediately: bigram is always
// ready; dictionary backends need their async load to have finished.
func (c *cjkSegmenter) cutReady() bool {
	switch c.mode {
	case cjkModeSST:
		return c.sstDict.Load() != nil
	case cjkModeGSE:
		return c.gseSeg.Load() != nil
	default:
		return true // bigram
	}
}

// cut segments a pure-Han run into tokens that tile it exactly. ok=false
// while a dictionary backend is still loading (or after a permanent
// failure); callers fall back to the UAX #29 result.
func (c *cjkSegmenter) cut(run string) (tokens []string, ok bool) {
	switch c.mode {
	case cjkModeBigram:
		return bigramCut(run), true
	case cjkModeSST:
		d := c.sstDict.Load()
		if d == nil {
			return nil, false
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.sstCutLocked(d, run), true
	case cjkModeGSE:
		seg := c.gseSeg.Load()
		if seg == nil {
			return nil, false
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		return seg.Cut(run), true
	}
	return nil, false
}

// bigramCut groups run into fixed 2-character chunks; an odd tail becomes a
// single character (快捷键 → 快捷|键). Chunks are counted in characters, not
// bytes, so extended-plane Han (4-byte runes) still pairs correctly. The
// output always tiles run exactly (tilesRun), and every chunk stays pure
// Han, so IsWordLike classification is unaffected.
func bigramCut(run string) []string {
	tokens := make([]string, 0, len(run)/6+1)
	for i := 0; i < len(run); {
		_, w1 := utf8.DecodeRuneInString(run[i:])
		j := i + w1
		if j >= len(run) {
			tokens = append(tokens, run[i:j]) // odd tail
			break
		}
		_, w2 := utf8.DecodeRuneInString(run[j:])
		tokens = append(tokens, run[i:j+w2])
		i = j + w2
	}
	return tokens
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
// using the selected backend. Everything else (Latin, digits, punctuation,
// whitespace, single Han characters) passes through unchanged. While a
// dictionary backend is not ready, the first Han run triggers the async load
// and the current call keeps the UAX #29 result; after a load failure it
// never tries again. bigram serves the very first call.
func mergeHanRunsWith(c *cjkSegmenter, segments []SegmentData) []SegmentData {
	// Fast path: no re-groupable Han run → input untouched, backend never
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
	c.ensureInit() // resolves the mode; dict backends start their async load
	if !c.cutReady() {
		// Still loading (or permanently failed after a load error): this
		// call keeps the UAX #29 result.
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

// tilesRun reports whether tokens exactly tile run (concatenate back to it,
// in order). Defensive guard: any backend output transformation (e.g. gse's
// ASCII lowercasing) must never reach the navigation code.
func tilesRun(run string, tokens []string) bool {
	total := 0
	for _, tok := range tokens {
		total += len(tok)
	}
	return total == len(run) && strings.Join(tokens, "") == run
}
