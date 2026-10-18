// pi: modes/rpc/jsonl.ts — strict LF-only JSONL framing for the RPC protocol.
//
// Go adaptation (declared): pi attaches data/end listeners to a Node stream
// and swaps a StringDecoder for incremental UTF-8 handling. Go reads from an
// io.Reader in a goroutine; bufio.Reader already handles incomplete runes at
// chunk boundaries, so the StringDecoder equivalent collapses into the
// reader. Framing is otherwise identical: split on LF only, tolerate a
// trailing CR, and emit a final unterminated line at EOF.
package rpc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// SerializeJsonLine serializes a single strict JSONL record (pi:
// serializeJsonLine). Framing is LF-only; payload strings may contain other
// Unicode separators such as U+2028 and U+2029. Clients must split records
// on `\n` only.
//
// Go's encoding/json unconditionally escapes U+2028/U+2029 (to keep output
// valid JavaScript) and, when escaping HTML, `<`, `>`, `&`; pi's
// JSON.stringify does neither. The encoder runs with SetEscapeHTML(false) and
// then unescapes only the two line-separator escapes, matching JSON.stringify.
func SerializeJsonLine(value any) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return string(unescapeLineSeparators(buf.Bytes())), nil
}

// unescapeLineSeparators converts the `\u2028`/`\u2029` escape sequences that
// encoding/json emits back into their raw UTF-8 form. Escaped backslashes are
// copied verbatim so a literal `\\u2028` in the source string is preserved.
func unescapeLineSeparators(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if data[i] != '\\' {
			out = append(out, data[i])
			i++
			continue
		}
		if i+6 <= len(data) && data[i+1] == 'u' &&
			data[i+2] == '2' && data[i+3] == '0' && data[i+4] == '2' &&
			(data[i+5] == '8' || data[i+5] == '9') {
			out = append(out, 0xE2, 0x80)
			if data[i+5] == '8' {
				out = append(out, 0xA8)
			} else {
				out = append(out, 0xA9)
			}
			i += 6
			continue
		}
		out = append(out, data[i])
		i++
		if i < len(data) {
			out = append(out, data[i])
			i++
		}
	}
	return out
}

// AttachJsonlLineReader attaches an LF-only JSONL reader to a stream (pi:
// attachJsonlLineReader). It intentionally does not use Go's text/scanner or
// any Unicode-separator-aware splitter: records split on `\n` only, because
// U+2028/U+2029 are valid inside JSON strings.
//
// The returned detach stops the reader loop after the current read returns;
// the underlying reader is not closed (matching pi, which only removes its
// stream listeners). The done channel closes when the loop exits.
func AttachJsonlLineReader(reader io.Reader, onLine func(line string)) (detach func(), done <-chan struct{}) {
	doneCh := make(chan struct{})
	// stopped is closed by detach; the loop checks it after every line so a
	// fast reader stops promptly.
	stopped := make(chan struct{})

	go func() {
		defer close(doneCh)
		buffered := bufio.NewReader(reader)
		for {
			select {
			case <-stopped:
				return
			default:
			}
			line, err := buffered.ReadString('\n')
			if len(line) > 0 {
				// pi's emitLine strips one trailing CR after removing the
				// LF (done here by trimming both suffixes).
				line = strings.TrimSuffix(line, "\n")
				line = strings.TrimSuffix(line, "\r")
				onLine(line)
			}
			if err != nil {
				return
			}
		}
	}()

	return func() { close(stopped) }, doneCh
}
