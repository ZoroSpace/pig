// tui/stdin_buffer_test.go — ported from pi: packages/tui/test/stdin-buffer.test.ts
package tui

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// stdinBufferHarness collects events from a StdinBuffer under test.
// Handlers fire on timer goroutines, so the log is mutex-guarded (-race).
type stdinBufferHarness struct {
	mu         sync.Mutex
	buffer     *StdinBuffer
	dataEvents []string
	pasteEvent *string
}

func newStdinBufferHarness(t *testing.T, opts *stdinBufferOptions) *stdinBufferHarness {
	t.Helper()
	h := &stdinBufferHarness{buffer: NewStdinBuffer(opts)}
	h.buffer.OnData(func(sequence string) {
		h.mu.Lock()
		h.dataEvents = append(h.dataEvents, sequence)
		h.mu.Unlock()
	})
	h.buffer.OnPaste(func(content string) {
		h.mu.Lock()
		c := content
		h.pasteEvent = &c
		h.mu.Unlock()
	})
	return h
}

func (h *stdinBufferHarness) processInput(data string) {
	h.buffer.ProcessString(data)
}

func (h *stdinBufferHarness) expectSequences(t *testing.T, want ...string) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.dataEvents) != len(want) {
		t.Fatalf("sequences = %q (%d), want %q (%d)", h.dataEvents, len(h.dataEvents), want, len(want))
	}
	for i := range want {
		if h.dataEvents[i] != want[i] {
			t.Fatalf("sequences = %q, want %q", h.dataEvents, want)
		}
	}
}

func (h *stdinBufferHarness) firstData() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.dataEvents[0]
}

func waitMs(t *testing.T, ms int) {
	t.Helper()
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

func TestStdinBufferRegularCharacters(t *testing.T) {
	t.Run("passes through regular characters immediately", func(t *testing.T) {
		h := newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
		h.processInput("a")
		h.expectSequences(t, "a")
	})
	t.Run("passes through multiple regular characters", func(t *testing.T) {
		h := newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
		h.processInput("abc")
		h.expectSequences(t, "a", "b", "c")
	})
	t.Run("handles unicode characters", func(t *testing.T) {
		h := newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
		h.processInput("hello 世界")
		h.expectSequences(t, "h", "e", "l", "l", "o", " ", "世", "界")
	})
}

func TestStdinBufferCompleteEscapeSequences(t *testing.T) {
	setup := func(t *testing.T) *stdinBufferHarness {
		return newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
	}
	t.Run("passes through complete mouse SGR sequences", func(t *testing.T) {
		h := setup(t)
		mouseSeq := "\x1b[<35;20;5m"
		h.processInput(mouseSeq)
		h.expectSequences(t, mouseSeq)
	})
	t.Run("passes through complete arrow key sequences", func(t *testing.T) {
		h := setup(t)
		upArrow := "\x1b[A"
		h.processInput(upArrow)
		h.expectSequences(t, upArrow)
	})
	t.Run("passes through complete function key sequences", func(t *testing.T) {
		h := setup(t)
		f1 := "\x1b[11~"
		h.processInput(f1)
		h.expectSequences(t, f1)
	})
	t.Run("passes through meta key sequences", func(t *testing.T) {
		h := setup(t)
		metaA := "\x1ba"
		h.processInput(metaA)
		h.expectSequences(t, metaA)
	})
	t.Run("passes through SS3 sequences", func(t *testing.T) {
		h := setup(t)
		ss3 := "\x1bOA"
		h.processInput(ss3)
		h.expectSequences(t, ss3)
	})
}

func TestStdinBufferPartialEscapeSequences(t *testing.T) {
	setup := func(t *testing.T) *stdinBufferHarness {
		return newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
	}
	t.Run("buffers incomplete mouse SGR sequence", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b")
		h.expectSequences(t)
		if got := h.buffer.GetBuffer(); got != "\x1b" {
			t.Fatalf("buffer = %q", got)
		}

		h.processInput("[<35")
		h.expectSequences(t)
		if got := h.buffer.GetBuffer(); got != "\x1b[<35" {
			t.Fatalf("buffer = %q", got)
		}

		h.processInput(";20;5m")
		h.expectSequences(t, "\x1b[<35;20;5m")
		if got := h.buffer.GetBuffer(); got != "" {
			t.Fatalf("buffer = %q", got)
		}
	})
	t.Run("buffers incomplete CSI sequence", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[")
		h.expectSequences(t)

		h.processInput("1;")
		h.expectSequences(t)

		h.processInput("5H")
		h.expectSequences(t, "\x1b[1;5H")
	})
	t.Run("buffers split across many chunks", func(t *testing.T) {
		h := setup(t)
		for _, chunk := range []string{"\x1b", "[", "<", "3", "5", ";", "2", "0", ";", "5", "m"} {
			h.processInput(chunk)
		}
		h.expectSequences(t, "\x1b[<35;20;5m")
	})
	t.Run("flushes incomplete sequence after timeout", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[<35")
		h.expectSequences(t)

		waitMs(t, 25)

		h.expectSequences(t, "\x1b[<35")
	})
	t.Run("flushes a lone ESC as Escape when CR arrives after the timeout", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b")
		waitMs(t, 20) // buffer timeout is 10ms
		h.processInput("\r")

		h.expectSequences(t, "\x1b", "\r")
		if !MatchesKey(h.firstData(), "escape") {
			t.Fatal("expected escape")
		}
	})
	t.Run("merges ESC + CR split across chunks within a larger timeout", func(t *testing.T) {
		h := newStdinBufferHarness(t, &stdinBufferOptions{EscapeTimeoutMs: 100})
		h.processInput("\x1b")
		waitMs(t, 20) // > 10ms default escapeTimeout, < 100ms configured escapeTimeout
		h.processInput("\r")

		h.expectSequences(t, "\x1b\r")
		if !MatchesKey(h.firstData(), "alt+enter") {
			t.Fatal("expected alt+enter")
		}
	})
	t.Run("does not apply the sequence timeout to a lone ESC", func(t *testing.T) {
		h := newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 100})
		h.processInput("\x1b")
		waitMs(t, 20)
		h.processInput("\r")

		h.expectSequences(t, "\x1b", "\r")
		if !MatchesKey(h.firstData(), "escape") {
			t.Fatal("expected escape")
		}
	})
	t.Run("keeps fragmented mouse sequences buffered across delayed chunks by default", func(t *testing.T) {
		h := newStdinBufferHarness(t, nil)
		h.processInput("\x1b[")
		waitMs(t, 20)
		h.expectSequences(t)
		h.processInput("<65;48;39M")
		h.expectSequences(t, "\x1b[<65;48;39M")
		h.buffer.Destroy()
	})
}

func TestStdinBufferMixedContent(t *testing.T) {
	setup := func(t *testing.T) *stdinBufferHarness {
		return newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
	}
	t.Run("handles characters followed by escape sequence", func(t *testing.T) {
		h := setup(t)
		h.processInput("abc\x1b[A")
		h.expectSequences(t, "a", "b", "c", "\x1b[A")
	})
	t.Run("handles escape sequence followed by characters", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[Aabc")
		h.expectSequences(t, "\x1b[A", "a", "b", "c")
	})
	t.Run("handles multiple complete sequences", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[A\x1b[B\x1b[C")
		h.expectSequences(t, "\x1b[A", "\x1b[B", "\x1b[C")
	})
	t.Run("handles partial sequence with preceding characters", func(t *testing.T) {
		h := setup(t)
		h.processInput("abc\x1b[<35")
		h.expectSequences(t, "a", "b", "c")
		if got := h.buffer.GetBuffer(); got != "\x1b[<35" {
			t.Fatalf("buffer = %q", got)
		}

		h.processInput(";20;5m")
		h.expectSequences(t, "a", "b", "c", "\x1b[<35;20;5m")
	})
}

func TestStdinBufferKittyKeyboardProtocol(t *testing.T) {
	setup := func(t *testing.T) *stdinBufferHarness {
		return newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
	}
	t.Run("handles Kitty CSI u press events", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[97u")
		h.expectSequences(t, "\x1b[97u")
	})
	t.Run("handles Kitty CSI u release events", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[97;1:3u")
		h.expectSequences(t, "\x1b[97;1:3u")
	})
	t.Run("handles batched Kitty press and release", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[97u\x1b[97;1:3u")
		h.expectSequences(t, "\x1b[97u", "\x1b[97;1:3u")
	})
	t.Run("handles multiple batched Kitty events", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[97u\x1b[97;1:3u\x1b[98u\x1b[98;1:3u")
		h.expectSequences(t, "\x1b[97u", "\x1b[97;1:3u", "\x1b[98u", "\x1b[98;1:3u")
	})
	t.Run("handles Kitty arrow keys with event type", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[1;1:1A")
		h.expectSequences(t, "\x1b[1;1:1A")
	})
	t.Run("handles Kitty functional keys with event type", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[3;1:3~")
		h.expectSequences(t, "\x1b[3;1:3~")
	})
	t.Run("splits ESC+ESC+CSI into standalone ESC and the CSI sequence (WezTerm Escape key regression)", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b\x1b[27;129:3u")
		h.expectSequences(t, "\x1b", "\x1b[27;129:3u")
	})
	t.Run("splits ESC+ESC+CSI with no modifier (no num_lock)", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b\x1b[27;1:3u")
		h.expectSequences(t, "\x1b", "\x1b[27;1:3u")
	})
	t.Run("still emits ESC+ESC as a single sequence when not followed by a new escape", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b\x1b")
		h.expectSequences(t, "\x1b\x1b")
	})
	t.Run("handles plain characters mixed with Kitty sequences", func(t *testing.T) {
		h := setup(t)
		h.processInput("a\x1b[97;1:3u")
		h.expectSequences(t, "a", "\x1b[97;1:3u")
	})
	t.Run("drops raw duplicate character after matching Kitty printable sequence", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[224uà")
		h.expectSequences(t, "\x1b[224u")
	})
	t.Run("drops raw duplicate character after matching Kitty printable sequence across chunks", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[64u")
		h.processInput("@")
		h.expectSequences(t, "\x1b[64u")
	})
	t.Run("keeps non-matching plain character after Kitty printable sequence", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[97ub")
		h.expectSequences(t, "\x1b[97u", "b")
	})
	t.Run("keeps raw character after modified Kitty printable sequence", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[64;3u@")
		h.expectSequences(t, "\x1b[64;3u", "@")
	})
	t.Run("handles rapid typing simulation with Kitty protocol", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[104u\x1b[104;1:3u\x1b[105u\x1b[105;1:3u")
		h.expectSequences(t, "\x1b[104u", "\x1b[104;1:3u", "\x1b[105u", "\x1b[105;1:3u")
	})
}

func TestStdinBufferMouseEvents(t *testing.T) {
	setup := func(t *testing.T) *stdinBufferHarness {
		return newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
	}
	t.Run("handles mouse press event", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[<0;10;5M")
		h.expectSequences(t, "\x1b[<0;10;5M")
	})
	t.Run("handles mouse release event", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[<0;10;5m")
		h.expectSequences(t, "\x1b[<0;10;5m")
	})
	t.Run("handles mouse move event", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[<35;20;5m")
		h.expectSequences(t, "\x1b[<35;20;5m")
	})
	t.Run("handles split mouse events", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[<3")
		h.processInput("5;1")
		h.processInput("5;")
		h.processInput("10m")
		h.expectSequences(t, "\x1b[<35;15;10m")
	})
	t.Run("handles multiple mouse events", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[<35;1;1m\x1b[<35;2;2m\x1b[<35;3;3m")
		h.expectSequences(t, "\x1b[<35;1;1m", "\x1b[<35;2;2m", "\x1b[<35;3;3m")
	})
	t.Run("handles old-style mouse sequence (ESC[M + 3 bytes)", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[M abc")
		h.expectSequences(t, "\x1b[M ab", "c")
	})
	t.Run("buffers incomplete old-style mouse sequence", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[M")
		if got := h.buffer.GetBuffer(); got != "\x1b[M" {
			t.Fatalf("buffer = %q", got)
		}

		h.processInput(" a")
		if got := h.buffer.GetBuffer(); got != "\x1b[M a" {
			t.Fatalf("buffer = %q", got)
		}

		h.processInput("b")
		h.expectSequences(t, "\x1b[M ab")
	})
}

func TestStdinBufferEdgeCases(t *testing.T) {
	setup := func(t *testing.T) *stdinBufferHarness {
		return newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
	}
	t.Run("handles empty input", func(t *testing.T) {
		h := setup(t)
		h.processInput("")
		// Empty string emits an empty data event
		h.expectSequences(t, "")
	})
	t.Run("handles lone escape character with timeout", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b")
		h.expectSequences(t)

		waitMs(t, 25)
		h.expectSequences(t, "\x1b")
	})
	t.Run("flushes a lone escape promptly with the longer default sequence timeout", func(t *testing.T) {
		h := newStdinBufferHarness(t, nil)
		h.processInput("\x1b")
		waitMs(t, 20)
		h.expectSequences(t, "\x1b")
		h.buffer.Destroy()
	})
	t.Run("handles lone escape character with explicit flush", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b")
		h.expectSequences(t)

		flushed := h.buffer.Flush()
		if len(flushed) != 1 || flushed[0] != "\x1b" {
			t.Fatalf("flushed = %q", flushed)
		}
	})
	t.Run("handles buffer input", func(t *testing.T) {
		h := setup(t)
		h.buffer.Process([]byte("\x1b[A"))
		h.expectSequences(t, "\x1b[A")
	})
	t.Run("handles very long sequences", func(t *testing.T) {
		h := setup(t)
		longSeq := "\x1b[" + repeatString("1;", 50) + "H"
		h.processInput(longSeq)
		h.expectSequences(t, longSeq)
	})
}

func repeatString(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func TestStdinBufferFlush(t *testing.T) {
	setup := func(t *testing.T) *stdinBufferHarness {
		return newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
	}
	t.Run("flushes incomplete sequences", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[<35")
		flushed := h.buffer.Flush()
		if len(flushed) != 1 || flushed[0] != "\x1b[<35" {
			t.Fatalf("flushed = %q", flushed)
		}
		if got := h.buffer.GetBuffer(); got != "" {
			t.Fatalf("buffer = %q", got)
		}
	})
	t.Run("returns empty array if nothing to flush", func(t *testing.T) {
		h := setup(t)
		flushed := h.buffer.Flush()
		if len(flushed) != 0 {
			t.Fatalf("flushed = %q", flushed)
		}
	})
	t.Run("emits flushed data via timeout", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[<35")
		h.expectSequences(t)

		waitMs(t, 25)
		h.expectSequences(t, "\x1b[<35")
	})
}

func TestStdinBufferClear(t *testing.T) {
	t.Run("clears buffered content without emitting", func(t *testing.T) {
		h := newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
		h.processInput("\x1b[<35")
		if got := h.buffer.GetBuffer(); got != "\x1b[<35" {
			t.Fatalf("buffer = %q", got)
		}

		h.buffer.Clear()
		if got := h.buffer.GetBuffer(); got != "" {
			t.Fatalf("buffer = %q", got)
		}
		h.expectSequences(t)
	})
}

func TestStdinBufferBracketedPaste(t *testing.T) {
	setup := func(t *testing.T) *stdinBufferHarness {
		return newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
	}
	expectPaste := func(t *testing.T, h *stdinBufferHarness, want string) {
		t.Helper()
		if h.pasteEvent == nil {
			t.Fatal("no paste event")
		}
		if *h.pasteEvent != want {
			t.Fatalf("paste = %q, want %q", *h.pasteEvent, want)
		}
	}
	t.Run("emits paste event for complete bracketed paste", func(t *testing.T) {
		h := setup(t)
		pasteStart := "\x1b[200~"
		pasteEnd := "\x1b[201~"
		content := "hello world"

		h.processInput(pasteStart + content + pasteEnd)

		expectPaste(t, h, "hello world")
		h.expectSequences(t) // No data events during paste
	})
	t.Run("handles paste arriving in chunks", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[200~")
		if h.pasteEvent != nil {
			t.Fatal("unexpected paste")
		}

		h.processInput("hello ")
		if h.pasteEvent != nil {
			t.Fatal("unexpected paste")
		}

		h.processInput("world\x1b[201~")
		expectPaste(t, h, "hello world")
		h.expectSequences(t)
	})
	t.Run("handles paste with input before and after", func(t *testing.T) {
		h := setup(t)
		h.processInput("a")
		h.processInput("\x1b[200~pasted\x1b[201~")
		h.processInput("b")

		h.expectSequences(t, "a", "b")
		expectPaste(t, h, "pasted")
	})
	t.Run("handles paste with newlines", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[200~line1\nline2\nline3\x1b[201~")

		expectPaste(t, h, "line1\nline2\nline3")
		h.expectSequences(t)
	})
	t.Run("handles paste with unicode", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[200~Hello 世界 🎉\x1b[201~")

		expectPaste(t, h, "Hello 世界 🎉")
		h.expectSequences(t)
	})
}

func TestStdinBufferDestroy(t *testing.T) {
	setup := func(t *testing.T) *stdinBufferHarness {
		return newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
	}
	t.Run("clears buffer on destroy", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[<35")
		if got := h.buffer.GetBuffer(); got != "\x1b[<35" {
			t.Fatalf("buffer = %q", got)
		}

		h.buffer.Destroy()
		if got := h.buffer.GetBuffer(); got != "" {
			t.Fatalf("buffer = %q", got)
		}
	})
	t.Run("clears pending timeouts on destroy", func(t *testing.T) {
		h := setup(t)
		h.processInput("\x1b[<35")
		h.buffer.Destroy()

		waitMs(t, 25)

		// Should not have emitted anything
		h.expectSequences(t)
	})
}

// --- Terminal Reply Strings (pi edb2eb966, v1.2.0) ---

// TestStdinBufferTerminalReplySplitAtEveryHeaderBoundary ports "keeps delayed
// <reply> intact at every split after its reply header".
func TestStdinBufferTerminalReplySplitAtEveryHeaderBoundary(t *testing.T) {
	replies := []string{
		"\x1b]4;1;rgb:eaea/6c6c/6d6d\x07",
		"\x1b]10;rgb:ffff/ffff/ffff\x1b\\",
		"\x1b]11;rgb:0000/0000/0000\x07",
		"\x1b]4;1;rgb:6464/6464/4d4d\x07",
		"\x1bP>|Ghostty 1.3.1\x1b\\",
		"\x1b_Gi=1;OK\x1b\\",
		"\x1b]7501;?\x1b\\",
	}
	for _, reply := range replies {
		match := terminalReplyHeaderRegex.FindStringIndex(reply)
		if match == nil {
			t.Fatalf("reply %q has no recognized header", reply)
		}
		headerLen := match[1]
		for split := headerLen; split < len(reply); split++ {
			h := newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 5, EscapeTimeoutMs: 5})
			h.processInput(reply[:split])
			waitMs(t, 15)
			h.processInput(reply[split:] + "x")
			h.expectSequences(t, reply, "x")
			h.buffer.Destroy()
		}
	}
}

// TestStdinBufferTerminalReplyBufferedAcrossTimeouts ports "keeps an OSC
// reply buffered across multiple expired sequence timeouts".
func TestStdinBufferTerminalReplyBufferedAcrossTimeouts(t *testing.T) {
	h := newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
	h.processInput("\x1b]4;1;rgb:eaea/6c")
	waitMs(t, 30)
	h.expectSequences(t)
	h.processInput("6c/6d6d")
	waitMs(t, 30)
	h.expectSequences(t)
	h.processInput("\x07")
	h.expectSequences(t, "\x1b]4;1;rgb:eaea/6c6c/6d6d\x07")
}

// TestStdinBufferStillTimesOutBareIntroducers ports "still times out bare
// string introducers as Alt keys".
func TestStdinBufferStillTimesOutBareIntroducers(t *testing.T) {
	h := newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10, EscapeTimeoutMs: 10})
	for _, prefix := range []string{"\x1b]", "\x1bP", "\x1b_"} {
		h.processInput(prefix)
		waitMs(t, 25)
	}
	h.expectSequences(t, "\x1b]", "\x1bP", "\x1b_")
}

// TestStdinBufferRecoversBatchedLegacyAltInput ports "recovers batched legacy
// Alt input <input> within the sequence timeout".
func TestStdinBufferRecoversBatchedLegacyAltInput(t *testing.T) {
	ambiguous := []string{
		"\x1b]a", "\x1b]1", "\x1b]10", "\x1b]0;title", "\x1b]4;", "\x1b]4;a;",
		"\x1bPa", "\x1bP>", "\x1b_a", "\x1b_G", "\x1b_Gi=1",
	}
	for _, input := range ambiguous {
		t.Run(fmt.Sprintf("%q", input), func(t *testing.T) {
			h := newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 120, EscapeTimeoutMs: 20})
			h.processInput(input)
			waitMs(t, 40)
			h.processInput("x\r\x03\x04")
			waitMs(t, 60)
			h.expectSequences(t)
			waitMs(t, 120)
			want := []string{input[:2]}
			for _, r := range input[2:] {
				want = append(want, string(r))
			}
			want = append(want, "x", "\r", "\x03", "\x04")
			h.expectSequences(t, want...)
			if got := h.buffer.GetBuffer(); got != "" {
				t.Fatalf("buffer = %q", got)
			}

			h.processInput("y\r\x03\x04")
			want = append(want, "y", "\r", "\x03", "\x04")
			h.expectSequences(t, want...)
			h.buffer.Destroy()
		})
	}
}

// TestStdinBufferRecognizesHeaderAssembledWithinTimeout ports "recognizes a
// reply header assembled within the ambiguous-input timeout".
func TestStdinBufferRecognizesHeaderAssembledWithinTimeout(t *testing.T) {
	h := newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
	h.processInput("\x1b]1")
	waitMs(t, 5)
	h.processInput("0;")
	waitMs(t, 50)
	h.expectSequences(t)
	h.processInput("rgb:ffff/ffff/ffff\x1b\\")
	h.expectSequences(t, "\x1b]10;rgb:ffff/ffff/ffff\x1b\\")
}

// TestStdinBufferUnterminatedReplyAbortsOnNewEscape ports "recovers from an
// unterminated reply when a new escape sequence arrives".
func TestStdinBufferUnterminatedReplyAbortsOnNewEscape(t *testing.T) {
	h := newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
	h.processInput("\x1b]4;1;rgb:eaea/6c")
	waitMs(t, 30)
	h.processInput("\x1b")
	waitMs(t, 30)
	h.processInput("[Ahello")
	h.expectSequences(t, "\x1b[A", "h", "e", "l", "l", "o")
}

// TestStdinBufferCanceledReplyDiscardsWithoutSwallowing ports "discards a
// canceled reply without swallowing subsequent input".
func TestStdinBufferCanceledReplyDiscardsWithoutSwallowing(t *testing.T) {
	h := newStdinBufferHarness(t, &stdinBufferOptions{TimeoutMs: 10})
	for _, cancel := range []string{"\x18", "\x1a"} {
		h.processInput("\x1b]11;rgb:0000" + cancel + "x")
	}
	h.expectSequences(t, "x", "x")
}
