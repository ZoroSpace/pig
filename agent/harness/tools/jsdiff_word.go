// jsdiff_word.go — DiffWords port from the npm `diff` package (v8.0.4,
// pin from pi's packages/agent/package.json): libesm/diff/word.js plus the
// string helpers it consumes from libesm/util/string.js. Consumed by the
// coding-agent tool-render layer's renderDiff (pi: modes/interactive/
// components/diff.ts imports `Diff.diffWords` from npm; pig vendors it).
//
// Declared deviations:
//   - js \s and trim operate on Unicode WhiteSpace; pig uses the js \s class
//     spelled out explicitly for tokenization/ws helpers, and
//     strings.TrimSpace for equals (differs only on U+FEFF inside tokens).
//   - Prefix/suffix/ws helpers operate on runes (JS: UTF-16 code units);
//     identical for the BMP text diffing is used on.
//   - The intlSegmenter option path and diffWordsWithSpace are not ported
//     (pi's diff.ts never passes options).
package tools

import (
	"regexp"
	"strings"
)

// wordExtendedChars is word.js's extendedWordChars (Latin script ranges
// counted as word characters).
const wordExtendedChars = `a-zA-Z0-9_\x{AD}\x{C0}-\x{D6}\x{D8}-\x{F6}\x{F8}-\x{2C6}\x{2C8}-\x{2D7}\x{2DE}-\x{2FF}\x{1E00}-\x{1EFF}`

// jsSpaceClass is the JavaScript \s character class (ECMA-262 WhiteSpace +
// LineTerminators). Go RE2's \s is ASCII-only, so it is spelled out.
const jsSpaceClass = `\f\n\r\t\v\x{0020}\x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// tokenizeIncludingWhitespace is word.js's tokenizeIncludingWhitespace:
// each token is a word run, a whitespace run, or a single non-word char.
var wordTokenizeRe = regexp.MustCompile(`[` + wordExtendedChars + `]+|[` + jsSpaceClass + `]+|[^` + wordExtendedChars + `]`)

// jsSpaceRe mirrors JS /\s/ tests on a single token/part.
var jsSpaceRe = regexp.MustCompile(`[` + jsSpaceClass + `]`)

// wordLeadingWsRe mirrors JS /^\s+/ (used by WordDiff.join).
var wordLeadingWsRe = regexp.MustCompile(`^[` + jsSpaceClass + `]+`)

// DiffWords produces the word-level change objects (jsdiff: diffWords with
// no options — pi's diff.ts call shape). Tokens carry their surrounding
// whitespace; equals trims; join strips non-initial leading whitespace; and
// the postProcess pass dedupes whitespace across adjacent change objects,
// so rendered diffs highlight words without their indentation.
func DiffWords(oldStr string, newStr string) []LineDiffChange {
	return diffTokens(wordDiffTokenize(oldStr), wordDiffTokenize(newStr), tokenDiffHooks{
		equals:      wordDiffEquals,
		join:        wordDiffJoin,
		postProcess: wordDiffPostProcess,
	})
}

// wordDiffEquals is WordDiff.equals: trim equality (options.ignoreCase is
// never set by pi's call shape).
func wordDiffEquals(left string, right string) bool {
	return strings.TrimSpace(left) == strings.TrimSpace(right)
}

// wordDiffTokenize is WordDiff.tokenize (non-segmenter path): regex parts
// stitched so each token carries its adjacent whitespace.
func wordDiffTokenize(value string) []string {
	parts := wordTokenizeRe.FindAllString(value, -1)
	tokens := make([]string, 0, len(parts))
	var prevPart *string
	for i := range parts {
		part := parts[i]
		switch {
		case jsSpaceRe.MatchString(part):
			if prevPart == nil {
				tokens = append(tokens, part)
			} else {
				tokens[len(tokens)-1] += part
			}
		case prevPart != nil && jsSpaceRe.MatchString(*prevPart):
			if tokens[len(tokens)-1] == *prevPart {
				tokens[len(tokens)-1] += part
			} else {
				tokens = append(tokens, *prevPart+part)
			}
		default:
			tokens = append(tokens, part)
		}
		prevPart = &parts[i]
	}
	return tokens
}

// wordDiffJoin is WordDiff.join: strip the leading whitespace from all
// tokens except the first.
func wordDiffJoin(tokens []string) string {
	var sb strings.Builder
	for i, token := range tokens {
		if i == 0 {
			sb.WriteString(token)
		} else {
			sb.WriteString(wordLeadingWsRe.ReplaceAllString(token, ""))
		}
	}
	return sb.String()
}

// wordDiffPostProcess is WordDiff.postProcess: whitespace dedupe between
// the change objects since the last keep (oneChangePerToken is never set).
func wordDiffPostProcess(changes []LineDiffChange) []LineDiffChange {
	if len(changes) == 0 {
		return changes
	}
	var lastKeep *LineDiffChange
	var insertion *LineDiffChange
	var deletion *LineDiffChange
	for i := range changes {
		c := &changes[i]
		if c.Added {
			insertion = c
		} else if c.Removed {
			deletion = c
		} else {
			if insertion != nil || deletion != nil {
				dedupeWhitespaceInChangeObjects(lastKeep, deletion, insertion, c)
			}
			lastKeep = c
			insertion = nil
			deletion = nil
		}
	}
	if insertion != nil || deletion != nil {
		dedupeWhitespaceInChangeObjects(lastKeep, deletion, insertion, nil)
	}
	return changes
}

// dedupeWhitespaceInChangeObjects is word.js's
// dedupeWhitespaceInChangeObjects: tidy leading/trailing whitespace across
// a deletion/insertion run between two keeps so trailing whitespace in one
// object is not repeated as leading whitespace in the next. nil endKeep
// means the run reaches the end of the text.
func dedupeWhitespaceInChangeObjects(startKeep *LineDiffChange, deletion *LineDiffChange, insertion *LineDiffChange, endKeep *LineDiffChange) {
	switch {
	case deletion != nil && insertion != nil:
		oldWsPrefix, oldWsSuffix := leadingAndTrailingWs(deletion.Value)
		newWsPrefix, newWsSuffix := leadingAndTrailingWs(insertion.Value)
		if startKeep != nil {
			commonWsPrefix := longestCommonPrefix(oldWsPrefix, newWsPrefix)
			startKeep.Value = replaceSuffix(startKeep.Value, newWsPrefix, commonWsPrefix)
			deletion.Value = removePrefix(deletion.Value, commonWsPrefix)
			insertion.Value = removePrefix(insertion.Value, commonWsPrefix)
		}
		if endKeep != nil {
			commonWsSuffix := longestCommonSuffix(oldWsSuffix, newWsSuffix)
			endKeep.Value = replacePrefix(endKeep.Value, newWsSuffix, commonWsSuffix)
			deletion.Value = removeSuffix(deletion.Value, commonWsSuffix)
			insertion.Value = removeSuffix(insertion.Value, commonWsSuffix)
		}
	case insertion != nil:
		// The whitespaces all reflect what was in the new text rather than
		// the old, so each change object keeps its trailing whitespace and
		// duplicate leading whitespace is deleted.
		if startKeep != nil {
			ws := leadingWs(insertion.Value)
			insertion.Value = substringFrom(insertion.Value, lenRunes(ws))
		}
		if endKeep != nil {
			ws := leadingWs(endKeep.Value)
			endKeep.Value = substringFrom(endKeep.Value, lenRunes(ws))
		}
		// otherwise we've got a deletion and no insertion
	case startKeep != nil && endKeep != nil:
		newWsFull := leadingWs(endKeep.Value)
		delWsStart, delWsEnd := leadingAndTrailingWs(deletion.Value)
		// Any whitespace that comes straight after startKeep in both the old
		// and new texts, assign to startKeep and remove from the deletion.
		newWsStart := longestCommonPrefix(newWsFull, delWsStart)
		deletion.Value = removePrefix(deletion.Value, newWsStart)
		// Any whitespace that comes straight before endKeep in both the old
		// and new texts, and hasn't already been assigned to startKeep,
		// assign to endKeep and remove from the deletion.
		newWsEnd := longestCommonSuffix(removePrefix(newWsFull, newWsStart), delWsEnd)
		deletion.Value = removeSuffix(deletion.Value, newWsEnd)
		endKeep.Value = replacePrefix(endKeep.Value, newWsFull, newWsEnd)
		// If there's any whitespace from the new text that HASN'T already
		// been assigned, assign it to the start:
		startKeep.Value = replaceSuffix(startKeep.Value, newWsFull, sliceRunes(newWsFull, 0, lenRunes(newWsFull)-lenRunes(newWsEnd)))
	case endKeep != nil:
		// We are at the start of the text. Preserve all the whitespace on
		// endKeep, and just remove whitespace from the end of deletion to
		// the extent that it overlaps with the start of endKeep.
		endKeepWsPrefix := leadingWs(endKeep.Value)
		deletionWsSuffix := trailingWs(deletion.Value)
		overlap := maximumOverlap(deletionWsSuffix, endKeepWsPrefix)
		deletion.Value = removeSuffix(deletion.Value, overlap)
	case startKeep != nil:
		// We are at the END of the text. Preserve all the whitespace on
		// startKeep, and just remove whitespace from the start of deletion
		// to the extent that it overlaps with the end of startKeep.
		startKeepWsSuffix := trailingWs(startKeep.Value)
		deletionWsPrefix := leadingWs(deletion.Value)
		overlap := maximumOverlap(startKeepWsSuffix, deletionWsPrefix)
		deletion.Value = removePrefix(deletion.Value, overlap)
	}
}

// longestCommonPrefix is util/string.js longestCommonPrefix.
func longestCommonPrefix(str1 string, str2 string) string {
	r1, r2 := []rune(str1), []rune(str2)
	i := 0
	for i < len(r1) && i < len(r2) && r1[i] == r2[i] {
		i++
	}
	return string(r1[:i])
}

// longestCommonSuffix is util/string.js longestCommonSuffix (including its
// empty-string special case guarding JS slice(-0)).
func longestCommonSuffix(str1 string, str2 string) string {
	if str1 == "" || str2 == "" {
		return ""
	}
	r1, r2 := []rune(str1), []rune(str2)
	if r1[len(r1)-1] != r2[len(r2)-1] {
		return ""
	}
	i := 0
	for i < len(r1) && i < len(r2) && r1[len(r1)-1-i] == r2[len(r2)-1-i] {
		i++
	}
	return string(r1[len(r1)-i:])
}

// replacePrefix is util/string.js replacePrefix (mismatch is a bug, same as
// the JS throw).
func replacePrefix(s string, oldPrefix string, newPrefix string) string {
	if !strings.HasPrefix(s, oldPrefix) {
		panic("string " + s + " doesn't start with prefix " + oldPrefix + "; this is a bug")
	}
	return newPrefix + substringFrom(s, lenRunes(oldPrefix))
}

// replaceSuffix is util/string.js replaceSuffix.
func replaceSuffix(s string, oldSuffix string, newSuffix string) string {
	if oldSuffix == "" {
		return s + newSuffix
	}
	if !strings.HasSuffix(s, oldSuffix) {
		panic("string " + s + " doesn't end with suffix " + oldSuffix + "; this is a bug")
	}
	return substringTo(s, lenRunes(s)-lenRunes(oldSuffix)) + newSuffix
}

// removePrefix is util/string.js removePrefix.
func removePrefix(s string, oldPrefix string) string {
	return replacePrefix(s, oldPrefix, "")
}

// removeSuffix is util/string.js removeSuffix.
func removeSuffix(s string, oldSuffix string) string {
	return replaceSuffix(s, oldSuffix, "")
}

// maximumOverlap is util/string.js maximumOverlap (KMP overlapCount).
func maximumOverlap(string1 string, string2 string) string {
	return sliceRunes(string2, 0, overlapCount([]rune(string1), []rune(string2)))
}

// overlapCount is util/string.js overlapCount (KMP back-reference table)
// over runes.
func overlapCount(a []rune, b []rune) int {
	startA := 0
	if len(a) > len(b) {
		startA = len(a) - len(b)
	}
	endB := len(b)
	if len(a) < len(b) {
		endB = len(a)
	}
	b = b[:endB]
	m := make([]int, endB)
	k := 0
	for j := 1; j < endB; j++ {
		if b[j] == b[k] {
			m[j] = m[k]
		} else {
			m[j] = k
		}
		for k > 0 && b[j] != b[k] {
			k = m[k]
		}
		if b[j] == b[k] {
			k++
		}
	}
	k = 0
	for i := startA; i < len(a); i++ {
		if k >= endB {
			// JS reads b[k] past the table (undefined): the mismatch chain
			// escapes the table and k stays undefined for the rest of the
			// scan, so maximumOverlap ends up returning the whole of
			// string2 (slice(0, undefined)). endB == len(b) exactly when
			// this is reachable, so report a full overlap.
			return endB
		}
		for k > 0 && a[i] != b[k] {
			k = m[k]
		}
		if a[i] == b[k] {
			k++
		}
	}
	return k
}

// leadingAndTrailingWs is util/string.js leadingAndTrailingWs
// (non-segmenter path).
func leadingAndTrailingWs(s string) (string, string) {
	return leadingWs(s), trailingWs(s)
}

// trailingWs is util/string.js trailingWs (non-segmenter path).
func trailingWs(s string) string {
	runes := []rune(s)
	i := len(runes) - 1
	for i >= 0 && jsIsSpace(runes[i]) {
		i--
	}
	return string(runes[i+1:])
}

// leadingWs is util/string.js leadingWs (non-segmenter path).
func leadingWs(s string) string {
	runes := []rune(s)
	i := 0
	for i < len(runes) && jsIsSpace(runes[i]) {
		i++
	}
	return string(runes[:i])
}

// jsIsSpace reports whether r is in the JavaScript \s class.
func jsIsSpace(r rune) bool {
	return jsSpaceRe.MatchString(string(r))
}

// lenRunes is rune length as a helper for the ws/prefix math above.
func lenRunes(s string) int { return len([]rune(s)) }

// substringFrom returns the substring starting at rune index i.
func substringFrom(s string, i int) string {
	r := []rune(s)
	if i >= len(r) {
		return ""
	}
	if i < 0 {
		i = 0
	}
	return string(r[i:])
}

// substringTo returns the substring ending before rune index i.
func substringTo(s string, i int) string {
	r := []rune(s)
	if i <= 0 {
		return ""
	}
	if i > len(r) {
		i = len(r)
	}
	return string(r[:i])
}

// sliceRunes returns runes[i:j] of s as a string.
func sliceRunes(s string, i int, j int) string {
	r := []rune(s)
	if i < 0 {
		i = 0
	}
	if j > len(r) {
		j = len(r)
	}
	if i >= j {
		return ""
	}
	return string(r[i:j])
}
