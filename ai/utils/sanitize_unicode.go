// pi: utils/sanitize-unicode.ts.
package utils

// SanitizeSurrogates removes unpaired UTF-16 surrogate code points from a
// string (pi: sanitizeSurrogates). Unpaired surrogates (U+D800-U+DBFF without
// a following low surrogate, or U+DC00-U+DFFF without a preceding high
// surrogate) cause JSON serialization errors in many API providers.
//
// Go strings are UTF-8, where a non-BMP character is one 4-byte sequence
// (never a surrogate pair), so well-formed input contains no surrogates at
// all. The only surrogates that can appear are the raw 3-byte UTF-8 encodings
// of unpaired surrogates from malformed producers (e.g. hand-built tool
// results); those are stripped here, exactly the characters pi's regex
// removes. Valid emoji and other non-BMP characters are unaffected.
func SanitizeSurrogates(text string) string {
	out := make([]byte, 0, len(text))
	for i := 0; i < len(text); {
		b := text[i]
		if b == 0xed && i+2 < len(text) {
			second := text[i+1]
			// High surrogates D800-DBFF encode as ED A0-BF; low surrogates
			// DC00-DFFF encode as ED 80-9F.
			if (second >= 0xa0 && second <= 0xbf) || (second >= 0x80 && second <= 0x9f) {
				i += 3
				continue
			}
		}
		out = append(out, b)
		i++
	}
	return string(out)
}
