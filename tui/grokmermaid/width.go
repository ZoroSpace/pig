// Ported from pi node_modules/grok-mermaid/src/width.ts (grok-mermaid v0.2.2,
// Apache-2.0) as part of T5b-4. Source string width measuring in grapheme
// clusters, backed by the generated unicode-width table.
//
// pig deviation: pi clusters via Intl.Segmenter (UAX #29); pig uses
// rivo/uniseg (also UAX #29) over runes. TS strings are UTF-16 code unit
// sequences while Go strings are UTF-8 — lone surrogates (invalid UTF-8)
// become U+FFFD, an edge case the parsers never produce since stripControls
// sanitizes untrusted source first.
package grokmermaid

import "github.com/rivo/uniseg"

const vs16 = 0xfe0f

func isRegionalIndicator(cp rune) bool { return cp >= 0x1f1e6 && cp <= 0x1f1ff }

// codePointWidth returns the width of one code point; the table covers the
// whole code point space.
func codePointWidth(cp rune) int {
	lo := 0
	hi := len(widths) - 1
	for lo <= hi {
		mid := (lo + hi) >> 1
		run := widths[mid]
		if cp < run.lo {
			hi = mid - 1
		} else if cp > run.hi {
			lo = mid + 1
		} else {
			return int(run.w)
		}
	}
	return 1
}

// clusterWidth returns the columns occupied by one grapheme cluster.
//
// The widest code point wins, so a base plus its combining marks measures as
// the base. Two adjustments: a variation selector requesting emoji
// presentation forces two columns, as does a regional indicator pair (a flag).
//
// Zero is a real answer — a soft hyphen or zero-width space occupies nothing,
// and callers skip painting such a cluster rather than reserving a cell.
func clusterWidth(cluster string) int {
	w := 0
	vs16Seen := false
	regional := 0
	for _, cp := range cluster {
		if cp == vs16 {
			vs16Seen = true
		}
		if isRegionalIndicator(cp) {
			regional++
		}
		if cw := codePointWidth(cp); cw > w {
			w = cw
		}
	}
	if vs16Seen || regional >= 2 {
		return 2
	}
	return w
}

// clusters iterates grapheme clusters, so no loop can split one.
func clusters(s string) []string {
	var out []string
	state := -1
	for len(s) > 0 {
		var cluster string
		cluster, s, _, state = uniseg.FirstGraphemeClusterInString(s, state)
		out = append(out, cluster)
	}
	return out
}

// measuredCluster is one grapheme cluster paired with its display width (pi:
// the [segment, width] pairs `measured` yields).
type measuredCluster struct {
	text string
	w    int
}

// measured iterates clusters paired with their display width.
func measured(s string) []measuredCluster {
	var out []measuredCluster
	state := -1
	for len(s) > 0 {
		var cluster string
		cluster, s, _, state = uniseg.FirstGraphemeClusterInString(s, state)
		out = append(out, measuredCluster{cluster, clusterWidth(cluster)})
	}
	return out
}

// stringWidth returns the display columns of a string.
func stringWidth(s string) int {
	w := 0
	state := -1
	for len(s) > 0 {
		var cluster string
		cluster, s, _, state = uniseg.FirstGraphemeClusterInString(s, state)
		w += clusterWidth(cluster)
	}
	return w
}
