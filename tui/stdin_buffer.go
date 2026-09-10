// tui/stdin_buffer.go (pi: packages/tui/src/stdin-buffer.ts) — buffers input
// and emits complete sequences.
//
// Stdin data events can arrive in partial chunks, especially for escape
// sequences like mouse events. Without buffering, partial sequences can be
// misinterpreted as regular keypresses. For example, the mouse SGR sequence
// `\x1b[<35;20;5m` might arrive as three chunks.
//
// Based on code from OpenTUI (https://github.com/anomalyco/opentui)
// MIT License - Copyright (c) 2025 opentui
//
// Porting notes:
//   - EventEmitter ('data'/'paste') → single handler functions registered via
//     OnData/OnPaste (pi has exactly one listener per event; multi-listener
//     EventEmitter semantics unused — declared deviation).
//   - setTimeout flush timers → time.AfterFunc; process/flush/timer callbacks
//     are serialized with a single mutex (pi: event loop ordering).
//   - Events are collected during Process and dispatched after the mutex is
//     released, in emission order (pi emits synchronously inside process;
//     ordering is preserved).
//   - Process takes []byte: the single-high-byte → ESC+(b-128) legacy meta
//     conversion is kept. Raw multi-byte input is assumed to arrive on valid
//     UTF-8 boundaries; ProcessTerminal pre-decodes partial UTF-8 tails
//     (Node's setEncoding semantics).
package tui

import (
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	defaultSequenceTimeoutMs = 50
	defaultEscapeTimeoutMs   = 10
	bracketedPasteStart      = "\x1b[200~"
	bracketedPasteEnd        = "\x1b[201~"
)

type stdinBufferOptions struct {
	// TimeoutMs is the maximum time to wait for an incomplete sequence such
	// as CSI or mouse (default: 50ms).
	TimeoutMs int
	// EscapeTimeoutMs is the maximum time to wait after a lone ESC before
	// treating it as Escape (default: 10ms). Increase for high-latency
	// Alt+key input (SSH).
	EscapeTimeoutMs int
}

// StdinBuffer buffers stdin input and emits complete sequences via the data
// handler. Handles partial escape sequences that arrive across multiple chunks.
type StdinBuffer struct {
	mu              sync.Mutex
	buffer          string
	timeout         *time.Timer
	timeoutMs       int
	escapeTimeoutMs int
	pasteMode       bool
	pasteBuffer     string
	// pendingKittyPrintableCodepoint: -1 = none (pi: undefined)
	pendingKittyPrintableCodepoint int

	dataHandler  func(string)
	pasteHandler func(string)
}

// NewStdinBuffer creates a StdinBuffer. options may be nil for defaults.
func NewStdinBuffer(options *stdinBufferOptions) *StdinBuffer {
	b := &StdinBuffer{
		timeoutMs:                      defaultSequenceTimeoutMs,
		escapeTimeoutMs:                defaultEscapeTimeoutMs,
		pendingKittyPrintableCodepoint: -1,
	}
	if options != nil {
		if options.TimeoutMs != 0 {
			b.timeoutMs = options.TimeoutMs
		}
		if options.EscapeTimeoutMs != 0 {
			b.escapeTimeoutMs = options.EscapeTimeoutMs
		}
	}
	return b
}

// OnData registers the handler for complete input sequences.
func (b *StdinBuffer) OnData(handler func(string)) { b.dataHandler = handler }

// OnPaste registers the handler for bracketed paste content.
func (b *StdinBuffer) OnPaste(handler func(string)) { b.pasteHandler = handler }

// stdinEvent is an ordered emission collected during Process.
type stdinEvent struct {
	isPaste bool
	payload string
}

// Process feeds input data through the buffer.
func (b *StdinBuffer) Process(data []byte) {
	// Handle high-byte conversion (for compatibility with parseKeypress):
	// if data is a single byte > 127, convert to ESC + (byte - 128).
	var str string
	if len(data) == 1 && data[0] > 127 {
		str = "\x1b" + string(rune(data[0]-128))
	} else {
		str = string(data)
	}
	b.processString(str)
}

// ProcessString feeds already-decoded input through the buffer (used by
// ProcessTerminal after incremental UTF-8 decoding).
func (b *StdinBuffer) ProcessString(data string) { b.processString(data) }

func (b *StdinBuffer) processString(str string) {
	var events []stdinEvent

	b.mu.Lock()
	b.processLocked(str, &events)
	b.mu.Unlock()

	for _, event := range events {
		if event.isPaste {
			if b.pasteHandler != nil {
				b.pasteHandler(event.payload)
			}
		} else {
			b.emitDataSequence(event.payload)
		}
	}
}

func (b *StdinBuffer) processLocked(str string, events *[]stdinEvent) {
	// Clear any pending timeout
	if b.timeout != nil {
		b.timeout.Stop()
		b.timeout = nil
	}

	if len(str) == 0 && len(b.buffer) == 0 {
		b.emitDataSequenceLocked("", events)
		return
	}

	b.buffer += str

	if b.pasteMode {
		b.pasteBuffer += b.buffer
		b.buffer = ""

		endIndex := strings.Index(b.pasteBuffer, bracketedPasteEnd)
		if endIndex != -1 {
			pastedContent := b.pasteBuffer[:endIndex]
			remaining := b.pasteBuffer[endIndex+len(bracketedPasteEnd):]

			b.pasteMode = false
			b.pasteBuffer = ""
			b.pendingKittyPrintableCodepoint = -1

			*events = append(*events, stdinEvent{isPaste: true, payload: pastedContent})

			if len(remaining) > 0 {
				b.processLocked(remaining, events)
			}
		}
		return
	}

	startIndex := strings.Index(b.buffer, bracketedPasteStart)
	if startIndex != -1 {
		if startIndex > 0 {
			beforePaste := b.buffer[:startIndex]
			sequences, _ := extractCompleteSequences(beforePaste)
			for _, sequence := range sequences {
				b.emitDataSequenceLocked(sequence, events)
			}
		}

		b.pendingKittyPrintableCodepoint = -1
		b.buffer = b.buffer[startIndex+len(bracketedPasteStart):]
		b.pasteMode = true
		b.pasteBuffer = b.buffer
		b.buffer = ""

		endIndex := strings.Index(b.pasteBuffer, bracketedPasteEnd)
		if endIndex != -1 {
			pastedContent := b.pasteBuffer[:endIndex]
			remaining := b.pasteBuffer[endIndex+len(bracketedPasteEnd):]

			b.pasteMode = false
			b.pasteBuffer = ""
			b.pendingKittyPrintableCodepoint = -1

			*events = append(*events, stdinEvent{isPaste: true, payload: pastedContent})

			if len(remaining) > 0 {
				b.processLocked(remaining, events)
			}
		}
		return
	}

	sequences, remainder := extractCompleteSequences(b.buffer)
	b.buffer = remainder

	for _, sequence := range sequences {
		b.emitDataSequenceLocked(sequence, events)
	}

	if len(b.buffer) > 0 {
		timeoutMs := b.timeoutMs
		if b.buffer == "\x1b" {
			timeoutMs = b.escapeTimeoutMs
		}
		b.timeout = time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() {
			flushed := b.flushWithLock()
			for _, sequence := range flushed {
				b.emitDataSequence(sequence)
			}
		})
	}
}

// emitDataSequence dispatches a data event, applying the pending-Kitty-printable
// dedup logic under the lock.
func (b *StdinBuffer) emitDataSequence(sequence string) {
	// The handler may re-enter the buffer (e.g. editor shutdown calls
	// Destroy → Clear), so it must run WITHOUT holding b.mu (pig fix; pi is
	// single-threaded).
	b.mu.Lock()
	var handler func(string)
	if b.dataHandler != nil {
		handler = b.dataHandler
	}
	b.emitDataSequenceLocked(sequence, nil)
	b.mu.Unlock()
	if handler != nil {
		handler(sequence)
	}
}

func (b *StdinBuffer) emitDataSequenceLocked(sequence string, events *[]stdinEvent) {
	rawCodepoint := -1
	if utf16Length(sequence) == 1 {
		if r, _ := utf8.DecodeRuneInString(sequence); r != utf8.RuneError || len(sequence) == 1 {
			rawCodepoint = int(r)
		}
	}
	if rawCodepoint != -1 && rawCodepoint == b.pendingKittyPrintableCodepoint {
		b.pendingKittyPrintableCodepoint = -1
		return
	}

	b.pendingKittyPrintableCodepoint = -1
	if cp, ok := parseUnmodifiedKittyPrintableCodepoint(sequence); ok {
		b.pendingKittyPrintableCodepoint = cp
	}

	if events != nil {
		*events = append(*events, stdinEvent{payload: sequence})
	}
}

// Flush returns and clears any buffered incomplete sequence.
func (b *StdinBuffer) Flush() []string { return b.flushWithLock() }

func (b *StdinBuffer) flushWithLock() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.timeout != nil {
		b.timeout.Stop()
		b.timeout = nil
	}

	if len(b.buffer) == 0 {
		return nil
	}

	sequences := []string{b.buffer}
	b.buffer = ""
	b.pendingKittyPrintableCodepoint = -1
	return sequences
}

// Clear drops buffered content without emitting.
func (b *StdinBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timeout != nil {
		b.timeout.Stop()
		b.timeout = nil
	}
	b.buffer = ""
	b.pasteMode = false
	b.pasteBuffer = ""
	b.pendingKittyPrintableCodepoint = -1
}

// GetBuffer returns the pending (incomplete) buffer contents.
func (b *StdinBuffer) GetBuffer() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer
}

// Destroy clears the buffer and stops timers.
func (b *StdinBuffer) Destroy() { b.Clear() }

// =============================================================================
// Sequence completeness detection
// =============================================================================

type sequenceStatus int

const (
	statusComplete sequenceStatus = iota
	statusIncomplete
	statusNotEscape
)

// isCompleteSequence checks whether data is a complete escape sequence.
func isCompleteSequence(data string) sequenceStatus {
	if !strings.HasPrefix(data, "\x1b") {
		return statusNotEscape
	}

	if len(data) == 1 {
		return statusIncomplete
	}

	afterEsc := data[1:]

	// CSI sequences: ESC [
	if strings.HasPrefix(afterEsc, "[") {
		// Check for old-style mouse sequence: ESC[M + 3 bytes
		if strings.HasPrefix(afterEsc, "[M") {
			// Old-style mouse needs ESC[M + 3 bytes = 6 total
			if len(data) >= 6 {
				return statusComplete
			}
			return statusIncomplete
		}
		return isCompleteCsiSequence(data)
	}

	// OSC sequences: ESC ]
	if strings.HasPrefix(afterEsc, "]") {
		return isCompleteOscSequence(data)
	}

	// DCS sequences: ESC P ... ESC \ (includes XTVersion responses)
	if strings.HasPrefix(afterEsc, "P") {
		return isCompleteDcsSequence(data)
	}

	// APC sequences: ESC _ ... ESC \ (includes Kitty graphics responses)
	if strings.HasPrefix(afterEsc, "_") {
		return isCompleteApcSequence(data)
	}

	// SS3 sequences: ESC O
	if strings.HasPrefix(afterEsc, "O") {
		// ESC O followed by a single character
		if len(afterEsc) >= 2 {
			return statusComplete
		}
		return statusIncomplete
	}

	// Meta key sequences: ESC followed by a single character
	if len(afterEsc) == 1 {
		return statusComplete
	}

	// Unknown escape sequence - treat as complete
	return statusComplete
}

// isCompleteCsiSequence checks whether a CSI sequence is complete.
// CSI sequences: ESC [ ... followed by a final byte (0x40-0x7E)
func isCompleteCsiSequence(data string) sequenceStatus {
	if !strings.HasPrefix(data, "\x1b[") {
		return statusComplete
	}

	// Need at least ESC [ and one more character
	if len(data) < 3 {
		return statusIncomplete
	}

	payload := data[2:]

	// CSI sequences end with a byte in the range 0x40-0x7E (@-~)
	// This includes all letters and several special characters
	lastChar := payload[len(payload)-1]

	if lastChar >= 0x40 && lastChar <= 0x7e {
		// Special handling for SGR mouse sequences
		// Format: ESC[<B;X;Ym or ESC[<B;X;YM
		if strings.HasPrefix(payload, "<") {
			// Must have format: <digits;digits;digits[Mm]
			if mouseMatchRegex.MatchString(payload) {
				return statusComplete
			}
			// If it ends with M or m but doesn't match the pattern, still incomplete
			if lastChar == 'M' || lastChar == 'm' {
				// Check if we have the right structure
				parts := strings.Split(payload[1:len(payload)-1], ";")
				if len(parts) == 3 && allDigits(parts) {
					return statusComplete
				}
			}
			return statusIncomplete
		}

		return statusComplete
	}

	return statusIncomplete
}

var mouseMatchRegex = regexp.MustCompile(`^<\d+;\d+;\d+[Mm]$`)
var digitsOnlyRegex = regexp.MustCompile(`^\d+$`)

func allDigits(parts []string) bool {
	for _, p := range parts {
		if !digitsOnlyRegex.MatchString(p) {
			return false
		}
	}
	return true
}

// isCompleteOscSequence checks whether an OSC sequence is complete.
// OSC sequences: ESC ] ... ST (where ST is ESC \ or BEL)
func isCompleteOscSequence(data string) sequenceStatus {
	if !strings.HasPrefix(data, "\x1b]") {
		return statusComplete
	}
	// OSC sequences end with ST (ESC \) or BEL (\x07)
	if strings.HasSuffix(data, "\x1b\\") || strings.HasSuffix(data, "\x07") {
		return statusComplete
	}
	return statusIncomplete
}

// isCompleteDcsSequence checks whether a DCS sequence is complete.
// DCS sequences: ESC P ... ST (ESC \). Used for XTVersion responses.
func isCompleteDcsSequence(data string) sequenceStatus {
	if !strings.HasPrefix(data, "\x1bP") {
		return statusComplete
	}
	// DCS sequences end with ST (ESC \)
	if strings.HasSuffix(data, "\x1b\\") {
		return statusComplete
	}
	return statusIncomplete
}

// isCompleteApcSequence checks whether an APC sequence is complete.
// APC sequences: ESC _ ... ST (ESC \). Used for Kitty graphics responses.
func isCompleteApcSequence(data string) sequenceStatus {
	if !strings.HasPrefix(data, "\x1b_") {
		return statusComplete
	}
	// APC sequences end with ST (ESC \)
	if strings.HasSuffix(data, "\x1b\\") {
		return statusComplete
	}
	return statusIncomplete
}

var unmodifiedKittyPrintableRegex = regexp.MustCompile(`^\x1b\[(\d+)(?::\d*)?(?::\d+)?u$`)

func parseUnmodifiedKittyPrintableCodepoint(sequence string) (int, bool) {
	m := unmodifiedKittyPrintableRegex.FindStringSubmatch(sequence)
	if m == nil {
		return 0, false
	}
	// parseInt without radix sign handling: the regex only allows digits.
	cp := 0
	for _, ch := range m[1] {
		cp = cp*10 + int(ch-'0')
	}
	if cp >= 32 {
		return cp, true
	}
	return 0, false
}

// extractCompleteSequences splits an accumulated buffer into complete
// sequences, returning them in order plus the trailing incomplete remainder.
func extractCompleteSequences(buffer string) (sequences []string, remainder string) {
	pos := 0

	for pos < len(buffer) {
		remaining := buffer[pos:]

		// Try to extract a sequence starting at this position
		if strings.HasPrefix(remaining, "\x1b") {
			// Find the end of this escape sequence
			seqEnd := 1
			for seqEnd <= len(remaining) {
				candidate := remaining[:seqEnd]
				status := isCompleteSequence(candidate)

				if status == statusComplete {
					// WezTerm with enable_kitty_keyboard sends the Escape key press as a
					// raw '\x1b' byte (simple text path in encode_kitty, ignoring
					// DISAMBIGUATE_ESCAPE_CODES) and the release as a full Kitty CSI-u
					// sequence. These arrive concatenated as '\x1b\x1b[27;...u'.
					// The buffer would normally treat '\x1b\x1b' as a complete meta-key
					// sequence (ESC + single char), leaving '[27;...u' to be typed as
					// plain text. If the character immediately following '\x1b\x1b'
					// would begin a new escape sequence, emit only the first ESC and
					// restart from the second.
					if candidate == "\x1b\x1b" && seqEnd < len(remaining) {
						nextChar := remaining[seqEnd]
						if nextChar == '[' || // CSI
							nextChar == ']' || // OSC
							nextChar == 'O' || // SS3
							nextChar == 'P' || // DCS
							nextChar == '_' { // APC
							sequences = append(sequences, "\x1b")
							pos++
							break
						}
					}
					sequences = append(sequences, candidate)
					pos += seqEnd
					break
				} else if status == statusIncomplete {
					seqEnd++
				} else {
					// Should not happen when starting with ESC
					sequences = append(sequences, candidate)
					pos += seqEnd
					break
				}
			}

			if seqEnd > len(remaining) {
				return sequences, remaining
			}
		} else {
			// Not an escape sequence - take a single character
			_, size := utf8.DecodeRuneInString(remaining)
			if size == 0 {
				size = 1
			}
			sequences = append(sequences, remaining[:size])
			pos += size
		}
	}

	return sequences, ""
}
