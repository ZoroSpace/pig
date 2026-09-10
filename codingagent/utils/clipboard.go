// clipboard.go (pi: src/utils/clipboard.ts) — system clipboard text
// read/write with OSC 52 fallback for remote sessions.
//
// Segment deviations:
//   - pi's clipboard-native.ts is a Node native addon (clipboard-rs). pig has
//     no native addon; every platform goes through the external tools (and
//     OSC 52). Reads therefore gain platform-tool fallbacks that pi's native
//     addon covered (pbpaste / powershell Get-Clipboard / xclip / xsel).
//   - The Linux native-addon skip (clipboard-rs is X11-only, does not retain
//     selection ownership) is moot for the same reason; the external-tool
//     chain below is the only path, matching pi's effective Linux behavior.
package utils

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const maxOsc52EncodedLength = 100_000

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

// clipboardRunInput runs a command with stdin input and a timeout (pi:
// execSync with { input, timeout, stdio: ["pipe","ignore","ignore"] }).
func clipboardRunInput(command string, args []string, input string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// copyToX11Clipboard tries xclip then xsel (pi: copyToX11Clipboard).
func copyToX11Clipboard(input string, timeout time.Duration) bool {
	if clipboardRunInput("xclip", []string{"-selection", "clipboard"}, input, timeout) {
		return true
	}
	return clipboardRunInput("xsel", []string{"--clipboard", "--input"}, input, timeout)
}

// readClipboardCommandText runs a read command with pi's READ_CLIPBOARD
// options (5s timeout, 50MB cap is not enforceable portably — declared).
func readClipboardCommandText(command string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, command, args...).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// readWaylandClipboardText reads via wl-paste (pi: readWaylandClipboardText).
func readWaylandClipboardText() (string, bool) {
	text, ok := readClipboardCommandText("wl-paste", "--no-newline", "--type", "text")
	if !ok {
		return "", false
	}
	return text, true
}

// isWaylandSessionEnv is clipboard-image's isWaylandSession (same-package
// reuse; see clipboard_image.go).
func isWaylandSessionEnv(env func(string) string) bool {
	if env == nil {
		env = os.Getenv
	}
	return env("WAYLAND_DISPLAY") != "" || env("XDG_SESSION_TYPE") == "wayland"
}

// ReadClipboardText reads plain text from the system clipboard (pi:
// readClipboardText). "" = nothing available.
func ReadClipboardText() string {
	env := os.Getenv
	if runtime.GOOS == "linux" && isWaylandSessionEnv(env) && env("WAYLAND_DISPLAY") != "" {
		if text, ok := readWaylandClipboardText(); ok {
			return text
		}
	}

	// pi's native-addon half; pig uses platform tools (declared deviation).
	switch runtime.GOOS {
	case "darwin":
		if text, ok := readClipboardCommandText("pbpaste"); ok {
			return strings.TrimSuffix(text, "\n")
		}
	case "windows":
		if text, ok := readClipboardCommandText("powershell.exe", "-NoProfile", "-Command",
			"Get-Clipboard -Raw"); ok {
			return strings.TrimSuffix(text, "\r\n")
		}
	default:
		// Linux: prefer wayland (above), then X11 tools.
		if text, ok := readClipboardCommandText("xclip", "-selection", "clipboard", "-o"); ok {
			return text
		}
		if text, ok := readClipboardCommandText("xsel", "--clipboard", "--output"); ok {
			return text
		}
	}
	return ""
}

// CopyToClipboard writes text to the system clipboard, preferring direct
// clipboard writes and falling back to OSC 52 (pi: copyToClipboard). See the
// file header for the native-addon deviation.
func CopyToClipboard(text string) error {
	copied := false

	p := runtime.GOOS

	// Prefer direct clipboard writes. Emitting OSC 52 first can make terminals
	// write the same native clipboard concurrently with the addon, and very large
	// OSC 52 payloads can desynchronize terminal rendering.
	//
	// On Linux, pi skips the native addon (clipboard-rs is X11-only and does
	// not retain selection ownership); pig's tool chain below is the same
	// effective path (see file header).
	if p != "linux" {
		switch p {
		case "darwin":
			copied = clipboardRunInput("pbcopy", nil, text, 5*time.Second)
		case "windows":
			copied = clipboardRunInput("clip", nil, text, 5*time.Second)
		}
	}

	remote := isRemoteSession(nil)
	if copied && !remote {
		return nil
	}

	if !copied {
		// Linux. Try Termux, Wayland, or X11 clipboard tools.
		if os.Getenv("TERMUX_VERSION") != "" {
			if clipboardRunInput("termux-clipboard-set", nil, text, 5*time.Second) {
				copied = true
			}
		}

		if !copied {
			hasWaylandDisplay := os.Getenv("WAYLAND_DISPLAY") != ""
			hasX11Display := os.Getenv("DISPLAY") != ""
			isWayland := isWaylandSessionEnv(nil)
			if isWayland && hasWaylandDisplay {
				if _, err := exec.LookPath("wl-copy"); err == nil {
					// wl-copy daemonizes; a failed run falls through to the
					// xclip/OSC 52 fallbacks.
					if clipboardRunInput("wl-copy", nil, text, 5*time.Second) {
						copied = true
					} else if hasX11Display {
						copied = copyToX11Clipboard(text, 5*time.Second)
					}
				} else if hasX11Display {
					copied = copyToX11Clipboard(text, 5*time.Second)
				}
			} else if hasX11Display {
				copied = copyToX11Clipboard(text, 5*time.Second)
			}
		}
	}

	if remote || !copied {
		osc52Copied := emitOsc52(text)
		copied = copied || osc52Copied
	}

	if !copied {
		return fmt.Errorf("Failed to copy to clipboard")
	}
	return nil
}
