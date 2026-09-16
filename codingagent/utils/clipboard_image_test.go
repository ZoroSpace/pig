package utils

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// sampleClipboardPng is a minimal well-formed PNG header payload used by the
// darwin clipboard tests (signature + IHDR length/type).
var sampleClipboardPng = append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}, bytes.Repeat([]byte{0xAB}, 8)...)

// osascriptPngData renders bytes the way `osascript -e 'the clipboard as
// «class PNGf»'` prints them.
func osascriptPngData(png []byte) string {
	return "«data PNGf" + hex.EncodeToString(png) + "»"
}

type clipboardCommandCall struct {
	command   string
	args      []string
	timeoutMs int64
}

// stubClipboardRunCommand replaces the clipboardRunCommandFn seam for the
// duration of the test and records every call. responder(command, args,
// timeoutMs) → (stdout, ok). The returned pointer must be dereferenced at
// assert time (the closure appends through it, so a snapshot taken at stub
// time would always be empty).
func stubClipboardRunCommand(t *testing.T, responder func(command string, args []string, timeoutMs int64) ([]byte, bool)) *[]clipboardCommandCall {
	t.Helper()
	calls := []clipboardCommandCall{}
	previous := clipboardRunCommandFn
	clipboardRunCommandFn = func(command string, args []string, timeoutMs int64) ([]byte, bool) {
		calls = append(calls, clipboardCommandCall{command: command, args: append([]string(nil), args...), timeoutMs: timeoutMs})
		return responder(command, args, timeoutMs)
	}
	t.Cleanup(func() { clipboardRunCommandFn = previous })
	return &calls
}

func clipboardTestEnv(termux bool) func(string) string {
	return func(key string) string {
		if key == "TERMUX_VERSION" && termux {
			return "1"
		}
		return ""
	}
}

func TestDecodeOsascriptPngData(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		got, ok := decodeOsascriptPngData([]byte(osascriptPngData(sampleClipboardPng)))
		if !ok {
			t.Fatalf("expected ok, got !ok")
		}
		if !bytes.Equal(got, sampleClipboardPng) {
			t.Fatalf("decoded bytes mismatch: got %d bytes, want %d", len(got), len(sampleClipboardPng))
		}
	})
	t.Run("trailing newline tolerated", func(t *testing.T) {
		got, ok := decodeOsascriptPngData([]byte(osascriptPngData(sampleClipboardPng) + "\n"))
		if !ok || !bytes.Equal(got, sampleClipboardPng) {
			t.Fatalf("expected ok with trailing newline, got ok=%v len=%d", ok, len(got))
		}
	})
	t.Run("missing prefix", func(t *testing.T) {
		if _, ok := decodeOsascriptPngData([]byte("89504E47»")); ok {
			t.Fatal("expected !ok for output without the «data PNGf prefix")
		}
	})
	t.Run("missing suffix", func(t *testing.T) {
		if _, ok := decodeOsascriptPngData([]byte(osascriptPngData(sampleClipboardPng)[:len(osascriptPngData(sampleClipboardPng))-3])); ok {
			t.Fatal("expected !ok for truncated output (no closing »)")
		}
	})
	t.Run("odd hex length", func(t *testing.T) {
		if _, ok := decodeOsascriptPngData([]byte("«data PNGf895»")); ok {
			t.Fatal("expected !ok for odd-length hex payload")
		}
	})
	t.Run("empty payload", func(t *testing.T) {
		if _, ok := decodeOsascriptPngData([]byte("«data PNGf»")); ok {
			t.Fatal("expected !ok for empty payload")
		}
	})
	t.Run("bad hex", func(t *testing.T) {
		if _, ok := decodeOsascriptPngData([]byte("«data PNGfzzzz»")); ok {
			t.Fatal("expected !ok for non-hex payload")
		}
	})
	t.Run("empty stdout", func(t *testing.T) {
		// osascript prints the error to stderr and nothing to stdout when the
		// clipboard carries no PNG flavor.
		if _, ok := decodeOsascriptPngData(nil); ok {
			t.Fatal("expected !ok for empty output")
		}
	})
}

// TestReadClipboardImageDarwinOsascriptPath locks the primary darwin route:
// osascript carries the PNG flavor and pngpaste must not run.
func TestReadClipboardImageDarwinOsascriptPath(t *testing.T) {
	callsPtr := stubClipboardRunCommand(t, func(command string, args []string, timeoutMs int64) ([]byte, bool) {
		switch command {
		case "osascript":
			return []byte(osascriptPngData(sampleClipboardPng)), true
		default:
			return nil, false
		}
	})

	image := ReadClipboardImage(&ReadClipboardImageOptions{Env: clipboardTestEnv(false), Platform: "darwin"})
	if image == nil {
		t.Fatal("expected image from osascript path, got nil")
	}
	if image.MimeType != "image/png" {
		t.Fatalf("mime = %q, want image/png", image.MimeType)
	}
	if !bytes.Equal(image.Bytes, sampleClipboardPng) {
		t.Fatalf("bytes mismatch: got %d bytes", len(image.Bytes))
	}
	if calls := *callsPtr; len(calls) != 1 || calls[0].command != "osascript" {
		t.Fatalf("expected exactly one osascript call, got %+v", calls)
	} else if calls[0].timeoutMs != clipboardDefaultOsascriptTimeoutMs {
		t.Fatalf("osascript timeout = %d, want %d", calls[0].timeoutMs, clipboardDefaultOsascriptTimeoutMs)
	}
}

// TestReadClipboardImageDarwinPngpasteFallback locks the fallback order:
// osascript failure → pngpaste.
func TestReadClipboardImageDarwinPngpasteFallback(t *testing.T) {
	callsPtr := stubClipboardRunCommand(t, func(command string, args []string, timeoutMs int64) ([]byte, bool) {
		switch command {
		case "pngpaste":
			return sampleClipboardPng, true
		default:
			return nil, false
		}
	})

	image := ReadClipboardImage(&ReadClipboardImageOptions{Env: clipboardTestEnv(false), Platform: "darwin"})
	if image == nil {
		t.Fatal("expected image from pngpaste fallback, got nil")
	}
	if image.MimeType != "image/png" || !bytes.Equal(image.Bytes, sampleClipboardPng) {
		t.Fatalf("unexpected image: mime=%q len=%d", image.MimeType, len(image.Bytes))
	}
	if calls := *callsPtr; len(calls) != 2 || calls[0].command != "osascript" || calls[1].command != "pngpaste" {
		t.Fatalf("expected [osascript, pngpaste] call order, got %+v", calls)
	} else if len(calls[1].args) != 1 || calls[1].args[0] != "-" {
		t.Fatalf("pngpaste args = %v, want [-]", calls[1].args)
	}
}

// TestReadClipboardImageDarwinUnavailable locks the silent-nothing contract:
// no PNG flavor anywhere → nil (handleClipboardPaste then falls back to text).
func TestReadClipboardImageDarwinUnavailable(t *testing.T) {
	callsPtr := stubClipboardRunCommand(t, func(command string, args []string, timeoutMs int64) ([]byte, bool) {
		return nil, false
	})

	if image := ReadClipboardImage(&ReadClipboardImageOptions{Env: clipboardTestEnv(false), Platform: "darwin"}); image != nil {
		t.Fatalf("expected nil when nothing is available, got mime=%q", image.MimeType)
	}
	if calls := *callsPtr; len(calls) != 2 {
		t.Fatalf("expected osascript then pngpaste attempts, got %+v", calls)
	}
}

// TestReadClipboardImagePngpasteEmptyOutput guards the zero-length branch:
// pngpaste succeeding with empty stdout must not produce an image.
func TestReadClipboardImagePngpasteEmptyOutput(t *testing.T) {
	stubClipboardRunCommand(t, func(command string, args []string, timeoutMs int64) ([]byte, bool) {
		return nil, true // pngpaste exits 0 but prints nothing
	})

	if image := ReadClipboardImage(&ReadClipboardImageOptions{Env: clipboardTestEnv(false), Platform: "darwin"}); image != nil {
		t.Fatalf("expected nil for empty pngpaste output, got mime=%q", image.MimeType)
	}
}

// TestReadClipboardImageTermuxShortCircuit locks the pre-existing TERMUX
// guard: the env check happens before any platform dispatch.
func TestReadClipboardImageTermuxShortCircuit(t *testing.T) {
	callsPtr := stubClipboardRunCommand(t, func(command string, args []string, timeoutMs int64) ([]byte, bool) {
		return sampleClipboardPng, true
	})

	if image := ReadClipboardImage(&ReadClipboardImageOptions{Env: clipboardTestEnv(true), Platform: "darwin"}); image != nil {
		t.Fatalf("expected nil under TERMUX_VERSION, got mime=%q", image.MimeType)
	}
	if calls := *callsPtr; len(calls) != 0 {
		t.Fatalf("expected no command spawns under TERMUX_VERSION, got %+v", calls)
	}
}
