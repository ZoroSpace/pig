package utils

import (
	"bytes"
	"encoding/hex"
	"os"
	"strings"
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

// --- v0.86.0 chain tests (pi: test/clipboard-image.test.ts, rewritten in
// v0.86.0). Pig adaptations: pi's native getImage has no Go equivalent on
// linux/win32 (declared), so native-leg cases collapse to the nil outcome;
// darwin keeps the pig-only osascript/pngpaste stand-in tested above. ---

func clipboardImageStub(t *testing.T, responder func(command string, args []string, timeoutMs int64) ([]byte, bool)) *[]clipboardCommandCall {
	t.Helper()
	callsPtr := stubClipboardRunCommand(t, responder)
	return callsPtr
}

func clipboardImageEnv(t *testing.T, values map[string]string) func(string) string {
	t.Helper()
	return func(key string) string { return values[key] }
}

func imageCallNames(callsPtr *[]clipboardCommandCall) []string {
	var names []string
	for _, call := range *callsPtr {
		names = append(names, call.command)
	}
	return names
}

// TestReadClipboardImageWaylandCommandResultStopsFallback ports the wayland
// rows of "command image present=%s stops fallback": an answered wl-paste
// chain decides the outcome — an image wins, "no image" stops the chain, an
// unavailable wl-paste falls through to X11.
func TestReadClipboardImageWaylandCommandResultStopsFallback(t *testing.T) {
	for _, present := range []bool{true, false} {
		t.Run("present="+boolLabel(present), func(t *testing.T) {
			callsPtr := clipboardImageStub(t, func(command string, args []string, timeoutMs int64) ([]byte, bool) {
				if command != "wl-paste" {
					t.Fatalf("unexpected command %q", command)
				}
				if len(args) > 0 && args[0] == "--list-types" {
					if present {
						return []byte("text/plain\nimage/png\n"), true
					}
					return []byte("text/plain\n"), true
				}
				return sampleClipboardPng, true
			})

			image := ReadClipboardImage(&ReadClipboardImageOptions{
				Env:      clipboardImageEnv(t, map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}),
				Platform: "linux",
			})
			if present {
				if image == nil || image.MimeType != "image/png" || !bytes.Equal(image.Bytes, sampleClipboardPng) {
					t.Fatalf("image = %+v, want png", image)
				}
			} else if image != nil {
				t.Fatalf("expected nil (null stops the chain), got mime=%q", image.MimeType)
			}
			wantCalls := 2
			if !present {
				wantCalls = 1 // pi: toHaveBeenCalledTimes(present ? 2 : 1)
			}
			if calls := *callsPtr; len(calls) != wantCalls {
				t.Fatalf("calls = %v, want %d", imageCallNames(callsPtr), wantCalls)
			} else if present && !hasStringArg(calls[1].args, "--no-newline") {
				t.Fatalf("second call args = %v", calls[1].args)
			}
		})
	}
}

// TestReadClipboardImageX11CommandResultStopsFallback ports the x11 rows of
// "command image present=%s stops fallback".
func TestReadClipboardImageX11CommandResultStopsFallback(t *testing.T) {
	for _, present := range []bool{true, false} {
		t.Run("present="+boolLabel(present), func(t *testing.T) {
			callsPtr := clipboardImageStub(t, func(command string, args []string, timeoutMs int64) ([]byte, bool) {
				if command != "xclip" {
					t.Fatalf("unexpected command %q", command)
				}
				if hasStringArg(args, "TARGETS") {
					if present {
						return []byte("text/plain\nimage/png\n"), true
					}
					return []byte("text/plain\n"), true
				}
				return sampleClipboardPng, true
			})

			image := ReadClipboardImage(&ReadClipboardImageOptions{
				Env:      clipboardImageEnv(t, map[string]string{"DISPLAY": ":0"}),
				Platform: "linux",
			})
			if present {
				if image == nil || image.MimeType != "image/png" || !bytes.Equal(image.Bytes, sampleClipboardPng) {
					t.Fatalf("image = %+v, want png", image)
				}
			} else if image != nil {
				t.Fatalf("expected nil (null stops the chain), got mime=%q", image.MimeType)
			}
			wantCalls := 2
			if !present {
				wantCalls = 1 // pi: toHaveBeenCalledTimes(present ? 2 : 1)
			}
			if calls := *callsPtr; len(calls) != wantCalls {
				t.Fatalf("calls = %v, want %d", imageCallNames(callsPtr), wantCalls)
			}
		})
	}
}

// TestReadClipboardImageLinuxUnavailableAfterXclipChain adapts "native X11
// result %j stops fallback": pig has no native clipboard on linux
// (declared), so an unavailable xclip chain ends the read with nil after the
// full 5-call probe (TARGETS + the four supported types).
func TestReadClipboardImageLinuxUnavailableAfterXclipChain(t *testing.T) {
	callsPtr := clipboardImageStub(t, func(command string, args []string, timeoutMs int64) ([]byte, bool) {
		return nil, false
	})
	if image := ReadClipboardImage(&ReadClipboardImageOptions{
		Env:      clipboardImageEnv(t, map[string]string{"DISPLAY": ":0", "WAYLAND_DISPLAY": "wayland-0"}),
		Platform: "linux",
	}); image != nil {
		t.Fatalf("expected nil, got mime=%q", image.MimeType)
	}
	names := imageCallNames(callsPtr)
	// pi: ["wl-paste", ...Array<string>(5).fill("xclip")].
	if len(names) != 6 || names[0] != "wl-paste" {
		t.Fatalf("calls = %v, want wl-paste then 5 xclip probes (TARGETS + 4 types)", names)
	}
	for _, name := range names[1:] {
		if name != "xclip" {
			t.Fatalf("unexpected command %q in %v", name, names)
		}
	}
}

// TestReadClipboardImageWaylandFallsBackToX11 ports "Wayland: falls back to
// X11 after missing module/unavailable display" (pig: the native leg is
// linux-absent, so the fallthrough is direct).
func TestReadClipboardImageWaylandFallsBackToX11(t *testing.T) {
	callsPtr := clipboardImageStub(t, func(command string, args []string, timeoutMs int64) ([]byte, bool) {
		if command == "wl-paste" {
			return nil, false
		}
		if hasStringArg(args, "TARGETS") {
			return []byte("image/png\n"), true
		}
		return sampleClipboardPng, true
	})
	image := ReadClipboardImage(&ReadClipboardImageOptions{
		Env:      clipboardImageEnv(t, map[string]string{"WAYLAND_DISPLAY": "wayland-0"}),
		Platform: "linux",
	})
	if image == nil || image.MimeType != "image/png" || !bytes.Equal(image.Bytes, sampleClipboardPng) {
		t.Fatalf("image = %+v, want png", image)
	}
	names := imageCallNames(callsPtr)
	if names[0] != "wl-paste" || names[1] != "xclip" {
		t.Fatalf("calls = %v, want wl-paste first then xclip", names)
	}
}

// TestReadClipboardImageWSLPowerShell ports "WSL: tries PowerShell before a
// broken native X11 bridge". The stub materializes the PNG at the wslpath
// target so the powershell leg succeeds.
func TestReadClipboardImageWSLPowerShell(t *testing.T) {
	var tmpFile string
	callsPtr := clipboardImageStub(t, func(command string, args []string, timeoutMs int64) ([]byte, bool) {
		switch command {
		case "wl-paste", "xclip":
			return nil, false
		case "wslpath":
			tmpFile = args[1]
			return []byte("C:\\Users\\O'Hare\\clip.png\n"), true
		case "powershell.exe":
			if !strings.Contains(args[2], "$path = 'C:\\Users\\O''Hare\\clip.png'") {
				t.Fatalf("powershell script arg = %q", args[2])
			}
			if tmpFile == "" {
				t.Fatal("wslpath should be called before powershell.exe")
			}
			if err := os.WriteFile(tmpFile, sampleClipboardPng, 0o600); err != nil {
				t.Fatal(err)
			}
			return []byte("ok\n"), true
		default:
			t.Fatalf("unexpected command %q", command)
			return nil, false
		}
	})

	image := ReadClipboardImage(&ReadClipboardImageOptions{
		Env:      clipboardImageEnv(t, map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}),
		Platform: "linux",
	})
	if image == nil || image.MimeType != "image/png" || !bytes.Equal(image.Bytes, sampleClipboardPng) {
		t.Fatalf("image = %+v, want png", image)
	}
	if _, err := os.Stat(tmpFile); err == nil {
		t.Fatal("expected the temp file to be cleaned up")
	}
	names := imageCallNames(callsPtr)
	if names[0] != "wl-paste" || names[1] != "xclip" {
		t.Fatalf("calls = %v, want wl-paste/xclip probed before wslpath", names)
	}
	if !containsString(names, "wslpath") || !containsString(names, "powershell.exe") {
		t.Fatalf("calls = %v, missing wslpath/powershell.exe", names)
	}
}

// TestReadClipboardImageWin32WithoutNativeHelper ports "returns null without
// a native helper": pig has no native clipboard on win32 (declared).
func TestReadClipboardImageWin32WithoutNativeHelper(t *testing.T) {
	callsPtr := clipboardImageStub(t, func(string, []string, int64) ([]byte, bool) {
		t.Fatal("no command should be spawned on win32")
		return nil, false
	})
	if image := ReadClipboardImage(&ReadClipboardImageOptions{
		Env:      clipboardImageEnv(t, map[string]string{}),
		Platform: "win32",
	}); image != nil {
		t.Fatalf("expected nil on win32, got mime=%q", image.MimeType)
	}
	if calls := *callsPtr; len(calls) != 0 {
		t.Fatalf("unexpected calls: %v", imageCallNames(callsPtr))
	}
}

// TestReadClipboardImageBmpYieldsNilUntilPhoton adapts the BMP conversion
// test (pi: test/clipboard-image-bmp-conversion.test.ts): pi converts BMP to
// PNG via photon (WASM); pig's photon pipeline lands with Phase 8 (declared
// in clipboard_image.go), so an image/bmp payload currently yields nil.
func TestReadClipboardImageBmpYieldsNilUntilPhoton(t *testing.T) {
	bmp := createTinyBmp1x1Red24bpp()
	clipboardImageStub(t, func(command string, args []string, timeoutMs int64) ([]byte, bool) {
		if command == "wl-paste" && hasStringArg(args, "--list-types") {
			return []byte("image/bmp\n"), true
		}
		if command == "wl-paste" && hasStringArg(args, "image/bmp") {
			return bmp, true
		}
		return nil, false
	})
	if image := ReadClipboardImage(&ReadClipboardImageOptions{
		Env:      clipboardImageEnv(t, map[string]string{"WAYLAND_DISPLAY": "wayland-0"}),
		Platform: "linux",
	}); image != nil {
		t.Fatalf("BMP should not pass through unconverted, got mime=%q", image.MimeType)
	}
}

// createTinyBmp1x1Red24bpp builds the minimal BMP fixture (pi:
// createTinyBmp1x1Red24bpp).
func createTinyBmp1x1Red24bpp() []byte {
	buf := make([]byte, 58)
	copy(buf[0:2], "BM")
	writeLeUint32(buf[2:], 58)
	writeLeUint32(buf[10:], 54)
	writeLeUint32(buf[14:], 40)
	writeLeInt32(buf[18:], 1)
	writeLeInt32(buf[22:], 1)
	writeLeUint16(buf[26:], 1)
	writeLeUint16(buf[28:], 24)
	writeLeUint32(buf[34:], 4)
	buf[54] = 0x00
	buf[55] = 0x00
	buf[56] = 0xff
	buf[57] = 0x00
	return buf
}

func writeLeUint16(buf []byte, v uint16) {
	buf[0] = byte(v)
	buf[1] = byte(v >> 8)
}

func writeLeUint32(buf []byte, v uint32) {
	buf[0] = byte(v)
	buf[1] = byte(v >> 8)
	buf[2] = byte(v >> 16)
	buf[3] = byte(v >> 24)
}

func writeLeInt32(buf []byte, v int32) { writeLeUint32(buf, uint32(v)) }

func hasStringArg(args []string, want string) bool { return containsString(args, want) }

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func boolLabel(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
