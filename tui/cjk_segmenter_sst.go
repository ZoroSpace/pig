// tui/cjk_segmenter_sst.go — SST (sorted string table) backend for the CJK
// word re-grouping (cjk_segmenter.go); pig-only, no pi counterpart.
//
// Zero third-party dependencies: the same vendored zh_s/zh_t dictionaries
// (tui/assets/dict — see SOURCE.md) are parsed into one sorted word table —
// a single []byte blob plus uint32 offsets and float64 frequencies — and
// segmented with the classic DAG + Viterbi maximum-probability path. The
// algorithm mirrors gse's dag.go shape point for point: prefix-driven DAG
// extension (walk while some dictionary word extends the fragment), exact
// words as edges, single-char fallback rows, log(freq)-log(total) scoring
// with the longer word winning ties — so token groupings match the gse
// backend wherever gse's HMM is not involved.
//
// Measured on the 587k-entry zh table (2026-09-14, dictbench probe):
//   - steady-state heap ~12MB (gse backend: ~206MB),
//   - transient allocation during the async build ~50MB peak (gse: ~300MB),
//   - ~29µs per cut of a 100-character run, zero allocations per cut
//     (scratch buffers below are reused; cut is serialized by the facade
//     mutex) — word-navigation call rates make this unmeasurable.
//
// Declared deviations vs the gse backend:
//   - No HMM: out-of-vocabulary runs keep per-character tokens (gse hands
//     single-character buffers to its HMM model). Pure-Han runs are the
//     only input here, so gse's ASCII-run buffering cannot trigger.
//   - Frequencies are summed in sorted word order; gse sums in file order.
//     Sub-ULP float rounding only — token-level behavior is pinned equal
//     by the tests wherever it matters.
package tui

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ZoroSpace/pig/utils"
)

// sstDictionary is the sorted word table: every dictionary word's UTF-8
// bytes packed into one blob, addressed by offsets, with frequencies in the
// same (sorted) order. Lookups are binary searches over the offset table —
// no trie, no map, ~12MB total for the 587k-word zh table.
type sstDictionary struct {
	blob    []byte    // concatenated word bytes, sorted by word
	offsets []uint32  // len = numWords+1; word i = blob[offsets[i]:offsets[i+1]]
	freqs   []float64 // per-word frequency (float64 to match gse's scoring)
	total   float64   // Σfreqs — the Viterbi normalizer (gse: Dict.totalFreq)
	maxWord int       // longest word in bytes — DAG extension bound (gse: maxTokenLen)
}

func (d *sstDictionary) numWords() int { return len(d.freqs) }

func (d *sstDictionary) word(i int) []byte {
	return d.blob[d.offsets[i]:d.offsets[i+1]]
}

// lookup binary-searches the table for q. It returns the index of the first
// word ≥ q, whether that word starts with q (some dictionary word extends q
// — the DAG walk may continue), and whether it IS q (exact match → DAG
// edge). One search serves both questions, mirroring how gse's single trie
// Jump answers "can the fragment extend" and "is it a word" together.
func (d *sstDictionary) lookup(q string) (idx int, prefixOK, exact bool) {
	lo, hi := 0, d.numWords()
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if compareWordBytes(d.word(mid), q) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == d.numWords() || !hasWordPrefix(d.word(lo), q) {
		return lo, false, false
	}
	return lo, true, len(d.word(lo)) == len(q)
}

// compareWordBytes compares a dictionary word (bytes) with an input slice
// (string) without allocating — []byte(q) would copy on every binary-search
// step. Returns -1/0/1 like bytes.Compare.
func compareWordBytes(w []byte, q string) int {
	n := len(w)
	if len(q) < n {
		n = len(q)
	}
	for i := 0; i < n; i++ {
		switch {
		case w[i] < q[i]:
			return -1
		case w[i] > q[i]:
			return 1
		}
	}
	switch {
	case len(w) < len(q):
		return -1
	case len(w) > len(q):
		return 1
	}
	return 0
}

// hasWordPrefix reports whether the dictionary word w starts with q.
func hasWordPrefix(w []byte, q string) bool {
	return len(w) >= len(q) && compareWordBytes(w[:len(q)], q) == 0
}

// buildSSTDictionary parses the vendored dictionaries into the sorted table.
//
// Parity with the gse backend's word list (gse LoadDictStr → AddToken):
//   - lines need "word freq [pos]" — pos is irrelevant to segmentation and
//     dropped here (gse stores it in Token but Cut never reads it);
//   - unparseable and sub-MinTokenFreq frequencies skip the line (gse Init()
//     default MinTokenFreq=2.0 → Size() returns 0; the vendored files
//     contain no such line — the guard keeps the lists from silently
//     diverging);
//   - single-character words get their frequency forced to exactly 2 (gse
//     Size(): "filter words with a length of 1 to reduce the word
//     frequency" — unconditional, after the MinTokenFreq filter). Without
//     this the real char frequencies let single-char paths beat rare
//     multi-char words and the groupings drift from gse;
//   - duplicate words keep their first occurrence in file order (gse's
//     AddToken skips words already in the trie) — zh_s before zh_t, then a
//     stable sort keeps that relative order within equal words.
func buildSSTDictionary() *sstDictionary {
	type entry struct {
		word string
		freq float64
	}
	entries := make([]entry, 0, 620000)
	for _, raw := range []string{zhSDict, zhTDict} {
		for rest := raw; len(rest) > 0; {
			line := rest
			if i := strings.IndexByte(rest, '\n'); i >= 0 {
				line, rest = rest[:i], rest[i+1:]
			} else {
				rest = "" // last line, no trailing newline
			}
			sp := strings.IndexByte(line, ' ')
			if sp <= 0 {
				continue
			}
			freqText := line[sp+1:]
			if sp2 := strings.IndexByte(freqText, ' '); sp2 >= 0 {
				freqText = freqText[:sp2] // drop pos
			}
			freq, err := strconv.ParseFloat(freqText, 64)
			if err != nil || freq < 2.0 {
				continue
			}
			// gse Size(): single-character words are forced to freq 2 (see
			// the parity note above) — the char's real frequency must not
			// leak into the Viterbi scoring.
			if utf8.RuneCountInString(line[:sp]) < 2 {
				freq = 2
			}
			entries = append(entries, entry{line[:sp], freq})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].word < entries[j].word })

	d := &sstDictionary{}
	d.offsets = make([]uint32, 0, len(entries)+1)
	d.freqs = make([]float64, 0, len(entries))
	for k := 0; k < len(entries); k++ {
		if k > 0 && entries[k].word == entries[k-1].word {
			continue // duplicates are adjacent after the stable sort; first wins
		}
		e := entries[k]
		d.offsets = append(d.offsets, uint32(len(d.blob)))
		d.blob = append(d.blob, e.word...)
		d.freqs = append(d.freqs, e.freq)
		d.total += e.freq
		if len(e.word) > d.maxWord {
			d.maxWord = len(e.word)
		}
	}
	d.offsets = append(d.offsets, uint32(len(d.blob)))
	return d
}

// loadSST is the background loader goroutine — same panic containment and
// ready-channel contract as loadGSE (cjk_segmenter.go).
func (c *cjkSegmenter) loadSST() {
	defer func() {
		if r := recover(); r != nil {
			c.loadFailed.Store(true)
			utils.LogPanic("tui.cjk-sst-load", r)
		}
		close(c.ready)
	}()
	c.sstDict.Store(buildSSTDictionary())
}

// sstScratch holds the per-cut working buffers. cut is serialized by the
// facade mutex, so one instance serves all calls; slices grow on demand and
// are reused, which is what keeps a cut allocation-free (except the result
// slice itself — tokens are subslices of run, never copies).
type sstScratch struct {
	offs     []int32   // byte offset per character, len = nChars+1
	rowStart []int32   // len = nChars+1; row i = dagEnds[rowStart[i]:rowStart[i+1]]
	dagEnds  []int32   // flat DAG edges: end (exclusive char index) per row, ascending
	best     []float64 // Viterbi score per position, len = nChars+1
	nextEnd  []int32   // chosen row-end per position, len = nChars
}

// sstCutLocked segments run (pure Han by construction — mergeHanRunsWith
// only feeds all-Han runs) and returns tokens that tile it exactly.
// Callers hold c.mu.
func (c *cjkSegmenter) sstCutLocked(d *sstDictionary, run string) []string {
	s := &c.sstScratch

	// Pass 1: character byte offsets.
	s.offs = s.offs[:0]
	for i := 0; i < len(run); {
		s.offs = append(s.offs, int32(i))
		_, w := utf8.DecodeRuneInString(run[i:])
		i += w
	}
	s.offs = append(s.offs, int32(len(run)))
	n := len(s.offs) - 1
	if n == 0 {
		return nil
	}

	// Pass 2: DAG (gse getDag shape). For every start position, extend one
	// character at a time while some dictionary word extends the fragment
	// (the sorted-table property makes "no extension" terminal for all
	// longer fragments too); exact words become edges. A position nothing
	// matched gets the single-char fallback edge (gse: dag[k] = [k]).
	s.rowStart = s.rowStart[:0]
	s.dagEnds = s.dagEnds[:0]
	for i := 0; i < n; i++ {
		s.rowStart = append(s.rowStart, int32(len(s.dagEnds)))
		for j := i + 1; j <= n; j++ {
			q := run[s.offs[i]:s.offs[j]]
			if len(q) > d.maxWord {
				break // longer fragments cannot be dictionary words either
			}
			_, prefixOK, exact := d.lookup(q)
			if !prefixOK {
				break
			}
			if exact {
				s.dagEnds = append(s.dagEnds, int32(j))
			}
		}
		if int(s.rowStart[i]) == len(s.dagEnds) {
			s.dagEnds = append(s.dagEnds, int32(i+1)) // single-char fallback
		}
	}
	s.rowStart = append(s.rowStart, int32(len(s.dagEnds)))

	// Pass 3: Viterbi (gse calc shape). Work backward: best path score from
	// position i is max over edges of log(freq)−log(total)+best[end]; the
	// fallback edge scores with the log(1.0) pseudo-frequency. Ties prefer
	// the longer word (larger end index); edges arrive in ascending end
	// order, matching gse's dag construction order.
	s.best = append(s.best[:0], make([]float64, n+1)...)
	s.nextEnd = append(s.nextEnd[:0], make([]int32, n)...)
	logTotal := math.Log(d.total)
	for i := n - 1; i >= 0; i-- {
		bf := math.Inf(1)
		be := int32(i + 1)
		first := true
		for _, e := range s.dagEnds[s.rowStart[i]:s.rowStart[i+1]] {
			var f float64
			if idx, _, exact := d.lookup(run[s.offs[i]:s.offs[e]]); exact {
				f = math.Log(d.freqs[idx]) - logTotal + s.best[e]
			} else {
				f = math.Log(1.0) - logTotal + s.best[e]
			}
			if first || f > bf || (f == bf && e > be) {
				bf, be = f, e
				first = false
			}
		}
		s.best[i] = bf
		s.nextEnd[i] = be
	}

	// Pass 4: emit. Tokens are subslices of run (no byte copies); the
	// chosen ends strictly increase, so the walk terminates at n.
	tokens := make([]string, 0, n/2+1)
	for x := int32(0); x < int32(n); {
		y := s.nextEnd[x]
		tokens = append(tokens, run[s.offs[x]:s.offs[y]])
		x = y
	}
	return tokens
}
