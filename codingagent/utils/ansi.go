// pi: src/utils/ansi.ts — strip ANSI escape sequences.
//
// Portions of this file are derived from:
// - ansi-regex (https://github.com/chalk/ansi-regex)
// - strip-ansi (https://github.com/chalk/strip-ansi)
// MIT License, Copyright (c) Sindre Sorhus — see the pi source for the full
// notice; the regex pattern is ported verbatim.
package utils

import "regexp"

// ansiRegexPattern mirrors pi's ansiRegex(): OSC sequences plus CSI and
// related C1 sequences.
const ansiRegexPattern = "(?:\x1b\\][\\s\\S]*?(?:\x07|\x1b\\\\|\u009c))|[\x1b\u009b][[\\]()#;?]*(?:\\d{1,4}(?:[;:]\\d{0,4})*)?[\\dA-PR-TZcf-nq-uy=><~]"

var ansiRegex = regexp.MustCompile(ansiRegexPattern)

// StripAnsi removes ANSI escape sequences (pi: stripAnsi). The TypeError
// guard has no Go counterpart (static typing).
func StripAnsi(value string) string {
	// Fast path: ANSI codes require ESC (7-bit) or CSI (8-bit) introducer.
	if !containsAny(value, "\x1b", "\x9b") {
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
