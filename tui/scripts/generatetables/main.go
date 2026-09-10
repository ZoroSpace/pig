// tui/scripts/generatetables — regenerates tui/tables_generated.go.
//
// pi's tui/src/utils.ts relies on ECMAScript v-flag Unicode property regexes
// (\p{Default_Ignorable_Code_Point}, \p{Control}, \p{Mark}, \p{Surrogate},
// \p{Format}, \p{Spacing_Mark}, \p{RGI_Emoji}) and Intl.Segmenter. Go's
// regexp/stdlib has no equivalents for Default_Ignorable_Code_Point,
// Grapheme_Cluster_Break=SpacingMark, Script_Extensions, or RGI_Emoji, so the
// needed tables are generated here from the Unicode Character Database.
//
// Data source pin: UCD 15.1.0 (matches the ICU bundled with Node 22 / Bun,
// which pi-tui runs on). Go stdlib / uniseg may carry a newer Unicode version;
// the delta is limited to newly-assigned code points.
//
// Downloaded files:
//
//	https://www.unicode.org/Public/15.1.0/ucd/DerivedCoreProperties.txt
//	https://www.unicode.org/Public/15.1.0/ucd/auxiliary/GraphemeBreakProperty.txt
//	https://www.unicode.org/Public/15.1.0/ucd/ScriptExtensions.txt
//	https://www.unicode.org/Public/15.1.0/ucd/Scripts.txt
//	https://www.unicode.org/Public/15.1.0/ucd/emoji/emoji-data.txt
//	https://www.unicode.org/Public/15.1.0/ucd/emoji/emoji-sequences.txt
//	https://www.unicode.org/Public/15.1.0/ucd/emoji/emoji-zwj-sequences.txt
//
// Run: cd tui/scripts/generatetables && go run .
// Output: tui/tables_generated.go (overwritten in place).
package main

import (
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const ucdBase = "https://www.unicode.org/Public/15.1.0/ucd"
const emojiBase = "https://www.unicode.org/Public/emoji/15.1"

type rng struct{ lo, hi rune }

func fetch(path string) string {
	return fetchAt(ucdBase, path)
}

func fetchAt(base, path string) string {
	url := base + path
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fatalf("fetch %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fatalf("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fatalf("read %s: %v", url, err)
	}
	return string(body)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "generatetables: "+format+"\n", args...)
	os.Exit(1)
}

// parseCodeRanges extracts the code point field (before the first ';') of UCD
// lines that contain the wanted property value in the given field index, and
// returns them as ranges. Handles both single code points and lo..hi ranges.
func parseCodeRanges(data, property string, fieldIndex int) []rng {
	var out []rng
	for _, line := range strings.Split(data, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ";")
		if len(fields) <= fieldIndex {
			continue
		}
		values := strings.Fields(fields[fieldIndex])
		if len(values) == 0 || values[0] != property {
			continue
		}
		codeField := strings.TrimSpace(fields[0])
		loStr, hiStr, isRange := strings.Cut(codeField, "..")
		lo, err := strconv.ParseUint(loStr, 16, 32)
		if err != nil {
			fatalf("bad code point %q in line %q", codeField, line)
		}
		hi := lo
		if isRange {
			hi, err = strconv.ParseUint(hiStr, 16, 32)
			if err != nil {
				fatalf("bad range %q in line %q", codeField, line)
			}
		}
		out = append(out, rng{lo: rune(lo), hi: rune(hi)})
	}
	return out
}

// parseCodeSetField matches lines where any whitespace-separated value in the
// given field equals the wanted property (used for Script_Extensions, where a
// line lists all scripts sharing the extension).
func parseCodeSetField(data, field string, fieldIndex int) []rng {
	var out []rng
	for _, line := range strings.Split(data, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ";")
		if len(fields) <= fieldIndex {
			continue
		}
		values := strings.Fields(fields[fieldIndex])
		found := false
		for _, v := range values {
			if v == field {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		codeField := strings.TrimSpace(fields[0])
		loStr, hiStr, isRange := strings.Cut(codeField, "..")
		lo, err := strconv.ParseUint(loStr, 16, 32)
		if err != nil {
			fatalf("bad code point %q in line %q", codeField, line)
		}
		hi := lo
		if isRange {
			hi, err = strconv.ParseUint(hiStr, 16, 32)
			if err != nil {
				fatalf("bad range %q in line %q", codeField, line)
			}
		}
		out = append(out, rng{lo: rune(lo), hi: rune(hi)})
	}
	return out
}

// parseSequences collects the sequences of all lines whose type_field (index
// 1) is in properties. Used for emoji sequence files, where RGI_Emoji is the
// union of several type fields (see UTS #51 ED-27 and the file headers).
func parseSequences(data string, properties ...string) []string {
	want := make(map[string]bool, len(properties))
	for _, p := range properties {
		want[p] = true
	}
	var out []string
	for _, line := range strings.Split(data, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ";")
		if len(fields) < 2 || !want[strings.TrimSpace(fields[1])] {
			continue
		}
		codeField := strings.TrimSpace(fields[0])
		// Multi-code-point sequences are space-separated (e.g. "00A9 FE0F");
		// ranges (e.g. "231A..231B") expand to individual single-code-point
		// sequences; a single hex value is one sequence.
		if strings.Contains(codeField, " ") {
			var sb strings.Builder
			for _, cpStr := range strings.Fields(codeField) {
				cp, err := strconv.ParseUint(cpStr, 16, 32)
				if err != nil {
					fatalf("bad code point %q in line %q", cpStr, line)
				}
				sb.WriteRune(rune(cp))
			}
			out = append(out, sb.String())
			continue
		}
		loStr, hiStr, isRange := strings.Cut(codeField, "..")
		lo, err := strconv.ParseUint(loStr, 16, 32)
		if err != nil {
			fatalf("bad code point %q in line %q", codeField, line)
		}
		hi := lo
		if isRange {
			hi, err = strconv.ParseUint(hiStr, 16, 32)
			if err != nil {
				fatalf("bad range %q in line %q", codeField, line)
			}
		}
		for cp := lo; cp <= hi; cp++ {
			out = append(out, string(rune(cp)))
		}
	}
	return out
}

func mergeRanges(in []rng) []rng {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].lo < in[j].lo })
	var out []rng
	cur := in[0]
	for _, r := range in[1:] {
		if r.lo <= cur.hi+1 {
			if r.hi > cur.hi {
				cur.hi = r.hi
			}
			continue
		}
		out = append(out, cur)
		cur = r
	}
	return append(out, cur)
}

// subRange removes the single code point r from the ranges.
func subRange(in []rng, r rune) []rng {
	var out []rng
	for _, g := range in {
		if r < g.lo || r > g.hi {
			out = append(out, g)
			continue
		}
		if g.lo < r {
			out = append(out, rng{g.lo, r - 1})
		}
		if r < g.hi {
			out = append(out, rng{r + 1, g.hi})
		}
	}
	return out
}

func mergeTables(tables ...[]rng) []rng {
	return mergeRanges(concat(tables...))
}

func concat(lists ...[]rng) []rng {
	var out []rng
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

func emitRangeTable(w *strings.Builder, varName, doc string, ranges []rng) {
	fmt.Fprintf(w, "// %s\nvar %s = &unicode.RangeTable{\n", doc, varName)
	latinOffset := 0
	var r16, r32 []rng
	for _, r := range ranges {
		if r.hi <= 0xFFFF {
			r16 = append(r16, r)
			if r.hi <= 0x00FF {
				latinOffset++
			}
		} else {
			r32 = append(r32, r)
		}
	}
	if len(r16) > 0 {
		w.WriteString("\tR16: []unicode.Range16{\n")
		for _, r := range r16 {
			fmt.Fprintf(w, "\t\t{Lo: 0x%04X, Hi: 0x%04X, Stride: 1},\n", r.lo, r.hi)
		}
		w.WriteString("\t},\n")
		fmt.Fprintf(w, "\tLatinOffset: %d,\n", latinOffset)
	}
	if len(r32) > 0 {
		w.WriteString("\tR32: []unicode.Range32{\n")
		for _, r := range r32 {
			fmt.Fprintf(w, "\t\t{Lo: 0x%08X, Hi: 0x%08X, Stride: 1},\n", r.lo, r.hi)
		}
		w.WriteString("\t},\n")
	}
	w.WriteString("}\n\n")
}

func main() {
	dcp := fetch("/DerivedCoreProperties.txt")
	gbp := fetch("/auxiliary/GraphemeBreakProperty.txt")
	scx := fetch("/ScriptExtensions.txt")
	scripts := fetch("/Scripts.txt")
	emojiData := fetch("/emoji/emoji-data.txt")
	emojiSeq := fetchAt(emojiBase, "/emoji-sequences.txt")
	emojiZwj := fetchAt(emojiBase, "/emoji-zwj-sequences.txt")

	// Default_Ignorable_Code_Point (DerivedCoreProperties).
	diRanges := parseCodeRanges(dcp, "Default_Ignorable_Code_Point", 1)

	// Grapheme_Cluster_Break=SpacingMark, folded exactly like pi's
	// terminalSpacingMarkRegex:
	//   [\p{Spacing_Mark}--[\u1734\u302E\u302F]] |
	//   [\u065F\u0F7F\u102B\u102C\u1031\u1033-\u1035\u1038\u103A-\u103E]
	spacingMark := parseCodeRanges(gbp, "SpacingMark", 1)
	for _, r := range []rune{0x1734, 0x302E, 0x302F} {
		spacingMark = subRange(spacingMark, r)
	}
	extras := []rng{{0x065F, 0x065F}, {0x0F7F, 0x0F7F}, {0x102B, 0x102C}, {0x1031, 0x1031},
		{0x1033, 0x1035}, {0x1038, 0x1038}, {0x103A, 0x103E}}
	spacingMarkTerminal := mergeRanges(append(spacingMark, extras...))

	// Script_Extensions union for pi's cjkBreakRegex:
	//   [\p{Script_Extensions=Han}\p{Script_Extensions=Hiragana}
	//    \p{Script_Extensions=Katakana}\p{Script_Extensions=Hangul}
	//    \p{Script_Extensions=Bopomofo}]
	// ScriptExtensions.txt uses 4-letter ISO script codes and only lists code
	// points whose Script_Extensions differs from Script; unlisted code points
	// inherit their Script property, so union with the Scripts.txt bodies.
	cjkScripts := []struct{ scx, script string }{
		{"Hani", "Han"},
		{"Hira", "Hiragana"},
		{"Kana", "Katakana"},
		{"Hang", "Hangul"},
		{"Bopo", "Bopomofo"},
	}
	var scxTables [][]rng
	for _, s := range cjkScripts {
		sigma := parseCodeSetField(scx, s.scx, 1)
		sigma = append(sigma, parseCodeRanges(scripts, s.script, 1)...)
		scxTables = append(scxTables, sigma)
	}
	scxCJK := mergeTables(scxTables...)

	// RGI_Emoji (UTS #51 ED-27): union of Basic_Emoji, Emoji_Keycap_Sequence,
	// RGI_Emoji_Flag_Sequence, RGI_Emoji_Tag_Sequence, RGI_Emoji_Modifier_Sequence
	// (emoji-sequences.txt) and RGI_Emoji_ZWJ_Sequence (emoji-zwj-sequences.txt).
	rgiSequences := []string{
		"Basic_Emoji",
		"Emoji_Keycap_Sequence",
		"RGI_Emoji_Flag_Sequence",
		"RGI_Emoji_Tag_Sequence",
		"RGI_Emoji_Modifier_Sequence",
	}
	rgi := parseSequences(emojiSeq, rgiSequences...)
	rgi = append(rgi, parseSequences(emojiZwj, "RGI_Emoji_ZWJ_Sequence")...)
	sort.Strings(rgi)
	if len(rgi) == 0 {
		fatalf("no RGI_Emoji sequences parsed — emoji sequence files format changed?")
	}
	_ = emojiData

	var b strings.Builder
	b.WriteString("// Code generated by tui/scripts/generatetables; DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// Unicode property tables for tui/utils.go, replacing pi-tui's ECMAScript\n")
	b.WriteString("// v-flag property regexes. Data pinned to UCD 15.1.0 (ICU level of the\n")
	b.WriteString("// Node/Bun runtime pi-tui runs on); see the generator header for sources.\n")
	b.WriteString("//\n")
	b.WriteString("// pi source: packages/tui/src/utils.ts\n\n")
	b.WriteString("package tui\n\n")
	b.WriteString("import \"unicode\"\n\n")

	emitRangeTable(&b, "tableDefaultIgnorable",
		"tableDefaultIgnorable is Default_Ignorable_Code_Point (DerivedCoreProperties).", diRanges)
	emitRangeTable(&b, "tableSpacingMarkTerminal",
		"tableSpacingMarkTerminal is GCB=SpacingMark folded like pi's terminalSpacingMarkRegex\n"+
			"// (minus U+1734/U+302E/U+302F, plus the legacy-wcwidth non-spacing exceptions).", spacingMarkTerminal)
	emitRangeTable(&b, "tableScxCJK",
		"tableScxCJK is the Script_Extensions union of Han/Hiragana/Katakana/Hangul/Bopomofo\n"+
			"// (pi's cjkBreakRegex).", scxCJK)

	b.WriteString("// rgiEmojiSet holds the RGI_Emoji sequences (emoji-data.txt), keyed by\n")
	b.WriteString("// UTF-8 string; pi tests them with /^\\p{RGI_Emoji}$/v.\n")
	b.WriteString("var rgiEmojiSet = map[string]struct{}{\n")
	for _, s := range rgi {
		fmt.Fprintf(&b, "\t%q: {},\n", s)
	}
	b.WriteString("}\n")

	src, err := format.Source([]byte(b.String()))
	if err != nil {
		fatalf("format generated source: %v", err)
	}
	out, err := filepath.Abs("../../tables_generated.go")
	if err != nil {
		fatalf("resolve output path: %v", err)
	}
	if err := os.WriteFile(out, src, 0o644); err != nil {
		fatalf("write %s: %v", out, err)
	}
	fmt.Printf("wrote %s (DI %d ranges, SpacingMarkTerminal %d, ScxCJK %d, RGI_Emoji %d sequences)\n",
		out, len(diRanges), len(spacingMarkTerminal), len(scxCJK), len(rgi))
}
