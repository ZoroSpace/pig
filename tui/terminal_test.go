// tui/terminal_test.go — ported from pi: packages/tui/test/terminal.test.ts.
// The pi harness monkey-patches process.stdout.write / process.stdin.on; the
// Go port injects an io.Pipe and a recording writer into the unexported
// in/out fields instead (same-package test seam).
package tui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer safe for concurrent Write (terminal
// goroutines) and String/Len (test assertions): the OSC 7501 and negotiation
// paths write from the readLoop goroutine while tests read snapshots.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *syncBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncBuffer) WriteString(s string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.WriteString(s)
}

func (w *syncBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func (w *syncBuffer) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Len()
}

func TestResolveEscapeTimeoutMs(t *testing.T) {
	t.Run("uses PI_TUI_ESC_TIMEOUT when configured", func(t *testing.T) {
		t.Setenv("PI_TUI_ESC_TIMEOUT", "80")
		if got := ResolveEscapeTimeoutMs(); got != 80 {
			t.Fatalf("got %d", got)
		}
		t.Setenv("SSH_TTY", "/dev/pts/1")
		if got := ResolveEscapeTimeoutMs(); got != 80 {
			t.Fatalf("got %d", got)
		}
	})
	t.Run("ignores invalid PI_TUI_ESC_TIMEOUT values", func(t *testing.T) {
		t.Setenv("SSH_CONNECTION", "")
		t.Setenv("SSH_TTY", "")
		for _, v := range []string{"abc", "0", "-5", ""} {
			t.Setenv("PI_TUI_ESC_TIMEOUT", v)
			if got := ResolveEscapeTimeoutMs(); got != 10 {
				t.Fatalf("PI_TUI_ESC_TIMEOUT=%q: got %d", v, got)
			}
		}
	})
	t.Run("defaults to 100ms over SSH", func(t *testing.T) {
		t.Setenv("PI_TUI_ESC_TIMEOUT", "")
		t.Setenv("SSH_CLIENT", "")
		t.Setenv("SSH_TTY", "")
		t.Setenv("SSH_CONNECTION", "10.0.0.1 22")
		if got := ResolveEscapeTimeoutMs(); got != 100 {
			t.Fatalf("got %d", got)
		}
		t.Setenv("SSH_CONNECTION", "")
		t.Setenv("SSH_TTY", "/dev/pts/1")
		if got := ResolveEscapeTimeoutMs(); got != 100 {
			t.Fatalf("got %d", got)
		}
	})
	t.Run("defaults to 10ms otherwise", func(t *testing.T) {
		t.Setenv("PI_TUI_ESC_TIMEOUT", "")
		t.Setenv("SSH_CONNECTION", "")
		t.Setenv("SSH_CLIENT", "")
		t.Setenv("SSH_TTY", "")
		if got := ResolveEscapeTimeoutMs(); got != 10 {
			t.Fatalf("got %d", got)
		}
	})
}

func TestNormalizeNativeShiftEnterInput(t *testing.T) {
	t.Run("rewrites Return to CSI-u Shift+Enter when native Shift detection is enabled and Shift is pressed", func(t *testing.T) {
		if got := NormalizeNativeShiftEnterInput("\r", true, true); got != "\x1b[13;2u" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("leaves Return unchanged when native Shift detection is disabled", func(t *testing.T) {
		if got := NormalizeNativeShiftEnterInput("\r", false, true); got != "\r" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("leaves Return unchanged when Shift is not pressed", func(t *testing.T) {
		if got := NormalizeNativeShiftEnterInput("\r", true, false); got != "\r" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("leaves non-Return input unchanged", func(t *testing.T) {
		if got := NormalizeNativeShiftEnterInput("\x1b[13;2u", true, true); got != "\x1b[13;2u" {
			t.Fatalf("got %q", got)
		}
		if got := NormalizeNativeShiftEnterInput("a", true, true); got != "a" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestNormalizeAppleTerminalInput(t *testing.T) {
	t.Run("rewrites Apple Terminal Return to CSI-u Shift+Enter when Shift is pressed", func(t *testing.T) {
		if got := NormalizeAppleTerminalInput("\r", true, true); got != "\x1b[13;2u" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("leaves Apple Terminal Return unchanged when Shift is not pressed", func(t *testing.T) {
		if got := NormalizeAppleTerminalInput("\r", true, false); got != "\r" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("leaves non-Apple Terminal Return unchanged when Shift is pressed", func(t *testing.T) {
		if got := NormalizeAppleTerminalInput("\r", false, true); got != "\r" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("leaves non-Return input unchanged", func(t *testing.T) {
		if got := NormalizeAppleTerminalInput("\x1b[13;2u", true, true); got != "\x1b[13;2u" {
			t.Fatalf("got %q", got)
		}
		if got := NormalizeAppleTerminalInput("a", true, true); got != "a" {
			t.Fatalf("got %q", got)
		}
	})
}

// negotiationHarness mirrors pi's setupNegotiation test harness.
type negotiationHarness struct {
	terminal *ProcessTerminal
	writes   *syncBuffer
	mu       sync.Mutex
	input    string
	hasInput bool
	inWriter *io.PipeWriter
	cleaned  bool
}

func setupNegotiation(t *testing.T) *negotiationHarness {
	t.Helper()
	terminal := NewProcessTerminal()
	writes := &syncBuffer{}
	pipeReader, pipeWriter := io.Pipe()
	terminal.in = pipeReader
	terminal.out = writes

	h := &negotiationHarness{terminal: terminal, writes: writes, inWriter: pipeWriter}
	terminal.inputHandler = func(data string) {
		h.mu.Lock()
		h.input = data
		h.hasInput = true
		h.mu.Unlock()
	}
	terminal.queryAndEnableKittyProtocol()
	t.Cleanup(h.cleanup)
	return h
}

func (h *negotiationHarness) send(data string) {
	if _, err := h.inWriter.Write([]byte(data)); err != nil {
		panic(err)
	}
}

// waitForWrite polls until writes contains substring or the timeout elapses;
// it reports whether the substring appeared. Input handling is asynchronous
// (readLoop goroutine), so sequence assertions must wait (pi's tests are
// synchronous on the Node event loop).
func (h *negotiationHarness) waitForWrite(substring string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		found := strings.Contains(h.writes.String(), substring)
		h.mu.Unlock()
		if found {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// sawInput reports whether any input reached the handler so far.
func (h *negotiationHarness) sawInput() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hasInput
}

func (h *negotiationHarness) waitForInput(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		has := h.sawInput()
		if has {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

func (h *negotiationHarness) cleanup() {
	if h.cleaned {
		return
	}
	h.cleaned = true
	_ = h.inWriter.Close()
	h.terminal.Stop()
	SetKittyProtocolActive(false)
}

func countSubstrings(s, sub string) int {
	return strings.Count(s, sub)
}

func TestProcessTerminalKittyNegotiation(t *testing.T) {
	t.Run("queries Kitty mode before enabling modifyOtherKeys fallback", func(t *testing.T) {
		h := setupNegotiation(t)
		// S17 (pi 503c60552): the OSC 7501 program status query rides between
		// the Kitty query and the DA sentinel.
		if got := h.writes.String(); !strings.HasPrefix(got, "\x1b[>7u\x1b[?u\x1b]7501;?\x1b\\\x1b[c") {
			t.Fatalf("first writes = %q", got)
		}
		if strings.Contains(h.writes.String(), "\x1b[>4;2m") {
			t.Fatal("modifyOtherKeys must not be enabled before negotiation")
		}
		if h.terminal.KittyProtocolActive() {
			t.Fatal("kitty protocol must be inactive")
		}
	})
	t.Run("activates Kitty mode for non-zero negotiated flags", func(t *testing.T) {
		h := setupNegotiation(t)
		h.send("\x1b[?7u")

		if h.waitForInput(50 * time.Millisecond) {
			h.mu.Lock()
			input := h.input
			h.mu.Unlock()
			t.Fatalf("unexpected input %q", input)
		}
		if !h.terminal.KittyProtocolActive() {
			t.Fatal("kitty protocol should be active")
		}
		if strings.Contains(h.writes.String(), "\x1b[>4;2m") || strings.Contains(h.writes.String(), "\x1b[>4;0m") {
			t.Fatal("modifyOtherKeys must not be toggled")
		}

		h.cleanup()
		time.Sleep(20 * time.Millisecond)
		if got := countSubstrings(h.writes.String(), "\x1b[<u"); got != 1 {
			t.Fatalf("disable-kitty writes = %d, want 1", got)
		}
		if strings.Contains(h.writes.String(), "\x1b[>4;0m") {
			t.Fatal("modifyOtherKeys must not be disabled (never enabled)")
		}
	})
	t.Run("falls back to modifyOtherKeys for zero Kitty flags", func(t *testing.T) {
		h := setupNegotiation(t)
		h.send("\x1b[?0u")

		if h.waitForInput(50 * time.Millisecond) {
			h.mu.Lock()
			input := h.input
			h.mu.Unlock()
			t.Fatalf("unexpected input %q", input)
		}
		if h.terminal.KittyProtocolActive() {
			t.Fatal("kitty protocol should be inactive")
		}
		if got := countSubstrings(h.writes.String(), "\x1b[>4;2m"); got != 1 {
			t.Fatalf("enable-modifyOtherKeys writes = %d, want 1", got)
		}

		h.cleanup()
		time.Sleep(20 * time.Millisecond)
		if got := countSubstrings(h.writes.String(), "\x1b[>4;0m"); got != 1 {
			t.Fatalf("disable-modifyOtherKeys writes = %d, want 1", got)
		}
	})
	t.Run("falls back to modifyOtherKeys for device attributes without Kitty flags", func(t *testing.T) {
		h := setupNegotiation(t)
		h.send("\x1b[?62;4;52c")

		if h.waitForInput(50 * time.Millisecond) {
			h.mu.Lock()
			input := h.input
			h.mu.Unlock()
			t.Fatalf("unexpected input %q", input)
		}
		if h.terminal.KittyProtocolActive() {
			t.Fatal("kitty protocol should be inactive")
		}
		if got := countSubstrings(h.writes.String(), "\x1b[>4;2m"); got != 1 {
			t.Fatalf("enable-modifyOtherKeys writes = %d, want 1", got)
		}
	})
	t.Run("forwards normal input while waiting for Kitty response", func(t *testing.T) {
		h := setupNegotiation(t)
		h.send("a")

		if !h.waitForInput(200 * time.Millisecond) {
			t.Fatal("input was not forwarded")
		}
		h.mu.Lock()
		input := h.input
		h.mu.Unlock()
		if input != "a" {
			t.Fatalf("input = %q", input)
		}
		if h.terminal.KittyProtocolActive() {
			t.Fatal("kitty protocol should be inactive")
		}
	})
	t.Run("tracks split Kitty confirmation", func(t *testing.T) {
		h := setupNegotiation(t)
		h.send("\x1b[?7")
		time.Sleep(30 * time.Millisecond) // within the 150ms fragment window

		if h.waitForInput(5 * time.Millisecond) {
			t.Fatalf("unexpected input %q", h.input)
		}

		h.send("u")

		deadline := time.Now().Add(500 * time.Millisecond)
		for {
			if h.terminal.KittyProtocolActive() {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("kitty protocol should be active after split confirmation")
			}
			time.Sleep(2 * time.Millisecond)
		}
		if strings.Contains(h.writes.String(), "\x1b[>4;2m") {
			t.Fatal("modifyOtherKeys must not be enabled")
		}
	})
	t.Run("replays buffered CSI-prefix input when it is not a Kitty response", func(t *testing.T) {
		h := setupNegotiation(t)
		h.send("\x1b[")
		time.Sleep(80 * time.Millisecond) // StdinBuffer sequence timeout (50ms) fires first

		if h.waitForInput(5 * time.Millisecond) {
			t.Fatalf("unexpected early input %q", h.input)
		}

		time.Sleep(200 * time.Millisecond) // negotiation fragment timeout (150ms) replays it
		if !h.waitForInput(100 * time.Millisecond) {
			t.Fatal("buffered CSI-prefix input was not replayed")
		}
		h.mu.Lock()
		input := h.input
		h.mu.Unlock()
		if input != "\x1b[" {
			t.Fatalf("input = %q", input)
		}
	})
}

func TestProcessTerminalProgress(t *testing.T) {
	t.Run("writes a valid OSC 9;4 clear sequence", func(t *testing.T) {
		terminal := NewProcessTerminal()
		writes := &bytes.Buffer{}
		terminal.out = writes

		terminal.SetProgress(false)
		if got := writes.String(); got != "\x1b]9;4;0\x07" {
			t.Fatalf("writes = %q", got)
		}
	})
}

func TestProcessTerminalDimensions(t *testing.T) {
	t.Run("falls back to COLUMNS and LINES before default dimensions", func(t *testing.T) {
		terminal := NewProcessTerminal()
		terminal.out = &bytes.Buffer{} // not an *os.File → GetSize unavailable

		t.Setenv("COLUMNS", "123")
		t.Setenv("LINES", "45")

		if got := terminal.Columns(); got != 123 {
			t.Fatalf("columns = %d", got)
		}
		if got := terminal.Rows(); got != 45 {
			t.Fatalf("rows = %d", got)
		}
	})
	t.Run("defaults to 80x24 without env or file", func(t *testing.T) {
		terminal := NewProcessTerminal()
		terminal.out = &bytes.Buffer{}
		t.Setenv("COLUMNS", "")
		t.Setenv("LINES", "")
		if got := terminal.Columns(); got != 80 {
			t.Fatalf("columns = %d", got)
		}
		if got := terminal.Rows(); got != 24 {
			t.Fatalf("rows = %d", got)
		}
	})
}

func TestResolveWriteLogPath(t *testing.T) {
	t.Run("uses existing directory with timestamped file", func(t *testing.T) {
		dir := t.TempDir()
		now := time.Date(2026, 9, 10, 12, 34, 56, 0, time.UTC)
		got := resolveWriteLogPath(dir, now)
		if !strings.HasPrefix(got, dir+"/tui-2026-09-10_12-34-56-") || !strings.HasSuffix(got, ".log") {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("uses non-directory path as-is", func(t *testing.T) {
		got := resolveWriteLogPath("/tmp/my.log", time.Now())
		if got != "/tmp/my.log" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("empty env means no logging", func(t *testing.T) {
		if got := resolveWriteLogPath("", time.Now()); got != "" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestAppendWriteLog(t *testing.T) {
	path := t.TempDir() + "/write.log"
	appendWriteLog(path, "hello")
	appendWriteLog(path, " world")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Fatalf("log = %q", data)
	}
}

// TestProcessTerminalProgramStatus ports the OSC 7501 half of pi's
// terminal.test.ts "program status (OSC 7501)" describe block (S17
// 503c60552, #10607).
func TestProcessTerminalProgramStatus(t *testing.T) {
	const working = "\x1b]7501;state=working:app=pi\x1b\\"
	const clear = "\x1b]7501;state=clear\x1b\\"

	t.Run("reports the latest status once the terminal answers the query before DA", func(t *testing.T) {
		h := setupNegotiation(t)
		h.terminal.SetProgramStatus(ProgramStatus{State: ProgramStatusWorking, App: "pi"})
		if strings.Contains(h.writes.String(), working) {
			t.Fatal("nothing may be written before the terminal answers")
		}

		h.send("\x1b]7501;?\x1b\\")
		if !h.waitForWrite(working, 2*time.Second) {
			t.Fatal("the confirmed status must be re-sent")
		}
		if h.sawInput() {
			t.Fatal("the reply must not reach the input handler")
		}
		if got := h.writes.String(); strings.Count(got, working) != 1 {
			t.Fatalf("the confirmed status must be re-sent once: %q", got)
		}
		h.send("\x1b[?62;4;52c")

		h.terminal.SetProgramStatus(ProgramStatus{State: ProgramStatusDone})
		if got := h.writes.String(); !strings.HasSuffix(got, "\x1b]7501;state=done\x1b\\") {
			t.Fatalf("tail = %q", got[len(got)-40:])
		}
	})

	t.Run("reports nothing when DA arrives first and swallows late replies", func(t *testing.T) {
		h := setupNegotiation(t)
		h.send("\x1b[?62;4;52c")
		h.send("\x1b]7501;?\x07")
		time.Sleep(50 * time.Millisecond) // let both replies settle
		h.terminal.SetProgramStatus(ProgramStatus{State: ProgramStatusWorking, App: "pi"})

		if h.sawInput() {
			t.Fatal("the reply must not reach the input handler")
		}
		if strings.Contains(h.writes.String(), "\x1b]7501;state=") {
			t.Fatal("a terminal that answered DA first does not support OSC 7501")
		}
	})

	t.Run("skips the query when PI_PROGRAM_STATUS is set", func(t *testing.T) {
		t.Setenv("PI_PROGRAM_STATUS", "1")
		h := setupNegotiation(t)
		if got := h.writes.String(); !strings.HasPrefix(got, "\x1b[>7u\x1b[?u\x1b[c") {
			t.Fatalf("override 1 skips the query: %q", got)
		}
		h.terminal.SetProgramStatus(ProgramStatus{State: ProgramStatusWorking, App: "pi"})
		if !strings.HasSuffix(h.writes.String(), working) {
			t.Fatal("override 1 reports without confirmation")
		}

		t.Setenv("PI_PROGRAM_STATUS", "0")
		h0 := setupNegotiation(t)
		if got := h0.writes.String(); !strings.HasPrefix(got, "\x1b[>7u\x1b[?u\x1b[c") {
			t.Fatalf("override 0 skips the query: %q", got)
		}
		h0.terminal.SetProgramStatus(ProgramStatus{State: ProgramStatusWorking, App: "pi"})
		if strings.Contains(h0.writes.String(), working) {
			t.Fatal("override 0 never reports")
		}
	})

	t.Run("does not let a DA reply from before a restart end the new query", func(t *testing.T) {
		h := setupNegotiation(t)
		h.terminal.SetProgramStatus(ProgramStatus{State: ProgramStatusWorking, App: "pi"})
		h.terminal.Stop()
		// Restart in place (the harness owns one terminal instance). The old
		// readLoop must exit first: io.Pipe hands each Write to exactly one
		// Reader, and a lingering old loop would swallow the replies.
		time.Sleep(50 * time.Millisecond)
		h.terminal.queryAndEnableKittyProtocol()

		// The first start's replies arrive late: its DA, then the second
		// start's reply and DA.
		h.send("\x1b[?62;4;52c")
		h.send("\x1b]7501;?\x1b\\")
		h.send("\x1b[?62;4;52c")
		if !strings.HasSuffix(h.writes.String(), working) {
			t.Fatal("the late DA from the first start must not end the new query")
		}
		if h.sawInput() {
			t.Fatal("replies must not reach the input handler")
		}
	})

	t.Run("clears the status on stop and reports it again after restart", func(t *testing.T) {
		h := setupNegotiation(t)
		h.terminal.SetProgramStatus(ProgramStatus{State: ProgramStatusWorking, App: "pi"})
		h.send("\x1b]7501;?\x1b\\")
		if !h.waitForWrite(working, 2*time.Second) {
			t.Fatal("confirmation must re-send the status")
		}
		h.terminal.Stop()
		if got := h.writes.String(); !strings.Contains(got, clear) {
			t.Fatal("stop must clear the status")
		}

		// Stopped: nothing is written until the restarted terminal confirms
		// support again.
		afterStop := h.writes.Len()
		h.terminal.SetProgramStatus(ProgramStatus{State: ProgramStatusWorking, App: "pi"})
		if h.writes.Len() != afterStop {
			t.Fatal("stopped terminal must not report")
		}

		time.Sleep(50 * time.Millisecond) // old readLoop exits before the restart reads
		h.terminal.queryAndEnableKittyProtocol()
		h.send("\x1b]7501;?\x1b\\")
		if !h.waitForWrite(working, 2*time.Second) {
			t.Fatal("the restarted terminal reports again after confirmation")
		}
	})
}
