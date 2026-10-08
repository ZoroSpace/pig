// pi: src/utils/ansi.ts — strip ANSI escape sequences.
//
// Portions of this file are derived from:
// - ansi-regex (https://github.com/chalk/ansi-regex)
// - strip-ansi (https://github.com/chalk/strip-ansi)
// MIT License, Copyright (c) Sindre Sorhus — see the pi source for the full
// notice; the regex patterns are ported verbatim.
package utils

import "regexp"

// Valid string terminator sequences are BEL, ESC\, and 0x9c (pi: ST).
const ansiST = "(?:\x07|\x1b\\\\|\u009c)"

// OSC sequences: ESC ] ... ST (pi: OSC_START).
const ansiOSCStart = "\x1b\\]"

// CSI and related: ESC/C1, optional intermediates, optional params (supports
// ; and :), then final byte (pi: CSI_START / CSI_FINAL).
const ansiCSIStart = "[\x1b\u009b][[\\]()#;?]*(?:\\d{1,4}(?:[;:]\\d{0,4})*)?"

const ansiCSIFinal = "[\\dA-PR-TZcf-nq-uy=><~]"

// Complete sequences. OSC is non-greedy until the first ST (pi: ansiRegex,
// compiled once at module level).
var ansiRegex = regexp.MustCompile("(?:" + ansiOSCStart + "[\\s\\S]*?" + ansiST + ")|" + ansiCSIStart + ansiCSIFinal)

// Unfinished sequence at the end of the text: OSC without its ST (a trailing
// ESC may start ESC\), or CSI without its final byte (pi:
// unfinishedAnsiAtEndRegex). Go's RE2 has no look-ahead, so pi's
// `\u001B(?!\\)` is rewritten as the equivalent alternation
// `\x1b(?:$|[^\x5c])`: ESC at end-of-text, or ESC followed by a
// non-backslash character. Escapes here are regex-level (double backslashes
// in the Go source); the class members are literal BEL/U+009C/ESC bytes.
var unfinishedAnsiAtEndRegex = regexp.MustCompile(
	"(?:" + ansiOSCStart + "(?:[^\x07\u009c\x1b]|\\x1b(?:$|[^\\x5c]))*|" + ansiCSIStart + ")$")

// maxPendingAnsiLength is the longest unfinished sequence held back while
// streaming; longer ones are processed as-is (pi: MAX_PENDING_ANSI_LENGTH).
const maxPendingAnsiLength = 256

// SplitIncompleteAnsiSuffix splits streamed text into a part that is safe to
// pass to StripAnsi now and a trailing unfinished escape sequence that should
// be prepended to the next chunk (pi: splitIncompleteAnsiSuffix).
//
// Window arithmetic deviates from pi by design: pi slices by UTF-16 code
// units, Go by bytes — the window is only a bounding heuristic for the
// look-back, and every possible split point (before an ESC/CSI introducer or
// the U+009B rune) is at a rune boundary in both.
func SplitIncompleteAnsiSuffix(value string) (complete, pending string) {
	// Fast path: unfinished sequences require ESC (7-bit) or CSI (8-bit).
	if !containsAny(value, "\x1b", "\u009b") {
		return value, ""
	}
	windowStart := len(value) - maxPendingAnsiLength
	if windowStart < 0 {
		windowStart = 0
	}
	match := unfinishedAnsiAtEndRegex.FindStringIndex(value[windowStart:])
	if match == nil {
		return value, ""
	}
	splitAt := windowStart + match[0]
	return value[:splitAt], value[splitAt:]
}

// StripAnsi removes ANSI escape sequences (pi: stripAnsi). The TypeError
// guard has no Go counterpart (static typing).
func StripAnsi(value string) string {
	// Fast path: ANSI codes require ESC (7-bit) or CSI (8-bit) introducer.
	if !containsAny(value, "\x1b", "\u009b") {
		return value
	}
	return ansiRegex.ReplaceAllString(value, "")
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if n != "" && len(s) >= len(n) {
			for i := 0; i+len(n) <= len(s); i++ {
				if s[i:i+len(n)] == n {
					return true
				}
			}
		}
	}
	return false
}
