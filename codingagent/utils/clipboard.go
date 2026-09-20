// clipboard.go (pi: src/utils/clipboard.ts) — system clipboard text
// read/write with OSC 52 fallback for remote sessions (rewritten in pi
// v0.86.0 around runClipboardCommand).
//
// Segment deviations:
//   - pi's native path (getNativeClipboard()) has no Go equivalent:
//     pig's GetNativeClipboard() is nil (S6-2). Every platform goes through
//     the external tools (and OSC 52); reads gain platform-tool fallbacks
//     that pi's native addon covered (pbpaste / powershell Get-Clipboard /
//     xclip / xsel), and writes skip the non-Linux native setText step.
//     Declared; see docs/feat/MAC-CLIPBOARD-IMAGE.md for the image-path
//     equivalent.
//   - pi returns `string | null`; pig returns "" for null (existing
//     signature).
package utils

import (
	"encoding/base64"
	"fmt"
	"os"
	"runtime"
)

const maxOsc52EncodedLength = 100_000

// clipboardCommandTimeoutMs is the shared 5s budget (pi: timeoutMs: 5000).
const clipboardCommandTimeoutMs = 5000

// clipboardPlatformFn indirections the platform lookup (pig-only test seam;
// pi: node:os platform() mock).
var clipboardPlatformFn = func() string { return runtime.GOOS }

// clipboardRunCommandOptionsFn indirections runClipboardCommand for the text
// path (pig-only test seam; pi: vi.mock of ./clipboard-command.ts). The
// image path keeps the legacy clipboardRunCommandFn seam.
var clipboardRunCommandOptionsFn = runClipboardCommand

// osc52Writer is the stdout seam for emitOsc52 (pig: tests).
var osc52Writer = func(b []byte) (int, error) { return os.Stdout.Write(b) }

// isRemoteSession reports whether the session runs over SSH/Mosh (pi:
// isRemoteSession).
func isRemoteSession(env func(string) string) bool {
	if env == nil {
		env = os.Getenv
	}
	return env("SSH_CONNECTION") != "" || env("SSH_CLIENT") != "" || env("MOSH_CONNECTION") != ""
}

// emitOsc52 writes the text to the system clipboard via OSC 52 (pi:
// emitOsc52); false = payload too large.
func emitOsc52(text string) bool {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	if len(encoded) > maxOsc52EncodedLength {
		return false
	}
	_, _ = osc52Writer([]byte(fmt.Sprintf("\x1b]52;c;%s\x07", encoded)))
	return true
}

// isWaylandSessionEnv reports a Wayland session (pi: isWaylandSession,
// clipboard-image.ts). isWaylandSessionEnv lives here; see
// clipboard_image.go for the exported alias.
func isWaylandSessionEnv(env func(string) string) bool {
	if env == nil {
		env = os.Getenv
	}
	return env("WAYLAND_DISPLAY") != "" || env("XDG_SESSION_TYPE") == "wayland"
}

// IsWaylandSession reports a Wayland session (pi: isWaylandSession,
// clipboard-image.ts; kept for callers).
func IsWaylandSession(env func(string) string) bool {
	return isWaylandSessionEnv(env)
}

// ReadClipboardText reads plain text from the system clipboard (pi:
// readClipboardText; v0.86.0 chains the platform tools via
// runClipboardCommand). "" = nothing available (pi null).
func ReadClipboardText() string {
	if clipboardPlatformFn() == "linux" {
		type clipboardCommand struct {
			command string
			args    []string
		}
		var commands []clipboardCommand
		if os.Getenv("TERMUX_VERSION") != "" {
			commands = append(commands, clipboardCommand{command: "termux-clipboard-get"})
		}
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			commands = append(commands, clipboardCommand{command: "wl-paste", args: []string{"--no-newline", "--type", "text"}})
		}
		if os.Getenv("DISPLAY") != "" {
			commands = append(commands,
				clipboardCommand{command: "xclip", args: []string{"-selection", "clipboard", "-out"}},
				clipboardCommand{command: "xsel", args: []string{"--clipboard", "--output"}},
			)
		}
		for _, command := range commands {
			if bytes, ok := clipboardRunCommandOptionsFn(command.command, command.args,
				&clipboardCommandOptions{TimeoutMs: clipboardCommandTimeoutMs}); ok {
				text := string(bytes)
				if text == "" {
					return "" // pi: bytes.toString("utf8") || null
				}
				return text
			}
		}
	}

	// pi's native getText() half; pig has no native clipboard (file header):
	// nil → null → "". A native failure is swallowed like pi's try/catch.
	return ""
}

// CopyToClipboard writes text to the system clipboard, preferring direct
// clipboard writes and emitting OSC 52 for remote sessions (pi:
// copyToClipboard; v0.86.0 — "Direct writes precede OSC 52 so the terminal
// cannot race the native writer"). See the file header for the native-addon
// deviation.
func CopyToClipboard(text string) error {
	p := clipboardPlatformFn()
	copied := false

	// pi first tries the native clipboard on non-Linux platforms; pig has no
	// native clipboard, so the tool chain runs on every platform.

	if !copied {
		type clipboardWriteCommand struct {
			command string
			args    []string
		}
		var commands []clipboardWriteCommand
		switch p {
		case "darwin":
			commands = append(commands, clipboardWriteCommand{command: "pbcopy"})
		case "windows":
			commands = append(commands, clipboardWriteCommand{command: "clip"})
		default:
			if os.Getenv("TERMUX_VERSION") != "" {
				commands = append(commands, clipboardWriteCommand{command: "termux-clipboard-set"})
			}
			if os.Getenv("WAYLAND_DISPLAY") != "" {
				commands = append(commands, clipboardWriteCommand{command: "wl-copy"})
			}
			if os.Getenv("DISPLAY") != "" {
				commands = append(commands,
					clipboardWriteCommand{command: "xclip", args: []string{"-selection", "clipboard"}},
					clipboardWriteCommand{command: "xsel", args: []string{"--clipboard", "--input"}},
				)
			}
		}
		for _, command := range commands {
			if _, ok := clipboardRunCommandOptionsFn(command.command, command.args,
				&clipboardCommandOptions{Input: text, HasInput: true, TimeoutMs: clipboardCommandTimeoutMs}); ok {
				copied = true
				break
			}
		}
	}

	if isRemoteSession(nil) {
		copied = emitOsc52(text) || copied
	}

	if !copied {
		if p == "linux" {
			if os.Getenv("TERMUX_VERSION") != "" {
				return fmt.Errorf("Clipboard unavailable: install the Termux:API app and `termux-api` package")
			}
			if os.Getenv("WAYLAND_DISPLAY") != "" {
				return fmt.Errorf("Clipboard unavailable: install `wl-clipboard` (`wl-copy`) or check Wayland access")
			}
			if os.Getenv("DISPLAY") != "" {
				return fmt.Errorf("Clipboard unavailable: install `xclip` or `xsel`, or check X11 access")
			}
			return fmt.Errorf("Clipboard unavailable: no Wayland or X11 display detected")
		}
		return fmt.Errorf("Clipboard unavailable")
	}
	return nil
}
