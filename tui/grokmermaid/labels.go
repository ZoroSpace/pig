// Ported from pi node_modules/grok-mermaid/src/labels.ts (grok-mermaid v0.2.2,
// Apache-2.0) as part of T5b-4. Label measuring, wrapping, truncation and
// source-text cleaning helpers.
package grokmermaid

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// WRAP_WIDTH: node labels wrap to at most this many display columns per line.
const WrapWidth = 24

// MAX_LINES: ... and at most this many lines; overflow is truncated with an
// ellipsis.
const MaxLines = 4

// MAX_LABEL: edge labels are truncated to this many columns.
const MaxLabel = 28

// LabelBreakChars are identifier-boundary characters preferred as break points
// when a single word is too wide to fit, so it is not sliced mid-segment.
//
// Mirrors TOKEN_BREAK_CHARS in grok-build's
// third_party/mermaid-to-svg/src/text_wrap.rs; the two renderers are
// deliberately independent, so keep these in sync.
var LabelBreakChars = []string{"_", "-", ".", "/"}

// asciiLower is ASCII-only case folding, matching Rust's to_ascii_lowercase.
//
// String.prototype.toLowerCase can change a string's length (İ becomes two
// code points), which would desync the byte offsets some parsers slice with.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

// asciiUpper is the ASCII-only mirror of asciiLower.
func asciiUpper(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r + ('A' - 'a')
		}
		return r
	}, s)
}

// controlsRe matches C0 and C1 controls, less the \t\n\r the parsers and
// SrcLines read.
//
// They measure one column and paint none, so a box sized around one is drawn a
// column short of its own border; NUL also collides with the CONT sentinel
// and is dropped after layout has already paid for its cell; ESC would inject
// ANSI into the caller's scrollback. decodeEntityBody refuses to decode an
// entity into one — this closes the same hole for literals.
var controlsRe = regexp.MustCompile(`[\x{0}-\x{8}\x{b}\x{c}\x{e}-\x{1f}\x{7f}-\x{9f}]`)

// stripControls is applied by every public entry point that takes untrusted
// source.
func stripControls(src string) string { return controlsRe.ReplaceAllString(src, "") }

// srcLines splits source into lines the way Rust's str::lines() does: on \n,
// with a trailing \r stripped, and without a final empty line when the input
// ends in a newline. String.split yields that extra element, which would show
// up as a spurious blank row inside a source box.
func srcLines(src string) []string {
	parts := strings.Split(src, "\n")
	for i, l := range parts {
		parts[i] = strings.TrimSuffix(l, "\r")
	}
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// isAlphanumeric matches Rust's char::is_alphanumeric (pi: the
// /[\p{Alphabetic}\p{N}]/u test). pig deviation: Go has no Alphabetic
// property check, so Letter|Number is used — the difference is only
// Other_Alphabetic combining marks, which never appear uncombined in labels.
func isAlphanumeric(c string) bool {
	for _, r := range c {
		return unicode.IsLetter(r) || unicode.IsNumber(r)
	}
	return false
}

// isIdChar reports the characters allowed in a bare node/state/class
// identifier.
func isIdChar(c string) bool { return isAlphanumeric(c) || c == "_" }

const entityLookahead = 10

var namedEntities = map[string]string{
	"lt":   "<",
	"gt":   ">",
	"amp":  "&",
	"quot": "\"",
	"apos": "'",
}

func decodeEntityBody(body string) string {
	if named, ok := namedEntities[body]; ok {
		return named
	}
	if !strings.HasPrefix(body, "#") {
		return ""
	}
	num := body[1:]
	hex := num != "" && (num[0] == 'x' || num[0] == 'X')
	digits := num
	if hex {
		digits = num[1:]
	}
	valid := digits != ""
	if valid && hex {
		for _, r := range digits {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				valid = false
				break
			}
		}
	} else if valid {
		for _, r := range digits {
			if r < '0' || r > '9' {
				valid = false
				break
			}
		}
	}
	if !valid {
		return ""
	}
	base := 10
	if hex {
		base = 16
	}
	code, err := strconv.ParseInt(digits, base, 64)
	if err != nil {
		return ""
	}
	// Surrogates and out-of-range values are not characters at all.
	if code > 0x10ffff || (code >= 0xd800 && code <= 0xdfff) {
		return ""
	}
	// Reject control chars: NUL collides with the CONT sentinel and ESC would
	// inject ANSI into scrollback.
	if code < 0x20 || (code >= 0x7f && code <= 0x9f) {
		return ""
	}
	return string(rune(code))
}

// decodeHtmlEntities decodes HTML entities in label text. Called once per
// label: via cleanLabel for bracketed labels, or explicitly at each
// direct-push sink.
func decodeHtmlEntities(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	chars := []rune(s)
	var out strings.Builder
	i := 0
	for i < len(chars) {
		if chars[i] != '&' {
			out.WriteRune(chars[i])
			i++
			continue
		}
		// Scan a bounded window including the terminating `;`, so a stray `&`
		// or an over-long run stays literal.
		hi := i + 1 + entityLookahead
		if hi > len(chars) {
			hi = len(chars)
		}
		semi := -1
		for j := i + 1; j < hi; j++ {
			if chars[j] == ';' {
				semi = j
				break
			}
		}
		decoded := ""
		if semi != -1 {
			decoded = decodeEntityBody(string(chars[i+1 : semi]))
		}
		if decoded == "" {
			out.WriteByte('&')
			i++
		} else {
			// Resume past the `;`. The single pass never re-scans emitted text,
			// so `&amp;lt;` decodes to the literal `&lt;` rather than to `<`.
			out.WriteString(decoded)
			i = semi + 1
		}
	}
	return out.String()
}

// stripMarkdown strips markdown emphasis from a `backtick` label string.
func stripMarkdown(s string) string {
	var noCode strings.Builder
	for _, c := range s {
		if c != '`' {
			noCode.WriteRune(c)
		}
	}
	noStrong := strings.ReplaceAll(strings.ReplaceAll(noCode.String(), "**", ""), "__", "")
	chars := []rune(noStrong)
	var out strings.Builder
	for i := 0; i < len(chars); i++ {
		c := chars[i]
		// Keep `*`/`_` only when they sit inside a word, so snake_case survives.
		inWord := i > 0 &&
			isAlphanumeric(string(chars[i-1])) &&
			i+1 < len(chars) &&
			isAlphanumeric(string(chars[i+1]))
		if (c == '*' || c == '_') && !inWord {
			continue
		}
		out.WriteRune(c)
	}
	return strings.TrimSpace(out.String())
}

// htmlFormatTags are inline formatting tags that carry no meaning in a
// terminal. Anything else that looks like a tag — Vec<String>, <id> — is left
// alone.
var htmlFormatTags = map[string]bool{
	"b": true, "strong": true, "i": true, "em": true, "u": true, "s": true,
	"strike": true, "del": true, "ins": true, "mark": true, "small": true,
	"big": true, "sub": true, "sup": true, "code": true, "kbd": true,
	"samp": true, "var": true, "tt": true, "span": true, "font": true,
	"q": true, "abbr": true, "cite": true, "pre": true,
}

// htmlTagAt reads a tag starting at start, returning its name and the index
// after `>`. Indices are rune indices into chars.
func htmlTagAt(chars []rune, start int) (name string, end int, ok bool) {
	i := start + 1
	if i < len(chars) && chars[i] == '/' {
		i++
	}
	nameStart := i
	for i < len(chars) && ((chars[i] >= '0' && chars[i] <= '9') || (chars[i] >= 'A' && chars[i] <= 'Z') || (chars[i] >= 'a' && chars[i] <= 'z')) {
		i++
	}
	if i == nameStart {
		return "", 0, false
	}
	name = string(chars[nameStart:i])
	for i < len(chars) && chars[i] != '>' {
		if chars[i] == '<' {
			return "", 0, false
		}
		i++
	}
	if i < len(chars) && chars[i] == '>' {
		return name, i + 1, true
	}
	return "", 0, false
}

func stripHtmlTags(s string) string {
	chars := []rune(s)
	var out strings.Builder
	i := 0
	for i < len(chars) {
		if chars[i] == '<' {
			if tag, end, ok := htmlTagAt(chars, i); ok {
				lower := asciiLower(tag)
				if lower == "br" {
					out.WriteByte(' ')
					i = end
					continue
				}
				if htmlFormatTags[lower] {
					i = end
					continue
				}
			}
		}
		out.WriteRune(chars[i])
		i++
	}
	return out.String()
}

// unwrap strips one matching pair of wrapping delimiters, if present. ok is
// false when the delimiters are absent.
func unwrap(s, open, close string) (string, bool) {
	if len(s) >= len(open)+len(close) && strings.HasPrefix(s, open) && strings.HasSuffix(s, close) {
		return s[len(open) : len(s)-len(close)], true
	}
	return "", false
}

// cleanLabel normalises raw label text: strip markup, unquote, and decode
// entities.
//
// Decoding happens after tag-stripping so <b> is removed as markup while
// &lt;b&gt; survives as the literal text <b>.
func cleanLabel(raw string) string {
	trimmed := strings.TrimSpace(stripHtmlTags(strings.TrimSpace(raw)))
	unquoted, ok := unwrap(trimmed, "\"", "\"")
	if !ok {
		unquoted, ok = unwrap(trimmed, "'", "'")
	}
	if !ok {
		unquoted = trimmed
	}
	unquoted = strings.TrimSpace(unquoted)
	md, ok := unwrap(unquoted, "`", "`")
	if !ok {
		return decodeHtmlEntities(unquoted)
	}
	return decodeHtmlEntities(stripMarkdown(strings.TrimSpace(md)))
}

// lastBreak returns the rune index of the last identifier-boundary character,
// or -1.
func lastBreak(s string) int {
	best := -1
	for _, c := range LabelBreakChars {
		if i := strings.LastIndex(s, c); i > best {
			best = i
		}
	}
	return best
}

// wrapLabel wraps a label to width columns over at most maxLines lines,
// truncating the last line with an ellipsis if it overflows.
//
// A word too wide to fit is broken after the last identifier boundary
// (_-./) that fits, falling back to a per-character break when it has none.
func wrapLabel(label string, width, maxLines int) []string {
	if width < 1 {
		width = 1
	}
	var lines []string
	cur := ""
	curW := 0

	for _, word := range splitFields(label) {
		ww := stringWidth(word)
		if ww > width {
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
			chunk := ""
			chunkW := 0
			for _, ch := range measured(word) {
				if chunkW+ch.w > width && chunk != "" {
					p := lastBreak(chunk)
					carry := ""
					if p != -1 {
						carry = chunk[p+1:]
						lines = append(lines, chunk[:p+1])
					} else {
						lines = append(lines, chunk)
					}
					chunk = carry
					chunkW = stringWidth(carry)
				}
				chunk += ch.text
				chunkW += ch.w
			}
			cur = chunk
			curW = chunkW
		} else if cur == "" {
			cur = word
			curW = ww
		} else if curW+1+ww <= width {
			cur += " " + word
			curW += 1 + ww
		} else {
			lines = append(lines, cur)
			cur = word
			curW = ww
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}

	if len(lines) > maxLines {
		lines = lines[:maxLines]
		target := width - 1
		if target < 1 {
			target = 1
		}
		s := ""
		sw := 0
		for _, ch := range measured(lines[len(lines)-1]) {
			if sw+ch.w > target {
				break
			}
			s += ch.text
			sw += ch.w
		}
		lines[len(lines)-1] = s + "…"
	}
	return lines
}

// fitLabel truncates to inner columns, leaving room for the ellipsis.
func fitLabel(label string, inner int) string {
	if stringWidth(label) <= inner {
		return label
	}
	out := ""
	used := 0
	for _, ch := range measured(label) {
		if used+ch.w+1 > inner {
			break
		}
		out += ch.text
		used += ch.w
	}
	return out + "…"
}

// splitFields mirrors JS `s.split(/\s+/).filter(w => w !== ”)` — JS \s is
// not exactly unicode.IsSpace (it adds \ufeff, omits a few), so the class is
// spelled out.
var jsWhitespaceRe = regexp.MustCompile("[\t\n\v\f\r \u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff]+")

func splitFields(s string) []string {
	var out []string
	for _, f := range jsWhitespaceRe.Split(s, -1) {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}
