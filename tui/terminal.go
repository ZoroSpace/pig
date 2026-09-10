// tui/terminal.go (pi: packages/tui/src/terminal.ts) — minimal terminal
// interface for the TUI plus the real-terminal implementation.
//
// Porting notes (declared deviations):
//   - Node process.stdin/stdout → injectable in/out io.Reader/io.Writer
//     fields (default os.Stdin/os.Stdout). pi's tests monkey-patch
//     process.stdout.write / process.stdin.on; Go tests inject pipes instead.
//   - Node raw mode → golang.org/x/term MakeRaw/Restore (saved termios is the
//     wasRaw equivalent).
//   - process.stdout 'resize' event → SIGWINCH via signal.Notify goroutine.
//   - setEncoding('utf8') → incremental UTF-8 decode in the stdin read loop
//     (StringDecoder semantics for multibyte sequences split across reads).
//   - enableWindowsVTInput and the win32-console-mode native helper have no
//     Go counterpart — no-op (see native_modifiers.go).
//   - drainInput returns Promise<void> → blocking method.
package tui

import (
	"io"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	terminalProgressKeepaliveMs               = 1000
	terminalProgressActiveSequence            = "\x1b]9;4;3\x07"
	terminalProgressClearSequence             = "\x1b]9;4;0\x07"
	nativeShiftEnterSequence                  = "\x1b[13;2u"
	desiredKittyKeyboardProtocolFlags         = 7
	keyboardProtocolResponseFragmentTimeoutMs = 150
	defaultEscapeTimeout                      = 10
	defaultSshEscapeTimeout                   = 100
)

var kittyKeyboardProtocolQuery = "\x1b[>" + itoa(desiredKittyKeyboardProtocolFlags) + "u\x1b[?u\x1b[c"

// KeyboardProtocolNegotiationSequence is a parsed keyboard-protocol response.
// pi: tagged union { type: "kitty-flags" | "device-attributes", ... }.
type KeyboardProtocolNegotiationSequence struct {
	Type  string // "kitty-flags" | "device-attributes"
	Flags int    // only for "kitty-flags"
}

// ParseKeyboardProtocolNegotiationSequence parses a Kitty flags reply
// (CSI ? <flags> u) or a device-attributes reply (CSI ? <attrs> c).
// Returns nil when the sequence is not a negotiation response.
func ParseKeyboardProtocolNegotiationSequence(sequence string) *KeyboardProtocolNegotiationSequence {
	if m := kittyFlagsReplyRegex.FindStringSubmatch(sequence); m != nil {
		flags, _ := strconv.Atoi(m[1])
		return &KeyboardProtocolNegotiationSequence{Type: "kitty-flags", Flags: flags}
	}
	if deviceAttributesReplyRegex.MatchString(sequence) {
		return &KeyboardProtocolNegotiationSequence{Type: "device-attributes"}
	}
	return nil
}

var kittyFlagsReplyRegex = regexp.MustCompile(`^\x1b\[\?(\d+)u$`)
var deviceAttributesReplyRegex = regexp.MustCompile(`^\x1b\[\?[\d;]*c$`)

func isKeyboardProtocolNegotiationSequencePrefix(sequence string) bool {
	return sequence == "\x1b[" || kittyFlagsPrefixRegex.MatchString(sequence)
}

var kittyFlagsPrefixRegex = regexp.MustCompile(`^\x1b\[\?[\d;]*$`)

// IsAppleTerminalSession reports whether we run in Apple Terminal on macOS.
func IsAppleTerminalSession() bool {
	return runtime.GOOS == "darwin" && os.Getenv("TERM_PROGRAM") == "Apple_Terminal"
}

// NormalizeNativeShiftEnterInput rewrites a bare Return to the CSI-u
// Shift+Enter sequence when native Shift detection is enabled and Shift is
// physically held.
func NormalizeNativeShiftEnterInput(data string, shouldDetectNativeShiftEnter bool, isShiftPressed bool) string {
	if shouldDetectNativeShiftEnter && data == "\r" && isShiftPressed {
		return nativeShiftEnterSequence
	}
	return data
}

// NormalizeAppleTerminalInput is NormalizeNativeShiftEnterInput for Apple
// Terminal sessions.
func NormalizeAppleTerminalInput(data string, isAppleTerminal bool, isShiftPressed bool) string {
	return NormalizeNativeShiftEnterInput(data, isAppleTerminal, isShiftPressed)
}

// Terminal is the minimal terminal interface for the TUI.
type Terminal interface {
	// Start the terminal with input and resize handlers
	Start(onInput func(data string), onResize func())

	// Stop the terminal and restore state
	Stop()

	// DrainInput drains stdin before exiting to prevent Kitty key release
	// events from leaking to the parent shell over slow SSH connections.
	// maxMs: maximum time to drain (default 1000ms); idleMs: exit early if no
	// input arrives within this time (default 50ms).
	DrainInput(maxMs int, idleMs int)

	// Write output to terminal
	Write(data string)

	// Terminal dimensions
	Columns() int
	Rows() int

	// Whether Kitty keyboard protocol is active
	KittyProtocolActive() bool

	// Cursor positioning (relative to current position)
	MoveBy(lines int) // Move cursor up (negative) or down (positive) by N lines

	// Cursor visibility
	HideCursor() // Hide the cursor
	ShowCursor() // Show the cursor

	// Clear operations
	ClearLine()       // Clear current line
	ClearFromCursor() // Clear from cursor to end of screen
	ClearScreen()     // Clear entire screen and move cursor to (0,0)

	// Title operations
	SetTitle(title string) // Set terminal window title

	// Progress indicator (OSC 9;4)
	SetProgress(active bool)
}

// ResolveEscapeTimeoutMs resolves how long to wait for the rest of an escape
// sequence before dispatching a lone ESC as the Escape key. Legacy Alt+key
// input is ESC plus another byte, so high-latency transports need a longer
// reassembly window.
func ResolveEscapeTimeoutMs() int {
	configured, err := strconv.Atoi(os.Getenv("PI_TUI_ESC_TIMEOUT"))
	if err == nil && configured > 0 {
		return configured
	}
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return defaultSshEscapeTimeout
	}
	return defaultEscapeTimeout
}

// ProcessTerminal is the real terminal using stdin/stdout.
type ProcessTerminal struct {
	// in/out are injectable for tests (pi: process.stdin / process.stdout).
	in  io.Reader
	out io.Writer

	mu sync.Mutex

	oldStateSaved bool
	oldState      *term.State
	inputHandler  func(data string)
	resizeHandler func()

	kittyProtocolActive               bool
	modifyOtherKeysActive             bool
	keyboardProtocolPushed            bool
	keyboardProtocolNegotiationBuffer string
	negotiationFlushTimer             *time.Timer

	stdinBuffer *StdinBuffer
	stopRead    chan struct{}
	readWG      sync.WaitGroup

	progressTimer *time.Timer
	winchStop     chan struct{}
	winchWG       sync.WaitGroup

	writeLogPath string

	// drainInput state (guarded by drainMu; lastDataTime atomic)
	drainLastDataTime atomic.Int64
}

// NewProcessTerminal creates a ProcessTerminal bound to stdin/stdout.
func NewProcessTerminal() *ProcessTerminal {
	return &ProcessTerminal{
		in:           os.Stdin,
		out:          os.Stdout,
		writeLogPath: resolveWriteLogPath(os.Getenv("PI_TUI_WRITE_LOG"), time.Now()),
	}
}

func (t *ProcessTerminal) KittyProtocolActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.kittyProtocolActive
}

// ModifyOtherKeysActive reports whether the modifyOtherKeys fallback is on.
func (t *ProcessTerminal) ModifyOtherKeysActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.modifyOtherKeysActive
}

func (t *ProcessTerminal) Start(onInput func(data string), onResize func()) {
	t.mu.Lock()
	t.inputHandler = onInput
	t.resizeHandler = onResize

	// Save previous state and enable raw mode
	fd := int(os.Stdin.Fd())
	if state, err := term.MakeRaw(fd); err == nil {
		t.oldState = state
		t.oldStateSaved = true
	}

	t.writeLocked("\x1b[?2004h")

	t.mu.Unlock()

	// Set up resize handler immediately (SIGWINCH goroutine)
	t.startWinchHandler(onResize)

	// Refresh terminal dimensions - they may be stale after suspend/resume
	// (SIGWINCH is lost while process is stopped). Unix only.
	_ = syscall.Kill(os.Getpid(), syscall.SIGWINCH)

	// On Windows, enable ENABLE_VIRTUAL_TERMINAL_INPUT so the console sends
	// VT escape sequences. No Go counterpart — no-op (native helper).

	// Query Kitty keyboard protocol and fall back to modifyOtherKeys when DA
	// confirms no Kitty response.
	t.queryAndEnableKittyProtocol()
}

// startWinchHandler runs a goroutine forwarding SIGWINCH to onResize.
func (t *ProcessTerminal) startWinchHandler(onResize func()) {
	t.mu.Lock()
	if t.winchStop != nil {
		t.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	t.winchStop = stop
	t.mu.Unlock()

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	t.winchWG.Add(1)
	go func() {
		defer t.winchWG.Done()
		for {
			select {
			case <-stop:
				signal.Stop(winch)
				return
			case <-winch:
				if onResize != nil {
					onResize()
				}
			}
		}
	}()
}

// setupStdinBuffer splits batched input into individual sequences so that
// components receive single events (making MatchesKey/IsKeyRelease work
// correctly). It also watches for Kitty protocol responses.
func (t *ProcessTerminal) setupStdinBuffer() {
	t.stdinBuffer = NewStdinBuffer(&stdinBufferOptions{EscapeTimeoutMs: ResolveEscapeTimeoutMs()})

	// Forward individual sequences to the input handler
	t.stdinBuffer.OnData(func(sequence string) {
		t.mu.Lock()
		negotiationSequence, bufferedToForward, pending := t.readKeyboardProtocolNegotiationSequenceLocked(sequence)
		if pending {
			t.scheduleKeyboardProtocolNegotiationBufferFlushLocked()
			t.mu.Unlock()
			return // Wait briefly for the rest of a split Kitty response.
		}
		handled := false
		if negotiationSequence != nil {
			handled = t.handleKeyboardProtocolNegotiationSequenceLocked(negotiationSequence)
		}
		forward := !handled
		inputHandler := t.inputHandler
		t.mu.Unlock()

		if handled {
			return
		}
		if forward {
			// Dispatch outside t.mu: the handler descends into TuiBase
			// locking (b.mu), and doRender holds b.mu while taking t.mu for
			// writes — dispatching under t.mu is an ABBA deadlock (pig fix;
			// pi is single-threaded).
			if bufferedToForward != "" {
				t.dispatchInputSequence(sequence, bufferedToForward, inputHandler)
			} else {
				t.dispatchInputSequence(sequence, "", inputHandler)
			}
		}
	})

	// Re-wrap paste content with bracketed paste markers for existing editor handling
	t.stdinBuffer.OnPaste(func(content string) {
		t.mu.Lock()
		inputHandler := t.inputHandler
		t.mu.Unlock()
		if inputHandler != nil {
			inputHandler("\x1b[200~" + content + "\x1b[201~")
		}
	})
}

// queryAndEnableKittyProtocol queries the terminal for Kitty keyboard
// protocol support and enables it if available.
//
// Kitty's progressive enhancement detection requires requesting the desired
// flags before querying them. The trailing DA query is a sentinel supported by
// terminals that do not know Kitty keyboard protocol; receiving DA before a
// Kitty response enables modifyOtherKeys fallback without a startup timeout.
//
// The requested flags are:
//   - 1 = disambiguate escape codes
//   - 2 = report event types (press/repeat/release)
//   - 4 = report alternate keys (shifted key, base layout key)
func (t *ProcessTerminal) queryAndEnableKittyProtocol() {
	t.mu.Lock()
	t.setupStdinBufferLocked()
	t.keyboardProtocolPushed = true
	t.clearKeyboardProtocolNegotiationBufferLocked()
	t.writeLocked(kittyKeyboardProtocolQuery)
	t.mu.Unlock()

	// Start reading stdin (Node: process.stdin.on("data", ...) + resume)
	t.readWG.Add(1)
	t.stopRead = make(chan struct{})
	go t.readLoop()
}

func (t *ProcessTerminal) setupStdinBufferLocked() {
	t.setupStdinBuffer() // registers handlers; no t.mu re-entry (StdinBuffer has own lock)
}

// readLoop reads raw stdin, decodes UTF-8 incrementally, and feeds the
// StdinBuffer (Node setEncoding('utf8') equivalent: multibyte sequences split
// across reads are buffered until complete).
func (t *ProcessTerminal) readLoop() {
	defer t.readWG.Done()
	t.mu.Lock()
	stop := t.stopRead
	stdin := t.stdinBuffer
	in := t.in
	t.mu.Unlock()
	if stop == nil || stdin == nil {
		return
	}
	buf := make([]byte, 1024)
	var pending []byte // incomplete UTF-8 tail
	for {
		select {
		case <-stop:
			return
		default:
		}
		n, err := in.Read(buf)
		if n > 0 {
			t.drainLastDataTime.Store(time.Now().UnixMilli())
			pending = append(pending, buf[:n]...)
			// Decode all complete UTF-8 sequences; keep the incomplete tail.
			var out strings.Builder
			i := 0
			for i < len(pending) {
				size := utf8SequenceLength(pending[i:])
				if size == 0 {
					// Incomplete trailing sequence — wait for more bytes.
					break
				}
				r, rs := utf8.DecodeRune(pending[i : i+size])
				out.WriteRune(r)
				i += rs
			}
			pending = append(pending[:0], pending[i:]...)
			if out.Len() > 0 {
				stdin.ProcessString(out.String())
			}
		}
		if err != nil {
			return
		}
	}
}

// utf8SequenceLength returns the byte length of the UTF-8 sequence starting
// at b[0], or 0 when b holds an incomplete (truncated) sequence.
func utf8SequenceLength(b []byte) int {
	c := b[0]
	var need int
	switch {
	case c < 0x80:
		return 1
	case c&0xE0 == 0xC0:
		need = 2
	case c&0xF0 == 0xE0:
		need = 3
	case c&0xF8 == 0xF0:
		need = 4
	default:
		return 1 // invalid byte; DecodeRune yields RuneError with size 1
	}
	for i := 1; i < need; i++ {
		if i >= len(b) {
			return 0
		}
		if b[i]&0xC0 != 0x80 {
			return 1 // invalid continuation; let DecodeRune handle it
		}
	}
	return need
}

func (t *ProcessTerminal) handleKeyboardProtocolNegotiationSequenceLocked(negotiationSequence *KeyboardProtocolNegotiationSequence) bool {
	if negotiationSequence == nil {
		return false
	}
	t.clearKeyboardProtocolNegotiationBufferLocked()
	if negotiationSequence.Type == "kitty-flags" {
		if negotiationSequence.Flags != 0 {
			t.disableModifyOtherKeysLocked()
			if !t.kittyProtocolActive {
				t.kittyProtocolActive = true
				SetKittyProtocolActive(true)
			}
		} else {
			t.enableModifyOtherKeysLocked()
		}
		return true
	}

	if !t.kittyProtocolActive {
		t.enableModifyOtherKeysLocked()
	}
	return true
}

// Returns (sequence, bufferedToForward, pending). pending=true means the
// buffered prefix may become a negotiation response and should be flushed
// on timeout. bufferedToForward carries a previously buffered prefix that
// turned out not to be a negotiation response; the caller must forward it
// as input WITHOUT holding t.mu.
func (t *ProcessTerminal) readKeyboardProtocolNegotiationSequenceLocked(sequence string) (*KeyboardProtocolNegotiationSequence, string, bool) {
	if t.keyboardProtocolNegotiationBuffer != "" {
		bufferedSequence := t.keyboardProtocolNegotiationBuffer + sequence
		if negotiationSequence := ParseKeyboardProtocolNegotiationSequence(bufferedSequence); negotiationSequence != nil {
			t.clearKeyboardProtocolNegotiationBufferLocked()
			return negotiationSequence, "", false
		}
		if isKeyboardProtocolNegotiationSequencePrefix(bufferedSequence) {
			t.setKeyboardProtocolNegotiationBufferLocked(bufferedSequence)
			return nil, "", true
		}
		buffered := t.keyboardProtocolNegotiationBuffer
		t.clearKeyboardProtocolNegotiationBufferLocked()
		return nil, buffered, false
	}

	if negotiationSequence := ParseKeyboardProtocolNegotiationSequence(sequence); negotiationSequence != nil {
		return negotiationSequence, "", false
	}
	if isKeyboardProtocolNegotiationSequencePrefix(sequence) {
		t.setKeyboardProtocolNegotiationBufferLocked(sequence)
		return nil, "", true
	}
	return nil, "", false
}

func (t *ProcessTerminal) setKeyboardProtocolNegotiationBufferLocked(sequence string) {
	t.clearKeyboardProtocolNegotiationBufferFlushTimerLocked()
	t.keyboardProtocolNegotiationBuffer = sequence
}

func (t *ProcessTerminal) clearKeyboardProtocolNegotiationBufferLocked() {
	t.clearKeyboardProtocolNegotiationBufferFlushTimerLocked()
	t.keyboardProtocolNegotiationBuffer = ""
}

func (t *ProcessTerminal) scheduleKeyboardProtocolNegotiationBufferFlushLocked() {
	if t.keyboardProtocolNegotiationBuffer == "" || t.negotiationFlushTimer != nil {
		return
	}
	t.negotiationFlushTimer = time.AfterFunc(time.Duration(keyboardProtocolResponseFragmentTimeoutMs)*time.Millisecond, func() {
		t.mu.Lock()
		t.negotiationFlushTimer = nil
		sequence := t.keyboardProtocolNegotiationBuffer
		t.clearKeyboardProtocolNegotiationBufferLocked()
		inputHandler := t.inputHandler
		t.mu.Unlock()
		// Dispatch outside t.mu (see the OnData deadlock note).
		if sequence != "" {
			t.resolveInputHandlerAndForward(sequence, inputHandler)
		}
	})
}

func (t *ProcessTerminal) clearKeyboardProtocolNegotiationBufferFlushTimerLocked() {
	if t.negotiationFlushTimer == nil {
		return
	}
	t.negotiationFlushTimer.Stop()
	t.negotiationFlushTimer = nil
}

// resolveInputHandlerAndForward resolves the effective handler under t.mu
// and forwards without the lock (pig: the wrapper half of pi's
// forwardInputSequence; the no-lock dispatch split is the OnData deadlock
// fix).
func (t *ProcessTerminal) resolveInputHandlerAndForward(sequence string, inputHandler func(string)) {
	t.mu.Lock()
	if inputHandler == nil {
		inputHandler = t.inputHandler
	}
	t.mu.Unlock()
	if inputHandler == nil {
		return
	}
	t.forwardInputSequence(sequence, inputHandler)
}

// dispatchInputSequence forwards a (possibly empty-split) pair of input
// sequences without holding t.mu (pig: see the OnData deadlock note).
func (t *ProcessTerminal) dispatchInputSequence(sequence string, bufferedFirst string, inputHandler func(string)) {
	if inputHandler == nil {
		return
	}
	if bufferedFirst != "" {
		t.resolveInputHandlerAndForward(bufferedFirst, inputHandler)
	}
	t.forwardInputSequence(sequence, inputHandler)
}

func (t *ProcessTerminal) forwardInputSequence(sequence string, inputHandler func(string)) {
	if inputHandler == nil {
		return
	}
	shouldDetectNativeShiftEnter := sequence == "\r" && (IsAppleTerminalSession() || runtime.GOOS == "windows")
	input := sequence
	if shouldDetectNativeShiftEnter {
		input = NormalizeNativeShiftEnterInput(sequence, shouldDetectNativeShiftEnter,
			IsNativeModifierPressed("shift"))
	}
	inputHandler(input)
}

func (t *ProcessTerminal) enableModifyOtherKeysLocked() {
	if t.kittyProtocolActive || t.modifyOtherKeysActive {
		return
	}
	t.writeLocked("\x1b[>4;2m")
	t.modifyOtherKeysActive = true
}

func (t *ProcessTerminal) disableModifyOtherKeysLocked() {
	if !t.modifyOtherKeysActive {
		return
	}
	t.writeLocked("\x1b[>4;0m")
	t.modifyOtherKeysActive = false
}

// DrainInput drains stdin before exiting. See Terminal.
func (t *ProcessTerminal) DrainInput(maxMs int, idleMs int) {
	t.mu.Lock()
	shouldDisableKittyProtocol := t.keyboardProtocolPushed || t.kittyProtocolActive
	t.clearKeyboardProtocolNegotiationBufferLocked()
	if shouldDisableKittyProtocol {
		// Disable Kitty keyboard protocol first so any late key releases
		// do not generate new Kitty escape sequences.
		t.writeLocked("\x1b[<u")
		t.keyboardProtocolPushed = false
		t.kittyProtocolActive = false
		SetKittyProtocolActive(false)
	}
	t.disableModifyOtherKeysLocked()
	t.mu.Unlock()

	if maxMs <= 0 {
		maxMs = 1000
	}
	if idleMs <= 0 {
		idleMs = 50
	}

	start := time.Now()
	for {
		now := time.Now()
		timeLeft := maxMs - int(now.Sub(start).Milliseconds())
		if timeLeft <= 0 {
			break
		}
		if now.UnixMilli()-t.drainLastDataTime.Load() >= int64(idleMs) {
			break
		}
		sleep := time.Duration(idleMs) * time.Millisecond
		if timeLeft < idleMs {
			sleep = time.Duration(timeLeft) * time.Millisecond
		}
		time.Sleep(sleep)
	}
}

func (t *ProcessTerminal) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.clearProgressIntervalLocked() {
		t.writeLocked(terminalProgressClearSequence)
	}

	// Disable bracketed paste mode
	t.writeLocked("\x1b[?2004l")

	shouldDisableKittyProtocol := t.keyboardProtocolPushed || t.kittyProtocolActive
	t.clearKeyboardProtocolNegotiationBufferLocked()

	// Disable Kitty keyboard protocol if not already done by DrainInput
	if shouldDisableKittyProtocol {
		t.writeLocked("\x1b[<u")
		t.keyboardProtocolPushed = false
		t.kittyProtocolActive = false
		SetKittyProtocolActive(false)
	}
	t.disableModifyOtherKeysLocked()

	// Stop the stdin read loop and clean up the StdinBuffer
	if t.stopRead != nil {
		close(t.stopRead)
		t.stopRead = nil
	}
	if t.stdinBuffer != nil {
		t.stdinBuffer.Destroy()
		t.stdinBuffer = nil
	}
	t.inputHandler = nil

	// Stop the winch handler
	if t.winchStop != nil {
		close(t.winchStop)
		t.winchStop = nil
	}

	// Restore raw mode state
	if t.oldStateSaved {
		_ = term.Restore(int(os.Stdin.Fd()), t.oldState)
		t.oldStateSaved = false
		t.oldState = nil
	}
}

func (t *ProcessTerminal) Write(data string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writeLocked(data)
}

func (t *ProcessTerminal) writeLocked(data string) {
	_, _ = io.WriteString(t.out, data)
	if t.writeLogPath != "" {
		appendWriteLog(t.writeLogPath, data)
	}
}

func (t *ProcessTerminal) Columns() int {
	if f, ok := t.out.(*os.File); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width != 0 {
			return width
		}
	}
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n != 0 {
			return n
		}
	}
	return 80
}

func (t *ProcessTerminal) Rows() int {
	if f, ok := t.out.(*os.File); ok {
		if _, height, err := term.GetSize(int(f.Fd())); err == nil && height != 0 {
			return height
		}
	}
	if v := os.Getenv("LINES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n != 0 {
			return n
		}
	}
	return 24
}

func (t *ProcessTerminal) MoveBy(lines int) {
	if lines > 0 {
		t.Write("\x1b[" + itoa(lines) + "B")
	} else if lines < 0 {
		t.Write("\x1b[" + itoa(-lines) + "A")
	}
	// lines === 0: no movement
}

func (t *ProcessTerminal) HideCursor() { t.Write("\x1b[?25l") }

func (t *ProcessTerminal) ShowCursor() { t.Write("\x1b[?25h") }

func (t *ProcessTerminal) ClearLine() { t.Write("\x1b[K") }

func (t *ProcessTerminal) ClearFromCursor() { t.Write("\x1b[J") }

func (t *ProcessTerminal) ClearScreen() {
	t.Write("\x1b[2J\x1b[H") // Clear screen and move to home (1,1)
}

func (t *ProcessTerminal) SetTitle(title string) {
	// OSC 0;title BEL - set terminal window title
	t.Write("\x1b]0;" + title + "\x07")
}

func (t *ProcessTerminal) SetProgress(active bool) {
	if active {
		// OSC 9;4;3 - indeterminate progress
		t.Write(terminalProgressActiveSequence)
		t.mu.Lock()
		if t.progressTimer == nil {
			t.progressTimer = time.AfterFunc(time.Duration(terminalProgressKeepaliveMs)*time.Millisecond, t.progressKeepalive)
		}
		t.mu.Unlock()
	} else {
		t.mu.Lock()
		t.clearProgressIntervalLocked()
		t.mu.Unlock()
		// OSC 9;4;0 - clear progress
		t.Write(terminalProgressClearSequence)
	}
}

func (t *ProcessTerminal) progressKeepalive() {
	t.mu.Lock()
	t.writeLocked(terminalProgressActiveSequence)
	t.progressTimer = time.AfterFunc(time.Duration(terminalProgressKeepaliveMs)*time.Millisecond, t.progressKeepalive)
	t.mu.Unlock()
}

func (t *ProcessTerminal) clearProgressIntervalLocked() bool {
	if t.progressTimer == nil {
		return false
	}
	t.progressTimer.Stop()
	t.progressTimer = nil
	return true
}

// resolveWriteLogPath resolves PI_TUI_WRITE_LOG: an existing directory gets a
// timestamped log file; anything else is used as a file path as-is.
func resolveWriteLogPath(envValue string, now time.Time) string {
	if envValue == "" {
		return ""
	}
	if stat, err := os.Stat(envValue); err == nil && stat.IsDir() {
		ts := now.Format("2006-01-02_15-04-05")
		return envValue + "/tui-" + ts + "-" + itoa(os.Getpid()) + ".log"
	}
	return envValue
}

func appendWriteLog(path string, data string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return // Ignore logging errors
	}
	defer f.Close()
	_, _ = io.WriteString(f, data)
}

// Platform helpers -----------------------------------------------------------
